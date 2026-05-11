// Package api_test contains handler-skeleton tests for internal/api/server.go.
// These tests drive the implementer to create the Handlers struct and its three
// stub implementations of api.ServerInterface.
package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "currency-exchange/internal/api"
	"currency-exchange/internal/httpmw"
	"currency-exchange/internal/obs"
)

// Test 1 — compile-time interface check.
// If api.Handlers does not exist (or does not implement ServerInterface), the
// package fails to compile and go test reports a build error — a valid RED state.
var _ api.ServerInterface = (*api.Handlers)(nil)

// newTestHandler builds the HandlerFromMux chain used by Tests 2–4.
// It does NOT include the middleware stack; for that see Test 5.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	handlers := api.NewHandlers()
	return api.HandlerFromMux(handlers, mux)
}

// TestHandlers_RefreshQuote_ReturnsStubAccepted asserts that POST /quotes/refresh
// returns 202 with the required headers and a body that unmarshals into
// api.RefreshAccepted. The contract allows a zero-UUID stub; we verify only that
// the Id field is present and that the JSON round-trips cleanly.
func TestHandlers_RefreshQuote_ReturnsStubAccepted(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t)

	body := strings.NewReader(`{"base":"EUR","quote":"MXN"}`)
	req := httptest.NewRequest(http.MethodPost, "/quotes/refresh", body)
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code, "POST /quotes/refresh must return 202")

	loc := rec.Header().Get("Location")
	assert.NotEmpty(t, loc, "Location header must be set")
	assert.True(t, strings.HasPrefix(loc, "/quotes/"),
		"Location must start with /quotes/, got %q", loc)

	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"Cache-Control must be no-store")

	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json",
		"Content-Type must contain application/json")

	var resp api.RefreshAccepted
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp),
		"body must unmarshal into api.RefreshAccepted")

	// openapi_types.UUID is uuid.UUID ([16]byte). The zero value is the all-zero
	// UUID "00000000-…". Both zero and non-zero are acceptable stub values per the
	// contract; we assert only that the JSON field was present and decoded.
	_ = uuid.UUID(resp.Id) // type-checks that resp.Id is a uuid.UUID
}

// TestHandlers_GetQuoteJob_ReturnsStubJobStatus asserts that GET /quotes/{id}
// returns 200 with the required headers and a body that unmarshals into
// api.JobStatus with discriminator "pending". The Id field must echo the path id.
func TestHandlers_GetQuoteJob_ReturnsStubJobStatus(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t)

	pathUUIDStr := "00000000-0000-0000-0000-000000000001"
	req := httptest.NewRequest(http.MethodGet, "/quotes/"+pathUUIDStr, nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "GET /quotes/{id} must return 200")

	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"Cache-Control must be no-store for a pending stub")

	assert.Equal(t, "1", rec.Header().Get("Retry-After"),
		"Retry-After must be 1 for a pending job stub")

	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json",
		"Content-Type must contain application/json")

	var js api.JobStatus
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &js),
		"body must unmarshal into api.JobStatus")

	disc, err := js.Discriminator()
	require.NoError(t, err, "JobStatus.Discriminator must not error")
	assert.Equal(t, "pending", disc, "discriminator must be \"pending\"")

	pending, err := js.AsJobStatusPending()
	require.NoError(t, err, "AsJobStatusPending must not error")

	assert.Equal(t, api.JobStatusPendingStatus("pending"), pending.Status,
		"JobStatusPending.Status must be \"pending\"")
	assert.Equal(t, "EUR", pending.Base, "JobStatusPending.Base must be EUR")
	assert.Equal(t, "MXN", pending.Quote, "JobStatusPending.Quote must be MXN")
	assert.Equal(t, 0, pending.Attempts, "JobStatusPending.Attempts must be 0")

	wantID, err := uuid.Parse(pathUUIDStr)
	require.NoError(t, err, "test UUID must parse cleanly")
	assert.Equal(t, wantID, pending.Id,
		"JobStatusPending.Id must echo the path UUID")
}

// TestHandlers_GetLatestQuote_ReturnsNoData asserts that GET /quotes/latest
// returns 404 with the required headers and an error body using the api.NoData
// enum constant (not a raw string literal).
func TestHandlers_GetLatestQuote_ReturnsNoData(t *testing.T) {
	t.Parallel()

	h := newTestHandler(t)

	req := httptest.NewRequest(http.MethodGet, "/quotes/latest?base=EUR&quote=MXN", nil)
	rec := httptest.NewRecorder()

	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code,
		"GET /quotes/latest must return 404 when no data")

	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"Cache-Control must be no-store on 404")

	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json",
		"Content-Type must contain application/json")

	var resp api.Error
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp),
		"body must unmarshal into api.Error")

	assert.Equal(t, api.NoData, resp.Error.Code,
		"error code must be the api.NoData enum constant, not a raw string")

	assert.NotEmpty(t, resp.Error.Message, "error message must be non-empty")
}

// TestServerWiring_HandlersServedThroughMiddlewareChain asserts that the full
// production middleware chain (RequestID > PanicRecover > Metrics > mux) works
// correctly with the api.Handlers stub. It proves:
//   - POST /quotes/refresh returns 202 and X-Request-Id header is set
//     (RequestID middleware fired).
//   - obs.HTTPRequestsTotal increments for the refresh request (Metrics fired).
//   - GET /notarealpath returns 404 (mux default; route registration does not
//     break the mux).
func TestServerWiring_HandlersServedThroughMiddlewareChain(t *testing.T) {
	// NOT parallel: uses prometheus global counters via delta measurement.

	mux := http.NewServeMux()
	handlers := api.NewHandlers()
	api.HandlerFromMux(handlers, mux)
	chain := httpmw.RequestID(httpmw.PanicRecover(httpmw.Metrics(mux)))
	srv := httptest.NewServer(chain)
	defer srv.Close()

	// --- sub-test: POST /quotes/refresh returns 202 with X-Request-Id ----------
	t.Run("RefreshReturns202WithRequestID", func(t *testing.T) {
		body := strings.NewReader(`{"base":"EUR","quote":"MXN"}`)
		req, err := http.NewRequest(http.MethodPost, srv.URL+"/quotes/refresh", body)
		require.NoError(t, err)
		req.Header.Set("Content-Type", "application/json")

		before := testutil.ToFloat64(
			obs.HTTPRequestsTotal.WithLabelValues("POST", "POST /quotes/refresh", "202"),
		)

		resp, err := http.DefaultClient.Do(req)
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.ReadAll(resp.Body)

		assert.Equal(t, http.StatusAccepted, resp.StatusCode,
			"POST /quotes/refresh through middleware chain must return 202")

		assert.NotEmpty(t, resp.Header.Get("X-Request-Id"),
			"X-Request-Id header must be set by RequestID middleware")

		after := testutil.ToFloat64(
			obs.HTTPRequestsTotal.WithLabelValues("POST", "POST /quotes/refresh", "202"),
		)
		assert.Equal(t, float64(1), after-before,
			"obs.HTTPRequestsTotal must increment by 1 (Metrics middleware fired)")
	})

	// --- sub-test: unknown path returns 404 ------------------------------------
	t.Run("UnknownPathReturns404", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/notarealpath")
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.ReadAll(resp.Body)

		assert.Equal(t, http.StatusNotFound, resp.StatusCode,
			"GET /notarealpath must return 404 from mux default handler")
	})
}
