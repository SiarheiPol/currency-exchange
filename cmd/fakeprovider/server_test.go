package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/ratesprovider"
	"currency-exchange/internal/ratesprovider/apilayer"
)

// liveResponse mirrors the JSON shape returned by the /live handler.
type liveResponse struct {
	Success   bool               `json:"success"`
	Timestamp int64              `json:"timestamp"`
	Source    string             `json:"source"`
	Quotes    map[string]float64 `json:"quotes"`
	Error     *liveError         `json:"error,omitempty"`
}

type liveError struct {
	Code int    `json:"code"`
	Info string `json:"info"`
}

func getLive(t *testing.T, srv *httptest.Server, path string) (int, liveResponse) {
	t.Helper()
	resp, err := srv.Client().Get(srv.URL + path)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	var body liveResponse
	require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
	return resp.StatusCode, body
}

// TestLiveHandler_RoundTrip_ApilayerClientParses verifies that the /live
// endpoint produces a response that the apilayer.Provider client can
// successfully parse via FetchPairs.
func TestLiveHandler_RoundTrip_ApilayerClientParses(t *testing.T) {
	t.Parallel()

	state := NewState(42, 100)
	server := NewServer(state, "", clock.New())
	srv := httptest.NewServer(server)
	defer srv.Close()

	provider := &apilayer.Provider{
		APIKey:     "testkey",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}

	result, err := provider.FetchPairs(context.Background(), []ratesprovider.Pair{
		{Base: "USD", Quote: "EUR"},
	})

	require.NoError(t, err)
	_, hasUSDEUR := result.Quotes[ratesprovider.Pair{Base: "USD", Quote: "EUR"}]
	require.True(t, hasUSDEUR, "result must contain USD/EUR pair")
	require.Nil(t, result.Missing, "Missing must be nil when pair is present")

	q := result.Quotes[ratesprovider.Pair{Base: "USD", Quote: "EUR"}]
	require.False(t, q.FetchedAt.IsZero(), "FetchedAt must be non-zero")
}

// TestLiveHandler_MissingAccessKey_Returns101 verifies that a GET /live request
// with no access_key query parameter returns success=false with error code 101.
func TestLiveHandler_MissingAccessKey_Returns101(t *testing.T) {
	t.Parallel()

	state := NewState(42, 100)
	server := NewServer(state, "", clock.New())
	srv := httptest.NewServer(server)
	defer srv.Close()

	statusCode, body := getLive(t, srv, "/live")

	require.Equal(t, http.StatusOK, statusCode)
	require.False(t, body.Success)
	require.NotNil(t, body.Error)
	require.Equal(t, 101, body.Error.Code)
}

// TestLiveHandler_EmptyAccessKey_Returns101 verifies that a GET /live request
// with access_key= (empty value) returns success=false with error code 101.
func TestLiveHandler_EmptyAccessKey_Returns101(t *testing.T) {
	t.Parallel()

	state := NewState(42, 100)
	server := NewServer(state, "", clock.New())
	srv := httptest.NewServer(server)
	defer srv.Close()

	statusCode, body := getLive(t, srv, "/live?access_key=")

	require.Equal(t, http.StatusOK, statusCode)
	require.False(t, body.Success)
	require.NotNil(t, body.Error)
	require.Equal(t, 101, body.Error.Code)
}

// TestLiveHandler_QuotaExhausted_Returns104 verifies that after quota is
// consumed, subsequent requests return success=false with error code 104.
func TestLiveHandler_QuotaExhausted_Returns104(t *testing.T) {
	t.Parallel()

	state := NewState(42, 1)
	server := NewServer(state, "", clock.New())
	srv := httptest.NewServer(server)
	defer srv.Close()

	// First request should succeed.
	statusCode1, body1 := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR")
	require.Equal(t, http.StatusOK, statusCode1)
	require.True(t, body1.Success, "first request must succeed while quota available")

	// Second request should fail with code 104.
	statusCode2, body2 := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR")
	require.Equal(t, http.StatusOK, statusCode2)
	require.False(t, body2.Success)
	require.NotNil(t, body2.Error)
	require.Equal(t, 104, body2.Error.Code)
}

// TestLiveHandler_SourceDefaultsToUSD verifies that when the source query
// parameter is absent, the response uses USD as the source currency.
func TestLiveHandler_SourceDefaultsToUSD(t *testing.T) {
	t.Parallel()

	state := NewState(42, 100)
	server := NewServer(state, "", clock.New())
	srv := httptest.NewServer(server)
	defer srv.Close()

	statusCode, body := getLive(t, srv, "/live?access_key=testkey&currencies=EUR")

	require.Equal(t, http.StatusOK, statusCode)
	require.True(t, body.Success)
	require.Equal(t, "USD", body.Source)
	_, hasUSDEUR := body.Quotes["USDEUR"]
	require.True(t, hasUSDEUR, "quotes must contain USDEUR when source defaults to USD")
}

// TestLiveHandler_CurrenciesFilterRespected verifies that when currencies=EUR
// is specified, only the USDEUR quote is returned and USDMXN is absent.
func TestLiveHandler_CurrenciesFilterRespected(t *testing.T) {
	t.Parallel()

	state := NewState(42, 100)
	server := NewServer(state, "", clock.New())
	srv := httptest.NewServer(server)
	defer srv.Close()

	statusCode, body := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR")

	require.Equal(t, http.StatusOK, statusCode)
	require.True(t, body.Success)
	require.Len(t, body.Quotes, 1, "only one pair must be returned when currencies=EUR")
	_, hasUSDEUR := body.Quotes["USDEUR"]
	require.True(t, hasUSDEUR, "USDEUR must be present")
	_, hasMXN := body.Quotes["USDMXN"]
	require.False(t, hasMXN, "USDMXN must NOT be present when not requested")
}

// TestLiveHandler_TimestampIsRecentUnixSeconds verifies that the timestamp in
// a successful response is within ±60 seconds of the current wall time.
func TestLiveHandler_TimestampIsRecentUnixSeconds(t *testing.T) {
	t.Parallel()

	state := NewState(42, 100)
	server := NewServer(state, "", clock.New())
	srv := httptest.NewServer(server)
	defer srv.Close()

	before := time.Now().Unix()
	statusCode, body := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR")
	after := time.Now().Unix()

	require.Equal(t, http.StatusOK, statusCode)
	require.True(t, body.Success)
	require.GreaterOrEqual(t, body.Timestamp, before-60, "timestamp must not be more than 60s in the past")
	require.LessOrEqual(t, body.Timestamp, after+60, "timestamp must not be more than 60s in the future")
}
