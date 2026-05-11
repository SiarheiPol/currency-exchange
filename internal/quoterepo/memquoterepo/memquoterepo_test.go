// Package memquoterepo_test contains unit tests for the in-memory QuoteRepo.
package memquoterepo_test

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/quoterepo"
	"currency-exchange/internal/quoterepo/memquoterepo"
	"currency-exchange/internal/ratesprovider"
)

// TestGetLatest_NotFound asserts that GetLatest on an empty repo returns
// quoterepo.ErrNoData.
func TestGetLatest_NotFound(t *testing.T) {
	t.Parallel()

	r := memquoterepo.New()
	_, err := r.GetLatest(context.Background(), "EUR", "MXN")
	require.Error(t, err)
	assert.ErrorIs(t, err, quoterepo.ErrNoData,
		"empty repo must return quoterepo.ErrNoData")
}

// TestGetLatest_Found asserts that after UpsertBatch the stored quote is
// returned by GetLatest with matching pair, price, and FetchedAt.
func TestGetLatest_Found(t *testing.T) {
	t.Parallel()

	r := memquoterepo.New()
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
	assert.Equal(t, wantTime, got.FetchedAt)
}
