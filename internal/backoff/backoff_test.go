package backoff

import (
	"math/rand/v2"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// TestCompute_Attempt0WindowIsBaseDelay confirms attempt 0 produces values in [0, 1s).
func TestCompute_Attempt0WindowIsBaseDelay(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 100; i++ {
		d := Compute(0, rng)
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
		rng := rand.New(rand.NewPCG(uint64(tc.attempt), 0))
		for i := 0; i < 100; i++ {
			d := Compute(tc.attempt, rng)
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
		rng := rand.New(rand.NewPCG(uint64(attempt), 7))
		for i := 0; i < 100; i++ {
			d := Compute(attempt, rng)
			require.True(t, d >= 0 && d < 60*time.Second,
				"attempt=%d: got %v, want in [0, 60s)", attempt, d)
		}
	}
}

// TestCompute_NegativeAttemptTreatedAsZero confirms the defensive clamp:
// negative attempts produce the same window as attempt 0.
func TestCompute_NegativeAttemptTreatedAsZero(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(3, 3))
	for _, attempt := range []int{-1, -100} {
		for i := 0; i < 50; i++ {
			d := Compute(attempt, rng)
			require.True(t, d >= 0 && d < time.Second,
				"attempt=%d: got %v, want in [0, 1s)", attempt, d)
		}
	}
}

// TestCompute_DeterministicWithFixedSeed confirms the function is purely a consumer
// of the injected rng — two identical seeds produce identical sequences.
func TestCompute_DeterministicWithFixedSeed(t *testing.T) {
	t.Parallel()

	rng1 := rand.New(rand.NewPCG(42, 99))
	rng2 := rand.New(rand.NewPCG(42, 99))

	attempts := []int{0, 1, 3, 6, 10}
	results1 := make([]time.Duration, len(attempts))
	results2 := make([]time.Duration, len(attempts))

	for i, attempt := range attempts {
		results1[i] = Compute(attempt, rng1)
	}
	for i, attempt := range attempts {
		results2[i] = Compute(attempt, rng2)
	}

	require.Equal(t, results1, results2)
}

// TestCompute_FullJitterDistributionIsUniform confirms that jitter spreads results
// uniformly across the window. Loose bound avoids RNG-version flakiness.
func TestCompute_FullJitterDistributionIsUniform(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(7, 13))
	var total int64
	const n = 1000
	for i := 0; i < n; i++ {
		d := Compute(5, rng)
		total += int64(d)
	}
	mean := time.Duration(total / n)

	require.True(t, mean >= 12*time.Second && mean < 20*time.Second,
		"mean jitter %v not in [12s, 20s) — distribution may not be uniform", mean)
}
