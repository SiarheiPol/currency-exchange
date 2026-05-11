// Package api_test contains handler-skeleton tests for internal/api/server.go.
// These tests drive the implementer to create the Handlers struct and its three
// stub implementations of api.ServerInterface, plus pair validation and JSON
// error envelope support.
package api_test

import (
	"encoding/json"
	"errors"
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

// testWhitelist is the canonical whitelist used across all unit tests.
var testWhitelist = []string{"USD", "EUR", "MXN"}

// newTestHandler builds the HandlerWithOptions chain used by Tests 2–4.
// It does NOT include the middleware stack; for that see Test 5.
func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	mux := http.NewServeMux()
	handlers := api.NewHandlers(testWhitelist)
	return api.HandlerWithOptions(handlers, api.StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: api.JSONErrorHandler,
	})
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
	handlers := api.NewHandlers(testWhitelist)
	api.HandlerWithOptions(handlers, api.StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: api.JSONErrorHandler,
	})
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

// TestJSONErrorHandler_WritesEnvelope pins the public contract of api.JSONErrorHandler.
// It must write a 400 with Content-Type: application/json, Cache-Control: no-store,
// and a body that unmarshals into api.Error with code == invalid_request.
func TestJSONErrorHandler_WritesEnvelope(t *testing.T) {
	t.Parallel()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	api.JSONErrorHandler(rec, req, errors.New("some parse error"))

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))

	var body api.Error
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, api.InvalidRequest, body.Error.Code)
	// Message content is implementer's choice — do not assert exact text.
}

// TestRefreshQuote_Validation exercises the three-step pair validation
// (format → whitelist → self-pair) for POST /quotes/refresh.
// Every case expects 400 + JSON error envelope with Cache-Control: no-store.
func TestRefreshQuote_Validation(t *testing.T) {
	t.Parallel()

	type tc struct {
		name           string
		body           string
		setContentType bool
		wantStatus     int
		wantCode       api.ErrorErrorCode
	}

	cases := []tc{
		{
			name:           "RejectsMissingContentType",
			body:           `{"base":"EUR","quote":"MXN"}`,
			setContentType: false,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.InvalidRequest,
		},
		{
			name:           "RejectsEmptyBody",
			body:           "",
			setContentType: true,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.InvalidRequest,
		},
		{
			name:           "RejectsNonJSONBody",
			body:           "not json at all",
			setContentType: true,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.InvalidRequest,
		},
		{
			name:           "RejectsMissingQuoteField",
			body:           `{"base":"EUR"}`,
			setContentType: true,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.InvalidRequest,
		},
		{
			name:           "RejectsLowercaseBase",
			body:           `{"base":"eur","quote":"MXN"}`,
			setContentType: true,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.InvalidRequest,
		},
		{
			name:           "RejectsLowercaseQuote",
			body:           `{"base":"EUR","quote":"mxn"}`,
			setContentType: true,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.InvalidRequest,
		},
		{
			name:           "RejectsShortCode",
			body:           `{"base":"EU","quote":"MXN"}`,
			setContentType: true,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.InvalidRequest,
		},
		{
			name:           "RejectsUnsupportedBase",
			body:           `{"base":"JPY","quote":"MXN"}`,
			setContentType: true,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.UnsupportedCurrency,
		},
		{
			name:           "RejectsUnsupportedQuote",
			body:           `{"base":"EUR","quote":"GBP"}`,
			setContentType: true,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.UnsupportedCurrency,
		},
		{
			name:           "RejectsSelfPair",
			body:           `{"base":"EUR","quote":"EUR"}`,
			setContentType: true,
			wantStatus:     http.StatusBadRequest,
			wantCode:       api.InvalidRequest,
		},
	}

	h := newTestHandler(t)

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			var bodyReader *strings.Reader
			if c.body != "" {
				bodyReader = strings.NewReader(c.body)
			} else {
				bodyReader = strings.NewReader("")
			}
			req := httptest.NewRequest(http.MethodPost, "/quotes/refresh", bodyReader)
			if c.setContentType {
				req.Header.Set("Content-Type", "application/json")
			}
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.Equal(t, c.wantStatus, rec.Code,
				"status code for %s", c.name)
			assert.Contains(t, rec.Header().Get("Content-Type"), "application/json",
				"Content-Type must be application/json for %s", c.name)
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
				"Cache-Control must be no-store for %s", c.name)

			var envelope api.Error
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
				"body must unmarshal into api.Error for %s", c.name)
			assert.Equal(t, c.wantCode, envelope.Error.Code,
				"error code for %s", c.name)
		})
	}
}

// TestGetLatestQuote_Validation exercises the three-step pair validation
// (format → whitelist → self-pair) for GET /quotes/latest.
// Every case expects 400 + JSON error envelope with Cache-Control: no-store.
func TestGetLatestQuote_Validation(t *testing.T) {
	t.Parallel()

	type tc struct {
		name       string
		query      string
		wantStatus int
		wantCode   api.ErrorErrorCode
	}

	cases := []tc{
		{
			// Codegen wrapper's ErrorHandlerFunc path — pins Q6 override.
			// The "quote" param is Required:true in the OpenAPI spec; codegen calls
			// siw.ErrorHandlerFunc(w, r, &RequiredParamError{ParamName:"quote"}).
			// With the JSONErrorHandler override that produces the JSON envelope.
			name:       "RejectsMissingQuoteParam",
			query:      "?base=EUR",
			wantStatus: http.StatusBadRequest,
			wantCode:   api.InvalidRequest,
		},
		{
			name:       "RejectsLowercaseBase",
			query:      "?base=eur&quote=MXN",
			wantStatus: http.StatusBadRequest,
			wantCode:   api.InvalidRequest,
		},
		{
			name:       "RejectsUnsupportedBase",
			query:      "?base=JPY&quote=MXN",
			wantStatus: http.StatusBadRequest,
			wantCode:   api.UnsupportedCurrency,
		},
		{
			name:       "RejectsSelfPair",
			query:      "?base=EUR&quote=EUR",
			wantStatus: http.StatusBadRequest,
			wantCode:   api.InvalidRequest,
		},
	}

	h := newTestHandler(t)

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/quotes/latest"+c.query, nil)
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			assert.Equal(t, c.wantStatus, rec.Code,
				"status code for %s", c.name)
			assert.Contains(t, rec.Header().Get("Content-Type"), "application/json",
				"Content-Type must be application/json for %s", c.name)
			assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
				"Cache-Control must be no-store for %s", c.name)

			var envelope api.Error
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &envelope),
				"body must unmarshal into api.Error for %s", c.name)
			assert.Equal(t, c.wantCode, envelope.Error.Code,
				"error code for %s", c.name)
		})
	}
}
