// Package api_test contains tests for the Swagger UI and raw OpenAPI spec
// handlers defined in internal/api/swagger.go.
//
// RED phase: internal/api/swagger.go does not exist yet. Every test in this
// file will produce a compile error on api.OpenAPIJSONHandler and
// api.SwaggerUIHandler — that is the expected RED state.
package api_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	api "currency-exchange/internal/api"
	"currency-exchange/internal/httpmw"
)

// ---- compile-time signature checks -----------------------------------------

// Both handlers must satisfy http.Handler. If the implementer changes the type
// (e.g. uses a plain func instead of http.HandlerFunc), these lines fail to
// compile and the test is automatically RED.
var _ http.Handler = api.OpenAPIJSONHandler
var _ http.Handler = api.SwaggerUIHandler

// ---- Test 1 — raw spec endpoint --------------------------------------------

// TestOpenAPIJSONHandler_ReturnsValidSpec asserts that OpenAPIJSONHandler
// serves a valid JSON OpenAPI document at /openapi.json with the correct
// Content-Type and the expected top-level structure.
func TestOpenAPIJSONHandler_ReturnsValidSpec(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()

	api.OpenAPIJSONHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"GET /openapi.json must return 200")

	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json",
		"Content-Type must contain application/json")

	body := rec.Body.Bytes()
	assert.NotEmpty(t, body, "body must be non-empty")

	// Confirm the body is valid JSON.
	var doc map[string]any
	require.NoError(t, json.Unmarshal(body, &doc),
		"body must unmarshal into map[string]any — must be valid JSON")

	// Confirm top-level OpenAPI document keys are present.
	for _, key := range []string{"openapi", "info", "paths"} {
		assert.Contains(t, doc, key,
			"OpenAPI document must have top-level key %q", key)
	}

	// Confirm the three API paths are declared in the spec.
	paths, ok := doc["paths"].(map[string]any)
	require.True(t, ok, "\"paths\" must be a JSON object")

	for _, wantPath := range []string{
		"/quotes/refresh",
		"/quotes/{id}",
		"/quotes/latest",
	} {
		assert.Contains(t, paths, wantPath,
			"spec paths must contain %q", wantPath)
	}
}

// ---- Test 2 — Swagger UI root ----------------------------------------------

// TestSwaggerUIHandler_DocsRootReturnsHTML asserts that SwaggerUIHandler
// serves an HTML document at /docs/ that:
//   - references /openapi.json (not the default petstore URL),
//   - contains "swagger-ui" (proves it is the Swagger UI bundle),
//   - does NOT reference any CDN hostname (proves offline vendoring).
func TestSwaggerUIHandler_DocsRootReturnsHTML(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/docs/", nil)
	rec := httptest.NewRecorder()

	api.SwaggerUIHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"GET /docs/ must return 200")

	assert.Contains(t, rec.Header().Get("Content-Type"), "text/html",
		"Content-Type must contain text/html")

	bodyStr := rec.Body.String()

	assert.True(t,
		strings.Contains(strings.ToLower(bodyStr), "swagger-ui"),
		"body must contain \"swagger-ui\" (case-insensitive)")

	assert.Contains(t, bodyStr, "/openapi.json",
		"index.html must reference /openapi.json so the UI loads our spec, not petstore")

	// Vendored bundle must be offline-only — no CDN references.
	cdnHosts := []string{"cdn.jsdelivr.net", "unpkg.com", "cdnjs.cloudflare.com"}
	for _, cdn := range cdnHosts {
		assert.NotContains(t, bodyStr, cdn,
			"index.html must NOT reference CDN host %q — bundle must be fully vendored", cdn)
	}
}

// ---- Test 3 — Swagger UI static asset -------------------------------------

// TestSwaggerUIHandler_ServesCSS asserts that SwaggerUIHandler serves the
// swagger-ui.css file that is part of every Swagger UI dist bundle from 3.x
// onward. This filename is stable across versions and confirms the file server
// is wired to the correct directory.
//
// Path used: /docs/swagger-ui.css
// Rationale: swagger-ui.css has been stable since Swagger UI 3.x; swagger-ui-
// bundle.js names have varied more between major versions. CSS is the safer
// choice for a version-agnostic asset-presence test.
func TestSwaggerUIHandler_ServesCSS(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/docs/swagger-ui.css", nil)
	rec := httptest.NewRecorder()

	api.SwaggerUIHandler.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code,
		"GET /docs/swagger-ui.css must return 200")

	ct := rec.Header().Get("Content-Type")
	assert.True(t,
		strings.Contains(ct, "css") || strings.Contains(ct, "text/plain"),
		"Content-Type must indicate CSS content, got %q", ct)

	assert.Greater(t, rec.Body.Len(), 1000,
		"swagger-ui.css must be > 1000 bytes (not an empty stub)")
}

// ---- Test 4 — both endpoints through the full middleware chain -------------

// TestSwaggerRoutes_ThroughMiddlewareChain asserts that both /openapi.json and
// /docs/ work correctly through the full production middleware stack:
//
//	RequestID → PanicRecover → Metrics → OpenAPIValidate → mux
//
// Specifically:
//   - GET /openapi.json returns 200, X-Request-Id is set, body is valid JSON.
//   - GET /docs/ returns 200, X-Request-Id is set.
//
// The OpenAPIValidate middleware passes non-spec paths through (see
// openapivalidate.go: "Path not in spec — pass through; 404 handled by mux").
// /openapi.json and /docs/ are not in the OpenAPI spec, so they bypass
// validation and reach the mux handlers directly.
func TestSwaggerRoutes_ThroughMiddlewareChain(t *testing.T) {
	// Not parallel: uses prometheus global counters (Metrics middleware).

	mux := http.NewServeMux()
	mux.Handle("/openapi.json", api.OpenAPIJSONHandler)
	mux.Handle("/docs/", api.SwaggerUIHandler)

	spec, err := api.GetSpec()
	require.NoError(t, err, "GetSpec must not error")

	inner := httpmw.OpenAPIValidate(spec, mux)
	chain := httpmw.RequestID(httpmw.PanicRecover(httpmw.Metrics(inner)))

	srv := httptest.NewServer(chain)
	defer srv.Close()

	t.Run("OpenAPIJSON_Returns200WithRequestID", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/openapi.json") //nolint:noctx
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()

		body, err := io.ReadAll(resp.Body)
		require.NoError(t, err)

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"GET /openapi.json through middleware chain must return 200")

		assert.NotEmpty(t, resp.Header.Get("X-Request-Id"),
			"X-Request-Id header must be set by RequestID middleware")

		var doc map[string]any
		assert.NoError(t, json.Unmarshal(body, &doc),
			"body must be valid JSON even through middleware chain")
	})

	t.Run("DocsRoot_Returns200WithRequestID", func(t *testing.T) {
		resp, err := http.Get(srv.URL + "/docs/") //nolint:noctx
		require.NoError(t, err)
		defer func() { _ = resp.Body.Close() }()
		_, _ = io.ReadAll(resp.Body)

		assert.Equal(t, http.StatusOK, resp.StatusCode,
			"GET /docs/ through middleware chain must return 200")

		assert.NotEmpty(t, resp.Header.Get("X-Request-Id"),
			"X-Request-Id header must be set by RequestID middleware")
	})
}
