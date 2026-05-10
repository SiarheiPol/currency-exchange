// Package startup contains synchronous capability checks executed at
// service boot before traffic is accepted.
package startup

import (
	"context"
	"errors"
	"fmt"

	"currency-exchange/internal/ratesprovider"
)

// probePair is the hardcoded test pair for the capability check.
// USD/EUR is supported by every realistic upstream and tests are written
// against this exact pair.
var probePair = ratesprovider.Pair{Base: "USD", Quote: "EUR"}

// Probe issues one capability-check FetchPairs call against p. It returns
// nil iff probePair is present in FetchResult.Quotes; any failure (batch
// error, missing pair, transient or permanent) results in a non-nil error.
// Callers should treat any non-nil return as a fatal startup condition.
func Probe(ctx context.Context, p ratesprovider.RatesProvider) error {
	res, err := p.FetchPairs(ctx, []ratesprovider.Pair{probePair})
	if err != nil {
		return fmt.Errorf("startup probe: provider returned error: %w", err)
	}
	if _, ok := res.Quotes[probePair]; ok {
		return nil
	}
	// Pair absent from Quotes — likely in Missing (silent drop).
	return errors.New("startup probe: provider did not return USD/EUR (silent drop)")
}
