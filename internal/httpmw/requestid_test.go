package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"

	"currency-exchange/internal/httpmw"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/obs"
)

func TestRequestID_Generated(t *testing.T) {
	t.Parallel()

	expectedID := "00000000-0000-0000-0000-000000000001"
	gen := idgen.NewSeq()

	handler := httpmw.RequestID(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := obs.RequestID(r.Context())
			assert.Equal(t, expectedID, id)
			w.WriteHeader(http.StatusOK)
		}),
		httpmw.WithIDGenerator(gen),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, expectedID, rec.Header().Get("X-Request-Id"))
}

func TestRequestID_Propagated(t *testing.T) {
	t.Parallel()

	clientID := "client-id-123"
	handler := httpmw.RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id := obs.RequestID(r.Context())
		assert.Equal(t, clientID, id)
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", clientID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, clientID, rec.Header().Get("X-Request-Id"))
}

func TestRequestID_InvalidOverridden(t *testing.T) {
	t.Parallel()

	expectedID := "00000000-0000-0000-0000-000000000001"
	gen := idgen.NewSeq()

	invalidID := "@@@invalid@@@"
	handler := httpmw.RequestID(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			id := obs.RequestID(r.Context())
			assert.Equal(t, expectedID, id)
			w.WriteHeader(http.StatusOK)
		}),
		httpmw.WithIDGenerator(gen),
	)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("X-Request-Id", invalidID)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	assert.Equal(t, expectedID, rec.Header().Get("X-Request-Id"))
}
