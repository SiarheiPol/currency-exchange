//go:build integration

package pgqueue_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/jackc/pgerrcode"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/pgqueue"
	"currency-exchange/internal/testhelper/pgtest"
)

// TestEnqueue_WritesAllColumns queries base and quote from quote_jobs and
// asserts both match the enqueued values. Replaces the old single-currency
// column assertion.
func TestEnqueue_WritesAllColumns(t *testing.T) {
	t.Parallel()

	knownTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(knownTime)
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)

	idg := idgen.NewSeq()
	j := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Base:      "USD",
		Quote:     "MXN",
		DedupKey:  "test-dedup-key",
		NextRunAt: knownTime.Add(5 * time.Minute),
		Attempts:  0,
		Source:    "scheduler",
	}

	id, inserted, err := q.Enqueue(context.Background(), j)
	require.NoError(t, err)
	assert.Equal(t, j.ID, id)
	assert.True(t, inserted)

	row := pool.QueryRow(context.Background(),
		`SELECT id, base, quote, status, attempts, next_run_at, created_at, updated_at,
		        dedup_key, locked_by, lease_until, completed_at, last_error
		 FROM quote_jobs WHERE id = $1`, string(j.ID))

	var (
		dbID          string
		dbBase        string
		dbQuote       string
		dbStatus      string
		dbAttempts    int
		dbNextRunAt   time.Time
		dbCreatedAt   time.Time
		dbUpdatedAt   time.Time
		dbDedupKey    *string
		dbLockedBy    *string
		dbLeaseUntil  *time.Time
		dbCompletedAt *time.Time
		dbLastError   *string
	)

	err = row.Scan(
		&dbID,
		&dbBase,
		&dbQuote,
		&dbStatus,
		&dbAttempts,
		&dbNextRunAt,
		&dbCreatedAt,
		&dbUpdatedAt,
		&dbDedupKey,
		&dbLockedBy,
		&dbLeaseUntil,
		&dbCompletedAt,
		&dbLastError,
	)
	require.NoError(t, err)

	assert.Equal(t, string(j.ID), dbID)
	assert.Equal(t, "USD", dbBase)
	assert.Equal(t, "MXN", dbQuote)
	assert.Equal(t, "pending", dbStatus)
	assert.Equal(t, 0, dbAttempts)
	assert.Equal(t, j.NextRunAt.Truncate(time.Microsecond), dbNextRunAt.Truncate(time.Microsecond))
	assert.Equal(t, knownTime.Truncate(time.Microsecond), dbCreatedAt.Truncate(time.Microsecond))
	assert.Equal(t, knownTime.Truncate(time.Microsecond), dbUpdatedAt.Truncate(time.Microsecond))
	require.NotNil(t, dbDedupKey)
	assert.Equal(t, "test-dedup-key", *dbDedupKey)
	assert.Nil(t, dbLockedBy)
	assert.Nil(t, dbLeaseUntil)
	assert.Nil(t, dbCompletedAt)
	assert.Nil(t, dbLastError)
}

// TestEnqueue_SelfPair_Rejected asserts that enqueueing a job where Base ==
// Quote is rejected by the database CHECK constraint (no_self_pair), and that
// the error is not ErrNotFound or ErrNotReserved.
func TestEnqueue_SelfPair_Rejected(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)

	idg := idgen.NewSeq()
	j := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Base:      "EUR",
		Quote:     "EUR",
		DedupKey:  "",
		NextRunAt: clk.Now(),
		Source:    "scheduler",
	}

	_, _, err := q.Enqueue(context.Background(), j)
	require.Error(t, err, "expected error from CHECK constraint on self-pair, got nil")
	assert.NotErrorIs(t, err, queue.ErrNotFound, "self-pair error must not be ErrNotFound")
	assert.NotErrorIs(t, err, queue.ErrNotReserved, "self-pair error must not be ErrNotReserved")

	var pgErr *pgconn.PgError
	require.True(t, errors.As(err, &pgErr),
		"expected error to wrap *pgconn.PgError, got %T: %v", err, err)
	assert.Equal(t, pgerrcode.CheckViolation, pgErr.Code,
		"expected CHECK violation (code 23514), got %s", pgErr.Code)
	assert.Equal(t, "no_self_pair", pgErr.ConstraintName,
		"expected violation of constraint no_self_pair, got %q", pgErr.ConstraintName)
}

// TestReserve_PopulatesBasePair asserts that Reserve returns a job with both
// Base and Quote populated correctly from the database.
func TestReserve_PopulatesBasePair(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)

	idg := idgen.NewSeq()
	j := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Base:      "GBP",
		Quote:     "JPY",
		DedupKey:  "",
		NextRunAt: clk.Now(),
		Source:    "scheduler",
	}

	_, inserted, err := q.Enqueue(context.Background(), j)
	require.NoError(t, err)
	require.True(t, inserted)

	reserved, err := q.Reserve(context.Background(), 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 1)

	assert.Equal(t, "GBP", reserved[0].Base)
	assert.Equal(t, "JPY", reserved[0].Quote)

	// Verify state transitions via direct SQL — no queue.Job field additions needed.
	var (
		dbStatus     string
		dbLeaseUntil time.Time
	)
	err = pool.QueryRow(context.Background(),
		`SELECT status, lease_until FROM quote_jobs WHERE id = $1`,
		string(reserved[0].ID),
	).Scan(&dbStatus, &dbLeaseUntil)
	require.NoError(t, err)

	assert.Equal(t, "running", dbStatus)
	assert.False(t, dbLeaseUntil.IsZero(), "lease_until must be set after Reserve")
	assert.True(t, dbLeaseUntil.After(clk.Now()), "lease_until must be in the future relative to clock")
}

// TestEnqueue_CoalescingCounterIncrements asserts that the second Enqueue of
// a job sharing a dedup_key advances obs.CoalescingCollapsedTotal by exactly
// one. NOT t.Parallel'd: the counter is a global singleton, and parallel
// subtests in the contract suite Enqueue many jobs with shared dedup_keys.
// As a non-parallel top-level test it runs in the serial phase before the
// parallel batch, so the delta is exact.
func TestEnqueue_CoalescingCounterIncrements(t *testing.T) {
	knownTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(knownTime)
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)
	ctx := context.Background()
	idg := idgen.NewSeq()

	j1 := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Base:      "EUR",
		Quote:     "USD",
		DedupKey:  "k-coalesce",
		NextRunAt: knownTime,
		Source:    "scheduler",
	}
	j2 := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Base:      "EUR",
		Quote:     "USD",
		DedupKey:  "k-coalesce",
		NextRunAt: knownTime,
		Source:    "scheduler",
	}

	_, _, err := q.Enqueue(ctx, j1)
	require.NoError(t, err)

	before := testutil.ToFloat64(obs.CoalescingCollapsedTotal)
	_, inserted, err := q.Enqueue(ctx, j2)
	require.NoError(t, err)
	require.False(t, inserted, "second Enqueue with same dedup_key must not insert")

	after := testutil.ToFloat64(obs.CoalescingCollapsedTotal)
	require.Equal(t, before+1, after,
		"expected CoalescingCollapsedTotal to advance by exactly 1 on collapse")
}
