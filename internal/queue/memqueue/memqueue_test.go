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
	n := q.RecoverExpired()
	assert.Equal(t, 1, n)

	// Now Reserve must return the recovered job.
	third, err := q.Reserve(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	assert.Len(t, third, 1)
}

// TestRecoverExpired_ResetsExpiredRunningJobsToPending confirms that
// RecoverExpired resets an expired-lease running job back to pending so that
// a subsequent Reserve can return it.
func TestRecoverExpired_ResetsExpiredRunningJobsToPending(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixedTime)
	ids := idgen.NewSeq()
	q := memqueue.New(clk)
	ctx := context.Background()

	job := makeJob(ids, "EUR", "k1", fixedTime)
	_, _, err := q.Enqueue(ctx, job)
	require.NoError(t, err)

	reserved, err := q.Reserve(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 1)

	clk.Advance(31 * time.Second)

	n := q.RecoverExpired()
	assert.Equal(t, 1, n)

	jobs, err := q.Reserve(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	assert.Len(t, jobs, 1)
}

// TestRecoverExpired_DoesNotResetJobWithActiveLease confirms that
// RecoverExpired leaves a job with an active (non-expired) lease untouched.
func TestRecoverExpired_DoesNotResetJobWithActiveLease(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixedTime)
	ids := idgen.NewSeq()
	q := memqueue.New(clk)
	ctx := context.Background()

	job := makeJob(ids, "EUR", "k1", fixedTime)
	_, _, err := q.Enqueue(ctx, job)
	require.NoError(t, err)

	reserved, err := q.Reserve(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 1)

	// Do NOT advance clock — lease is still active.
	n := q.RecoverExpired()
	assert.Equal(t, 0, n)

	jobs, err := q.Reserve(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

// TestRecoverExpired_IgnoresCompletedAndFailedJobs confirms that
// RecoverExpired does not reset jobs in statusDone or statusFailed.
func TestRecoverExpired_IgnoresCompletedAndFailedJobs(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixedTime)
	ids := idgen.NewSeq()
	q := memqueue.New(clk)
	ctx := context.Background()

	job1 := makeJob(ids, "EUR", "k1", fixedTime)
	job2 := makeJob(ids, "USD", "k2", fixedTime)

	_, _, err := q.Enqueue(ctx, job1)
	require.NoError(t, err)
	_, _, err = q.Enqueue(ctx, job2)
	require.NoError(t, err)

	// Reserve both.
	reserved, err := q.Reserve(ctx, 2, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 2)

	// Complete one, fail the other.
	err = q.Complete(ctx, job1.ID)
	require.NoError(t, err)
	err = q.Fail(ctx, job2.ID, "terminal error")
	require.NoError(t, err)

	clk.Advance(60 * time.Second)

	n := q.RecoverExpired()
	assert.Equal(t, 0, n)

	jobs, err := q.Reserve(ctx, 10, 30*time.Second)
	require.NoError(t, err)
	assert.Empty(t, jobs)
}

// TestRecoverExpired_ClearsLeaseUntilField confirms that RecoverExpired clears
// leaseUntil so the recovered job is not perpetually eligible for recovery.
func TestRecoverExpired_ClearsLeaseUntilField(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixedTime)
	ids := idgen.NewSeq()
	q := memqueue.New(clk)
	ctx := context.Background()

	job := makeJob(ids, "EUR", "k1", fixedTime)
	_, _, err := q.Enqueue(ctx, job)
	require.NoError(t, err)

	reserved, err := q.Reserve(ctx, 1, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 1)

	clk.Advance(31 * time.Second)

	n := q.RecoverExpired()
	assert.Equal(t, 1, n)

	// Claim the recovered job with a new lease.
	second, err := q.Reserve(ctx, 1, 60*time.Second)
	require.NoError(t, err)
	require.Len(t, second, 1)

	// Without advancing the clock further, a third Reserve must return nothing
	// (the job is running with an active lease and leaseUntil was not left stale).
	third, err := q.Reserve(ctx, 1, 60*time.Second)
	require.NoError(t, err)
	assert.Empty(t, third)
}

// TestRecoverExpired_ReturnsCountOfRecoveredJobs confirms that RecoverExpired
// returns the exact count of jobs reset from running-expired to pending.
func TestRecoverExpired_ReturnsCountOfRecoveredJobs(t *testing.T) {
	t.Parallel()

	fixedTime := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	clk := clock.NewFake(fixedTime)
	ids := idgen.NewSeq()
	q := memqueue.New(clk)
	ctx := context.Background()

	job1 := makeJob(ids, "EUR", "k1", fixedTime)
	job2 := makeJob(ids, "USD", "k2", fixedTime)
	job3 := makeJob(ids, "GBP", "k3", fixedTime)

	_, _, err := q.Enqueue(ctx, job1)
	require.NoError(t, err)
	_, _, err = q.Enqueue(ctx, job2)
	require.NoError(t, err)
	_, _, err = q.Enqueue(ctx, job3)
	require.NoError(t, err)

	reserved, err := q.Reserve(ctx, 3, 30*time.Second)
	require.NoError(t, err)
	require.Len(t, reserved, 3)

	clk.Advance(31 * time.Second)

	n := q.RecoverExpired()
	assert.Equal(t, 3, n)
}
