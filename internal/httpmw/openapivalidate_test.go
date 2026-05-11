package httpmw_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/api"
	"currency-exchange/internal/httpmw"
	"currency-exchange/internal/obs"
)

// validRefreshRequest returns an *http.Request for POST /quotes/refresh with a
// schema-valid body and Content-Type header.
func validRefreshRequest(t *testing.T) *http.Request {
	t.Helper()
	body := strings.NewReader(`{"base":"EUR","quote":"MXN"}`)
	req := httptest.NewRequest(http.MethodPost, "/quotes/refresh", body)
	req.Header.Set("Content-Type", "application/json")
	return req
}

// TestOpenAPIValidate_ValidRequestPassesThrough asserts that a fully valid
// request and a contract-conformant response both pass through the middleware
// unchanged: same status code and identical body.
func TestOpenAPIValidate_ValidRequestPassesThrough(t *testing.T) {
	t.Parallel()

	validHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Location", "/quotes/00000000-0000-0000-0000-000000000000")
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"id":"00000000-0000-0000-0000-000000000000"}`))
	})

	spec, err := api.GetSpec()
	require.NoError(t, err)
	wrapped := httpmw.OpenAPIValidate(spec, validHandler)

	req := validRefreshRequest(t)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
	assert.Equal(t, `{"id":"00000000-0000-0000-0000-000000000000"}`, rec.Body.String())
}

// TestOpenAPIValidate_RejectsInvalidRequest asserts that a request whose body
// is not valid JSON is rejected with 400 + invalid_request before reaching the
// downstream handler. The sentinel handler panics if called to confirm the
// short-circuit.
func TestOpenAPIValidate_RejectsInvalidRequest(t *testing.T) {
	t.Parallel()

	spec, err := api.GetSpec()
	require.NoError(t, err)

	sentinel := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("handler must not be reached when request is invalid")
	})
	wrapped := httpmw.OpenAPIValidate(spec, sentinel)

	body := strings.NewReader(`not-json-at-all`)
	req := httptest.NewRequest(http.MethodPost, "/quotes/refresh", body)
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var errEnvelope api.Error
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errEnvelope))
	assert.Equal(t, api.InvalidRequest, errEnvelope.Error.Code)
}

// TestOpenAPIValidate_RejectsInvalidResponse asserts that when the downstream
// handler writes a response that does not conform to the OpenAPI schema, the
// middleware replaces it with a 500 internal error, without leaking internal
// validator details.
func TestOpenAPIValidate_RejectsInvalidResponse(t *testing.T) {
	t.Parallel()

	spec, err := api.GetSpec()
	require.NoError(t, err)

	buggyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"wrong_field":"definitely-not-RefreshAccepted-shape"}`))
	})
	wrapped := httpmw.OpenAPIValidate(spec, buggyHandler)

	req := validRefreshRequest(t)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var errEnvelope api.Error
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errEnvelope))
	assert.Equal(t, api.Internal, errEnvelope.Error.Code)
	assert.NotContains(t, errEnvelope.Error.Message, "wrong_field",
		"response body must not leak internal validator field names")
}

// TestOpenAPIValidate_SpecPathsPresent is a sanity check that api.GetSpec()
// returns a spec containing all three expected paths. This catches stale-blob
// regressions where the embedded spec diverges from api/openapi.yaml.
func TestOpenAPIValidate_SpecPathsPresent(t *testing.T) {
	t.Parallel()

	spec, err := api.GetSpec()
	require.NoError(t, err)
	require.NotNil(t, spec)
	require.NotNil(t, spec.Paths)

	assert.NotNil(t, spec.Paths.Find("/quotes/refresh"))
	assert.NotNil(t, spec.Paths.Find("/quotes/{id}"))
	assert.NotNil(t, spec.Paths.Find("/quotes/latest"))
}

// TestOpenAPIValidate_SignatureCompileTime pins the signature of
// httpmw.OpenAPIValidate: it must accept a spec and an http.Handler and return
// an http.Handler. Any signature change causes a compile error.
func TestOpenAPIValidate_SignatureCompileTime(t *testing.T) {
	t.Parallel()

	spec, _ := api.GetSpec()
	h := httpmw.OpenAPIValidate(spec, http.NotFoundHandler())
	_ = h // pins the return type
}

// TestOpenAPIValidate_LogsOnInvalidResponse asserts that when the middleware
// intercepts an invalid response it emits a structured log record whose msg
// equals obs.EvOpenAPIResponseInvalid.
func TestOpenAPIValidate_LogsOnInvalidResponse(t *testing.T) {
	t.Parallel()

	spec, err := api.GetSpec()
	require.NoError(t, err)

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	buggyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_, _ = w.Write([]byte(`{"wrong_field":"definitely-not-RefreshAccepted-shape"}`))
	})
	wrapped := httpmw.OpenAPIValidate(spec, buggyHandler)

	req := validRefreshRequest(t).WithContext(ctx)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	// Scan log output for the expected event.
	wantMsg := string(obs.EvOpenAPIResponseInvalid)
	var found bool
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var record map[string]any
		if err := json.Unmarshal(line, &record); err != nil {
			continue
		}
		if record["msg"] == wantMsg {
			found = true
			break
		}
	}

	assert.True(t, found, "expected a log record with msg=%q; got log output:\n%s", wantMsg, buf.String())
}
