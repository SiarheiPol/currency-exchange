// Package pgquoterepo provides a Postgres-backed implementation of
// quoterepo.QuoteRepo.
package pgquoterepo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/shopspring/decimal"

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

// GetLatest returns the stored quote for the given (base, quote) pair.
// Returns quoterepo.ErrNoData if no row exists; otherwise returns the stored
// quote and nil.
func (r *Repo) GetLatest(ctx context.Context, base, quote string) (ratesprovider.Quote, error) {
	var priceStr string
	var updatedAt time.Time
	err := r.pool.QueryRow(ctx,
		`SELECT price::text, updated_at FROM quotes WHERE base=$1 AND quote=$2`,
		base, quote,
	).Scan(&priceStr, &updatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ratesprovider.Quote{}, quoterepo.ErrNoData
	}
	if err != nil {
		return ratesprovider.Quote{}, fmt.Errorf("pgquoterepo get latest: %w", err)
	}
	price, err := decimal.NewFromString(priceStr)
	if err != nil {
		return ratesprovider.Quote{}, fmt.Errorf("pgquoterepo get latest: parse price: %w", err)
	}
	return ratesprovider.Quote{
		Pair:      ratesprovider.Pair{Base: base, Quote: quote},
		Price:     price,
		FetchedAt: updatedAt,
	}, nil
}
