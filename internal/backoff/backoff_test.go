package backoff

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCompute_Attempt0WindowIsBaseDelay confirms attempt 0 produces values in [0, 1s).
func TestCompute_Attempt0WindowIsBaseDelay(t *testing.T) {
	t.Parallel()

	for i := 0; i < 100; i++ {
		d := Compute(0)
		require.True(t, d >= 0 && d < time.Second, "got %v", d)
	}
}

// TestCompute_AttemptExpandsWindowExponentially confirms each pre-saturation attempt
// doubles the window.
func TestCompute_AttemptExpandsWindowExponentially(t *testing.T) {
	t.Parallel()

	cases := []struct {
		attempt            int
		expectedUpperBound time.Duration
	}{
		{1, 2 * time.Second},
		{2, 4 * time.Second},
		{3, 8 * time.Second},
		{4, 16 * time.Second},
		{5, 32 * time.Second},
	}

	for _, tc := range cases {
		tc := tc
		for i := 0; i < 100; i++ {
			d := Compute(tc.attempt)
			require.True(t, d >= 0 && d < tc.expectedUpperBound,
				"attempt=%d: got %v, want in [0, %v)", tc.attempt, d, tc.expectedUpperBound)
		}
	}
}

// TestCompute_CapEnforcedAtAndAboveSaturation confirms the 60s cap holds at the
// saturation boundary and well beyond, with no overflow.
func TestCompute_CapEnforcedAtAndAboveSaturation(t *testing.T) {
	t.Parallel()

	for _, attempt := range []int{6, 10, 30} {
		attempt := attempt
		for i := 0; i < 100; i++ {
			d := Compute(attempt)
			require.True(t, d >= 0 && d < 60*time.Second,
				"attempt=%d: got %v, want in [0, 60s)", attempt, d)
		}
	}
}

// TestCompute_NegativeAttemptTreatedAsZero confirms the defensive clamp:
// negative attempts produce the same window as attempt 0.
func TestCompute_NegativeAttemptTreatedAsZero(t *testing.T) {
	t.Parallel()

	for _, attempt := range []int{-1, -100} {
		for i := 0; i < 50; i++ {
			d := Compute(attempt)
			require.True(t, d >= 0 && d < time.Second,
				"attempt=%d: got %v, want in [0, 1s)", attempt, d)
		}
	}
}

// TestCompute_FullJitterDistributionIsUniform confirms that jitter spreads results
// uniformly across the 32-second window at attempt 5. Uses bucket coverage,
// extreme bounds, and mean sanity to catch dead bands without RNG flakiness.
func TestCompute_FullJitterDistributionIsUniform(t *testing.T) {
	t.Parallel()

	const (
		n          = 10_000
		attempt    = 5
		numBuckets = 32
		// Per-bucket count for a uniform distribution follows Binomial(n, 1/numBuckets):
		// expected = n/numBuckets = 312.5, std dev = sqrt(n·(1/B)·(1-1/B)) ≈ 17.4.
		// ±50% slack (±156) is ≈ 9 std devs — false-positive probability is
		// effectively zero, so the test does not flake on a working RNG. Tighter
		// bounds (e.g. ±10% ≈ 1.8σ) would flake routinely; looser bounds would
		// stop catching bimodal {0, max} skew (variance W²/4 instead of W²/12).
		bucketMin = 156 // floor(312.5 · 0.5)
		bucketMax = 469 // ceil(312.5 · 1.5)
	)

	var (
		buckets [numBuckets]int
		total   int64
	)
	for i := 0; i < n; i++ {
		d := Compute(attempt)
		total += int64(d)
		idx := int(d / time.Second)
		if idx >= numBuckets {
			idx = numBuckets - 1
		}
		buckets[idx]++
	}

	// Every bucket must be hit — no dead bands in the distribution.
	for i, count := range buckets {
		require.True(t, count > 0,
			"bucket [%ds, %ds) has zero hits — dead band detected", i, i+1)
		require.True(t, count >= bucketMin && count <= bucketMax,
			"bucket [%ds, %ds) count %d not in [%d, %d]", i, i+1, count, bucketMin, bucketMax)
	}

	// Mean must be within 5% of the theoretical midpoint (16s).
	mean := time.Duration(total / n)
	require.InDelta(t, float64(16*time.Second), float64(mean), float64(16*time.Second)*0.05,
		"mean jitter %v not within 5%% of 16s", mean)
}
