package httpmw

import (
	"net/http"
	"runtime/debug"

	"currency-exchange/internal/obs"
)

// PanicRecover returns a middleware that recovers from panics in downstream
// handlers. On panic it logs the event via obs.LogPanicRecovered and responds
// with a generic 500 JSON error envelope. The panic value is never written to
// the response body.
//
// Wire order: RequestID(PanicRecover(Metrics(handler))). RequestID enriches
// r.Context() with a request_id before calling PanicRecover, so when the
// recover defer fires it reads request_id directly from r.Context() — no
// header-readback required.
func PanicRecover(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				obs.LogPanicRecovered(r.Context(), rec, debug.Stack())
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(http.StatusInternalServerError)
				_, _ = w.Write([]byte(`{"error":{"code":"internal","message":"internal server error"}}`))
			}
		}()
		next.ServeHTTP(w, r)
	})
}
