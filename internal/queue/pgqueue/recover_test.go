//go:build integration

package pgqueue_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/pgqueue"
	"currency-exchange/internal/testhelper/pgtest"
)

// TestRecoverExpired_ResetsExpiredRunningJobs verifies that a running job
// whose lease has expired is reset to pending with cleared lease fields,
// correct updated_at, and unchanged attempts / next_run_at.
func TestRecoverExpired_ResetsExpiredRunningJobs(t *testing.T) {
	t.Parallel()

	knownTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(knownTime)
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)
	ctx := context.Background()

	idg := idgen.NewSeq()
	j := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Currency:  "USD",
		NextRunAt: knownTime,
	}

	_, _, err := q.Enqueue(ctx, j)
	require.NoError(t, err)

	// Reserve with a 5s lease.
	reserved, err := q.Reserve(ctx, 1, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 1)

	attemptsAfterReserve := reserved[0].Attempts
	nextRunAtAfterReserve := reserved[0].NextRunAt

	// Advance clock past the lease expiry.
	clk.Advance(10 * time.Second)
	recoverTime := clk.Now()

	n, err := q.RecoverExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	var (
		dbStatus     string
		dbLeaseUntil *time.Time
		dbLockedBy   *string
		dbUpdatedAt  time.Time
		dbAttempts   int
		dbNextRunAt  time.Time
	)
	err = pool.QueryRow(ctx,
		`SELECT status, lease_until, locked_by, updated_at, attempts, next_run_at
		 FROM quote_jobs WHERE id = $1`, string(j.ID),
	).Scan(&dbStatus, &dbLeaseUntil, &dbLockedBy, &dbUpdatedAt, &dbAttempts, &dbNextRunAt)
	require.NoError(t, err)

	assert.Equal(t, "pending", dbStatus)
	assert.Nil(t, dbLeaseUntil)
	assert.Nil(t, dbLockedBy)
	assert.Equal(t, recoverTime.Truncate(time.Microsecond), dbUpdatedAt.Truncate(time.Microsecond))
	assert.Equal(t, attemptsAfterReserve, dbAttempts)
	assert.Equal(t, nextRunAtAfterReserve.Truncate(time.Microsecond), dbNextRunAt.Truncate(time.Microsecond))
}

// TestRecoverExpired_LeavesActiveLease verifies that a running job with a
// lease that has not yet expired is left untouched.
func TestRecoverExpired_LeavesActiveLease(t *testing.T) {
	t.Parallel()

	knownTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(knownTime)
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)
	ctx := context.Background()

	idg := idgen.NewSeq()
	j := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Currency:  "EUR",
		NextRunAt: knownTime,
	}

	_, _, err := q.Enqueue(ctx, j)
	require.NoError(t, err)

	// Reserve with a 60s lease; do NOT advance the clock.
	reserved, err := q.Reserve(ctx, 1, 60*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 1)

	n, err := q.RecoverExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	var (
		dbStatus     string
		dbLeaseUntil *time.Time
	)
	err = pool.QueryRow(ctx,
		`SELECT status, lease_until FROM quote_jobs WHERE id = $1`, string(j.ID),
	).Scan(&dbStatus, &dbLeaseUntil)
	require.NoError(t, err)

	assert.Equal(t, "running", dbStatus)
	assert.NotNil(t, dbLeaseUntil)
}

// TestRecoverExpired_IgnoresNonRunningStatuses verifies that jobs with
// statuses other than 'running' are not touched, even when their lease_until
// is in the past.
func TestRecoverExpired_IgnoresNonRunningStatuses(t *testing.T) {
	t.Parallel()

	knownTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(knownTime)
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)
	ctx := context.Background()

	idg := idgen.NewSeq()
	pastLease := knownTime.Add(-1 * time.Hour)

	// Insert three rows directly with non-running statuses and a past lease_until.
	statuses := []string{"pending", "done", "failed"}
	ids := make([]string, len(statuses))
	for i, status := range statuses {
		id := idg.NewID()
		ids[i] = id
		_, err := pool.Exec(ctx, `
			INSERT INTO quote_jobs (
				id, currency, status, attempts,
				next_run_at, created_at, updated_at,
				lease_until
			) VALUES ($1, $2, $3, 0, $4, $4, $4, $5)`,
			id, "GBP", status, knownTime, pastLease,
		)
		require.NoError(t, err)
	}

	n, err := q.RecoverExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	// Verify all three rows are unchanged.
	for i, id := range ids {
		var dbStatus string
		err = pool.QueryRow(ctx,
			`SELECT status FROM quote_jobs WHERE id = $1`, id,
		).Scan(&dbStatus)
		require.NoError(t, err)
		assert.Equal(t, statuses[i], dbStatus, "row %s should not have changed", id)
	}
}

