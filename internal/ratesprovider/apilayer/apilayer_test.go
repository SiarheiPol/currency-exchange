package apilayer_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"currency-exchange/internal/ratesprovider"
	"currency-exchange/internal/ratesprovider/apilayer"
)

// helpers

func newProvider(t *testing.T, srv *httptest.Server) *apilayer.Provider {
	t.Helper()
	return &apilayer.Provider{
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}
}

func successBody(source string, timestamp int64, quotes map[string]float64) string {
	type payload struct {
		Success   bool               `json:"success"`
		Timestamp int64              `json:"timestamp"`
		Source    string             `json:"source"`
		Quotes    map[string]float64 `json:"quotes"`
	}
	b, _ := json.Marshal(payload{
		Success:   true,
		Timestamp: timestamp,
		Source:    source,
		Quotes:    quotes,
	})
	return string(b)
}

func errorBody(code int, info string) string {
	type apiError struct {
		Code int    `json:"code"`
		Info string `json:"info"`
	}
	type payload struct {
		Success bool     `json:"success"`
		Error   apiError `json:"error"`
	}
	b, _ := json.Marshal(payload{
		Success: false,
		Error:   apiError{Code: code, Info: info},
	})
	return string(b)
}

// ---- Test 1: single base, all pairs returned ----

// TestProvider_SingleBase_AllPairsReturned verifies the happy path for a
// single-base fetch: both requested pairs appear in Quotes, FetchedAt is
// derived from the upstream timestamp, Missing is nil, and exactly one HTTP
// request was issued.
func TestProvider_SingleBase_AllPairsReturned(t *testing.T) {
	t.Parallel()

	var reqCount int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successBody("USD", 1778354527, map[string]float64{
			"USDEUR": 0.84804,
			"USDMXN": 17.177604,
		})))
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	pairs := []ratesprovider.Pair{
		{Base: "USD", Quote: "EUR"},
		{Base: "USD", Quote: "MXN"},
	}

	result, err := p.FetchPairs(context.Background(), pairs)

	require.NoError(t, err)
	require.Nil(t, result.Missing)
	require.Len(t, result.Quotes, 2)

	wantTime := time.Unix(1778354527, 0)
	eurQuote, ok := result.Quotes[ratesprovider.Pair{Base: "USD", Quote: "EUR"}]
	require.True(t, ok, "USD/EUR must be in Quotes")
	require.Equal(t, wantTime, eurQuote.FetchedAt)

	mxnQuote, ok := result.Quotes[ratesprovider.Pair{Base: "USD", Quote: "MXN"}]
	require.True(t, ok, "USD/MXN must be in Quotes")
	require.Equal(t, wantTime, mxnQuote.FetchedAt)

	require.Equal(t, 1, reqCount, "exactly one HTTP request must be issued")
}

// ---- Test 2: single base, some pairs missing ----

// TestProvider_SingleBase_SomePairsMissing verifies that pairs absent from the
// upstream quotes map appear in FetchResult.Missing while present pairs appear
// in FetchResult.Quotes.
func TestProvider_SingleBase_SomePairsMissing(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successBody("USD", 1778354527, map[string]float64{
			"USDEUR": 0.84804,
			// USDMXN intentionally absent
		})))
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	pairs := []ratesprovider.Pair{
		{Base: "USD", Quote: "EUR"},
		{Base: "USD", Quote: "MXN"},
	}

	result, err := p.FetchPairs(context.Background(), pairs)

	require.NoError(t, err)
	require.Len(t, result.Quotes, 1)
	_, hasEUR := result.Quotes[ratesprovider.Pair{Base: "USD", Quote: "EUR"}]
	require.True(t, hasEUR, "USD/EUR must be in Quotes")
	require.ElementsMatch(t, []ratesprovider.Pair{{Base: "USD", Quote: "MXN"}}, result.Missing)
}

// ---- Test 3: duplicate input pairs, Missing deduplicated ----

