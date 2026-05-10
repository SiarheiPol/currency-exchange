// Package fake provides a test double for ratesprovider.RatesProvider.
package fake

import (
	"context"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/ratesprovider"
)

// Compile-time assertion that Fake satisfies RatesProvider.
var _ ratesprovider.RatesProvider = (*Fake)(nil)

// Fake is a hand-written test double for ratesprovider.RatesProvider.
// Callers initialise fields directly; no constructor is provided.
type Fake struct {
	Clock      clock.Clock
	Quotes     map[ratesprovider.Pair]ratesprovider.Quote
	BatchError *ratesprovider.ProviderError
	Calls      int
}

// FetchPairs implements ratesprovider.RatesProvider.
// It increments Calls unconditionally, returns BatchError immediately if set,
// otherwise returns quotes from Fake.Quotes with FetchedAt set to the current
// clock time. Pairs absent from Fake.Quotes are added to FetchResult.Missing
// (deduplicated).
func (f *Fake) FetchPairs(_ context.Context, pairs []ratesprovider.Pair) (ratesprovider.FetchResult, error) {
	f.Calls++

	if f.BatchError != nil {
		return ratesprovider.FetchResult{}, f.BatchError
	}

	now := f.Clock.Now()

	var result ratesprovider.FetchResult
	seen := make(map[ratesprovider.Pair]struct{})

	for _, p := range pairs {
		if q, ok := f.Quotes[p]; ok {
			if result.Quotes == nil {
				result.Quotes = make(map[ratesprovider.Pair]ratesprovider.Quote)
			}
			q.FetchedAt = now
			q.Pair = p
			result.Quotes[p] = q
		} else {
			if _, already := seen[p]; !already {
				seen[p] = struct{}{}
				result.Missing = append(result.Missing, p)
			}
		}
	}

	return result, nil
}