// TestRecoverExpired_ReturnsCorrectCount verifies that RecoverExpired returns
// the exact count of rows it reset.
func TestRecoverExpired_ReturnsCorrectCount(t *testing.T) {
	t.Parallel()

	knownTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(knownTime)
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)
	ctx := context.Background()

	idg := idgen.NewSeq()

	// Enqueue 3 jobs.
	for i := 0; i < 3; i++ {
		j := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "JPY",
			NextRunAt: knownTime,
		}
		_, _, err := q.Enqueue(ctx, j)
		require.NoError(t, err)
	}

	// Reserve all 3 with a 5s lease.
	reserved, err := q.Reserve(ctx, 3, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 3)

	// Advance clock past the lease.
	clk.Advance(10 * time.Second)

	n, err := q.RecoverExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	var pendingCount int
	err = pool.QueryRow(ctx,
		`SELECT count(*) FROM quote_jobs WHERE status = 'pending'`,
	).Scan(&pendingCount)
	require.NoError(t, err)
	assert.Equal(t, 3, pendingCount)
}

// TestRecoverExpired_AfterRecovery_ReservePicksUpJob verifies that after
// RecoverExpired resets an expired job, Reserve can pick it up again.
func TestRecoverExpired_AfterRecovery_ReservePicksUpJob(t *testing.T) {
	t.Parallel()

	knownTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(knownTime)
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)
	ctx := context.Background()

	idg := idgen.NewSeq()
	j := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Currency:  "CAD",
		NextRunAt: knownTime,
	}

	_, _, err := q.Enqueue(ctx, j)
	require.NoError(t, err)

	// Reserve with a 5s lease.
	reserved, err := q.Reserve(ctx, 1, 5*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 1)

	// Advance clock past the lease.
	clk.Advance(10 * time.Second)

	_, err = q.RecoverExpired(ctx)
	require.NoError(t, err)

	// Reserve again — should return the same job.
	reReserved, err := q.Reserve(ctx, 1, 60*time.Second)
	require.NoError(t, err)
	require.Len(t, reReserved, 1)
	assert.Equal(t, j.ID, reReserved[0].ID)
	assert.Equal(t, j.Currency, reReserved[0].Currency)
}

// TestRecoverExpired_ExactBoundary_LeaseEqualNowIsNotExpired verifies that
// the expiry check is strict less-than: a job whose lease_until equals
// clk.Now() exactly is NOT recovered.
func TestRecoverExpired_ExactBoundary_LeaseEqualNowIsNotExpired(t *testing.T) {
	t.Parallel()

	knownTime := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(knownTime)
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)
	ctx := context.Background()

	idg := idgen.NewSeq()
	j := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Currency:  "CHF",
		NextRunAt: knownTime,
	}

	_, _, err := q.Enqueue(ctx, j)
	require.NoError(t, err)

	// Reserve with a 10s lease; lease_until = knownTime + 10s.
	reserved, err := q.Reserve(ctx, 1, 10*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 1)

	// Advance clock by exactly 10s so clk.Now() == lease_until.
	clk.Advance(10 * time.Second)

	n, err := q.RecoverExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, 0, n)

	var dbStatus string
	err = pool.QueryRow(ctx,
		`SELECT status FROM quote_jobs WHERE id = $1`, string(j.ID),
	).Scan(&dbStatus)
	require.NoError(t, err)
	assert.Equal(t, "running", dbStatus)
}
