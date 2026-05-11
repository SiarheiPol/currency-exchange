package scheduler_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/memqueue"
	"currency-exchange/internal/ratesprovider"
	"currency-exchange/internal/scheduler"
)

// sixPairs is the canonical six-pair whitelist used across scheduler tests.
var sixPairs = []ratesprovider.Pair{
	{Base: "USD", Quote: "EUR"},
	{Base: "USD", Quote: "MXN"},
	{Base: "EUR", Quote: "USD"},
	{Base: "EUR", Quote: "MXN"},
	{Base: "MXN", Quote: "USD"},
	{Base: "MXN", Quote: "EUR"},
}

// reserveAll drains up to 100 jobs from the queue and returns them.
func reserveAll(t *testing.T, q queue.JobQueue) []queue.Job {
	t.Helper()
	jobs, err := q.Reserve(context.Background(), 100, time.Hour)
	require.NoError(t, err)
	return jobs
}

// TestScheduler_BootstrapTick_EnqueuesAllPairsImmediately asserts that a single
// Tick call enqueues exactly one job per configured pair and that all pairs in
// the whitelist are covered.
func TestScheduler_BootstrapTick_EnqueuesAllPairsImmediately(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	gen := idgen.NewSeq()

	s := scheduler.New(
		scheduler.WithInterval(10*time.Minute),
		scheduler.WithBucketSize(5*time.Minute),
		scheduler.WithPairs(sixPairs),
		scheduler.WithQueue(q),
		scheduler.WithClock(clk),
		scheduler.WithIDGen(gen),
	)

	ctx := context.Background()
	require.NoError(t, s.Tick(ctx))

	jobs := reserveAll(t, q)
	require.Len(t, jobs, len(sixPairs), "expected exactly one job per pair")

	gotPairs := make([]ratesprovider.Pair, 0, len(jobs))
	for _, j := range jobs {
		gotPairs = append(gotPairs, ratesprovider.Pair{Base: j.Base, Quote: j.Quote})
	}
	require.ElementsMatch(t, sixPairs, gotPairs, "enqueued pairs must match the whitelist")
}

// TestScheduler_EnqueuedJobs_HaveCorrectFields asserts that each job enqueued
// by Tick has the correct Base, Quote, DedupKey, and NextRunAt fields.
func TestScheduler_EnqueuedJobs_HaveCorrectFields(t *testing.T) {
	t.Parallel()

	t0 := time.Unix(1000, 0)
	bucketSize := 600 * time.Second
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	gen := idgen.NewSeq()

	onePair := []ratesprovider.Pair{{Base: "USD", Quote: "EUR"}}

	s := scheduler.New(
		scheduler.WithInterval(10*time.Minute),
		scheduler.WithBucketSize(bucketSize),
		scheduler.WithPairs(onePair),
		scheduler.WithQueue(q),
		scheduler.WithClock(clk),
		scheduler.WithIDGen(gen),
	)

	ctx := context.Background()
	require.NoError(t, s.Tick(ctx))

	jobs := reserveAll(t, q)
	require.Len(t, jobs, 1)

	job := jobs[0]
	expectedKey := queue.DedupKey("USD", "EUR", t0, bucketSize)

	require.Equal(t, "USD", job.Base)
	require.Equal(t, "EUR", job.Quote)
	require.Equal(t, expectedKey, job.DedupKey)
	require.True(t, job.NextRunAt.Equal(clk.Now()),
		"NextRunAt %v must equal clock now %v", job.NextRunAt, clk.Now())
}

// TestScheduler_SecondTickWithinBucket_CoalescesToZeroNewJobs asserts that a
// second Tick within the same bucket window does not add new jobs because all
// dedup_keys collide with existing ones.
//
// NOT t.Parallel'd: CoalescingCollapsedTotal is a global counter; running
// concurrently would make the exact delta unreliable.
func TestScheduler_SecondTickWithinBucket_CoalescesToZeroNewJobs(t *testing.T) {
	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	gen := idgen.NewSeq()

	s := scheduler.New(
		scheduler.WithInterval(10*time.Minute),
		scheduler.WithBucketSize(5*time.Minute),
		scheduler.WithPairs(sixPairs),
		scheduler.WithQueue(q),
		scheduler.WithClock(clk),
		scheduler.WithIDGen(gen),
	)

	ctx := context.Background()

	// Bootstrap tick — all 6 pairs enqueued.
	require.NoError(t, s.Tick(ctx))

	before := testutil.ToFloat64(obs.CoalescingCollapsedTotal)

	// Second tick within the same bucket — all dedup_keys collide.
	require.NoError(t, s.Tick(ctx))

	after := testutil.ToFloat64(obs.CoalescingCollapsedTotal)
	require.Equal(t, before+float64(len(sixPairs)), after,
		"CoalescingCollapsedTotal must advance by %d (one collision per pair)", len(sixPairs))

	// Still only 6 jobs reservable.
	jobs := reserveAll(t, q)
	require.Len(t, jobs, len(sixPairs),
		"second tick within same bucket must not add new jobs")
}