// TestProvider_MissingDeduplication verifies that when the input slice contains
// duplicate pairs and neither appears in the upstream response, Missing contains
// each unique pair exactly once.
func TestProvider_MissingDeduplication(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successBody("USD", 1778354527, map[string]float64{})))
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	// duplicate USDEUR in input
	pairs := []ratesprovider.Pair{
		{Base: "USD", Quote: "EUR"},
		{Base: "USD", Quote: "EUR"},
		{Base: "USD", Quote: "MXN"},
	}

	result, err := p.FetchPairs(context.Background(), pairs)

	require.NoError(t, err)
	require.Empty(t, result.Quotes)
	require.Len(t, result.Missing, 2, "Missing must contain exactly 2 unique pairs")
	require.ElementsMatch(t, []ratesprovider.Pair{
		{Base: "USD", Quote: "EUR"},
		{Base: "USD", Quote: "MXN"},
	}, result.Missing)
}

// ---- Test 4: API error code 101 → permanent ----

// TestProvider_ErrorCode101_Permanent verifies that API error code 101
// (invalid access key) maps to ProviderError.Code "permanent" with
// IsTransient() == false, and FetchResult is zero.
func TestProvider_ErrorCode101_Permanent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(errorBody(101, "You have not supplied a valid API Access Key.")))
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	result, err := p.FetchPairs(context.Background(), []ratesprovider.Pair{{Base: "USD", Quote: "EUR"}})

	require.Error(t, err)
	var pe *ratesprovider.ProviderError
	require.True(t, errors.As(err, &pe), "error must unwrap to *ProviderError")
	require.Equal(t, "permanent", pe.Code)
	require.Equal(t, 101, pe.APICode)
	require.False(t, pe.IsTransient())
	require.Empty(t, result.Quotes)
	require.Empty(t, result.Missing)
}

// ---- Test 5: API error code 104 → quota_exceeded ----

// TestProvider_ErrorCode104_QuotaExceeded verifies that API error code 104
// (monthly quota exceeded) maps to ProviderError.Code "quota_exceeded" with
// IsTransient() == true.
func TestProvider_ErrorCode104_QuotaExceeded(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(errorBody(104, "monthly quota exceeded")))
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	result, err := p.FetchPairs(context.Background(), []ratesprovider.Pair{{Base: "USD", Quote: "EUR"}})

	require.Error(t, err)
	var pe *ratesprovider.ProviderError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, "quota_exceeded", pe.Code)
	require.Equal(t, 104, pe.APICode)
	require.True(t, pe.IsTransient())
	require.Empty(t, result.Quotes)
	require.Empty(t, result.Missing)
}

// ---- Test 6: unknown error code → permanent (fail-safe) ----

// TestProvider_UnknownErrorCode_Permanent verifies that an unrecognised API
// error code maps to "permanent" as the fail-safe default.
func TestProvider_UnknownErrorCode_Permanent(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(errorBody(9999, "future error")))
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	result, err := p.FetchPairs(context.Background(), []ratesprovider.Pair{{Base: "USD", Quote: "EUR"}})

	require.Error(t, err)
	var pe *ratesprovider.ProviderError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, "permanent", pe.Code, "unknown codes must default to permanent")
	require.Equal(t, 9999, pe.APICode)
	require.Empty(t, result.Quotes)
	require.Empty(t, result.Missing)
}

// ---- Test 7: HTTP 500 → transient (body not parsed) ----

// TestProvider_HTTP500_Transient verifies that a non-200 HTTP status code
// produces a ProviderError with Code "transient" and the matching HTTPCode,
// and that the body is not parsed.
func TestProvider_HTTP500_Transient(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	result, err := p.FetchPairs(context.Background(), []ratesprovider.Pair{{Base: "USD", Quote: "EUR"}})

	require.Error(t, err)
	var pe *ratesprovider.ProviderError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, "transient", pe.Code)
	require.Equal(t, 500, pe.HTTPCode)
	require.True(t, pe.IsTransient())
	require.Empty(t, result.Quotes)
	require.Empty(t, result.Missing)
}

// ---- Test 8: malformed JSON → transient ----

