package httpmw_test

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/httpmw"
	"currency-exchange/internal/obs"
)

// compile-time signature check: Metrics must be a standard middleware.
var _ func(http.Handler) http.Handler = httpmw.Metrics

// labelMatch reports whether the metric has a label with the given name and value.
func labelMatch(m *dto.Metric, name, value string) bool {
	for _, lp := range m.GetLabel() {
		if lp.GetName() == name && lp.GetValue() == value {
			return true
		}
	}
	return false
}

// histogramSampleCountFor gathers from the default registry and returns the
// SampleCount for the HTTPRequestDurationSeconds histogram series matching the
// given method and path labels. Returns 0 if no matching series is found.
func histogramSampleCountFor(t *testing.T, method, path string) uint64 {
	t.Helper()
	reg := obs.NewRegistry()
	gathered, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range gathered {
		if mf.GetName() != obs.MetricHTTPRequestDurationSeconds {
			continue
		}
		for _, m := range mf.GetMetric() {
			if labelMatch(m, "method", method) && labelMatch(m, "path", path) {
				return m.GetHistogram().GetSampleCount()
			}
		}
	}
	return 0
}

// TestMetrics_IncrementsRequestsTotal asserts that two requests through the
// Metrics middleware increment HTTPRequestsTotal by exactly 2 for the matching
// (method, path, status) label combination.
func TestMetrics_IncrementsRequestsTotal(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := httpmw.Metrics(mux)

	before := testutil.ToFloat64(obs.HTTPRequestsTotal.WithLabelValues("GET", "GET /test", "200"))

	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
	}

	after := testutil.ToFloat64(obs.HTTPRequestsTotal.WithLabelValues("GET", "GET /test", "200"))
	assert.Equal(t, float64(2), after-before,
		"HTTPRequestsTotal should increase by exactly 2 after two requests")
}

// TestMetrics_RecordsDurationHistogram asserts that a request through the
// Metrics middleware records exactly one observation in HTTPRequestDurationSeconds
// for the matching (method, path) label combination.
func TestMetrics_RecordsDurationHistogram(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /test", func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(10 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
	})
	wrapped := httpmw.Metrics(mux)

	before := histogramSampleCountFor(t, "GET", "GET /test")

	req := httptest.NewRequest(http.MethodGet, "/test", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	after := histogramSampleCountFor(t, "GET", "GET /test")
	assert.Equal(t, uint64(1), after-before,
		"HTTPRequestDurationSeconds should record exactly 1 new observation after one request")
}

// TestMetrics_TracksInFlightGauge asserts that HTTPInFlightRequests is
// incremented while a request is in flight and decremented when it completes.
func TestMetrics_TracksInFlightGauge(t *testing.T) {
	released := make(chan struct{})
	inHandler := make(chan struct{})

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(inHandler)
		<-released
		w.WriteHeader(http.StatusOK)
	})

	mux := http.NewServeMux()
	mux.Handle("GET /test", handler)
	wrapped := httpmw.Metrics(mux)

	baseline := testutil.ToFloat64(obs.HTTPInFlightRequests)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		req := httptest.NewRequest(http.MethodGet, "/test", nil)
		rec := httptest.NewRecorder()
		wrapped.ServeHTTP(rec, req)
	}()

	// Wait until the handler is executing (in-flight).
	<-inHandler
	assert.Equal(t, baseline+1, testutil.ToFloat64(obs.HTTPInFlightRequests),
		"HTTPInFlightRequests should be baseline+1 while a request is in flight")

	// Release the handler.
	close(released)
	wg.Wait()

	assert.Equal(t, baseline, testutil.ToFloat64(obs.HTTPInFlightRequests),
		"HTTPInFlightRequests should return to baseline after the request completes")
}

// TestMetrics_DefersDecrementOnPanic asserts that HTTPInFlightRequests is
// decremented even when a panic propagates through the Metrics middleware.
// The chain mirrors production wire order: RequestID > PanicRecover > Metrics > mux.
func TestMetrics_DefersDecrementOnPanic(t *testing.T) {
	panickyHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("intentional test panic")
	})

	mux := http.NewServeMux()
	mux.Handle("GET /panicky", panickyHandler)

	chain := httpmw.RequestID(httpmw.PanicRecover(httpmw.Metrics(mux)))

	baseline := testutil.ToFloat64(obs.HTTPInFlightRequests)

	req := httptest.NewRequest(http.MethodGet, "/panicky", nil).WithContext(discardCtx())
	rec := httptest.NewRecorder()
	chain.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code,
		"PanicRecover should return 500 when the inner handler panics")
	assert.Equal(t, baseline, testutil.ToFloat64(obs.HTTPInFlightRequests),
		"HTTPInFlightRequests should return to baseline after panic is recovered")
}

// TestMetrics_SkipsRecordingWhenPatternEmpty asserts that when r.Pattern is
// empty (no mux wrapping), the Metrics middleware skips all metric recording:
// neither HTTPRequestsTotal, HTTPRequestDurationSeconds, nor HTTPInFlightRequests
// are updated.
func TestMetrics_SkipsRecordingWhenPatternEmpty(t *testing.T) {
	plainHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	wrapped := httpmw.Metrics(plainHandler)

	// Record baselines for all three metrics.
	beforeCounter := testutil.ToFloat64(obs.HTTPRequestsTotal.WithLabelValues("GET", "", "200"))
	beforeHistogram := histogramSampleCountFor(t, "GET", "")
	beforeInFlight := testutil.ToFloat64(obs.HTTPInFlightRequests)

	req := httptest.NewRequest(http.MethodGet, "/anything", nil)
	rec := httptest.NewRecorder()
	wrapped.ServeHTTP(rec, req)

	assert.Equal(t, beforeCounter, testutil.ToFloat64(obs.HTTPRequestsTotal.WithLabelValues("GET", "", "200")),
		"HTTPRequestsTotal must not be updated when r.Pattern is empty")
	assert.Equal(t, beforeHistogram, histogramSampleCountFor(t, "GET", ""),
		"HTTPRequestDurationSeconds must not be updated when r.Pattern is empty")
	assert.Equal(t, beforeInFlight, testutil.ToFloat64(obs.HTTPInFlightRequests),
		"HTTPInFlightRequests must not be updated when r.Pattern is empty")
}
