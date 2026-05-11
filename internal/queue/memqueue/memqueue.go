// Package memqueue is an in-memory implementation of queue.JobQueue used as
// the unit-test fake for higher-layer code (worker, scheduler, handlers).
package memqueue

import (
	"context"
	"sort"
	"sync"
	"time"

	"github.com/shopspring/decimal"

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
	job            queue.Job
	status         status
	leaseUntil     time.Time
	createdAt      time.Time
	completedAt    time.Time // zero unless Complete or Fail was called
	price          decimal.Decimal
	quoteUpdatedAt time.Time
	lastError      string // empty unless Fail was called
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
// returns its id with inserted=false. Returns ErrInvalidSource if j.Source is
// not "refresh" or "scheduler".
func (q *Queue) Enqueue(ctx context.Context, j queue.Job) (queue.JobID, bool, error) {
	if j.Source != "refresh" && j.Source != "scheduler" {
		return "", false, queue.ErrInvalidSource
	}

	q.mu.Lock()
	defer q.mu.Unlock()

	if j.DedupKey != "" {
		if existingID, ok := q.dedup[j.DedupKey]; ok {
			obs.CoalescingCollapsedTotal.Inc()
			obs.LogCoalescingCollapsed(ctx, string(existingID), j.Base, j.Quote)
			return existingID, false, nil
		}
	}

	now := q.clock.Now()
	q.jobs[j.ID] = &record{
		job:       j,
		status:    statusPending,
		createdAt: now,
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
		// Return a copy of the job with queue-owned fields populated.
		j := r.job
		j.CreatedAt = r.createdAt
		result = append(result, j)
	}
	return result, nil
}

// Complete marks the job done and persists the fetched quote snapshot.
// Returns ErrNotFound if no job has the given id, or ErrNotReserved if the
// job is not currently running.
func (q *Queue) Complete(_ context.Context, id queue.JobID, price decimal.Decimal, quoteUpdatedAt time.Time) error {
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
	r.completedAt = q.clock.Now()
	r.price = price
	r.quoteUpdatedAt = quoteUpdatedAt
	return nil
}

// ReadBack is a test-time backdoor returning the persisted price,
// quote_updated_at, and status for the given job. It exists so the shared
// queue contract test can verify the denormalized snapshot without adding a
// public GetByID method to JobQueue. Returns ErrNotFound if no job has the
// given id.
func (q *Queue) ReadBack(_ context.Context, id queue.JobID) (decimal.Decimal, time.Time, string, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.jobs[id]
	if !ok {
		return decimal.Zero, time.Time{}, "", queue.ErrNotFound
	}
	return r.price, r.quoteUpdatedAt, statusName(r.status), nil
}

func statusName(s status) string {
	switch s {
	case statusPending:
		return "pending"
	case statusRunning:
		return "running"
	case statusDone:
		return "done"
	case statusFailed:
		return "failed"
	default:
		return "unknown"
	}
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

// GetByID returns the read-side view of the job identified by id.
// Returns ErrNotFound if no job exists with the given id.
func (q *Queue) GetByID(_ context.Context, id queue.JobID) (queue.JobView, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	r, ok := q.jobs[id]
	if !ok {
		return queue.JobView{}, queue.ErrNotFound
	}
	return buildView(id, r), nil
}

// buildView constructs a JobView from an internal record.
func buildView(id queue.JobID, r *record) queue.JobView {
	v := queue.JobView{
		ID:        id,
		Base:      r.job.Base,
		Quote:     r.job.Quote,
		Status:    statusName(r.status),
		Attempts:  r.job.Attempts,
		CreatedAt: r.createdAt,
		LastError: r.lastError,
	}
	if !r.completedAt.IsZero() {
		t := r.completedAt
		v.CompletedAt = &t
	}
	if r.status == statusDone {
		p := r.price
		v.Price = &p
		qt := r.quoteUpdatedAt
		v.QuoteUpdatedAt = &qt
	}
	return v
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
func (q *Queue) Fail(_ context.Context, id queue.JobID, reason string) error {
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
	r.completedAt = q.clock.Now()
	r.lastError = reason
	return nil
}
