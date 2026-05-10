// Package pgquoterepo provides a Postgres-backed implementation of
// quoterepo.QuoteRepo.
package pgquoterepo

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"

	"currency-exchange/internal/quoterepo"
	"currency-exchange/internal/ratesprovider"
)

// Compile-time assertion that Repo satisfies QuoteRepo.
var _ quoterepo.QuoteRepo = (*Repo)(nil)

// Repo is a Postgres-backed implementation of quoterepo.QuoteRepo.
type Repo struct {
	pool *pgxpool.Pool
}

// New returns a Repo that uses pool for storage.
func New(pool *pgxpool.Pool) *Repo {
	return &Repo{pool: pool}
}

// UpsertBatch inserts or updates each quote in qs. The operation is idempotent:
// a second write for the same (base, quote) pair overwrites the previous entry.
func (r *Repo) UpsertBatch(ctx context.Context, qs []ratesprovider.Quote) error {
	for _, q := range qs {
		_, err := r.pool.Exec(ctx, `
			INSERT INTO quotes (base, quote, price, updated_at)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (base, quote) DO UPDATE
			  SET price      = EXCLUDED.price,
			      updated_at = EXCLUDED.updated_at`,
			q.Pair.Base, q.Pair.Quote, q.Price, q.FetchedAt,
		)
		if err != nil {
			return fmt.Errorf("pgquoterepo upsert (%s/%s): %w", q.Pair.Base, q.Pair.Quote, err)
		}
	}
	return nil
}
