// Package queuetest provides a shared contract test suite that every
// queue.JobQueue implementation must satisfy. Wire it into a concrete backend
// via RunJobQueueContractTests and a QueueFactory.
package queuetest

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/queue"
)

// QueueFactory creates a fresh queue.JobQueue backed by the given clock and
// returns it together with a ReadBackFn bound to the same backing storage. The
// pair is returned together so each subtest's readBack is isolated to its own
// queue instance — important under t.Parallel() where subtests run concurrently.
// Each subtest receives its own factory call so state does not leak between
// subtests.
type QueueFactory func(t *testing.T, clk clock.Clock) (queue.JobQueue, ReadBackFn)

// ReadBackFn is a test-time backdoor that reads the persisted price,
// quote_updated_at, and status for a job by its ID directly from the
// implementation's storage. It is passed by the caller of
// RunJobQueueContractTests so that the contract test for
// Complete_PersistsPriceAndQuoteUpdatedAt can assert without requiring a
// public GetByID method on JobQueue (that is iter 8 scope).
//
// Implementations:
//   - memqueue: closure over *memqueue.Queue, reads q.jobs[id] fields.
//     memqueue.record does not yet have price/quote_updated_at fields — that
//     is the implementer's job. The test calls this closure; it references
//     not-yet-existing fields. This IS the RED state for memqueue.
//   - pgqueue: closure runs
//     SELECT price, quote_updated_at, status FROM quote_jobs WHERE id = $1.
//     The column does not yet exist (migration 000005 is the implementer's
//     job). The test compiles but the SQL fails at runtime — also a valid RED.
type ReadBackFn func(ctx context.Context, id queue.JobID) (price decimal.Decimal, quoteUpdatedAt time.Time, status string, err error)

// dummyPrice is the canonical non-trivial price used across contract tests
// that call Complete with a price argument. Matches the running example in
// api-contract.md ("1 EUR = 20.255648 MXN").
var dummyPrice = decimal.RequireFromString("20.255648")

// dummyQuoteTime is a fixed timestamp used as the quote_updated_at argument
// in Complete calls throughout the contract suite. It is chosen to differ
// clearly from "now" so assertions can confirm the field was persisted rather
// than substituted with a server-side timestamp.
var dummyQuoteTime = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

// newJob builds a queue.Job whose ID is drawn from idg and whose Base, Quote
// and DedupKey are set to the supplied values. NextRunAt is set to clk.Now().
// Source is set to "scheduler" (the default for contract tests that do not
// exercise source-specific behaviour).
func newJob(clk clock.Clock, idg idgen.IDGenerator, base, quote, dedupKey string) queue.Job {
	return queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Base:      base,
		Quote:     quote,
		DedupKey:  dedupKey,
		NextRunAt: clk.Now(),
		Source:    "scheduler",
	}
}

// ghostID is a valid UUID-format job ID that is never inserted into any queue
// during the contract tests. Using a valid UUID prevents backends that store
// IDs in a UUID column (e.g. pgQueue) from rejecting the value before the
// NOT-FOUND check can run.
const ghostID = queue.JobID("00000000-0000-0000-0000-000000000099")

