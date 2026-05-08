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
func (q *Queue) Reserve(ctx context.Context, n int, lease time.Duration) ([]queue.Job, error) {
	now := q.clk.Now()
	rows, err := q.pool.Query(ctx, `
		WITH selected AS (
			SELECT id
			FROM quote_jobs
			WHERE status = 'pending'
			  AND next_run_at <= $1
			ORDER BY next_run_at ASC
			LIMIT $2
			FOR UPDATE SKIP LOCKED
		)
		UPDATE quote_jobs
		   SET status      = 'running',
		       lease_until = $3,
		       updated_at  = $1
		FROM selected
		WHERE quote_jobs.id = selected.id
		RETURNING quote_jobs.id, quote_jobs.currency, quote_jobs.attempts, quote_jobs.next_run_at`,
		now, n, now.Add(lease),
	)
	if err != nil {
		return nil, fmt.Errorf("pgqueue reserve: %w", err)
	}
	defer rows.Close()

	var jobs []queue.Job
	for rows.Next() {
		var j queue.Job
		var id string
		if err := rows.Scan(&id, &j.Currency, &j.Attempts, &j.NextRunAt); err != nil {
			return nil, fmt.Errorf("pgqueue reserve: scan: %w", err)
		}
		j.ID = queue.JobID(id)
		jobs = append(jobs, j)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("pgqueue reserve: rows: %w", err)
	}
	if jobs == nil {
		jobs = []queue.Job{}
	}
	return jobs, nil
}

// Complete marks the job done.
func (q *Queue) Complete(ctx context.Context, id queue.JobID) error {
	now := q.clk.Now()
	tag, err := q.pool.Exec(ctx, `
		UPDATE quote_jobs
		   SET status       = 'done',
		       completed_at = $2,
		       updated_at   = $2
		WHERE id = $1 AND status = 'running'`,
		string(id), now,
	)
	if err != nil {
		return fmt.Errorf("pgqueue complete: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return q.probeNotFoundOrNotReserved(ctx, id, "pgqueue complete")
}

// Reschedule returns the job to pending with NextRunAt = now + after.
func (q *Queue) Reschedule(ctx context.Context, id queue.JobID, reason string, after time.Duration) error {
	now := q.clk.Now()
	tag, err := q.pool.Exec(ctx, `
		UPDATE quote_jobs
		   SET status      = 'pending',
		       next_run_at = $2,
		       attempts    = attempts + 1,
		       last_error  = $3,
		       updated_at  = $4,
		       lease_until = NULL
		WHERE id = $1 AND status = 'running'`,
		string(id), now.Add(after), reason, now,
	)
	if err != nil {
		return fmt.Errorf("pgqueue reschedule: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return q.probeNotFoundOrNotReserved(ctx, id, "pgqueue reschedule")
}

// Fail marks the job permanently failed and records the reason.
func (q *Queue) Fail(ctx context.Context, id queue.JobID, reason string) error {
	now := q.clk.Now()
	tag, err := q.pool.Exec(ctx, `
		UPDATE quote_jobs
		   SET status       = 'failed',
		       last_error   = $2,
		       updated_at   = $3,
		       completed_at = $3
		WHERE id = $1 AND status = 'running'`,
		string(id), reason, now,
	)
	if err != nil {
		return fmt.Errorf("pgqueue fail: %w", err)
	}
	if tag.RowsAffected() == 1 {
		return nil
	}
	return q.probeNotFoundOrNotReserved(ctx, id, "pgqueue fail")
}

// RecoverExpired resets running jobs whose lease has expired back to pending.
// It returns the number of rows updated.
func (q *Queue) RecoverExpired(ctx context.Context) (int, error) {
	now := q.clk.Now()
	tag, err := q.pool.Exec(ctx, `
		UPDATE quote_jobs
		   SET status      = 'pending',
		       lease_until = NULL,
		       locked_by   = NULL,
		       updated_at  = $1
		 WHERE status = 'running'
		   AND lease_until < $1`,
		now,
	)
	if err != nil {
		return 0, fmt.Errorf("pgqueue recover expired: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (q *Queue) probeNotFoundOrNotReserved(ctx context.Context, id queue.JobID, op string) error {
	var exists bool
	err := q.pool.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM quote_jobs WHERE id = $1)`,
		string(id),
	).Scan(&exists)
	if err != nil {
		return fmt.Errorf("%s: probe: %w", op, err)
	}
	if !exists {
		return fmt.Errorf("%s: %w", op, queue.ErrNotFound)
	}
	return fmt.Errorf("%s: %w", op, queue.ErrNotReserved)
}
