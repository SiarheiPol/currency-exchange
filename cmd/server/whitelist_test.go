package main

import (
	"testing"

	"currency-exchange/internal/ratesprovider"

	"github.com/stretchr/testify/require"
)

func TestExpandWhitelist_ThreeCurrencies_ProducesSixDirectionalPairs(t *testing.T) {
	t.Parallel()

	result := expandWhitelist([]string{"USD", "EUR", "MXN"})

	require.Len(t, result, 6)

	seen := map[ratesprovider.Pair]int{}
	for _, p := range result {
		seen[p]++
	}

	directional := []ratesprovider.Pair{
		{Base: "USD", Quote: "EUR"},
		{Base: "USD", Quote: "MXN"},
		{Base: "EUR", Quote: "USD"},
		{Base: "EUR", Quote: "MXN"},
		{Base: "MXN", Quote: "USD"},
		{Base: "MXN", Quote: "EUR"},
	}
	for _, p := range directional {
		require.Equal(t, 1, seen[p], "expected exactly one occurrence of pair %v", p)
	}

	selfPairs := []ratesprovider.Pair{
		{Base: "USD", Quote: "USD"},
		{Base: "EUR", Quote: "EUR"},
		{Base: "MXN", Quote: "MXN"},
	}
	for _, p := range selfPairs {
		require.Equal(t, 0, seen[p], "expected no self-pair %v", p)
	}
}

func TestExpandWhitelist_TwoCurrencies_ProducesTwoPairs(t *testing.T) {
	t.Parallel()

	result := expandWhitelist([]string{"USD", "EUR"})

	require.ElementsMatch(t, []ratesprovider.Pair{
		{Base: "USD", Quote: "EUR"},
		{Base: "EUR", Quote: "USD"},
	}, result)
}
