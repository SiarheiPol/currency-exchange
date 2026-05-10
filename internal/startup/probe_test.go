package startup_test

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
	"currency-exchange/internal/startup"
)

func TestProbe_BatchError_ReturnsError(t *testing.T) {
	t.Parallel()

	f := &fake.Fake{
		BatchError: &ratesprovider.ProviderError{
			Code:    "permanent",
			APICode: 101,
			Message: "invalid key",
		},
	}

	err := startup.Probe(context.Background(), f)

	require.Error(t, err)
	var provErr *ratesprovider.ProviderError
	require.True(t, errors.As(err, &provErr), "error chain must contain *ratesprovider.ProviderError")
	require.Equal(t, 101, provErr.APICode)
}

func TestProbe_TransientBatchError_AlsoAborts(t *testing.T) {
	t.Parallel()

	f := &fake.Fake{
		BatchError: &ratesprovider.ProviderError{
			Code:    "transient",
			Message: "network blip",
		},
	}

	err := startup.Probe(context.Background(), f)

	require.Error(t, err)
}

func TestProbe_MissingPair_ReturnsError(t *testing.T) {
	t.Parallel()

	f := &fake.Fake{
		Clock:  clock.NewFake(time.Unix(0, 0)),
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{},
	}

	err := startup.Probe(context.Background(), f)

	require.Error(t, err)
}

func TestProbe_HappyPath_ReturnsNil(t *testing.T) {
	t.Parallel()

	testPair := ratesprovider.Pair{Base: "USD", Quote: "EUR"}
	f := &fake.Fake{
		Clock: clock.NewFake(time.Unix(0, 0)),
		Quotes: map[ratesprovider.Pair]ratesprovider.Quote{
			testPair: {Price: decimal.NewFromFloat(0.84)},
		},
	}

	err := startup.Probe(context.Background(), f)

	require.NoError(t, err)
}
