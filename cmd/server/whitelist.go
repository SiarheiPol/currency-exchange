package main

import "currency-exchange/internal/ratesprovider"

// expandWhitelist returns all directional pairs (Base != Quote) for the given
// currencies. For N currencies, returns N*(N-1) pairs.
func expandWhitelist(currs []string) []ratesprovider.Pair {
	pairs := make([]ratesprovider.Pair, 0, len(currs)*(len(currs)-1))
	for _, base := range currs {
		for _, quote := range currs {
			if base == quote {
				continue
			}
			pairs = append(pairs, ratesprovider.Pair{Base: base, Quote: quote})
		}
	}
	return pairs
}
