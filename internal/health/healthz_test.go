package health_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"currency-exchange/internal/health"
)

// TestHealthz_AlwaysOK asserts the liveness contract from
// docs/discussions/monitoring.md: 200, JSON content-type, body {"status":"ok"}.
// All three matter — orchestrators parse the body, load balancers parse the
// status, and a missing Content-Type would be a regression in any Prom/k8s
// setup that gates on it.
func TestHealthz_AlwaysOK(t *testing.T) {
	h := health.Healthz()
	if h == nil {
		t.Fatal("Healthz returned nil")
	}

	srv := httptest.NewServer(h)
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}

	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want %q", ct, "application/json")
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	const want = `{"status":"ok"}`
	if string(body) != want {
		t.Errorf("body: got %q, want %q", string(body), want)
	}
}
