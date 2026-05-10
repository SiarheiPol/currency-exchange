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
	"currency-exchange/internal/queue/pgqueue"
	"currency-exchange/internal/testhelper/pgtest"
)

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
				id, base, quote, status, attempts,
				next_run_at, created_at, updated_at,
				lease_until
			) VALUES ($1, $2, $3, $4, 0, $5, $5, $5, $6)`,
			id, "GBP", "USD", status, knownTime, pastLease,
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
