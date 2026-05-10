package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/backoff"
	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/memqueue"
	"currency-exchange/internal/quoterepo/memquoterepo"
	"currency-exchange/internal/ratesprovider"
	"currency-exchange/internal/ratesprovider/fake"
	"currency-exchange/internal/worker"
)

// pairEURMXN is the canonical test pair used across worker tests.
var pairEURMXN = ratesprovider.Pair{Base: "EUR", Quote: "MXN"}

// happyFake returns a *fake.Fake pre-loaded with one quote for pairEURMXN at a
// deterministic price. Used by tests that only care about the non-dispatch
// behaviour of the worker (lifecycle, metrics, heartbeat).
func happyFake(clk clock.Clock) *fake.Fake {
	return &fake.Fake{
		Clock: clk,
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{
			pairEURMXN: {
				Pair:  pairEURMXN,
				Price: decimal.NewFromFloat(20.255648),
			},
		},
	}
}

// enqueueJob is a test helper that enqueues a single job and returns its ID.
func enqueueJob(t *testing.T, q queue.JobQueue, job queue.Job) queue.JobID {
	t.Helper()
	id, _, err := q.Enqueue(context.Background(), job)
	require.NoError(t, err)
	return id
}

// TestWorker_StopsOnContextCancel asserts that Run returns promptly when the
// context is cancelled, and that the returned error is nil or context.Canceled.
func TestWorker_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)
	w := worker.New(q, q, happyFake(clk), memquoterepo.New(), clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithCleanInterval(1*time.Millisecond),
	)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // cancel immediately

	done := make(chan error, 1)
	go func() { done <- w.Run(ctx) }()

	select {
	case err := <-done:
		require.True(t,
			err == nil || err == context.Canceled,
			"expected nil or context.Canceled, got %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not return within 200ms after context cancellation")
	}
}

// TestWorker_ReservesAndCompletesJob asserts that the worker reserves an
// eligible job and marks it complete so it is no longer pending afterward.
func TestWorker_ReservesAndCompletesJob(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)

	gen := idgen.NewSeq()
	enqueueJob(t, q, queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "EUR",
		Quote:     "MXN",
		NextRunAt: clk.Now(), // already eligible
	})

	w := worker.New(q, q, happyFake(clk), memquoterepo.New(), clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithLeaseTime(1*time.Second),
		worker.WithBatchSize(1),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	// After the worker has run, the job should have been completed — Reserve
	// must return an empty slice.
	jobs, err := q.Reserve(context.Background(), 1, time.Second)
	require.NoError(t, err)
	require.Empty(t, jobs, "expected job to be completed, but it was still reservable")
}

// spyCleaner wraps a queue.Cleaner and counts RecoverExpired calls.
type spyCleaner struct {
	queue.Cleaner
	calls int
}

func (s *spyCleaner) RecoverExpired(ctx context.Context) (int, error) {
	s.calls++
	return s.Cleaner.RecoverExpired(ctx)
}

// TestWorker_CallsRecoverExpired asserts that the worker invokes RecoverExpired
// at least once during a short run driven by a 1ms clean interval.
func TestWorker_CallsRecoverExpired(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)
	spy := &spyCleaner{Cleaner: q}

	w := worker.New(q, spy, happyFake(clk), memquoterepo.New(), clk,
		worker.WithCleanInterval(1*time.Millisecond),
		worker.WithPollInterval(1*time.Second), // large — keep poll out of the way
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	require.GreaterOrEqual(t, spy.calls, 1,
		"expected RecoverExpired to be called at least once")
}

// TestWorker_InstrumentsLifecycle asserts the worker advances the right
// metric counters during a single happy-path job execution. Verifies two
// regressions worth catching: WorkerIterationsTotal not advancing (loop
// liveness alert breaks) and QuoteJobsTotal{done} not advancing (terminal-
// state accounting breaks).
//
// NOT t.Parallel'd: counters are global singletons; running concurrently
// with TestWorker_ReservesAndCompletesJob (parallel) would also bump
// QuoteJobsTotal{done}, breaking the exact +1 delta. As a non-parallel
// top-level test it runs in the serial phase before the parallel batch.
func TestWorker_InstrumentsLifecycle(t *testing.T) {
	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)

	gen := idgen.NewSeq()
	enqueueJob(t, q, queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "EUR",
		Quote:     "MXN",
		NextRunAt: clk.Now(),
	})

	w := worker.New(q, q, happyFake(clk), memquoterepo.New(), clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithCleanInterval(1*time.Second), // keep clean out of the way
		worker.WithBatchSize(1),
	)

	okBefore := testutil.ToFloat64(obs.WorkerIterationsTotal.WithLabelValues("ok"))
	doneBefore := testutil.ToFloat64(obs.QuoteJobsTotal.WithLabelValues("done"))

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	okAfter := testutil.ToFloat64(obs.WorkerIterationsTotal.WithLabelValues("ok"))
	doneAfter := testutil.ToFloat64(obs.QuoteJobsTotal.WithLabelValues("done"))

	require.Greater(t, okAfter, okBefore,
		"WorkerIterationsTotal{outcome=ok} should advance during the run")
	require.Equal(t, doneBefore+1, doneAfter,
		"QuoteJobsTotal{status=done} should advance by exactly 1 (one job completed)")
}

