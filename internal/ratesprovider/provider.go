// Package ratesprovider defines the types and interface for fetching currency
// exchange rates from an external source.
package ratesprovider

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Pair identifies a directional currency pair.
// Base and Quote are ISO 4217 codes (e.g. "EUR", "MXN").
// The convention is: Price means "1 unit of Base = Price units of Quote".
// Self-pairs (Base == Quote) are not valid; the service never creates them.
type Pair struct {
	Base  string
	Quote string
}

// Quote holds a single successfully fetched exchange rate for one currency pair.
type Quote struct {
	// Pair identifies the base and quote currencies.
	Pair Pair
	// Price is the exchange rate: 1 unit of Base = Price units of Quote.
	Price decimal.Decimal
	// FetchedAt is the provider's notion of when the quote was valid.
	// For providers that supply a per-response timestamp (e.g. apilayer-family
	// "timestamp" Unix seconds), this is that value. For providers without a
	// timestamp, the concrete provider sets it to the time of the request.
	// Callers must not assume FetchedAt <= time.Now() (upstream clock skew is
	// possible) nor that FetchedAt is comparable across providers.
	FetchedAt time.Time
}

// FetchResult is the outcome of a single FetchPairs call.
type FetchResult struct {
	// Quotes holds successfully fetched exchange rates, keyed by Pair.
	// Nil or empty when no pair succeeded.
	Quotes map[Pair]Quote
	// Errors holds per-pair failures synthesised or reported by the provider.
	// Nil or empty when every requested pair succeeded.
	Errors map[Pair]*ProviderError
}

// RatesProvider is the interface any upstream exchange-rate source must implement.
type RatesProvider interface {
	// FetchPairs fetches exchange rates for the given pairs in as few round-trips
	// as the provider supports. A non-nil error is returned only for failures that
	// affect the entire call. Per-pair failures are communicated via FetchResult.Errors.
	FetchPairs(ctx context.Context, pairs []Pair) (FetchResult, error)
}

// ProviderError describes a failure that occurred while fetching rates.
type ProviderError struct {
	// Code classifies the failure: "transient", "permanent", or "quota_exceeded".
	Code string
	// HTTPCode is the HTTP status code returned by the upstream, or zero.
	HTTPCode int
	// APICode is the numeric API error code from the upstream JSON response
	// (e.g. 101 for invalid key, 104 for quota exhaustion), or zero when the
	// failure was not an application-layer error (network, timeout, malformed body).
	APICode int
	// Message is a human-readable description of the failure.
	Message string
	// Cause is the underlying error, if any.
	Cause error
}

// Error implements the error interface and returns a string that includes the
// error code, HTTP status code (when non-zero), API code (when non-zero),
// message, and the cause chain.
func (e *ProviderError) Error() string {
	s := fmt.Sprintf("provider error [%s]", e.Code)
	if e.HTTPCode != 0 {
		s += fmt.Sprintf(" http=%d", e.HTTPCode)
	}
	if e.APICode != 0 {
		s += fmt.Sprintf(" api_code=%d", e.APICode)
	}
	s += ": " + e.Message
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

// Unwrap returns the underlying cause for errors.Is / errors.As.
func (e *ProviderError) Unwrap() error {
	return e.Cause
}

// IsTransient reports whether the error indicates a retryable upstream failure.
// Returns true for "transient" and "quota_exceeded"; false for "permanent" and "".
func (e *ProviderError) IsTransient() bool {
	return e.Code == "transient" || e.Code == "quota_exceeded"
}