// TestProvider_MalformedJSON_Transient verifies that a 200 response with a
// non-JSON body produces a ProviderError with Code "transient" and a non-nil
// Cause.
func TestProvider_MalformedJSON_Transient(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("not json"))
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	result, err := p.FetchPairs(context.Background(), []ratesprovider.Pair{{Base: "USD", Quote: "EUR"}})

	require.Error(t, err)
	var pe *ratesprovider.ProviderError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, "transient", pe.Code)
	require.NotNil(t, pe.Cause)
	require.Empty(t, result.Quotes)
	require.Empty(t, result.Missing)
}

// ---- Test 9: network failure → transient ----

// TestProvider_NetworkFailure_Transient verifies that a connection failure
// (server closed before the call) produces a ProviderError with Code
// "transient" and a non-nil Cause.
func TestProvider_NetworkFailure_Transient(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	srv.Close() // close immediately before FetchPairs is called

	p := &apilayer.Provider{
		BaseURL:    srv.URL,
		HTTPClient: http.DefaultClient,
	}
	result, err := p.FetchPairs(context.Background(), []ratesprovider.Pair{{Base: "USD", Quote: "EUR"}})

	require.Error(t, err)
	var pe *ratesprovider.ProviderError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, "transient", pe.Code)
	require.NotNil(t, pe.Cause)
	require.Empty(t, result.Quotes)
	require.Empty(t, result.Missing)
}

// ---- Test 10: context cancelled → transient ----

// TestProvider_ContextCancelled_Transient verifies that a context cancellation
// mid-flight produces a ProviderError with Code "transient" and a Cause chain
// that reaches context.Canceled.
func TestProvider_ContextCancelled_Transient(t *testing.T) {
	t.Parallel()

	block := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-block // never unblocks
	}))
	defer func() {
		close(block)
		srv.Close()
	}()

	p := newProvider(t, srv)
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	result, err := p.FetchPairs(ctx, []ratesprovider.Pair{{Base: "USD", Quote: "EUR"}})

	require.Error(t, err)
	var pe *ratesprovider.ProviderError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, "transient", pe.Code)
	require.True(t, errors.Is(pe.Cause, context.Canceled), "cause chain must reach context.Canceled")
	require.Empty(t, result.Quotes)
	require.Empty(t, result.Missing)
}

// ---- Test 11: multiple bases, results merged ----

// TestProvider_MultipleBases_ResultsMerged verifies that when pairs span two
// distinct bases, the provider issues one HTTP call per base and merges the
// results into a single FetchResult.
func TestProvider_MultipleBases_ResultsMerged(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	reqCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqCount++
		mu.Unlock()

		source := r.URL.Query().Get("source")
		w.Header().Set("Content-Type", "application/json")
		switch source {
		case "EUR":
			_, _ = w.Write([]byte(successBody("EUR", 1778354527, map[string]float64{
				"EURUSD": 1.18,
			})))
		case "USD":
			_, _ = w.Write([]byte(successBody("USD", 1778354527, map[string]float64{
				"USDEUR": 0.84,
			})))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	pairs := []ratesprovider.Pair{
		{Base: "EUR", Quote: "USD"},
		{Base: "USD", Quote: "EUR"},
	}

	result, err := p.FetchPairs(context.Background(), pairs)

	require.NoError(t, err)
	require.Nil(t, result.Missing)
	require.Len(t, result.Quotes, 2)
	_, hasEURUSD := result.Quotes[ratesprovider.Pair{Base: "EUR", Quote: "USD"}]
	require.True(t, hasEURUSD, "EUR/USD must be in Quotes")
	_, hasUSDEUR := result.Quotes[ratesprovider.Pair{Base: "USD", Quote: "EUR"}]
	require.True(t, hasUSDEUR, "USD/EUR must be in Quotes")

	mu.Lock()
	count := reqCount
	mu.Unlock()
	require.Equal(t, 2, count, "exactly two HTTP requests must be issued")
}

// ---- Test 12: multiple bases called in lexical order ----

// TestProvider_MultipleBases_LexicalOrder verifies that when input pairs span
// multiple bases in non-sorted order, the HTTP calls are issued in lexical
// order of the base currency.
func TestProvider_MultipleBases_LexicalOrder(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var sources []string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		source := r.URL.Query().Get("source")
		mu.Lock()
		sources = append(sources, source)
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successBody(source, 1778354527, map[string]float64{})))
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	// bases in non-sorted input order: MXN, EUR, USD
	pairs := []ratesprovider.Pair{
		{Base: "MXN", Quote: "USD"},
		{Base: "EUR", Quote: "USD"},
		{Base: "USD", Quote: "EUR"},
	}

	_, err := p.FetchPairs(context.Background(), pairs)
	require.NoError(t, err)

	mu.Lock()
	got := make([]string, len(sources))
	copy(got, sources)
	mu.Unlock()

	require.Equal(t, []string{"EUR", "MXN", "USD"}, got)
}

