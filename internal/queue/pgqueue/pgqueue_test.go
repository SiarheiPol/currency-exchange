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

func TestEnqueue_WritesAllColumns(t *testing.T) {
	t.Parallel()

	knownTime := time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC)
	clk := clock.NewFake(knownTime)
	pool := pgtest.NewDB(t)
	q := pgqueue.New(pool, clk)

	idg := idgen.NewSeq()
	j := queue.Job{
		ID:        queue.JobID(idg.NewID()),
		Currency:  "USD",
		DedupKey:  "test-dedup-key",
		NextRunAt: knownTime.Add(5 * time.Minute),
		Attempts:  0,
	}

	id, inserted, err := q.Enqueue(context.Background(), j)
	require.NoError(t, err)
	assert.Equal(t, j.ID, id)
	assert.True(t, inserted)

	row := pool.QueryRow(context.Background(),
		`SELECT id, currency, status, attempts, next_run_at, created_at, updated_at,
		        dedup_key, locked_by, lease_until, completed_at, last_error
		 FROM quote_jobs WHERE id = $1`, string(j.ID))

	var (
		dbID          string
		dbCurrency    string
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
		&dbCurrency,
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
	assert.Equal(t, "USD", dbCurrency)
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
