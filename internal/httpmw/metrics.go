package httpmw

import (
	"net/http"
	"strconv"
	"time"

	"currency-exchange/internal/obs"
)

// Metrics returns a middleware that records HTTP request metrics for each
// matched route. It increments obs.HTTPInFlightRequests while a request is
// in flight (decrementing via defer so that panics do not leak the gauge),
// records the response status code in obs.HTTPRequestsTotal, and observes
// request latency in obs.HTTPRequestDurationSeconds.
//
// r.Pattern is populated by http.ServeMux during the call to next.ServeHTTP;
// the counter and histogram are recorded only when r.Pattern is non-empty
// after the call returns. An empty pattern means no mux pattern was matched,
// which would produce a degenerate label value that inflates cardinality
// without useful information.
//
// Wire order: RequestID(PanicRecover(Metrics(handler))). RequestID enriches
// the context with a request ID; PanicRecover catches any panics with that
// enriched context; Metrics measures the un-panicked completion path for
// counter/histogram (panicked requests skip the recording lines after
// next.ServeHTTP), while the in-flight gauge is still correctly decremented
// by its defer before the panic propagates to PanicRecover.
func Metrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		obs.HTTPInFlightRequests.Inc()
		defer obs.HTTPInFlightRequests.Dec()

		obs.LogHTTPRequestReceived(r.Context(), r.Method, r.URL.Path)

		start := time.Now()
		rw := &statusCaptureWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rw, r)
		elapsed := time.Since(start)

		obs.LogHTTPRequestCompleted(r.Context(), r.Method, r.URL.Path, rw.status, elapsed)

		// r.Pattern is set by http.ServeMux inside next.ServeHTTP. If it is
		// still empty after the call, no mux pattern was matched — skip
		// metric recording to avoid a degenerate empty-path label. Logs above
		// still fire for unmatched routes so 404s are visible.
		if r.Pattern == "" {
			return
		}
		obs.HTTPRequestsTotal.WithLabelValues(r.Method, r.Pattern, strconv.Itoa(rw.status)).Inc()
		obs.HTTPRequestDurationSeconds.WithLabelValues(r.Method, r.Pattern).Observe(elapsed.Seconds())
	})
}

// statusCaptureWriter wraps http.ResponseWriter to capture the first status
// code written by the downstream handler. The captured code is used to
// populate the "status" label on obs.HTTPRequestsTotal.
type statusCaptureWriter struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

// WriteHeader captures the first status code and delegates to the underlying
// ResponseWriter. Subsequent calls are no-ops so that a handler calling both
// WriteHeader and Write does not double-count.
func (w *statusCaptureWriter) WriteHeader(code int) {
	if w.wroteHeader {
		return
	}
	w.status = code
	w.wroteHeader = true
	w.ResponseWriter.WriteHeader(code)
}

// Write marks the header as implicitly written (status 200) if the downstream
// handler calls Write without a prior WriteHeader, then delegates to the
// underlying ResponseWriter.
func (w *statusCaptureWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.wroteHeader = true
		// status stays at the initialised value of http.StatusOK
	}
	return w.ResponseWriter.Write(b)
}
