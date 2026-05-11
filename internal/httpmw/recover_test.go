package httpmw_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/httpmw"
	"currency-exchange/internal/obs"
)

// discardCtx returns a context with a logger that swallows all output. Used by
// tests whose panic-recovery logging would otherwise emit visible ERROR lines
// in go test -v output.
func discardCtx() context.Context {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	return obs.WithLogger(context.Background(), logger)
}

// compile-time signature check: PanicRecover must be a standard middleware.
var _ func(http.Handler) http.Handler = httpmw.PanicRecover

func TestPanicRecover_RecoversAndReturns500(t *testing.T) {
	t.Parallel()

	panickyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	})
	wrapped := httpmw.PanicRecover(panickyHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(discardCtx())
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Type"), "application/json")

	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, "internal", body.Error.Code)
}

func TestPanicRecover_DoesNotLeakPanicValue(t *testing.T) {
	t.Parallel()

	sentinel := "secret-internal-detail-zzx9"
	panickyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic(sentinel)
	})
	wrapped := httpmw.PanicRecover(panickyHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(discardCtx())
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	var body struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.False(t, strings.Contains(body.Error.Message, sentinel),
		"response body must not contain the panic value; got: %q", body.Error.Message)
}

func TestPanicRecover_LogsPanicViaTypedHelper(t *testing.T) {
	t.Parallel()

	buf := &bytes.Buffer{}
	logger := slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	panickyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test-panic-value")
	})
	// Wire order: PanicRecover wraps RequestID wraps handler, so request_id is
	// in context when the panic fires.
	chain := httpmw.PanicRecover(httpmw.RequestID(panickyHandler))

	req := httptest.NewRequest(http.MethodGet, "/", nil).WithContext(ctx)
	// No X-Request-Id header — RequestID middleware generates one.
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	// Find the EvPanicRecovered log record among all emitted lines.
	wantMsg := string(obs.EvPanicRecovered)
	var panicRecord map[string]any
	for _, line := range bytes.Split(buf.Bytes(), []byte("\n")) {
		if len(bytes.TrimSpace(line)) == 0 {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal(line, &rec); err != nil {
			continue
		}
		if rec["msg"] == wantMsg {
			panicRecord = rec
			break
		}
	}

	require.NotNil(t, panicRecord, "expected a log record with msg=%q", wantMsg)

	requestID, ok := panicRecord["request_id"].(string)
	assert.True(t, ok && requestID != "", "panic log record must carry a non-empty request_id")

	panicVal, ok := panicRecord["panic"].(string)
	assert.True(t, ok && panicVal != "", "panic log record must carry a non-empty panic field")

	stack, ok := panicRecord["stack"].(string)
	assert.True(t, ok && stack != "", "panic log record must carry a non-empty stack field")
}

func TestPanicRecover_HappyPath(t *testing.T) {
	t.Parallel()

	normalHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
	})
	wrapped := httpmw.PanicRecover(normalHandler)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "hello", rec.Body.String())
}
