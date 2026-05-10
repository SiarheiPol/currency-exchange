package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestState_RandomWalkDeterminism verifies that two State instances seeded with
// the same value produce identical Rate sequences and that the walk advances
// beyond the initial value.
func TestState_RandomWalkDeterminism(t *testing.T) {
	t.Parallel()

	const seed = uint64(42)
	a := NewState(seed, 100)
	b := NewState(seed, 100)

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

	s := NewState(42, 3)

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

	s := NewState(42, 100)
	got := s.AllRatesForBase("USD", []string{"USD", "EUR", "MXN"})

	_, hasSelf := got["USDUSD"]
	require.False(t, hasSelf, "self-pair USDUSD must be filtered out")

	_, hasEUR := got["USDEUR"]
	require.True(t, hasEUR, "USDEUR must be present in the result")

	_, hasMXN := got["USDMXN"]
	require.True(t, hasMXN, "USDMXN must be present in the result")
}
