//go:build integration

// Package pgquoterepo_test contains integration tests for the Postgres-backed
// QuoteRepo. Each test creates its own schema via pgtest.NewDB for isolation.
package pgquoterepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/quoterepo"
	"currency-exchange/internal/quoterepo/pgquoterepo"
	"currency-exchange/internal/ratesprovider"
	"currency-exchange/internal/testhelper/pgtest"
)

// TestGetLatest_NotFound asserts that GetLatest on an empty schema returns
// quoterepo.ErrNoData.
func TestGetLatest_NotFound(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t)
	r := pgquoterepo.New(pool)

	_, err := r.GetLatest(context.Background(), "EUR", "MXN")
	require.Error(t, err)
	assert.ErrorIs(t, err, quoterepo.ErrNoData,
		"no row in quotes table must return quoterepo.ErrNoData")
}

// TestGetLatest_Found asserts that after UpsertBatch the stored quote is
// returned by GetLatest with matching pair, price, and FetchedAt.
func TestGetLatest_Found(t *testing.T) {
	t.Parallel()

	pool := pgtest.NewDB(t)
	r := pgquoterepo.New(pool)

	wantPrice := decimal.RequireFromString("20.255648")
	wantTime := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)

	err := r.UpsertBatch(context.Background(), []ratesprovider.Quote{
		{
			Pair:      ratesprovider.Pair{Base: "EUR", Quote: "MXN"},
			Price:     wantPrice,
			FetchedAt: wantTime,
		},
	})
	require.NoError(t, err)

	got, err := r.GetLatest(context.Background(), "EUR", "MXN")
	require.NoError(t, err)

	assert.Equal(t, "EUR", got.Pair.Base)
	assert.Equal(t, "MXN", got.Pair.Quote)
	assert.True(t, wantPrice.Equal(got.Price), "price must match")
	assert.WithinDuration(t, wantTime, got.FetchedAt, time.Second,
		"FetchedAt must round-trip through Postgres within 1s")
}
