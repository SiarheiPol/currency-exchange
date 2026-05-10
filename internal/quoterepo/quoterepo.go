// Package quoterepo defines the persistence boundary for fetched exchange-rate
// quotes. Implementations provide idempotent upsert semantics: a second write
// for the same (base, quote) pair overwrites the previous entry atomically.
package quoterepo

import (
	"context"

	"currency-exchange/internal/ratesprovider"
)

// QuoteRepo is the persistence boundary for fetched exchange-rate quotes.
type QuoteRepo interface {
	// UpsertBatch inserts or updates the given quotes atomically per quote.
	// Implementations use INSERT ... ON CONFLICT (base, quote) DO UPDATE for
	// idempotent overwrite semantics.
	UpsertBatch(ctx context.Context, quotes []ratesprovider.Quote) error
}
