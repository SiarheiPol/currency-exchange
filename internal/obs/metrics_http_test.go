package obs_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"currency-exchange/internal/obs"
)

// TestMetricsHandler_ExposesRegistry asserts the handler is wired to the
// singleton registry that holds the service collectors and serves Prometheus
// text format. A failure here means either the wrong registry is exposed or
// promhttp is not used — both are real regressions worth catching.
func TestMetricsHandler_ExposesRegistry(t *testing.T) {
	h := obs.MetricsHandler()
	if h == nil {
		t.Fatal("MetricsHandler returned nil")
	}

	// Touch a known collector so the family appears in the exposition output
	// even on a freshly imported registry. Without this, counters with zero
	// observations may be filtered out depending on client_golang version.
	obs.HTTPRequestsTotal.WithLabelValues("GET", "/metrics", "200").Inc()

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /metrics: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d, want 200", resp.StatusCode)
	}

	ct := resp.Header.Get("Content-Type")
	// Prometheus exposition uses text/plain (with version=… or similar
	// parameters); openmetrics is also valid. Both start with text/.
	if !strings.HasPrefix(ct, "text/plain") && !strings.HasPrefix(ct, "application/openmetrics-text") {
		t.Fatalf("Content-Type: got %q, want text/plain or application/openmetrics-text prefix", ct)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	bodyStr := string(body)

	if !strings.Contains(bodyStr, obs.MetricHTTPRequestsTotal) {
		t.Errorf("body missing metric %q (wrong registry?). Got:\n%s",
			obs.MetricHTTPRequestsTotal, bodyStr)
	}
}
