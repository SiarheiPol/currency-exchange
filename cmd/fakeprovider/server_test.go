package main

import (
	"context"
	"encoding/json"
	"math/rand/v2"
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

// noLatency is a zero-latency LatencyConfig for tests that don't exercise latency.
func noLatency() LatencyConfig {
	return LatencyConfig{MinMS: 0, MaxMS: 0, RNG: rand.New(rand.NewPCG(0, 0))}
}

// TestLiveHandler_RoundTrip_ApilayerClientParses verifies that the /live
// endpoint produces a response that the apilayer.Provider client can
// successfully parse via FetchPairs.
func TestLiveHandler_RoundTrip_ApilayerClientParses(t *testing.T) {
	t.Parallel()

	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), noLatency())
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

	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), noLatency())
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

	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), noLatency())
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

	state := NewState(42, 1, 0, clock.New())
	server := NewServer(state, "", clock.New(), noLatency())
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

	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), noLatency())
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

	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), noLatency())
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

	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), noLatency())
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

// TestLiveHandler_Cadence_TimestampIdenticalWithinWindow verifies that when
// cadenceSeconds=60 and the clock is at t=100 (windowStart=60), two /live
// calls without clock advancement return identical timestamp and quotes.
func TestLiveHandler_Cadence_TimestampIdenticalWithinWindow(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Unix(100, 0))
	state := NewState(42, 100, 60, clk)
	server := NewServer(state, "", clk, noLatency())
	srv := httptest.NewServer(server)
	defer srv.Close()

	_, body1 := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR,MXN")
	_, body2 := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR,MXN")

	require.Equal(t, int64(60), body1.Timestamp, "timestamp must be quantised to window start (60)")
	require.Equal(t, body1.Timestamp, body2.Timestamp, "timestamp must be identical within a cadence window")
	require.Equal(t, body1.Quotes, body2.Quotes, "quotes must be identical within a cadence window")
}

// TestLiveHandler_Cadence_QuotaConsumedOnSuppressedCall verifies that a
// cadence-suppressed call (same window, cached rate) still decrements the
// monthly quota, mirroring real apilayer behaviour.
func TestLiveHandler_Cadence_QuotaConsumedOnSuppressedCall(t *testing.T) {
	t.Parallel()

	clk := clock.NewFake(time.Unix(0, 0))
	state := NewState(42, 2, 60, clk)
	server := NewServer(state, "", clk, noLatency())
	srv := httptest.NewServer(server)
	defer srv.Close()

	const path = "/live?access_key=testkey&source=USD&currencies=EUR"

	// First call — quota 2 → 1, succeeds.
	_, body1 := getLive(t, srv, path)
	require.True(t, body1.Success, "first call must succeed")

	// Second call, no clock advance (cadence-suppressed) — quota 1 → 0, succeeds.
	_, body2 := getLive(t, srv, path)
	require.True(t, body2.Success, "second call (suppressed) must succeed and consume quota")

	// Third call — quota exhausted, returns 104.
	_, body3 := getLive(t, srv, path)
	require.False(t, body3.Success, "third call must fail after quota exhausted")
	require.NotNil(t, body3.Error)
	require.Equal(t, 104, body3.Error.Code, "error code must be 104 for quota exhaustion")
}

// TestLiveHandler_Latency_ZeroMinMax_NoDelay verifies that LatencyConfig{0,0}
// does not introduce any measurable sleep on a successful /live call.
func TestLiveHandler_Latency_ZeroMinMax_NoDelay(t *testing.T) {
	t.Parallel()

	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), LatencyConfig{MinMS: 0, MaxMS: 0, RNG: rand.New(rand.NewPCG(0, 0))})
	srv := httptest.NewServer(server)
	defer srv.Close()

	start := time.Now()
	_, body := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR")
	elapsed := time.Since(start)

	require.True(t, body.Success)
	require.Less(t, elapsed, 50*time.Millisecond, "zero latency config must not introduce delay (elapsed: %v)", elapsed)
}

