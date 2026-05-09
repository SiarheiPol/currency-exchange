package memqueue_test

import (
	"context"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/memqueue"
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

// TestEnqueue_CoalescingCounterIncrements asserts that the second Enqueue of
// a job sharing a dedup_key advances obs.CoalescingCollapsedTotal by exactly
// one. The test is intentionally NOT t.Parallel'd: the counter is a global
// singleton, and running concurrently with the contract suite (which Enqueues
// many jobs with shared dedup_keys via parallel subtests) would make exact
// deltas unreliable. As a non-parallel top-level test it runs in the serial
// phase before the parallel batch, in isolation.
func TestEnqueue_CoalescingCounterIncrements(t *testing.T) {
	clk := clock.NewFake(time.Now())
	ids := idgen.NewSeq()
	q := memqueue.New(clk)
	ctx := context.Background()

	j1 := makeJob(ids, "EUR", "k-coalesce", clk.Now())
	j2 := makeJob(ids, "EUR", "k-coalesce", clk.Now())

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