// TestWorker_LastIterationUpdates asserts the worker exposes a heartbeat
// (LastIteration time) that starts at zero, becomes non-zero once Run has
// observed a tick, and is recent. The /readyz worker check depends on this.
func TestWorker_LastIterationUpdates(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)
	w := worker.New(q, q, happyFake(clk), memquoterepo.New(), clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithCleanInterval(1*time.Millisecond),
	)

	require.True(t, w.LastIteration().IsZero(),
		"before Run: LastIteration should be zero")

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	last := w.LastIteration()
	require.False(t, last.IsZero(),
		"after Run: LastIteration should be set by at least one tick")
	require.WithinDuration(t, time.Now(), last, 500*time.Millisecond,
		"LastIteration should be recent (within 500ms of now)")
}

// ---------------------------------------------------------------------------
// Stage 3 dispatch tests
// ---------------------------------------------------------------------------

// TestWorker_DispatchesSuccessToUpsertAndComplete verifies the happy-path
// dispatch: when the provider returns a quote for the requested pair, the
// worker upserts it into the repo and marks the job done.
func TestWorker_DispatchesSuccessToUpsertAndComplete(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	repo := memquoterepo.New()

	expectedPrice := decimal.NewFromFloat(20.255648)
	provider := &fake.Fake{
		Clock: clk,
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{
			pairEURMXN: {
				Pair:  pairEURMXN,
				Price: expectedPrice,
			},
		},
	}

	gen := idgen.NewSeq()
	enqueueJob(t, q, queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "EUR",
		Quote:     "MXN",
		NextRunAt: clk.Now(),
	})

	w := worker.New(q, q, provider, repo, clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithLeaseTime(1*time.Second),
		worker.WithBatchSize(1),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	// The quote should have been upserted with the expected price and FetchedAt.
	got, ok := repo.Get(pairEURMXN)
	require.True(t, ok, "quote for EUR/MXN must be present in the repo after successful job")
	require.True(t, got.Price.Equal(expectedPrice),
		"upserted price %s must equal %s", got.Price, expectedPrice)
	require.Equal(t, t0, got.FetchedAt,
		"upserted FetchedAt must equal the fake clock's time")

	// The job must be in terminal done state — Reserve returns empty.
	jobs, err := q.Reserve(context.Background(), 1, time.Second)
	require.NoError(t, err)
	require.Empty(t, jobs, "job must be done (not re-reservable) after successful upsert")
}

// TestWorker_DispatchesMissingToPermanentFail verifies that when the provider
// reports the requested pair as missing (silent drop), the worker fails the job
// permanently without upserting anything.
func TestWorker_DispatchesMissingToPermanentFail(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)
	repo := memquoterepo.New()

	// Empty Quotes map → every requested pair ends up in FetchResult.Missing.
	provider := &fake.Fake{
		Clock:  clk,
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{},
	}

	gen := idgen.NewSeq()
	enqueueJob(t, q, queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "USD",
		Quote:     "ZZZ",
		Attempts:  0,
		NextRunAt: clk.Now(),
	})

	w := worker.New(q, q, provider, repo, clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithLeaseTime(1*time.Second),
		worker.WithMaxAttempts(5),
		worker.WithBatchSize(1),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	// No upsert should have happened.
	require.Equal(t, 0, repo.Len(),
		"repo must be empty: missing pair must not be upserted")

	// The job must be in permanently failed state — not re-reservable even
	// after advancing the clock well past any plausible reschedule time.
	clk.Advance(10 * time.Minute)
	jobs, err := q.Reserve(context.Background(), 1, time.Second)
	require.NoError(t, err)
	require.Empty(t, jobs,
		"job must be permanently failed (not re-reservable) after missing-pair dispatch")

}

// TestWorker_DispatchesTransientErrorToReschedule verifies that a transient
// provider error causes the worker to reschedule the job with an exponential
// backoff delay, leaving it pending for a future attempt.
//
// This test closes the deferred TestWorker_ReschedulesOnProcessError stub that
// was commented out in the original worker_test.go (Stage 3 wiring landed here).
func TestWorker_DispatchesTransientErrorToReschedule(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	repo := memquoterepo.New()

	provider := &fake.Fake{
		BatchError: &ratesprovider.ProviderError{Code: "transient", Message: "upstream timeout"},
	}

	gen := idgen.NewSeq()
	enqueueJob(t, q, queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "EUR",
		Quote:     "MXN",
		Attempts:  0,
		NextRunAt: clk.Now(),
	})

	w := worker.New(q, q, provider, repo, clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithLeaseTime(1*time.Second),
		worker.WithMaxAttempts(3),
		worker.WithBatchSize(1),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	// No upsert on a transient error.
	require.Equal(t, 0, repo.Len(),
		"repo must be empty after transient error")

	// The job must be rescheduled (pending), not failed. backoff.Compute(0)
	// returns a duration in [0, 1s). Advance the clock by 1s (the full window)
	// to guarantee NextRunAt has elapsed, then verify the job is re-reservable.
	clk.Advance(backoff.Compute(0) + time.Second)
	jobs, err := q.Reserve(context.Background(), 1, time.Second)
	require.NoError(t, err)
	require.Len(t, jobs, 1,
		"job must be pending (re-reservable) after transient-error reschedule")
	require.Equal(t, 1, jobs[0].Attempts,
		"Attempts must be incremented to 1 after the first reschedule")
}