// TestLiveHandler_Latency_FixedDelay_AtLeastMin verifies that when min==max==50ms
// the handler sleeps for at least 50ms before responding.
func TestLiveHandler_Latency_FixedDelay_AtLeastMin(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(99, 0))
	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), LatencyConfig{MinMS: 50, MaxMS: 50, RNG: rng})
	srv := httptest.NewServer(server)
	defer srv.Close()

	start := time.Now()
	_, body := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR")
	elapsed := time.Since(start)

	require.True(t, body.Success)
	require.GreaterOrEqual(t, elapsed, 50*time.Millisecond, "fixed 50ms latency must delay response by at least 50ms")
}

// TestLiveHandler_Latency_RangeDelay_InBounds verifies that when latency is
// configured as [20ms, 60ms], the actual response time falls within
// [20ms, 200ms] (lower bound load-bearing; upper is generous for CI scheduling).
func TestLiveHandler_Latency_RangeDelay_InBounds(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(99, 0))
	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), LatencyConfig{MinMS: 20, MaxMS: 60, RNG: rng})
	srv := httptest.NewServer(server)
	defer srv.Close()

	start := time.Now()
	_, body := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR")
	elapsed := time.Since(start)

	require.True(t, body.Success)
	require.GreaterOrEqual(t, elapsed, 20*time.Millisecond, "latency must be at least MinMS=20ms (elapsed: %v)", elapsed)
	require.LessOrEqual(t, elapsed, 200*time.Millisecond, "latency must not exceed 200ms ceiling (elapsed: %v)", elapsed)
}

// TestLiveHandler_Latency_CancelledRequest_ExitsBeforeMax verifies that when
// the client cancels the request before the sleep completes, the handler exits
// promptly (well before the configured 500ms latency).
func TestLiveHandler_Latency_CancelledRequest_ExitsBeforeMax(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(99, 0))
	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), LatencyConfig{MinMS: 500, MaxMS: 500, RNG: rng})
	srv := httptest.NewServer(server)
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		srv.URL+"/live?access_key=testkey&source=USD&currencies=EUR", nil)
	require.NoError(t, err)

	start := time.Now()
	_, _ = srv.Client().Do(req) //nolint:bodyclose // response may be nil on cancellation
	elapsed := time.Since(start)

	require.Less(t, elapsed, 200*time.Millisecond,
		"cancelled request must not wait for full 500ms latency (elapsed: %v)", elapsed)
}

// TestLiveHandler_Latency_AuthFailure_NoDelay verifies that an auth failure
// (missing access_key → code 101) returns immediately without sleeping, even
// when latency is configured.
func TestLiveHandler_Latency_AuthFailure_NoDelay(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(99, 0))
	state := NewState(42, 100, 0, clock.New())
	server := NewServer(state, "", clock.New(), LatencyConfig{MinMS: 200, MaxMS: 200, RNG: rng})
	srv := httptest.NewServer(server)
	defer srv.Close()

	start := time.Now()
	statusCode, body := getLive(t, srv, "/live") // no access_key
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, statusCode)
	require.False(t, body.Success)
	require.NotNil(t, body.Error)
	require.Equal(t, 101, body.Error.Code)
	require.Less(t, elapsed, 50*time.Millisecond,
		"auth failure must bypass sleep (elapsed: %v)", elapsed)
}

// TestLiveHandler_Latency_QuotaExhausted_NoDelay verifies that a quota-exhausted
// response (code 104) returns immediately without sleeping, even when latency is
// configured.
func TestLiveHandler_Latency_QuotaExhausted_NoDelay(t *testing.T) {
	t.Parallel()

	rng := rand.New(rand.NewPCG(99, 0))
	state := NewState(42, 0, 0, clock.New()) // quota=0 → immediately exhausted
	server := NewServer(state, "", clock.New(), LatencyConfig{MinMS: 200, MaxMS: 200, RNG: rng})
	srv := httptest.NewServer(server)
	defer srv.Close()

	start := time.Now()
	statusCode, body := getLive(t, srv, "/live?access_key=testkey&source=USD&currencies=EUR")
	elapsed := time.Since(start)

	require.Equal(t, http.StatusOK, statusCode)
	require.False(t, body.Success)
	require.NotNil(t, body.Error)
	require.Equal(t, 104, body.Error.Code)
	require.Less(t, elapsed, 50*time.Millisecond,
		"quota exhaustion must bypass sleep (elapsed: %v)", elapsed)
}