// ---- Test 13: first base fails → fail-fast, second base never called ----

// TestProvider_MultipleBases_FirstFails_FailFast verifies that when the first
// base (lexically) returns an error response, the batch fails immediately and
// no subsequent base call is issued.
func TestProvider_MultipleBases_FirstFails_FailFast(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	reqCount := 0

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		reqCount++
		mu.Unlock()

		source := r.URL.Query().Get("source")
		w.Header().Set("Content-Type", "application/json")
		switch source {
		case "EUR": // called first (lexical); returns quota error
			_, _ = w.Write([]byte(errorBody(104, "quota")))
		case "USD": // must never be called
			_, _ = w.Write([]byte(successBody("USD", 1778354527, map[string]float64{
				"USDEUR": 0.84,
			})))
		default:
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	p := newProvider(t, srv)
	pairs := []ratesprovider.Pair{
		{Base: "EUR", Quote: "USD"},
		{Base: "USD", Quote: "EUR"},
	}

	result, err := p.FetchPairs(context.Background(), pairs)

	require.Error(t, err)
	var pe *ratesprovider.ProviderError
	require.True(t, errors.As(err, &pe))
	require.Equal(t, "quota_exceeded", pe.Code)
	require.Equal(t, 104, pe.APICode)
	require.Empty(t, result.Quotes)
	require.Empty(t, result.Missing)

	mu.Lock()
	count := reqCount
	mu.Unlock()
	require.Equal(t, 1, count, "only one HTTP request must be issued (fail-fast)")
}

// ---- Test 14: request shape (method, access_key, source, currencies, body) ----

// TestProvider_RequestShape verifies that the provider issues a GET request
// with the correct query parameters and an empty body.
func TestProvider_RequestShape(t *testing.T) {
	t.Parallel()

	var (
		capturedMethod     string
		capturedQuery      map[string]string
		capturedBodyLength int
		capturedCurrencies []string
	)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedMethod = r.Method
		q := r.URL.Query()
		capturedQuery = map[string]string{
			"access_key": q.Get("access_key"),
			"source":     q.Get("source"),
		}
		capturedCurrencies = strings.Split(q.Get("currencies"), ",")
		capturedBodyLength = int(r.ContentLength)
		if capturedBodyLength < 0 {
			capturedBodyLength = 0
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(successBody("USD", 1778354527, map[string]float64{
			"USDEUR": 0.84804,
			"USDMXN": 17.177604,
		})))
	}))
	defer srv.Close()

	p := &apilayer.Provider{
		APIKey:     "TESTKEY",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
	}

	pairs := []ratesprovider.Pair{
		{Base: "USD", Quote: "EUR"},
		{Base: "USD", Quote: "MXN"},
	}

	_, err := p.FetchPairs(context.Background(), pairs)
	require.NoError(t, err)

	require.Equal(t, http.MethodGet, capturedMethod)
	require.Equal(t, "TESTKEY", capturedQuery["access_key"])
	require.Equal(t, "USD", capturedQuery["source"])
	require.ElementsMatch(t, []string{"EUR", "MXN"}, capturedCurrencies)
	require.Equal(t, 0, capturedBodyLength)
}
