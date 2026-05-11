package httpmw

import (
	"net/http"
	"runtime/debug"

	"currency-exchange/internal/obs"
)

// PanicRecover returns a middleware that recovers from panics in downstream
// handlers. On panic it logs the event via obs.LogPanicRecovered (which picks
// up the request_id already placed in context by the RequestID middleware) and
// responds with a generic 500 JSON error envelope. The panic value is never
// written to the response body.
//
// Wire order: PanicRecover must wrap RequestID so that by the time a panic
// fires, RequestID has already set the X-Request-Id response header. The
// recover defer reads that header back and re-attaches it to the context so
// that the log record carries request_id.
func PanicRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				// Reconstruct the enriched context: RequestID middleware has
				// already set the response header before calling its next
				// handler, so the request_id is recoverable from w even after
				// the panic unwinds the stack.
				ctx := r.Context()
				if id := w.Header().Get(HeaderRequestID); id != "" {
					ctx = obs.WithRequestID(ctx, id)
				}
				obs.LogPanicRecovered(ctx, rec, debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"internal server error"}}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
