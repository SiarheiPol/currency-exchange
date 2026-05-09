package health_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"currency-exchange/internal/health"
)

// stubChecker returns a fixed result, recording its name.
type stubChecker struct {
	name   string
	result string
}

func (s stubChecker) Name() string                   { return s.name }
func (s stubChecker) Check(_ context.Context) string { return s.result }

// stubPinger satisfies the Pinger interface for tests.
type stubPinger struct{ err error }

func (p stubPinger) Ping(_ context.Context) error { return p.err }

// stubHeartbeater satisfies the Heartbeater interface for tests.
type stubHeartbeater struct{ last time.Time }

func (h stubHeartbeater) LastIteration() time.Time { return h.last }

func decodeReadyz(t *testing.T, resp *http.Response) (status string, checks map[string]string) {
	t.Helper()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	var parsed struct {
		Status string            `json:"status"`
		Checks map[string]string `json:"checks"`
	}
	if err := json.Unmarshal(body, &parsed); err != nil {
		t.Fatalf("unmarshal body %q: %v", string(body), err)
	}
	return parsed.Status, parsed.Checks
}

// TestReadyz_AllOK asserts the happy path: 200, status:ok, all checks
// reflected in the body.
func TestReadyz_AllOK(t *testing.T) {
	hard := []health.Checker{stubChecker{name: "postgres", result: "ok"}}
	soft := []health.Checker{stubChecker{name: "worker", result: "ok"}}

	srv := httptest.NewServer(health.Readyz(hard, soft))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type: got %q, want application/json", ct)
	}

	status, checks := decodeReadyz(t, resp)
	if status != "ok" {
		t.Errorf("body status: got %q, want %q", status, "ok")
	}
	if checks["postgres"] != "ok" {
		t.Errorf(`checks["postgres"]: got %q, want "ok"`, checks["postgres"])
	}
	if checks["worker"] != "ok" {
		t.Errorf(`checks["worker"]: got %q, want "ok"`, checks["worker"])
	}
}

// TestReadyz_HardFail asserts that any non-"ok" result from a HARD checker
// produces 503 + status:fail. The bad check's message is preserved in the body
// so operators can see why the pod is out of rotation.
func TestReadyz_HardFail(t *testing.T) {
	hard := []health.Checker{stubChecker{name: "postgres", result: "fail: down"}}
	soft := []health.Checker{stubChecker{name: "worker", result: "ok"}}

	srv := httptest.NewServer(health.Readyz(hard, soft))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status: got %d, want 503", resp.StatusCode)
	}

	status, checks := decodeReadyz(t, resp)
	if status != "fail" {
		t.Errorf("body status: got %q, want %q", status, "fail")
	}
	if checks["postgres"] != "fail: down" {
		t.Errorf(`checks["postgres"]: got %q, want %q`, checks["postgres"], "fail: down")
	}
	if checks["worker"] != "ok" {
		t.Errorf(`checks["worker"]: got %q, want "ok"`, checks["worker"])
	}
}

// TestReadyz_SoftDegraded asserts that a non-"ok" result from a SOFT checker
// stays at 200 with status:ok — the body carries the degraded message but
// the LB is told to keep the pod in rotation. This is the contract from
// monitoring.md.
func TestReadyz_SoftDegraded(t *testing.T) {
	hard := []health.Checker{stubChecker{name: "postgres", result: "ok"}}
	soft := []health.Checker{stubChecker{name: "worker", result: "degraded: stale 75s"}}

	srv := httptest.NewServer(health.Readyz(hard, soft))
	defer srv.Close()

	resp, err := http.Get(srv.URL)
	if err != nil {
		t.Fatalf("GET /readyz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status: got %d, want 200 (soft check should not 503)", resp.StatusCode)
	}

	status, checks := decodeReadyz(t, resp)
	if status != "ok" {
		t.Errorf("body status: got %q, want %q (soft fail must not flip status)", status, "ok")
	}
	if !strings.HasPrefix(checks["worker"], "degraded:") {
		t.Errorf(`checks["worker"]: got %q, want degraded:* prefix`, checks["worker"])
	}
}

// TestPostgresChecker covers the two outcomes that matter: ping ok → "ok",
// ping error → "fail: <error>". Adapter is trivial; one test with both
// branches is enough.
func TestPostgresChecker(t *testing.T) {
	t.Run("OK", func(t *testing.T) {
		c := health.PostgresChecker(stubPinger{err: nil})
		if got := c.Check(context.Background()); got != "ok" {
			t.Errorf("Check: got %q, want %q", got, "ok")
		}
		if got := c.Name(); got != "postgres" {
			t.Errorf("Name: got %q, want %q", got, "postgres")
		}
	})
	t.Run("Error", func(t *testing.T) {
		c := health.PostgresChecker(stubPinger{err: errors.New("connection refused")})
		got := c.Check(context.Background())
		if !strings.HasPrefix(got, "fail:") {
			t.Errorf("Check: got %q, want fail:* prefix", got)
		}
		if !strings.Contains(got, "connection refused") {
			t.Errorf("Check: got %q, want underlying error included", got)
		}
	})
}

// TestWorkerChecker covers the threshold logic: heartbeat within threshold →
// "ok"; outside → "degraded: ..."; never observed (zero time) → "degraded:".
// All three are real states a stuck worker can land in.
func TestWorkerChecker(t *testing.T) {
	threshold := 30 * time.Second

	t.Run("Fresh", func(t *testing.T) {
		hb := stubHeartbeater{last: time.Now().Add(-5 * time.Second)}
		c := health.WorkerChecker(hb, threshold)
		if got := c.Check(context.Background()); got != "ok" {
			t.Errorf("Check: got %q, want %q", got, "ok")
		}
	})
	t.Run("Stale", func(t *testing.T) {
		hb := stubHeartbeater{last: time.Now().Add(-2 * time.Minute)}
		c := health.WorkerChecker(hb, threshold)
		got := c.Check(context.Background())
		if !strings.HasPrefix(got, "degraded:") {
			t.Errorf("Check: got %q, want degraded:* prefix", got)
		}
	})
	t.Run("NeverRan", func(t *testing.T) {
		hb := stubHeartbeater{last: time.Time{}}
		c := health.WorkerChecker(hb, threshold)
		got := c.Check(context.Background())
		if !strings.HasPrefix(got, "degraded:") {
			t.Errorf("Check: got %q, want degraded:* prefix (heartbeat never seen)", got)
		}
	})
}
