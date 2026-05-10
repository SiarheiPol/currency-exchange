package fake_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/ratesprovider"
	"currency-exchange/internal/ratesprovider/fake"
)

var (
	pairUSDEUR = ratesprovider.Pair{Base: "USD", Quote: "EUR"}
	pairUSDMXN = ratesprovider.Pair{Base: "USD", Quote: "MXN"}
)

// TestFetchPairs_AllFound_ReturnsQuotes_NoMissing verifies the success path:
// when every requested pair is pre-loaded in Fake.Quotes, FetchPairs returns
// all quotes with FetchedAt set to the current clock time, Missing is nil, and
// no error is returned.
func TestFetchPairs_AllFound_ReturnsQuotes_NoMissing(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)

	f := &fake.Fake{
		Clock: clk,
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{
			pairUSDEUR: {Pair: pairUSDEUR, Price: decimal.NewFromFloat(0.92)},
			pairUSDMXN: {Pair: pairUSDMXN, Price: decimal.NewFromFloat(17.10)},
		},
	}

	ctx := context.Background()
	result, err := f.FetchPairs(ctx, []ratesprovider.Pair{pairUSDEUR, pairUSDMXN})

	require.NoError(t, err)
	require.Nil(t, result.Missing)
	require.Len(t, result.Quotes, 2)
	require.Equal(t, t0, result.Quotes[pairUSDEUR].FetchedAt)
	require.Equal(t, t0, result.Quotes[pairUSDMXN].FetchedAt)
}

// TestFetchPairs_SomeMissing_AppearsInMissing verifies the partial-success path:
// pairs absent from Fake.Quotes appear in FetchResult.Missing while present
// pairs appear in FetchResult.Quotes.
func TestFetchPairs_SomeMissing_AppearsInMissing(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)

	f := &fake.Fake{
		Clock: clk,
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{
			pairUSDEUR: {Pair: pairUSDEUR, Price: decimal.NewFromFloat(0.92)},
		},
	}

	ctx := context.Background()
	result, err := f.FetchPairs(ctx, []ratesprovider.Pair{pairUSDEUR, pairUSDMXN})

	require.NoError(t, err)
	require.Len(t, result.Quotes, 1)
	_, hasEUR := result.Quotes[pairUSDEUR]
	require.True(t, hasEUR, "USDEUR must be in Quotes")
	require.ElementsMatch(t, []ratesprovider.Pair{pairUSDMXN}, result.Missing)
}

// TestFetchPairs_DuplicateInputPairs_MissingDeduplicated verifies that when the
// input slice contains duplicate pairs and none are in Fake.Quotes, Missing
// contains each unique pair exactly once.
func TestFetchPairs_DuplicateInputPairs_MissingDeduplicated(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC))

	f := &fake.Fake{
		Clock:  clk,
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{},
	}

	ctx := context.Background()
	result, err := f.FetchPairs(ctx, []ratesprovider.Pair{pairUSDEUR, pairUSDEUR})

	require.NoError(t, err)
	require.Empty(t, result.Quotes)
	require.Len(t, result.Missing, 1)
	require.Equal(t, pairUSDEUR, result.Missing[0])
}

// TestFetchPairs_BatchError_ReturnsTypedError_EmptyResult verifies the
// batch-failure path: when Fake.BatchError is set, FetchPairs returns that
// exact *ProviderError as the error, and the FetchResult is empty (no Quotes,
// no Missing), even when Fake.Quotes is non-empty.
func TestFetchPairs_BatchError_ReturnsTypedError_EmptyResult(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC))

	batchErr := &ratesprovider.ProviderError{
		Code:     "quota_exceeded",
		HTTPCode: 429,
		APICode:  104,
		Message:  "monthly quota exceeded",
	}

	f := &fake.Fake{
		Clock: clk,
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{
			pairUSDEUR: {Pair: pairUSDEUR, Price: decimal.NewFromFloat(0.92)},
		},
		BatchError: batchErr,
	}

	ctx := context.Background()
	result, err := f.FetchPairs(ctx, []ratesprovider.Pair{pairUSDEUR})

	require.Error(t, err)

	var pe *ratesprovider.ProviderError
	require.True(t, errors.As(err, &pe), "error must unwrap to *ProviderError")
	require.Same(t, batchErr, pe, "returned ProviderError must be the same pointer as BatchError")

	require.Empty(t, result.Quotes)
	require.Empty(t, result.Missing)
}

