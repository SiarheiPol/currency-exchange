package worker_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/memqueue"
	"currency-exchange/internal/worker"
)

// TestWorker_StopsOnContextCancel asserts that Run returns promptly when the
// context is cancelled, and that the returned error is nil or context.Canceled.
func TestWorker_StopsOnContextCancel(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)
	w := worker.New(q, q, clk,
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
	_, _, err := q.Enqueue(context.Background(), queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "EUR",
		Quote:     "MXN",
		NextRunAt: clk.Now(), // already eligible
	})
	require.NoError(t, err)

	w := worker.New(q, q, clk,
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

	w := worker.New(q, spy, clk,
		worker.WithCleanInterval(1*time.Millisecond),
		worker.WithPollInterval(1*time.Second), // large — keep poll out of the way
	)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	_ = w.Run(ctx)

	require.GreaterOrEqual(t, spy.calls, 1,
		"expected RecoverExpired to be called at least once")
}

// TestWorker_ReschedulesOnProcessError is deferred to Stage 3.
// processJob is a stub (returns nil) in the skeleton; there is no seam
// to inject a process error until RatesProvider is wired in Stage 3.

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
	_, _, err := q.Enqueue(context.Background(), queue.Job{
		ID:        queue.JobID(gen.NewID()),
		Base:      "EUR",
		Quote:     "MXN",
		NextRunAt: clk.Now(),
	})
	require.NoError(t, err)

	w := worker.New(q, q, clk,
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
	w := worker.New(q, q, clk,
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