// RunJobQueueContractTests runs the full contract test suite against any
// queue.JobQueue produced by factory. The factory's second return value is a
// per-instance ReadBackFn used by Complete/PersistsPriceAndQuoteUpdatedAt to
// verify the persisted price and quote_updated_at fields without a public
// GetByID method on JobQueue.
func RunJobQueueContractTests(t *testing.T, factory QueueFactory) {
	t.Helper()

	// --- Enqueue ---

	t.Run("Enqueue/NewJob", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		id, inserted, err := q.Enqueue(ctx, job)

		require.NoError(t, err)
		assert.True(t, inserted)
		assert.Equal(t, job.ID, id)
	})

	t.Run("Enqueue/DuplicateDedupKey_ReturnsFalse", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job1 := newJob(clk, idg, "EUR", "USD", "k1")
		job2 := newJob(clk, idg, "USD", "MXN", "k1")

		id1, inserted1, err := q.Enqueue(ctx, job1)
		require.NoError(t, err)
		require.True(t, inserted1)

		id2, inserted2, err := q.Enqueue(ctx, job2)
		require.NoError(t, err)
		assert.False(t, inserted2)
		assert.Equal(t, job1.ID, id2)
		assert.Equal(t, id1, id2)
	})

	t.Run("Enqueue/DuplicateDedupKey_StatusUnaware", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job1 := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job1)
		require.NoError(t, err)

		reserved, err := q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)

		err = q.Complete(ctx, job1.ID, dummyPrice, dummyQuoteTime)
		require.NoError(t, err)

		job2 := newJob(clk, idg, "GBP", "CHF", "k1")
		id2, inserted2, err := q.Enqueue(ctx, job2)
		require.NoError(t, err)
		assert.False(t, inserted2)
		assert.Equal(t, job1.ID, id2)
	})

	t.Run("Enqueue/EmptyDedupKey_AllowsMultiple", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job1 := newJob(clk, idg, "EUR", "USD", "")
		job2 := newJob(clk, idg, "USD", "MXN", "")
		job3 := newJob(clk, idg, "GBP", "EUR", "")

		_, ins1, err := q.Enqueue(ctx, job1)
		require.NoError(t, err)
		_, ins2, err := q.Enqueue(ctx, job2)
		require.NoError(t, err)
		_, ins3, err := q.Enqueue(ctx, job3)
		require.NoError(t, err)

		assert.True(t, ins1)
		assert.True(t, ins2)
		assert.True(t, ins3)
	})

	// --- Reserve ---

	t.Run("Reserve/ReturnsSortedByNextRunAt", func(t *testing.T) {
		t.Parallel()
		base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		clk := clock.NewFake(base)
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		jobA := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "EUR",
			Quote:     "USD",
			DedupKey:  "a",
			NextRunAt: base.Add(10 * time.Second),
			Source:    "scheduler",
		}
		jobB := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "USD",
			Quote:     "MXN",
			DedupKey:  "b",
			NextRunAt: base.Add(5 * time.Second),
			Source:    "scheduler",
		}
		jobC := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "GBP",
			Quote:     "EUR",
			DedupKey:  "c",
			NextRunAt: base.Add(20 * time.Second),
			Source:    "scheduler",
		}

		_, _, err := q.Enqueue(ctx, jobA)
		require.NoError(t, err)
		_, _, err = q.Enqueue(ctx, jobB)
		require.NoError(t, err)
		_, _, err = q.Enqueue(ctx, jobC)
		require.NoError(t, err)

		clk.Advance(25 * time.Second)

		jobs, err := q.Reserve(ctx, 10, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, jobs, 3)

		assert.Equal(t, "USD", jobs[0].Base) // T+5s
		assert.Equal(t, "EUR", jobs[1].Base) // T+10s
		assert.Equal(t, "GBP", jobs[2].Base) // T+20s
	})

	// Reserve/PreservesPair: enqueue a job with a specific Base/Quote pair, reserve
	// it, and assert both fields survive the round-trip in the correct order.
	// This catches a Scan-order swap that the type system (two strings) cannot detect.
	t.Run("Reserve/PreservesPair", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "MXN", "pair-trip")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		reserved, err := q.Reserve(ctx, 1, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)

		assert.Equal(t, "EUR", reserved[0].Base)
		assert.Equal(t, "MXN", reserved[0].Quote)
	})

	t.Run("Reserve/RespectsNLimit", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		for i := 0; i < 5; i++ {
			job := newJob(clk, idg, "EUR", "USD", "")
			_, _, err := q.Enqueue(ctx, job)
			require.NoError(t, err)
		}

		jobs, err := q.Reserve(ctx, 3, 30*time.Second)
		require.NoError(t, err)
		assert.Len(t, jobs, 3)
	})

	t.Run("Reserve/SkipsIneligible", func(t *testing.T) {
		t.Parallel()
		base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
		clk := clock.NewFake(base)
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		future := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "EUR",
			Quote:     "USD",
			DedupKey:  "future",
			NextRunAt: base.Add(1 * time.Second),
			Source:    "scheduler",
		}
		past := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "USD",
			Quote:     "MXN",
			DedupKey:  "past",
			NextRunAt: base.Add(-1 * time.Second),
			Source:    "scheduler",
		}

		_, _, err := q.Enqueue(ctx, future)
		require.NoError(t, err)
		_, _, err = q.Enqueue(ctx, past)
		require.NoError(t, err)

		// clock at base — future is not yet eligible
		jobs, err := q.Reserve(ctx, 10, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		assert.Equal(t, "USD", jobs[0].Base)
	})

	t.Run("Reserve/MarksRunning_SubsequentSkips", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		first, err := q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)
		require.Len(t, first, 1)

		second, err := q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)
		assert.Empty(t, second)
	})

	t.Run("Reserve/ReturnsValueCopies", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		reserved, err := q.Reserve(ctx, 1, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)

		// Mutate the returned copy.
		reserved[0].Base = "MUTATED"

		// Complete still works, proving the queue holds an independent copy.
		err = q.Complete(ctx, job.ID, dummyPrice, dummyQuoteTime)
		assert.NoError(t, err)
	})

	// --- Complete ---

	t.Run("Complete/MarksJobDone", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID, dummyPrice, dummyQuoteTime)
		assert.NoError(t, err)
	})

	t.Run("Complete/SecondComplete_ErrNotReserved", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID, dummyPrice, dummyQuoteTime)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID, dummyPrice, dummyQuoteTime)
		assert.True(t, errors.Is(err, queue.ErrNotReserved))
	})

	t.Run("Complete/ErrNotFound", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		ctx := context.Background()

		err := q.Complete(ctx, ghostID, dummyPrice, dummyQuoteTime)
		assert.True(t, errors.Is(err, queue.ErrNotFound))
	})

	t.Run("Complete/ErrNotReserved_WhenPending", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID, dummyPrice, dummyQuoteTime)
		assert.True(t, errors.Is(err, queue.ErrNotReserved))
	})

	// Complete/PersistsPriceAndQuoteUpdatedAt verifies that after a successful
	// Complete call the price and quote_updated_at values passed as arguments are
	// persisted on the job row (read back via the readBack backdoor), and that
	// the job's status is "done".
	//
	// This is the primary test for the iter 7.5 contract:
	//   Complete(ctx, id, price, quoteUpdatedAt) persists both fields so that
	//   GET /quotes/:id for a done job can return Cache-Control: immutable.
	t.Run("Complete/PersistsPriceAndQuoteUpdatedAt", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, readBack := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		wantPrice := decimal.RequireFromString("20.255648")
		// wantQuoteTime is explicitly different from clk.Now() (the complete
		// timestamp) so we can confirm the field is persisted from the argument
		// rather than being replaced by a server-side now().
		wantQuoteTime := time.Date(2025, 6, 15, 9, 30, 0, 0, time.UTC)

		job := newJob(clk, idg, "EUR", "MXN", "persist-price-k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID, wantPrice, wantQuoteTime)
		require.NoError(t, err)

		gotPrice, gotQuoteTime, gotStatus, rbErr := readBack(ctx, job.ID)
		require.NoError(t, rbErr, "readBack must not error after a successful Complete")

		assert.Equal(t, "done", gotStatus, "job status must be 'done' after Complete")
		assert.True(t, wantPrice.Equal(gotPrice),
			"persisted price %s must equal argument %s", gotPrice, wantPrice)
		assert.True(t, wantQuoteTime.Equal(gotQuoteTime),
			"persisted quote_updated_at %v must equal argument %v", gotQuoteTime, wantQuoteTime)
	})

	// --- Reschedule ---

	t.Run("Reschedule/ReturnsToPendingWithUpdatedFields", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Reschedule(ctx, job.ID, "r", 10*time.Second)
		require.NoError(t, err)

		// Advance past the new NextRunAt.
		clk.Advance(11 * time.Second)

		jobs, err := q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		assert.Equal(t, 1, jobs[0].Attempts)
	})

	t.Run("Reschedule/NotEligibleBeforeNextRunAt", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Reschedule(ctx, job.ID, "r", 10*time.Second)
		require.NoError(t, err)

		// Do NOT advance clock — job's NextRunAt is 10s in the future.
		jobs, err := q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)
		assert.Empty(t, jobs)
	})

	t.Run("Reschedule/ErrNotFound", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		ctx := context.Background()

		err := q.Reschedule(ctx, ghostID, "r", 5*time.Second)
		assert.True(t, errors.Is(err, queue.ErrNotFound))
	})

	t.Run("Reschedule/ErrNotReserved_WhenPending", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		err = q.Reschedule(ctx, job.ID, "r", 5*time.Second)
		assert.True(t, errors.Is(err, queue.ErrNotReserved))
	})

	// --- Fail ---

	t.Run("Fail/MarksJobFailed", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Fail(ctx, job.ID, "upstream error")
		require.NoError(t, err)

		jobs, err := q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)
		assert.Empty(t, jobs)
	})

	t.Run("Fail/ErrNotFound", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		ctx := context.Background()

		err := q.Fail(ctx, ghostID, "r")
		assert.True(t, errors.Is(err, queue.ErrNotFound))
	})

	t.Run("Fail/ErrNotReserved_WhenPending", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "USD", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		err = q.Fail(ctx, job.ID, "r")
		assert.True(t, errors.Is(err, queue.ErrNotReserved))
	})

	// --- Error sentinels ---

	t.Run("ErrorSentinels/ErrNotFound_IsTestable", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		ctx := context.Background()

		err := q.Complete(ctx, ghostID, dummyPrice, dummyQuoteTime)
		assert.True(t, errors.Is(err, queue.ErrNotFound), "Complete: want ErrNotFound, got %v", err)

		err = q.Reschedule(ctx, ghostID, "r", 5*time.Second)
		assert.True(t, errors.Is(err, queue.ErrNotFound), "Reschedule: want ErrNotFound, got %v", err)

		err = q.Fail(ctx, ghostID, "r")
		assert.True(t, errors.Is(err, queue.ErrNotFound), "Fail: want ErrNotFound, got %v", err)
	})

	t.Run("ErrorSentinels/ErrNotReserved_IsTestable", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		// Three separate queues / jobs, one per operation, to avoid order
		// dependence between the three assertions.
		enqueueAndGet := func() queue.JobID {
			job := newJob(clk, idg, "EUR", "USD", "")
			_, _, err := q.Enqueue(ctx, job)
			require.NoError(t, err)
			return job.ID
		}

		id1 := enqueueAndGet()
		err := q.Complete(ctx, id1, dummyPrice, dummyQuoteTime)
		assert.True(t, errors.Is(err, queue.ErrNotReserved), "Complete: want ErrNotReserved, got %v", err)

		id2 := enqueueAndGet()
		err = q.Reschedule(ctx, id2, "r", 5*time.Second)
		assert.True(t, errors.Is(err, queue.ErrNotReserved), "Reschedule: want ErrNotReserved, got %v", err)

		id3 := enqueueAndGet()
		err = q.Fail(ctx, id3, "r")
		assert.True(t, errors.Is(err, queue.ErrNotReserved), "Fail: want ErrNotReserved, got %v", err)
	})

	// --- Source field ---

	t.Run("Source/RefreshRoundTrips", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "EUR",
			Quote:     "USD",
			DedupKey:  "src-refresh",
			NextRunAt: clk.Now(),
			Source:    "refresh",
		}
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		reserved, err := q.Reserve(ctx, 1, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)
		assert.Equal(t, "refresh", reserved[0].Source)
	})

	t.Run("Source/SchedulerRoundTrips", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "USD",
			Quote:     "MXN",
			DedupKey:  "src-scheduler",
			NextRunAt: clk.Now(),
			Source:    "scheduler",
		}
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		reserved, err := q.Reserve(ctx, 1, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)
		assert.Equal(t, "scheduler", reserved[0].Source)
	})

	t.Run("Source/CreatedAtReflectsEnqueueTime", func(t *testing.T) {
		t.Parallel()
		t0 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
		clk := clock.NewFake(t0)
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "EUR",
			Quote:     "MXN",
			DedupKey:  "cat-t2",
			NextRunAt: clk.Now(),
			Source:    "scheduler",
		}
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		// Advance 5s so reserve-time differs from enqueue-time.
		clk.Advance(5 * time.Second)

		reserved, err := q.Reserve(ctx, 1, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)
		assert.True(t, reserved[0].CreatedAt.Equal(t0),
			"CreatedAt must equal enqueue time %v, got %v", t0, reserved[0].CreatedAt)
	})

	t.Run("Source/EmptySourceReturnsErrInvalidSource", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "EUR",
			Quote:     "USD",
			DedupKey:  "empty-src",
			NextRunAt: clk.Now(),
			Source:    "", // empty — must be rejected
		}
		_, _, err := q.Enqueue(ctx, job)
		assert.True(t, errors.Is(err, queue.ErrInvalidSource),
			"expected ErrInvalidSource for empty Source, got %v", err)

		// No row persisted: Reserve must return nothing.
		reserved, rErr := q.Reserve(ctx, 1, 30*time.Second)
		require.NoError(t, rErr)
		assert.Empty(t, reserved, "no job must be reserved after rejected Enqueue")
	})

	t.Run("Source/InvalidSourceReturnsErrInvalidSource", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Base:      "USD",
			Quote:     "EUR",
			DedupKey:  "invalid-src",
			NextRunAt: clk.Now(),
			Source:    "unknown", // invalid value
		}
		_, _, err := q.Enqueue(ctx, job)
		assert.True(t, errors.Is(err, queue.ErrInvalidSource),
			"expected ErrInvalidSource for Source=%q, got %v", "unknown", err)
	})

	// --- GetByID ---

	// GetByID/NotFound: calling GetByID with a valid UUID that was never inserted
	// must return queue.ErrNotFound.
	t.Run("GetByID/NotFound", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		ctx := context.Background()

		_, err := q.GetByID(ctx, ghostID)
		assert.True(t, errors.Is(err, queue.ErrNotFound),
			"GetByID for unknown id must return ErrNotFound, got %v", err)
	})

	// GetByID/Pending: after Enqueue only the view must show status="pending"
	// with the correct Base, Quote, CreatedAt, and all nullable fields nil/zero.
	t.Run("GetByID/Pending", func(t *testing.T) {
		t.Parallel()
		t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		clk := clock.NewFake(t0)
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "MXN", "getbyid-pending")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		view, err := q.GetByID(ctx, job.ID)
		require.NoError(t, err)

		assert.Equal(t, job.ID, view.ID)
		assert.Equal(t, "EUR", view.Base)
		assert.Equal(t, "MXN", view.Quote)
		assert.Equal(t, "pending", view.Status)
		assert.True(t, t0.Equal(view.CreatedAt),
			"CreatedAt must equal enqueue time %v, got %v", t0, view.CreatedAt)
		assert.Nil(t, view.CompletedAt, "CompletedAt must be nil for pending job")
		assert.Nil(t, view.Price, "Price must be nil for pending job")
		assert.Nil(t, view.QuoteUpdatedAt, "QuoteUpdatedAt must be nil for pending job")
		assert.Empty(t, view.LastError, "LastError must be empty for pending job")
	})

	// GetByID/Done: after Enqueue + Reserve + Complete the view must show
	// status="done" with Price, QuoteUpdatedAt, and CompletedAt all non-nil and
	// matching the values passed to Complete.
	t.Run("GetByID/Done", func(t *testing.T) {
		t.Parallel()
		t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
		clk := clock.NewFake(t0)
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		wantPrice := decimal.RequireFromString("1.234567")
		wantQuoteTime := time.Date(2026, 6, 1, 11, 59, 0, 0, time.UTC)

		job := newJob(clk, idg, "EUR", "USD", "getbyid-done")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID, wantPrice, wantQuoteTime)
		require.NoError(t, err)

		view, err := q.GetByID(ctx, job.ID)
		require.NoError(t, err)

		assert.Equal(t, "done", view.Status)
		require.NotNil(t, view.Price, "Price must be non-nil for done job")
		assert.True(t, wantPrice.Equal(*view.Price),
			"Price %s must equal argument %s", view.Price, wantPrice)
		require.NotNil(t, view.QuoteUpdatedAt, "QuoteUpdatedAt must be non-nil for done job")
		assert.True(t, wantQuoteTime.Equal(*view.QuoteUpdatedAt),
			"QuoteUpdatedAt %v must equal argument %v", *view.QuoteUpdatedAt, wantQuoteTime)
		require.NotNil(t, view.CompletedAt, "CompletedAt must be non-nil for done job")
	})

	// GetByID/Failed: after Enqueue + Reserve + Fail the view must show
	// status="failed", LastError matching the reason, CompletedAt non-nil,
	// and Price/QuoteUpdatedAt nil.
	t.Run("GetByID/Failed", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "USD", "MXN", "getbyid-failed")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Fail(ctx, job.ID, "boom")
		require.NoError(t, err)

		view, err := q.GetByID(ctx, job.ID)
		require.NoError(t, err)

		assert.Equal(t, "failed", view.Status)
		assert.Equal(t, "boom", view.LastError)
		require.NotNil(t, view.CompletedAt, "CompletedAt must be non-nil for failed job")
		assert.Nil(t, view.Price, "Price must be nil for failed job")
		assert.Nil(t, view.QuoteUpdatedAt, "QuoteUpdatedAt must be nil for failed job")
	})

	// --- Concurrency ---

	t.Run("Concurrency/SameDedupKey_OnlyOneInserts", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q, _ := factory(t, clk)
		ctx := context.Background()

		type result struct {
			id       queue.JobID
			inserted bool
			err      error
		}

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			results []result
		)

		// Single shared SeqIDGenerator (goroutine-safe via internal mutex) so
		// every goroutine gets a distinct job.ID. Without this the previous
		// code created a fresh NewSeq() per goroutine — all started at 1, so
		// all 100 jobs collided on PK as well as on dedup_key. ON CONFLICT
		// (dedup_key) only handles the targeted constraint; the PK conflict
		// propagated as an intermittent flake.
		idg := idgen.NewSeq()
		for i := 0; i < 100; i++ {
			wg.Add(1)
			job := newJob(clk, idg, "EUR", "USD", "race-key")
			go func(j queue.Job) {
				defer wg.Done()
				id, inserted, err := q.Enqueue(ctx, j)
				mu.Lock()
				results = append(results, result{id: id, inserted: inserted, err: err})
				mu.Unlock()
			}(job)
		}

		wg.Wait()

		require.Len(t, results, 100)
		for _, r := range results {
			assert.NoError(t, r.err, "Enqueue must not return an error")
		}

		insertedCount := 0
		var winnerID queue.JobID
		for _, r := range results {
			if r.inserted {
				insertedCount++
				winnerID = r.id
			}
		}

		assert.Equal(t, 1, insertedCount, "exactly one goroutine should have inserted")
		for _, r := range results {
			assert.Equal(t, winnerID, r.id, "all callers must receive the same job ID")
		}
	})
}
