// Package ratesprovider defines the types and interface for fetching currency
// exchange rates from an external source.
package ratesprovider

import (
	"context"
	"fmt"
	"time"

	"github.com/shopspring/decimal"
)

// Quote holds a single fetched exchange rate for one currency.
type Quote struct {
	// Currency is the ISO 4217 code of the quoted currency, e.g. "EUR".
	Currency string
	// Price is the exchange rate expressed as a decimal value.
	Price decimal.Decimal
	// FetchedAt is when the quote was obtained. The provider sets this field;
	// if the upstream response does not supply a timestamp the client is
	// expected to record the time of the request.
	FetchedAt time.Time
}

// FetchResult is the outcome of a single FetchBatch call.
type FetchResult struct {
	// Quotes holds the successfully retrieved exchange rates, keyed by ISO 4217
	// currency code. It is nil or empty when no currency succeeded.
	Quotes map[string]Quote
	// Errors holds per-currency failures for any currency that could not be
	// fetched. It is nil or empty when every currency succeeded.
	Errors map[string]*ProviderError
}

// RatesProvider is the interface that any upstream exchange-rate source must
// implement. Callers supply a list of currency codes and receive a FetchResult
// that may contain both successful quotes and per-currency errors.
type RatesProvider interface {
	// FetchBatch fetches exchange rates for the given currency codes in a
	// single round-trip where possible. A non-nil error is returned only for
	// failures that affect the entire batch; per-currency failures are
	// communicated via FetchResult.Errors.
	FetchBatch(ctx context.Context, currencies []string) (FetchResult, error)
}

// ProviderError describes a failure that occurred while fetching the exchange
// rate for a single currency.
type ProviderError struct {
	// Code classifies the failure: "transient", "permanent", or
	// "quota_exceeded".
	Code string
	// HTTPCode is the HTTP status code returned by the upstream, or zero if
	// the failure was not an HTTP error.
	HTTPCode int
	// Message is a human-readable description of the failure.
	Message string
	// Cause is the underlying error, if any.
	Cause error
}

// Error implements the error interface and returns a string that includes the
// error code, HTTP status code (when non-zero), message, and the cause chain.
func (e *ProviderError) Error() string {
	s := fmt.Sprintf("provider error [%s]", e.Code)
	if e.HTTPCode != 0 {
		s += fmt.Sprintf(" http=%d", e.HTTPCode)
	}
	s += ": " + e.Message
	if e.Cause != nil {
		s += ": " + e.Cause.Error()
	}
	return s
}

// Unwrap returns the underlying cause of the error, enabling errors.Is and
// errors.As to traverse the chain.
func (e *ProviderError) Unwrap() error {
	return e.Cause
}

// IsTransient reports whether the error is expected to be temporary and the
// request should be retried. It returns true when Code is "transient" or
// "quota_exceeded", and false for all other values including the empty string.
func (e *ProviderError) IsTransient() bool {
	return e.Code == "transient" || e.Code == "quota_exceeded"
}
