package memqueue_test

import (
	"context"
	"testing"
	"time"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/memqueue"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Compile-time interface assertion: memqueue.Queue must satisfy queue.JobQueue.
var _ queue.JobQueue = (*memqueue.Queue)(nil)

// makeJob constructs a queue.Job using the provided sequential ID generator.
func makeJob(ids *idgen.SeqIDGenerator, currency, dedupKey string, runAt time.Time) queue.Job {
	return queue.Job{
		ID:        queue.JobID(ids.NewID()),
		Currency:  currency,
		DedupKey:  dedupKey,
		NextRunAt: runAt,
	}
}

// --- RecoverExpired tests ---

// TestReserve_DoesNotAutoRecoverExpiredLease confirms that a job with an
// expired lease is NOT returned by Reserve without a prior RecoverExpired call.
// Auto-recovery has been removed; explicit RecoverExpired is required.
func TestReserve_DoesNotAutoRecoverExpiredLease(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixedTime)
	ids := idgen.NewSeq()
	q := memqueue.New(clk)
	ctx := context.Background()

	job := makeJob(ids, "EUR", "k1", fixedTime)
	_, _, err := q.Enqueue(ctx, job)
	require.NoError(t, err)

	first, err := q.Reserve(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, first, 1)

	// Advance clock past lease expiry.
	clk.Advance(31 * time.Second)

	// Without RecoverExpired, Reserve must NOT auto-recover the expired job.
	second, err := q.Reserve(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	assert.Empty(t, second)

	// After an explicit RecoverExpired, the job becomes pending again.
	n, err := q.RecoverExpired(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	// Now Reserve must return the recovered job.
	third, err := q.Reserve(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	assert.Len(t, third, 1)
}
