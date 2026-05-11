// Package worker provides the background job processing loop that reserves
// jobs from the queue, processes them, and handles completion, rescheduling,
// or failure based on the result.
package worker

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	"currency-exchange/internal/backoff"
	"currency-exchange/internal/clock"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/quoterepo"
	"currency-exchange/internal/ratesprovider"
)

// Option is a functional option for configuring a Worker.
type Option func(*Worker)

// Worker polls a JobQueue at a fixed interval and processes eligible jobs.
type Worker struct {
	q             queue.JobQueue
	cleaner       queue.Cleaner
	provider      ratesprovider.RatesProvider
	repo          quoterepo.QuoteRepo
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

// New constructs a Worker with the given queue, cleaner, rates provider, quote
// repo, clock, and options. Default values: leaseTime=60s, maxAttempts=5,
// cleanInterval=60s. WithPollInterval and WithBatchSize are required; New
// panics if either is omitted.
func New(
	q queue.JobQueue,
	cleaner queue.Cleaner,
	provider ratesprovider.RatesProvider,
	repo quoterepo.QuoteRepo,
	clk clock.Clock,
	opts ...Option,
) *Worker {
	w := &Worker{
		q:             q,
		cleaner:       cleaner,
		provider:      provider,
		repo:          repo,
		clk:           clk,
		leaseTime:     60 * time.Second,
		maxAttempts:   5,
		cleanInterval: 60 * time.Second,
	}
	for _, opt := range opts {
		opt(w)
	}
	if w.pollInterval <= 0 {
		panic("worker.New: WithPollInterval option is required")
	}
	if w.batchSize <= 0 {
		panic("worker.New: WithBatchSize option is required")
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
				obs.LogWorkerOpFailed(ctx, "reserve", err)
				obs.WorkerIterationsTotal.WithLabelValues("error").Inc()
				continue
			}
			if len(jobs) == 0 {
				obs.WorkerIterationsTotal.WithLabelValues("idle").Inc()
			} else {
				obs.WorkerIterationsTotal.WithLabelValues("work").Inc()
				for _, job := range jobs {
					obs.LogJobReserved(ctx, string(job.ID), job.Base, job.Quote)
				}
				w.dispatchBatch(ctx, jobs, time.Now())
			}
		case <-cleanTicker.C:
			w.lastIterationUnixNano.Store(time.Now().UnixNano())
			if _, err := w.cleaner.RecoverExpired(ctx); err != nil {
				obs.LogWorkerOpFailed(ctx, "recover_expired", err)
				obs.WorkerIterationsTotal.WithLabelValues("error").Inc()
			} else {
				obs.WorkerIterationsTotal.WithLabelValues("ok").Inc()
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// dispatchBatch fetches quotes for all jobs in the batch with a single
// FetchPairs call, then demultiplexes the result per job.
func (w *Worker) dispatchBatch(ctx context.Context, jobs []queue.Job, startedAt time.Time) {
	// Build the pairs slice for the single FetchPairs call.
	pairs := make([]ratesprovider.Pair, len(jobs))
	for i, job := range jobs {
		pairs[i] = ratesprovider.Pair{Base: job.Base, Quote: job.Quote}
	}

	res, err := w.provider.FetchPairs(ctx, pairs)
	if err != nil {
		// Batch-level error: apply to all jobs.
		for _, job := range jobs {
			w.handleBatchError(ctx, job, err)
		}
		return
	}

	// Demux: process each job according to whether its pair was returned or missing.
	for _, job := range jobs {
		pair := ratesprovider.Pair{Base: job.Base, Quote: job.Quote}
		if q, ok := res.Quotes[pair]; ok {
			// Happy path: upsert then complete.
			if uErr := w.repo.UpsertBatch(ctx, []ratesprovider.Quote{q}); uErr != nil {
				wrapped := fmt.Errorf("upsert quote: %w", uErr)
				w.rescheduleOrFail(ctx, job, wrapped)
				continue
			}
			if cErr := w.q.Complete(ctx, job.ID); cErr != nil {
				obs.LogWorkerOpFailed(ctx, "complete", cErr)
			} else {
				attempts := job.Attempts + 1
				obs.LogJobCompleted(ctx, string(job.ID), job.Base, job.Quote, time.Since(startedAt))
				obs.QuoteJobsTotal.WithLabelValues("done").Inc()
				obs.QuoteJobsAttempts.Observe(float64(attempts))
				// Observe end-to-end SLI latency only for jobs completing on their
				// first attempt (Attempts==0 is the pre-increment value returned by
				// Reserve). Retried jobs (Attempts>0) skew the SLI distribution
				// because their created_at reflects the original enqueue, not the
				// current dispatch cycle; per contract Flag 3.
				if job.Attempts == 0 {
					obs.QuoteJobsCompletionSeconds.WithLabelValues(job.Source).Observe(
						w.clk.Now().Sub(job.CreatedAt).Seconds(),
					)
				}
			}
		} else {
			// pair is in res.Missing (or absent from both — defensive): permanent fail.
			w.failJob(ctx, job, errors.New("missing in upstream response"))
		}
	}
}

// handleBatchError classifies the provider error and either reschedules or
// fails the job according to its Code and the attempt budget.
func (w *Worker) handleBatchError(ctx context.Context, job queue.Job, err error) {
	var pe *ratesprovider.ProviderError
	if !errors.As(err, &pe) {
		// Unknown error type — treat as transient.
		pe = &ratesprovider.ProviderError{Code: "transient", Message: err.Error()}
	}

	switch pe.Code {
	case "quota_exceeded":
		const quotaExceededDelay = time.Hour
		retryAt := w.clk.Now().Add(quotaExceededDelay)
		if rErr := w.q.Reschedule(ctx, job.ID, pe.Error(), quotaExceededDelay); rErr != nil {
			obs.LogWorkerOpFailed(ctx, "reschedule", rErr)
		} else {
			attempts := job.Attempts + 1
			obs.LogJobRescheduled(ctx, string(job.ID), job.Base, job.Quote, attempts, quotaExceededDelay)
			obs.LogProviderQuotaExceeded(ctx, "apilayer", retryAt)
		}

	case "transient":
		w.rescheduleOrFail(ctx, job, pe)

	default:
		// "permanent" or any unrecognised code — fail immediately.
		w.failJob(ctx, job, pe)
	}
}

// rescheduleOrFail reschedules the job with backoff if the attempt budget
// permits, otherwise fails it permanently.
func (w *Worker) rescheduleOrFail(ctx context.Context, job queue.Job, err error) {
	if job.Attempts < w.maxAttempts {
		delay := backoff.Compute(job.Attempts)
		if rErr := w.q.Reschedule(ctx, job.ID, err.Error(), delay); rErr != nil {
			obs.LogWorkerOpFailed(ctx, "reschedule", rErr)
		} else {
			attempts := job.Attempts + 1
			obs.LogJobRescheduled(ctx, string(job.ID), job.Base, job.Quote, attempts, delay)
		}
	} else {
		w.failJob(ctx, job, err)
	}
}

// failJob calls queue.Fail and emits the appropriate obs calls.
func (w *Worker) failJob(ctx context.Context, job queue.Job, err error) {
	if fErr := w.q.Fail(ctx, job.ID, err.Error()); fErr != nil {
		obs.LogWorkerOpFailed(ctx, "fail", fErr)
	} else {
		attempts := job.Attempts + 1
		obs.LogJobFailed(ctx, string(job.ID), job.Base, job.Quote, attempts, err)
		obs.QuoteJobsTotal.WithLabelValues("failed").Inc()
		obs.QuoteJobsAttempts.Observe(float64(attempts))
	}
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
