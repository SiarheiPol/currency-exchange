package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
)

// TestState_RandomWalkDeterminism verifies that two State instances seeded with
// the same value produce identical Rate sequences and that the walk advances
// beyond the initial value.
func TestState_RandomWalkDeterminism(t *testing.T) {
	t.Parallel()

	const seed = uint64(42)
	a := NewState(seed, 100, 0, clock.New())
	b := NewState(seed, 100, 0, clock.New())

	p := Pair{Base: "USD", Quote: "EUR"}

	var aRates, bRates [3]float64
	for i := 0; i < 3; i++ {
		ra, ok := a.Rate(p)
		require.True(t, ok, "Rate must return ok=true for known pair")
		require.Greater(t, ra, 0.0, "rate must be > 0")
		aRates[i] = ra

		rb, ok := b.Rate(p)
		require.True(t, ok, "Rate must return ok=true for known pair")
		require.Greater(t, rb, 0.0, "rate must be > 0")
		bRates[i] = rb
	}

	require.Equal(t, aRates, bRates, "identical seeds must produce identical rate sequences")

	// At least one value must differ from the initial 0.85 (walk has advanced).
	advanced := false
	for _, r := range aRates {
		if r != 0.85 {
			advanced = true
			break
		}
	}
	require.True(t, advanced, "random walk must advance at least one rate away from the initial 0.85")
}

// TestState_QuotaDecrementAndExhaustion verifies that Consume returns true up
// to the quota limit and false thereafter (sticky exhaustion).
func TestState_QuotaDecrementAndExhaustion(t *testing.T) {
	t.Parallel()

	s := NewState(42, 3, 0, clock.New())

	require.True(t, s.Consume(), "first Consume must return true")
	require.True(t, s.Consume(), "second Consume must return true")
	require.True(t, s.Consume(), "third Consume must return true")
	require.False(t, s.Consume(), "fourth Consume must return false (quota exhausted)")
	require.False(t, s.Consume(), "fifth Consume must return false (exhaustion is sticky)")
}

// TestState_AllRatesForBase_FiltersSelfPair verifies that AllRatesForBase
// filters out the self-pair (base==target) and returns the other requested
// pairs.
func TestState_AllRatesForBase_FiltersSelfPair(t *testing.T) {
	t.Parallel()

	s := NewState(42, 100, 0, clock.New())
	got := s.AllRatesForBase("USD", []string{"USD", "EUR", "MXN"})

	_, hasSelf := got["USDUSD"]
	require.False(t, hasSelf, "self-pair USDUSD must be filtered out")

	_, hasEUR := got["USDEUR"]
	require.True(t, hasEUR, "USDEUR must be present in the result")

	_, hasMXN := got["USDMXN"]
	require.True(t, hasMXN, "USDMXN must be present in the result")
}

// TestState_Cadence_ZeroMeansAdvanceEveryCall verifies that when cadenceSeconds=0,
// successive Rate calls without clock advancement still produce different values
// (today's behaviour preserved).
func TestState_Cadence_ZeroMeansAdvanceEveryCall(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Unix(0, 0))
	s := NewState(42, 100, 0, clk)

	p := Pair{Base: "USD", Quote: "EUR"}

	r1, ok1 := s.Rate(p)
	require.True(t, ok1)
	r2, ok2 := s.Rate(p)
	require.True(t, ok2)

	require.NotEqual(t, r1, r2, "cadence=0 must advance the walk on every call (values must differ)")
}

// TestState_Cadence_SameWindow_ReturnsCachedRate verifies that when cadenceSeconds=60
// and the clock does not advance, two Rate calls return byte-identical float64 values.
func TestState_Cadence_SameWindow_ReturnsCachedRate(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Unix(0, 0))
	s := NewState(42, 100, 60, clk)

	p := Pair{Base: "USD", Quote: "EUR"}

	r1, ok1 := s.Rate(p)
	require.True(t, ok1)
	r2, ok2 := s.Rate(p)
	require.True(t, ok2)

	require.Equal(t, r1, r2, "cadence=60 must return identical rate within the same window")
}

// TestState_Cadence_WindowBoundary_AdvancesWalk verifies that advancing the clock
// by exactly cadenceSeconds crosses a window boundary and the next Rate call
// returns a different value.
func TestState_Cadence_WindowBoundary_AdvancesWalk(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Unix(0, 0))
	s := NewState(42, 100, 60, clk)

	p := Pair{Base: "USD", Quote: "EUR"}

	r1, ok1 := s.Rate(p)
	require.True(t, ok1)

	clk.Advance(60 * time.Second)

	r2, ok2 := s.Rate(p)
	require.True(t, ok2)

	require.NotEqual(t, r1, r2, "advancing clock past window boundary must cause a new rate to be computed")
}

// TestState_Cadence_SkippedWindow_AdvancesOnce verifies that skipping multiple
// windows causes exactly one walk advance (not multiple), and subsequent calls
// within the new window return the same cached value.
func TestState_Cadence_SkippedWindow_AdvancesOnce(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Unix(0, 0))
	s := NewState(42, 100, 60, clk)

	p := Pair{Base: "USD", Quote: "EUR"}

	r1, ok1 := s.Rate(p)
	require.True(t, ok1)

	// Skip two full windows.
	clk.Advance(120 * time.Second)

	r2, ok2 := s.Rate(p)
	require.True(t, ok2)

	r3, ok3 := s.Rate(p)
	require.True(t, ok3)

	require.NotEqual(t, r1, r2, "skipping windows must produce a new rate")
	require.Equal(t, r2, r3, "second and third calls within the new window must return the same cached rate")
}

// TestState_Cadence_AllRatesForBase_CachedWithinWindow verifies that calling
// AllRatesForBase twice within the same cadence window returns deeply-equal maps
// for all requested pairs.
func TestState_Cadence_AllRatesForBase_CachedWithinWindow(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Unix(0, 0))
	s := NewState(42, 100, 60, clk)

	m1 := s.AllRatesForBase("USD", []string{"EUR", "MXN"})
	m2 := s.AllRatesForBase("USD", []string{"EUR", "MXN"})

	require.Equal(t, m1, m2, "AllRatesForBase must return identical maps within the same cadence window")
}
