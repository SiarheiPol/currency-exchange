// Package memqueue is an in-memory implementation of queue.JobQueue used as
// the unit-test fake for higher-layer code (worker, scheduler, handlers).
package memqueue

import (
	"context"
	"sort"
	"sync"
	"time"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue"
)

type status int

const (
	statusPending status = iota
	statusRunning
	statusDone
	statusFailed
)

// Queue is a thread-safe in-memory queue.JobQueue implementation.
type Queue struct {
	clock clock.Clock
	mu    sync.Mutex
	jobs  map[queue.JobID]*record
	dedup map[string]queue.JobID
}

type record struct {
	job        queue.Job
	status     status
	leaseUntil time.Time
}

var _ queue.JobQueue = (*Queue)(nil)
var _ queue.Cleaner = (*Queue)(nil)

// New returns a Queue using the given clock for time decisions.
func New(c clock.Clock) *Queue {
	return &Queue{
		clock: c,
		jobs:  make(map[queue.JobID]*record),
		dedup: make(map[string]queue.JobID),
	}
}

// Enqueue inserts j or, if a job with the same DedupKey already exists,
// returns its id with inserted=false.
func (q *Queue) Enqueue(ctx context.Context, j queue.Job) (queue.JobID, bool, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if j.DedupKey != "" {
		if existingID, ok := q.dedup[j.DedupKey]; ok {
			obs.CoalescingCollapsedTotal.Inc()
			obs.LogCoalescingCollapsed(ctx, j.Currency, j.DedupKey)
			return existingID, false, nil
		}
	}

	q.jobs[j.ID] = &record{
		job:    j,
		status: statusPending,
	}
	if j.DedupKey != "" {
		q.dedup[j.DedupKey] = j.ID
	}
	return j.ID, true, nil
}

// Reserve marks up to n pending jobs as running with the given lease duration
// and returns value-copies of them.
func (q *Queue) Reserve(_ context.Context, n int, lease time.Duration) ([]queue.Job, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.clock.Now()

	var eligible []*record
	for _, r := range q.jobs {
		if r.status != statusPending {
			continue
		}
		if r.job.NextRunAt.Compare(now) <= 0 {
			eligible = append(eligible, r)
		}
	}

	sort.Slice(eligible, func(i, j int) bool {
		return eligible[i].job.NextRunAt.Before(eligible[j].job.NextRunAt)
	})

	if len(eligible) > n {
		eligible = eligible[:n]
	}

	result := make([]queue.Job, 0, len(eligible))
	for _, r := range eligible {
		r.status = statusRunning
		r.leaseUntil = now.Add(lease)
		result = append(result, r.job)
	}
	return result, nil
}

// Complete marks the job done. Returns ErrNotFound if no job has the given id,
// or ErrNotReserved if the job is not currently running.
func (q *Queue) Complete(_ context.Context, id queue.JobID) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	r, ok := q.jobs[id]
	if !ok {
		return queue.ErrNotFound
	}
	if r.status != statusRunning {
		return queue.ErrNotReserved
	}
	r.status = statusDone
	return nil
}

// Reschedule returns the job to pending with NextRunAt = now + after and
// increments Attempts. Returns ErrNotFound or ErrNotReserved as appropriate.
func (q *Queue) Reschedule(_ context.Context, id queue.JobID, _ string, after time.Duration) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	r, ok := q.jobs[id]
	if !ok {
		return queue.ErrNotFound
	}
	if r.status != statusRunning {
		return queue.ErrNotReserved
	}
	r.status = statusPending
	r.job.NextRunAt = q.clock.Now().Add(after)
	r.job.Attempts++
	r.leaseUntil = time.Time{}
	return nil
}

// RecoverExpired resets statusRunning records with an expired lease back to
// statusPending. Returns the count of recovered records.
func (q *Queue) RecoverExpired(_ context.Context) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()

	now := q.clock.Now()
	count := 0
	for _, r := range q.jobs {
		if r.status == statusRunning && r.leaseUntil.Before(now) {
			r.status = statusPending
			r.leaseUntil = time.Time{}
			count++
		}
	}
	return count, nil
}

// Fail marks the job permanently failed. Returns ErrNotFound or ErrNotReserved
// as appropriate.
func (q *Queue) Fail(_ context.Context, id queue.JobID, _ string) error {
	q.mu.Lock()
	defer q.mu.Unlock()

	r, ok := q.jobs[id]
	if !ok {
		return queue.ErrNotFound
	}
	if r.status != statusRunning {
		return queue.ErrNotReserved
	}
	r.status = statusFailed
	return nil
}
