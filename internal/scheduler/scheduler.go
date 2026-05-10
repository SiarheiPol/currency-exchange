// Package scheduler enqueues one refresh job per configured currency pair on
// every tick interval. An initial tick fires immediately on Run so the queue
// is populated before the first periodic tick fires.
package scheduler

import (
	"context"
	"sync"
	"time"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/ratesprovider"
)

// Option is a functional option for configuring a Scheduler.
type Option func(*Scheduler)

// Scheduler enqueues refresh jobs for all configured pairs on each tick.
type Scheduler struct {
	interval   time.Duration
	bucketSize time.Duration
	pairs      []ratesprovider.Pair
	queue      queue.JobQueue
	clk        clock.Clock
	idgen      idgen.IDGenerator

	lastTickMu sync.Mutex
	lastTick   time.Time
}

// WithInterval sets the interval between ticks.
func WithInterval(d time.Duration) Option { return func(s *Scheduler) { s.interval = d } }

// WithBucketSize sets the bucket window used for dedup key computation.
func WithBucketSize(d time.Duration) Option { return func(s *Scheduler) { s.bucketSize = d } }

// WithPairs sets the currency pairs to enqueue on each tick.
func WithPairs(p []ratesprovider.Pair) Option { return func(s *Scheduler) { s.pairs = p } }

// WithQueue sets the job queue the scheduler enqueues into.
func WithQueue(q queue.JobQueue) Option { return func(s *Scheduler) { s.queue = q } }

// WithClock sets the clock used to stamp jobs and compute dedup keys.
func WithClock(c clock.Clock) Option { return func(s *Scheduler) { s.clk = c } }

// WithIDGen sets the ID generator used to assign job IDs.
func WithIDGen(g idgen.IDGenerator) Option { return func(s *Scheduler) { s.idgen = g } }

// New constructs a Scheduler with the given options. Callers must supply
// positive Interval and BucketSize via WithInterval and WithBucketSize; Run
// will panic on time.NewTicker(0) or panic on a divide-by-zero in DedupKey
// otherwise.
func New(opts ...Option) *Scheduler {
	s := &Scheduler{}
	for _, o := range opts {
		o(s)
	}
	return s
}

// Run fires an immediate bootstrap tick and then ticks at the configured
// interval until ctx is cancelled. Returns ctx.Err() when done.
func (s *Scheduler) Run(ctx context.Context) error {
	if err := s.Tick(ctx); err != nil {
		return err
	}
	ticker := time.NewTicker(s.interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if err := s.Tick(ctx); err != nil {
				obs.LogSchedulerTickFailed(ctx, err)
			}
		}
	}
}

// Tick enqueues one refresh job per configured pair for the current bucket
// window and records the tick time in LastTick.
func (s *Scheduler) Tick(ctx context.Context) error {
	now := s.clk.Now()
	for _, p := range s.pairs {
		id := queue.JobID(s.idgen.NewID())
		dedup := queue.DedupKey(p.Base, p.Quote, now, s.bucketSize)
		job := queue.Job{
			ID:        id,
			Base:      p.Base,
			Quote:     p.Quote,
			DedupKey:  dedup,
			NextRunAt: now,
		}
		if _, _, err := s.queue.Enqueue(ctx, job); err != nil {
			return err
		}
	}
	s.lastTickMu.Lock()
	s.lastTick = now
	s.lastTickMu.Unlock()
	obs.SchedulerTicksTotal.Inc()
	return nil
}

// LastTick returns the wall-clock time of the most recent Tick call, or the
// zero time if Tick has not yet been called. Safe for concurrent use.
func (s *Scheduler) LastTick() time.Time {
	s.lastTickMu.Lock()
	defer s.lastTickMu.Unlock()
	return s.lastTick
}
