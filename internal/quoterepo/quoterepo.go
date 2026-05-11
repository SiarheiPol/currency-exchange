// Package quoterepo defines the persistence boundary for fetched exchange-rate
// quotes. Implementations provide idempotent upsert semantics: a second write
// for the same (base, quote) pair overwrites the previous entry atomically.
package quoterepo

import (
	"context"
	"errors"

	"currency-exchange/internal/ratesprovider"
)

// ErrNoData is returned by GetLatest when no quote exists for the requested pair.
var ErrNoData = errors.New("no quote for pair")

// QuoteRepo is the persistence boundary for fetched exchange-rate quotes.
type QuoteRepo interface {
	// UpsertBatch inserts or updates the given quotes atomically per quote.
	// Implementations use INSERT ... ON CONFLICT (base, quote) DO UPDATE for
	// idempotent overwrite semantics.
	UpsertBatch(ctx context.Context, quotes []ratesprovider.Quote) error

	// GetLatest returns the stored quote for the given (base, quote) pair.
	// Returns ErrNoData if no row exists for the pair; otherwise returns the
	// stored quote and nil.
	GetLatest(ctx context.Context, base, quote string) (ratesprovider.Quote, error)
}
