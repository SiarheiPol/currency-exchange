// Package queue defines the JobQueue interface for the asynchronous refresh
// pipeline, plus the Job and JobID types it operates on. Two implementations
// satisfy this interface: memQueue (in-memory, for unit tests) and pgQueue
// (Postgres-backed, for integration tests and production).
package queue

import (
	"context"
	"errors"
	"time"
)

// JobID identifies a single quote-refresh job. Wraps a string so the type
// system distinguishes it from arbitrary text. Producers obtain a fresh JobID
// via JobID(idgen.NewID()).
type JobID string

// Job is the unit of work flowing through the queue. Operational state
// (status, lease, locked_by, audit timestamps, last_error) is managed by the
// queue implementation and is not exposed here.
type Job struct {
	// ID is the job's identifier.
	ID JobID
	// Currency is the ISO 4217 code the job will fetch (e.g., "EUR").
	Currency string
	// DedupKey collapses concurrent enqueues for the same logical work into
	// a single job. Empty disables coalescing for the job.
	DedupKey string
	// Attempts is the number of times the job has been reserved by a worker.
	// Zero on first enqueue; incremented on each Reschedule.
	Attempts int
	// NextRunAt is the earliest time the job is eligible to be reserved.
	NextRunAt time.Time
}

// JobQueue is the seam between producers (refresh handler, scheduler) and the
// worker pool. Implementations must be safe for concurrent use across
// goroutines and, for pgQueue, across multiple service instances.
type JobQueue interface {
	// Enqueue inserts j or, if a job with the same DedupKey already exists,
	// returns its id with inserted=false. Returns the id of the new or
	// existing job and whether it was newly inserted.
	Enqueue(ctx context.Context, j Job) (id JobID, inserted bool, err error)

	// Reserve marks up to n pending jobs as running with the given lease
	// duration and returns them. A reserved job is invisible to other
	// callers until the lease expires or the worker calls Complete,
	// Reschedule, or Fail.
	Reserve(ctx context.Context, n int, lease time.Duration) ([]Job, error)

	// Complete marks the job done. Returns ErrNotFound if no job has the
	// given id.
	Complete(ctx context.Context, id JobID) error

	// Reschedule returns the job to pending with NextRunAt = now + after,
	// records the reason as last_error, and increments Attempts. Returns
	// ErrNotFound if no job has the given id.
	Reschedule(ctx context.Context, id JobID, reason string, after time.Duration) error

	// Fail marks the job permanently failed and records the reason.
	// Returns ErrNotFound if no job has the given id.
	Fail(ctx context.Context, id JobID, reason string) error
}

// ErrNotFound is returned by Complete, Reschedule, and Fail when the target
// job does not exist. Callers test with errors.Is(err, queue.ErrNotFound).
var ErrNotFound = errors.New("job not found")
