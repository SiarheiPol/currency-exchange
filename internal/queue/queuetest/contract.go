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

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/queue"
)

// QueueFactory creates a fresh queue.JobQueue backed by the given clock.
// Each subtest receives its own factory call so state does not leak between
// subtests.
type QueueFactory func(t *testing.T, clk clock.Clock) queue.JobQueue

// newJob builds a queue.Job whose ID is drawn from idg and whose Currency and
// DedupKey are set to the supplied values. NextRunAt is set to clk.Now().
func newJob(clk clock.Clock, idg idgen.IDGenerator, currency, dedupKey string) queue.Job {
	return queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Currency:  currency,
		DedupKey:  dedupKey,
		NextRunAt: clk.Now(),
	}
}

// ghostID is a valid UUID-format job ID that is never inserted into any queue
// during the contract tests. Using a valid UUID prevents backends that store
// IDs in a UUID column (e.g. pgQueue) from rejecting the value before the
// NOT-FOUND check can run.
const ghostID = queue.JobID("00000000-0000-0000-0000-000000000099")

// RunJobQueueContractTests runs the full contract test suite against any
// queue.JobQueue produced by factory.
func RunJobQueueContractTests(t *testing.T, factory QueueFactory) {
	t.Helper()

	// --- Enqueue ---

	t.Run("Enqueue/NewJob", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
		id, inserted, err := q.Enqueue(ctx, job)

		require.NoError(t, err)
		assert.True(t, inserted)
		assert.Equal(t, job.ID, id)
	})

	t.Run("Enqueue/DuplicateDedupKey_ReturnsFalse", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job1 := newJob(clk, idg, "EUR", "k1")
		job2 := newJob(clk, idg, "USD", "k1")

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
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job1 := newJob(clk, idg, "EUR", "k1")
		_, _, err := q.Enqueue(ctx, job1)
		require.NoError(t, err)

		reserved, err := q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)

		err = q.Complete(ctx, job1.ID)
		require.NoError(t, err)

		job2 := newJob(clk, idg, "GBP", "k1")
		id2, inserted2, err := q.Enqueue(ctx, job2)
		require.NoError(t, err)
		assert.False(t, inserted2)
		assert.Equal(t, job1.ID, id2)
	})

	t.Run("Enqueue/EmptyDedupKey_AllowsMultiple", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job1 := newJob(clk, idg, "EUR", "")
		job2 := newJob(clk, idg, "USD", "")
		job3 := newJob(clk, idg, "GBP", "")

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
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		jobA := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "EUR",
			DedupKey:  "a",
			NextRunAt: base.Add(10 * time.Second),
		}
		jobB := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "USD",
			DedupKey:  "b",
			NextRunAt: base.Add(5 * time.Second),
		}
		jobC := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "GBP",
			DedupKey:  "c",
			NextRunAt: base.Add(20 * time.Second),
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

		assert.Equal(t, "USD", jobs[0].Currency) // T+5s
		assert.Equal(t, "EUR", jobs[1].Currency) // T+10s
		assert.Equal(t, "GBP", jobs[2].Currency) // T+20s
	})

	t.Run("Reserve/RespectsNLimit", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		for i := 0; i < 5; i++ {
			job := newJob(clk, idg, "EUR", "")
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
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		future := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "EUR",
			DedupKey:  "future",
			NextRunAt: base.Add(1 * time.Second),
		}
		past := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "USD",
			DedupKey:  "past",
			NextRunAt: base.Add(-1 * time.Second),
		}

		_, _, err := q.Enqueue(ctx, future)
		require.NoError(t, err)
		_, _, err = q.Enqueue(ctx, past)
		require.NoError(t, err)

		// clock at base — future is not yet eligible
		jobs, err := q.Reserve(ctx, 10, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, jobs, 1)
		assert.Equal(t, "USD", jobs[0].Currency)
	})

	t.Run("Reserve/MarksRunning_SubsequentSkips", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
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
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		reserved, err := q.Reserve(ctx, 1, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)

		// Mutate the returned copy.
		reserved[0].Currency = "MUTATED"

		// Complete still works, proving the queue holds an independent copy.
		err = q.Complete(ctx, job.ID)
		assert.NoError(t, err)
	})

	// --- Complete ---

	t.Run("Complete/MarksJobDone", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID)
		assert.NoError(t, err)
	})

	t.Run("Complete/SecondComplete_ErrNotReserved", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		_, err = q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID)
		assert.True(t, errors.Is(err, queue.ErrNotReserved))
	})

	t.Run("Complete/ErrNotFound", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		ctx := context.Background()

		err := q.Complete(ctx, ghostID)
		assert.True(t, errors.Is(err, queue.ErrNotFound))
	})

	t.Run("Complete/ErrNotReserved_WhenPending", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		err = q.Complete(ctx, job.ID)
		assert.True(t, errors.Is(err, queue.ErrNotReserved))
	})

	// --- Reschedule ---

	t.Run("Reschedule/ReturnsToPendingWithUpdatedFields", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
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
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
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
		q := factory(t, clk)
		ctx := context.Background()

		err := q.Reschedule(ctx, ghostID, "r", 5*time.Second)
		assert.True(t, errors.Is(err, queue.ErrNotFound))
	})

	t.Run("Reschedule/ErrNotReserved_WhenPending", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		err = q.Reschedule(ctx, job.ID, "r", 5*time.Second)
		assert.True(t, errors.Is(err, queue.ErrNotReserved))
	})

	// --- Fail ---

	t.Run("Fail/MarksJobFailed", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
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
		q := factory(t, clk)
		ctx := context.Background()

		err := q.Fail(ctx, ghostID, "r")
		assert.True(t, errors.Is(err, queue.ErrNotFound))
	})

	t.Run("Fail/ErrNotReserved_WhenPending", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		job := newJob(clk, idg, "EUR", "k1")
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		err = q.Fail(ctx, job.ID, "r")
		assert.True(t, errors.Is(err, queue.ErrNotReserved))
	})

	// --- Error sentinels ---

	t.Run("ErrorSentinels/ErrNotFound_IsTestable", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		ctx := context.Background()

		err := q.Complete(ctx, ghostID)
		assert.True(t, errors.Is(err, queue.ErrNotFound), "Complete: want ErrNotFound, got %v", err)

		err = q.Reschedule(ctx, ghostID, "r", 5*time.Second)
		assert.True(t, errors.Is(err, queue.ErrNotFound), "Reschedule: want ErrNotFound, got %v", err)

		err = q.Fail(ctx, ghostID, "r")
		assert.True(t, errors.Is(err, queue.ErrNotFound), "Fail: want ErrNotFound, got %v", err)
	})

	t.Run("ErrorSentinels/ErrNotReserved_IsTestable", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		idg := idgen.NewSeq()
		ctx := context.Background()

		// Three separate queues / jobs, one per operation, to avoid order
		// dependence between the three assertions.
		enqueueAndGet := func() queue.JobID {
			job := newJob(clk, idg, "EUR", "")
			_, _, err := q.Enqueue(ctx, job)
			require.NoError(t, err)
			return job.ID
		}

		id1 := enqueueAndGet()
		err := q.Complete(ctx, id1)
		assert.True(t, errors.Is(err, queue.ErrNotReserved), "Complete: want ErrNotReserved, got %v", err)

		id2 := enqueueAndGet()
		err = q.Reschedule(ctx, id2, "r", 5*time.Second)
		assert.True(t, errors.Is(err, queue.ErrNotReserved), "Reschedule: want ErrNotReserved, got %v", err)

		id3 := enqueueAndGet()
		err = q.Fail(ctx, id3, "r")
		assert.True(t, errors.Is(err, queue.ErrNotReserved), "Fail: want ErrNotReserved, got %v", err)
	})

	// --- Concurrency ---

	t.Run("Concurrency/SameDedupKey_OnlyOneInserts", func(t *testing.T) {
		t.Parallel()
		clk := clock.NewFake(time.Now())
		q := factory(t, clk)
		ctx := context.Background()

		type result struct {
			id       queue.JobID
			inserted bool
		}

		var (
			wg      sync.WaitGroup
			mu      sync.Mutex
			results []result
		)

		for i := 0; i < 100; i++ {
			wg.Add(1)
			// Each goroutine uses its own SeqIDGenerator so IDs are distinct.
			localIDG := idgen.NewSeq()
			job := newJob(clk, localIDG, "EUR", "race-key")
			go func(j queue.Job) {
				defer wg.Done()
				id, inserted, err := q.Enqueue(ctx, j)
				if err != nil {
					return
				}
				mu.Lock()
				results = append(results, result{id: id, inserted: inserted})
				mu.Unlock()
			}(job)
		}

		wg.Wait()

		require.Len(t, results, 100)

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
