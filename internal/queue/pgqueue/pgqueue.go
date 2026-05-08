// Package pgqueue implements queue.JobQueue backed by a Postgres table.
package pgqueue

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/queue"
)

// Queue is a Postgres-backed implementation of queue.JobQueue.
type Queue struct {
	pool *pgxpool.Pool
	clk  clock.Clock
}

// New returns a Queue that uses pool for storage and clk for timestamps.
func New(pool *pgxpool.Pool, clk clock.Clock) *Queue {
	return &Queue{pool: pool, clk: clk}
}

var _ queue.JobQueue = (*Queue)(nil)

// Enqueue inserts j or, if a job with the same DedupKey already exists,
// returns its id with inserted=false.
func (q *Queue) Enqueue(ctx context.Context, j queue.Job) (queue.JobID, bool, error) {
	now := q.clk.Now()

	var dedupKey *string
	if j.DedupKey != "" {
		dedupKey = &j.DedupKey
	}

	var returnedID string
	err := q.pool.QueryRow(ctx, `
		INSERT INTO quote_jobs (
			id, currency, status, attempts,
			next_run_at, created_at, updated_at,
			dedup_key, locked_by, lease_until, completed_at, last_error
		) VALUES (
			$1, $2, 'pending', 0,
			$3, $4, $4,
			$5, NULL, NULL, NULL, NULL
		)
		ON CONFLICT (dedup_key) WHERE dedup_key IS NOT NULL
		DO NOTHING
		RETURNING id`,
		string(j.ID), j.Currency, j.NextRunAt, now, dedupKey,
	).Scan(&returnedID)

	if err == nil {
		// Row was inserted and returned.
		return j.ID, true, nil
	}

	// Check if it's a "no rows" error (conflict fired, DO NOTHING).
	if errors.Is(err, pgx.ErrNoRows) {
		var existingID string
		err2 := q.pool.QueryRow(ctx,
			`SELECT id FROM quote_jobs WHERE dedup_key = $1`,
			j.DedupKey,
		).Scan(&existingID)
		if err2 != nil {
			return "", false, fmt.Errorf("pgqueue enqueue: lookup existing: %w", err2)
		}
		return queue.JobID(existingID), false, nil
	}

	return "", false, fmt.Errorf("pgqueue enqueue: %w", err)
}

// Reserve marks up to n pending jobs as running with the given lease duration.
func (q *Queue) Reserve(_ context.Context, _ int, _ time.Duration) ([]queue.Job, error) {
	return nil, fmt.Errorf("pgqueue reserve: not implemented")
}

// Complete marks the job done.
func (q *Queue) Complete(_ context.Context, _ queue.JobID) error {
	return fmt.Errorf("pgqueue complete: not implemented")
}

// Reschedule returns the job to pending with NextRunAt = now + after.
func (q *Queue) Reschedule(_ context.Context, _ queue.JobID, _ string, _ time.Duration) error {
	return fmt.Errorf("pgqueue reschedule: not implemented")
}

// Fail marks the job permanently failed and records the reason.
func (q *Queue) Fail(_ context.Context, _ queue.JobID, _ string) error {
	return fmt.Errorf("pgqueue fail: not implemented")
}
