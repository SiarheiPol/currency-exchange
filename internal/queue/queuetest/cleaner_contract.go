// Package queuetest provides shared contract test suites for queue
// implementations. This file defines the Cleaner contract suite.
package queuetest

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/queue"
)

// CleanerFactory creates a fresh queue.JobQueue and queue.Cleaner backed by
// the given clock. The two returned values must refer to the same underlying
// implementation. Each subtest receives its own factory call so state does not
// leak between subtests.
type CleanerFactory func(t *testing.T, clk clock.Clock) (queue.JobQueue, queue.Cleaner)

// RunCleanerContractTests runs the full Cleaner contract test suite against any
// implementation produced by factory.
func RunCleanerContractTests(t *testing.T, factory CleanerFactory) {
	t.Helper()

	t.Run("RecoverExpired/ResetsExpiredRunningJobs", func(t *testing.T) {
		t.Parallel()
		base := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
		clk := clock.NewFake(base)
		q, cleaner := factory(t, clk)
		ctx := context.Background()
		idg := idgen.NewSeq()

		job := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "EUR",
			NextRunAt: base,
		}
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		reserved, err := q.Reserve(ctx, 1, 5*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)
		firstID := reserved[0].ID

		// Advance past the 5s lease.
		clk.Advance(10 * time.Second)

		n, err := cleaner.RecoverExpired(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)

		// The job must be available for reservation again.
		reReserved, err := q.Reserve(ctx, 1, 30*time.Second)
		require.NoError(t, err)
		require.Len(t, reReserved, 1)
		assert.Equal(t, firstID, reReserved[0].ID)
	})

	t.Run("RecoverExpired/LeavesActiveLease", func(t *testing.T) {
		t.Parallel()
		base := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
		clk := clock.NewFake(base)
		q, cleaner := factory(t, clk)
		ctx := context.Background()
		idg := idgen.NewSeq()

		job := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "USD",
			NextRunAt: base,
		}
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		// Reserve with a generous lease; do NOT advance the clock.
		reserved, err := q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)

		n, err := cleaner.RecoverExpired(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, n)

		// The job must still be invisible (running with active lease).
		second, err := q.Reserve(ctx, 1, 30*time.Second)
		require.NoError(t, err)
		assert.Empty(t, second)
	})

	t.Run("RecoverExpired/ReturnsCorrectCount", func(t *testing.T) {
		t.Parallel()
		base := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
		clk := clock.NewFake(base)
		q, cleaner := factory(t, clk)
		ctx := context.Background()
		idg := idgen.NewSeq()

		for i := 0; i < 3; i++ {
			job := queue.Job{
				ID:        queue.JobID(idg.NewID()),
				Currency:  "GBP",
				NextRunAt: base,
			}
			_, _, err := q.Enqueue(ctx, job)
			require.NoError(t, err)
		}

		// Reserve all 3 with a 5s lease.
		reserved, err := q.Reserve(ctx, 3, 5*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 3)

		// Advance past the lease.
		clk.Advance(10 * time.Second)

		n, err := cleaner.RecoverExpired(ctx)
		require.NoError(t, err)
		assert.Equal(t, 3, n)
	})

	t.Run("RecoverExpired/AfterRecovery_ReservePicksUpJob", func(t *testing.T) {
		t.Parallel()
		base := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
		clk := clock.NewFake(base)
		q, cleaner := factory(t, clk)
		ctx := context.Background()
		idg := idgen.NewSeq()

		job := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "CAD",
			NextRunAt: base,
		}
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		reserved, err := q.Reserve(ctx, 1, 5*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)

		// Advance past the lease.
		clk.Advance(10 * time.Second)

		n, err := cleaner.RecoverExpired(ctx)
		require.NoError(t, err)
		assert.Equal(t, 1, n)

		// Reserve again — must return the same job with the same ID and Currency.
		reReserved, err := q.Reserve(ctx, 1, 60*time.Second)
		require.NoError(t, err)
		require.Len(t, reReserved, 1)
		assert.Equal(t, job.ID, reReserved[0].ID)
		assert.Equal(t, job.Currency, reReserved[0].Currency)
	})

	t.Run("RecoverExpired/ExactBoundary_LeaseEqualNowIsNotExpired", func(t *testing.T) {
		t.Parallel()
		base := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
		clk := clock.NewFake(base)
		q, cleaner := factory(t, clk)
		ctx := context.Background()
		idg := idgen.NewSeq()

		job := queue.Job{
			ID:        queue.JobID(idg.NewID()),
			Currency:  "CHF",
			NextRunAt: base,
		}
		_, _, err := q.Enqueue(ctx, job)
		require.NoError(t, err)

		// Reserve with a 10s lease; lease_until = base + 10s.
		reserved, err := q.Reserve(ctx, 1, 10*time.Second)
		require.NoError(t, err)
		require.Len(t, reserved, 1)

		// Advance exactly 10s so clk.Now() == lease_until.
		clk.Advance(10 * time.Second)

		// Expiry check is strict less-than: job must NOT be recovered.
		n, err := cleaner.RecoverExpired(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})

	t.Run("RecoverExpired/NoError", func(t *testing.T) {
		t.Parallel()
		base := time.Date(2025, 6, 1, 12, 0, 0, 0, time.UTC)
		clk := clock.NewFake(base)
		_, cleaner := factory(t, clk)
		ctx := context.Background()

		// Empty queue: must return (0, nil).
		n, err := cleaner.RecoverExpired(ctx)
		require.NoError(t, err)
		assert.Equal(t, 0, n)
	})
}