// TestScheduler_TickAfterBucketBoundary_EnqueuesNewJobs asserts that a tick
// that falls in a new bucket window enqueues fresh jobs with different
// dedup_keys, resulting in 12 total reservable jobs.
func TestScheduler_TickAfterBucketBoundary_EnqueuesNewJobs(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	bucketSize := 5 * time.Minute
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	gen := idgen.NewSeq()

	s := scheduler.New(
		scheduler.WithInterval(10*time.Minute),
		scheduler.WithBucketSize(bucketSize),
		scheduler.WithPairs(sixPairs),
		scheduler.WithQueue(q),
		scheduler.WithClock(clk),
		scheduler.WithIDGen(gen),
	)

	ctx := context.Background()

	// Bootstrap tick — 6 jobs enqueued.
	require.NoError(t, s.Tick(ctx))

	// Collect dedup keys from bootstrap tick.
	firstJobs := reserveAll(t, q)
	require.Len(t, firstJobs, len(sixPairs))
	firstKeys := make(map[string]bool, len(firstJobs))
	for _, j := range firstJobs {
		firstKeys[j.DedupKey] = true
	}

	// Advance clock past the bucket boundary.
	clk.Advance(bucketSize)

	// Second tick — new bucket, new dedup_keys.
	require.NoError(t, s.Tick(ctx))

	// Total: 6 running (from firstJobs) + 6 new pending.
	// Reserve already consumed 6; now reserve again to get the 6 new ones.
	secondJobs := reserveAll(t, q)
	require.Len(t, secondJobs, len(sixPairs),
		"new bucket tick must enqueue 6 additional jobs")

	// Verify dedup_keys differ across bucket boundaries.
	for _, j := range secondJobs {
		require.False(t, firstKeys[j.DedupKey],
			"new bucket job dedup_key %q must differ from bootstrap bucket keys", j.DedupKey)
	}
}

// TestScheduler_Run_ReturnsOnContextCancel asserts that Run returns promptly
// when the context is cancelled, with an error that wraps context.Canceled.
func TestScheduler_Run_ReturnsOnContextCancel(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Now())
	q := memqueue.New(clk)
	gen := idgen.NewSeq()

	s := scheduler.New(
		scheduler.WithInterval(1*time.Hour), // long — ticker won't fire in test
		scheduler.WithBucketSize(5*time.Minute),
		scheduler.WithPairs(sixPairs),
		scheduler.WithQueue(q),
		scheduler.WithClock(clk),
		scheduler.WithIDGen(gen),
	)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	cancel()

	select {
	case err := <-done:
		require.True(t,
			errors.Is(err, context.Canceled),
			"expected context.Canceled (or wrapped), got %v", err)
	case <-time.After(200 * time.Millisecond):
		t.Fatal("Run did not return within 200ms after context cancellation")
	}
}

func TestScheduler_Tick_SetsSourceScheduler(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	gen := idgen.NewSeq()

	onePair := []ratesprovider.Pair{{Base: "USD", Quote: "EUR"}}

	s := scheduler.New(
		scheduler.WithInterval(10*time.Minute),
		scheduler.WithBucketSize(5*time.Minute),
		scheduler.WithPairs(onePair),
		scheduler.WithQueue(q),
		scheduler.WithClock(clk),
		scheduler.WithIDGen(gen),
	)

	ctx := context.Background()
	require.NoError(t, s.Tick(ctx))

	jobs := reserveAll(t, q)
	require.Len(t, jobs, 1, "Tick must enqueue exactly one job for one pair")
	require.Equal(t, "scheduler", jobs[0].Source,
		"job enqueued by Tick must have Source=%q, got %q", "scheduler", jobs[0].Source)
}

// TestScheduler_LastTick_AdvancesAfterEachTick asserts that LastTick is zero
// before any tick and equals the fake clock's time after each Tick call.
func TestScheduler_LastTick_AdvancesAfterEachTick(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	gen := idgen.NewSeq()

	s := scheduler.New(
		scheduler.WithInterval(10*time.Minute),
		scheduler.WithBucketSize(5*time.Minute),
		scheduler.WithPairs(sixPairs),
		scheduler.WithQueue(q),
		scheduler.WithClock(clk),
		scheduler.WithIDGen(gen),
	)

	require.True(t, s.LastTick().IsZero(),
		"LastTick must be zero before any tick")

	ctx := context.Background()
	require.NoError(t, s.Tick(ctx))

	require.True(t, s.LastTick().Equal(clk.Now()),
		"LastTick %v must equal clock now %v after first tick", s.LastTick(), clk.Now())

	clk.Advance(1 * time.Minute)
	require.NoError(t, s.Tick(ctx))

	require.True(t, s.LastTick().Equal(clk.Now()),
		"LastTick %v must equal clock now %v after second tick", s.LastTick(), clk.Now())
}