// TestFetchPairs_CallsIncrements_AcrossPatterns verifies that Fake.Calls is
// incremented on every FetchPairs invocation regardless of the outcome
// (success, batch error, or partial-success with missing pairs), and that the
// fake does not mutate Fake.Quotes itself.
func TestFetchPairs_CallsIncrements_AcrossPatterns(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC))

	f := &fake.Fake{
		Clock: clk,
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{
			pairUSDEUR: {Pair: pairUSDEUR, Price: decimal.NewFromFloat(0.92)},
			pairUSDMXN: {Pair: pairUSDMXN, Price: decimal.NewFromFloat(17.10)},
		},
	}

	ctx := context.Background()

	// Call 1: success path — both pairs present.
	_, err := f.FetchPairs(ctx, []ratesprovider.Pair{pairUSDEUR})
	require.NoError(t, err)

	// Call 2: batch-error path.
	f.BatchError = &ratesprovider.ProviderError{Code: "transient", Message: "net"}
	_, err = f.FetchPairs(ctx, []ratesprovider.Pair{pairUSDEUR})
	require.Error(t, err)

	// Call 3: partial-missing path — caller removes USDEUR from Quotes.
	f.BatchError = nil
	delete(f.Quotes, pairUSDEUR)
	_, err = f.FetchPairs(ctx, []ratesprovider.Pair{pairUSDEUR})
	require.NoError(t, err)

	require.Equal(t, 3, f.Calls)

	// USDMXN was pre-loaded and never touched by the test or the fake.
	_, hasMXN := f.Quotes[pairUSDMXN]
	require.True(t, hasMXN, "Fake must not have deleted USDMXN from Quotes")
}

// TestFetchPairs_EmptyInput_EmptyResult_CallIncrements verifies that an empty
// input slice produces an empty result with no error, and that Calls is still
// incremented.
func TestFetchPairs_EmptyInput_EmptyResult_CallIncrements(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC))

	f := &fake.Fake{
		Clock: clk,
	}

	ctx := context.Background()
	result, err := f.FetchPairs(ctx, []ratesprovider.Pair{})

	require.NoError(t, err)
	require.Empty(t, result.Quotes)
	require.Nil(t, result.Missing)
	require.Equal(t, 1, f.Calls)
}

// TestFetchPairs_FetchedAt_PerCallTimestamp verifies that FetchedAt reflects
// the clock time at the moment of each individual call, so that advancing the
// fake clock between calls produces different FetchedAt values.
func TestFetchPairs_FetchedAt_PerCallTimestamp(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)

	f := &fake.Fake{
		Clock: clk,
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{
			pairUSDEUR: {Pair: pairUSDEUR, Price: decimal.NewFromFloat(0.92)},
		},
	}

	ctx := context.Background()

	result1, err := f.FetchPairs(ctx, []ratesprovider.Pair{pairUSDEUR})
	require.NoError(t, err)
	firstFetchedAt := result1.Quotes[pairUSDEUR].FetchedAt

	clk.Advance(1 * time.Second)

	result2, err := f.FetchPairs(ctx, []ratesprovider.Pair{pairUSDEUR})
	require.NoError(t, err)
	secondFetchedAt := result2.Quotes[pairUSDEUR].FetchedAt

	require.Equal(t, t0, firstFetchedAt)
	require.Equal(t, t0.Add(1*time.Second), secondFetchedAt)
}
