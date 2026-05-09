// Package worker provides the background job processing loop that reserves
// jobs from the queue, processes them, and handles completion, rescheduling,
// or failure based on the result.
package worker

import (
	"context"
	"fmt"
	"os"
	"sync/atomic"
	"time"

	"currency-exchange/internal/backoff"
	"currency-exchange/internal/clock"
	"currency-exchange/internal/queue"
)

// Option is a functional option for configuring a Worker.
type Option func(*Worker)

// Worker polls a JobQueue at a fixed interval and processes eligible jobs.
type Worker struct {
	q             queue.JobQueue
	cleaner       queue.Cleaner
	clk           clock.Clock
	pollInterval  time.Duration
	leaseTime     time.Duration
	maxAttempts   int
	batchSize     int
	cleanInterval time.Duration

	// lastIterationUnixNano stores the wall-clock time of the most recent loop
	// iteration as Unix nanoseconds. Updated on every poll-tick or clean-tick;
	// read by /readyz' worker checker via LastIteration. atomic.Int64 keeps
	// the read/write race-free without a mutex.
	lastIterationUnixNano atomic.Int64
}

// WithPollInterval sets the interval between queue polls.
func WithPollInterval(d time.Duration) Option {
	return func(w *Worker) { w.pollInterval = d }
}

// WithLeaseTime sets the duration for which a reserved job is locked.
func WithLeaseTime(d time.Duration) Option {
	return func(w *Worker) { w.leaseTime = d }
}

// WithMaxAttempts sets the maximum number of attempts before a job is failed.
func WithMaxAttempts(n int) Option {
	return func(w *Worker) { w.maxAttempts = n }
}

// WithBatchSize sets the number of jobs reserved per poll tick.
func WithBatchSize(n int) Option {
	return func(w *Worker) { w.batchSize = n }
}

// WithCleanInterval sets the interval between RecoverExpired calls.
func WithCleanInterval(d time.Duration) Option {
	return func(w *Worker) { w.cleanInterval = d }
}

// New constructs a Worker with the given queue, cleaner, clock, and options.
// Default values: pollInterval=5s, leaseTime=60s, maxAttempts=5, batchSize=1,
// cleanInterval=60s.
func New(q queue.JobQueue, cleaner queue.Cleaner, clk clock.Clock, opts ...Option) *Worker {
	w := &Worker{
		q:             q,
		cleaner:       cleaner,
		clk:           clk,
		pollInterval:  5 * time.Second,
		leaseTime:     60 * time.Second,
		maxAttempts:   5,
		batchSize:     1,
		cleanInterval: 60 * time.Second,
	}
	for _, opt := range opts {
		opt(w)
	}
	return w
}

// Run starts the worker loop. It returns when ctx is cancelled, returning
// ctx.Err().
func (w *Worker) Run(ctx context.Context) error {
	pollTicker := time.NewTicker(w.pollInterval)
	cleanTicker := time.NewTicker(w.cleanInterval)
	defer pollTicker.Stop()
	defer cleanTicker.Stop()

	for {
		select {
		case <-pollTicker.C:
			w.lastIterationUnixNano.Store(time.Now().UnixNano())
			jobs, err := w.q.Reserve(ctx, w.batchSize, w.leaseTime)
			if err != nil {
				fmt.Fprintf(os.Stderr, "worker: reserve error: %v\n", err)
				continue
			}
			for _, job := range jobs {
				if err := w.processJob(ctx, job); err != nil {
					if job.Attempts < w.maxAttempts {
						delay := backoff.Compute(job.Attempts)
						if rErr := w.q.Reschedule(ctx, job.ID, err.Error(), delay); rErr != nil {
							fmt.Fprintf(os.Stderr, "worker: reschedule error: %v\n", rErr)
						}
					} else {
						if fErr := w.q.Fail(ctx, job.ID, err.Error()); fErr != nil {
							fmt.Fprintf(os.Stderr, "worker: fail error: %v\n", fErr)
						}
					}
				} else {
					if cErr := w.q.Complete(ctx, job.ID); cErr != nil {
						fmt.Fprintf(os.Stderr, "worker: complete error: %v\n", cErr)
					}
				}
			}
		case <-cleanTicker.C:
			w.lastIterationUnixNano.Store(time.Now().UnixNano())
			if _, err := w.cleaner.RecoverExpired(ctx); err != nil {
				fmt.Fprintf(os.Stderr, "worker: recover expired error: %v\n", err)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// processJob executes the work for a single job. This is a stub that always
// returns nil; it will be wired to a RatesProvider in Stage 3.
func (w *Worker) processJob(_ context.Context, _ queue.Job) error {
	return nil
}

// LastIteration returns the wall-clock time of the most recent loop iteration,
// or the zero time if Run has not yet observed a tick. Used by the /readyz
// worker checker to detect a stalled worker. Safe for concurrent use.
func (w *Worker) LastIteration() time.Time {
	ns := w.lastIterationUnixNano.Load()
	if ns == 0 {
		return time.Time{}
	}
	return time.Unix(0, ns)
}