// TestWorker_DispatchesPermanentErrorToImmediateFail verifies that a permanent
// provider error causes the worker to fail the job immediately, without
// upserting or consuming any retry budget.
func TestWorker_DispatchesPermanentErrorToImmediateFail(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)
	repo := memquoterepo.New()

	provider := &fake.Fake{
		BatchError: &ratesprovider.ProviderError{Code: "permanent", Message: "invalid api key"},
	}

	gen := idgen.NewSeq()
	enqueueJob(t, q, queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "EUR",
		Quote:     "MXN",
		Attempts:  0,
		NextRunAt: clk.Now(),
	})

	w := worker.New(q, q, provider, repo, clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithLeaseTime(1*time.Second),
		worker.WithMaxAttempts(3), // well above current attempts; must not matter
		worker.WithBatchSize(1),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	// No upsert.
	require.Equal(t, 0, repo.Len(),
		"repo must be empty after permanent error")

	// Job must be in terminal failed state — not re-reservable even after a
	// generous clock advance (ensures no reschedule occurred either).
	clk.Advance(10 * time.Minute)
	jobs, err := q.Reserve(context.Background(), 1, time.Second)
	require.NoError(t, err)
	require.Empty(t, jobs,
		"job must be permanently failed after permanent provider error")
}

// TestWorker_QuotaExceeded_ReschedulesPlusOneHour verifies that a
// quota_exceeded error causes the worker to reschedule the job exactly one hour
// into the future, distinct from the exponential backoff used for generic
// transient errors.
func TestWorker_QuotaExceeded_ReschedulesPlusOneHour(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	repo := memquoterepo.New()

	provider := &fake.Fake{
		BatchError: &ratesprovider.ProviderError{Code: "quota_exceeded", Message: "monthly quota exhausted"},
	}

	gen := idgen.NewSeq()
	enqueueJob(t, q, queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "EUR",
		Quote:     "MXN",
		Attempts:  0,
		NextRunAt: clk.Now(),
	})

	w := worker.New(q, q, provider, repo, clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithLeaseTime(1*time.Second),
		worker.WithBatchSize(1),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	// No upsert.
	require.Equal(t, 0, repo.Len(),
		"repo must be empty after quota_exceeded error")

	// Job must NOT be reservable before 1h has elapsed (distinguishes from
	// backoff.Compute(0) which is at most ~1s).
	clk.Advance(59 * time.Minute)
	jobs, err := q.Reserve(context.Background(), 1, time.Second)
	require.NoError(t, err)
	require.Empty(t, jobs,
		"job must not be reservable before the 1h quota window has elapsed")

	// Advance past the 1h mark — job must become re-reservable.
	clk.Advance(2 * time.Minute) // total advance = 61 minutes
	jobs, err = q.Reserve(context.Background(), 1, time.Second)
	require.NoError(t, err)
	require.Len(t, jobs, 1,
		"job must be re-reservable after 1h quota reschedule window")
}

// TestWorker_AttemptBudgetExhausted_TransientBecomesFail verifies that when a
// job's Attempts count already equals the worker's maxAttempts, a transient
// error causes the worker to permanently fail the job instead of rescheduling.
func TestWorker_AttemptBudgetExhausted_TransientBecomesFail(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)
	repo := memquoterepo.New()

	provider := &fake.Fake{
		BatchError: &ratesprovider.ProviderError{Code: "transient", Message: "upstream timeout"},
	}

	gen := idgen.NewSeq()
	enqueueJob(t, q, queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "EUR",
		Quote:     "MXN",
		Attempts:  2, // already at the budget limit for WithMaxAttempts(2)
		NextRunAt: clk.Now(),
	})

	w := worker.New(q, q, provider, repo, clk,
		worker.WithPollInterval(1*time.Millisecond),
		worker.WithLeaseTime(1*time.Second),
		worker.WithMaxAttempts(2),
		worker.WithBatchSize(1),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	// No upsert.
	require.Equal(t, 0, repo.Len(),
		"repo must be empty when budget is exhausted")

	// Job must be permanently failed — no reschedule should have occurred even
	// after a generous clock advance.
	clk.Advance(10 * time.Minute)
	jobs, err := q.Reserve(context.Background(), 1, time.Second)
	require.NoError(t, err)
	require.Empty(t, jobs,
		"job must be permanently failed when attempt budget is exhausted")
}
