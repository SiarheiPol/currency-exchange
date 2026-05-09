package health

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// Checker reports the current state of one dependency or subsystem. The
// returned status is a free-form string; by convention "ok" means healthy,
// "fail: <message>" means unhealthy (only meaningful for hard checks), and
// "degraded: <message>" means subhealthy (only meaningful for soft checks).
//
// Only the literal "ok" prefix flips the readyz status to ok; everything else
// is treated as not-ok and either fails (hard) or degrades (soft) the result.
type Checker interface {
	Name() string
	Check(ctx context.Context) string
}

// Readyz returns a handler that runs all checkers and renders the readiness
// envelope from docs/discussions/monitoring.md:
//
//	{ "status": "ok"|"fail", "checks": { "<name>": "<result>", ... } }
//
// Hard checks (postgres) flip the HTTP status to 503 on any non-"ok" result —
// the load balancer removes the pod from rotation. Soft checks (worker,
// scheduler) never affect the HTTP status; their messages appear in the body
// so operators can see degraded state via metrics and dashboards.
func Readyz(hard, soft []Checker) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()

		checks := make(map[string]string, len(hard)+len(soft))
		hardFailed := false

		for _, c := range hard {
			res := c.Check(ctx)
			checks[c.Name()] = res
			if res != "ok" {
				hardFailed = true
			}
		}
		for _, c := range soft {
			checks[c.Name()] = c.Check(ctx)
		}

		status := "ok"
		code := http.StatusOK
		if hardFailed {
			status = "fail"
			code = http.StatusServiceUnavailable
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		_ = json.NewEncoder(w).Encode(struct {
			Status string            `json:"status"`
			Checks map[string]string `json:"checks"`
		}{Status: status, Checks: checks})
	})
}

// Pinger is the minimal interface needed by PostgresChecker. *pgxpool.Pool
// satisfies it. Defined here (rather than imported) so internal/health does
// not depend on pgx — the package stays a thin HTTP-facing layer.
type Pinger interface {
	Ping(ctx context.Context) error
}

type pgChecker struct{ p Pinger }

func (c pgChecker) Name() string { return "postgres" }
func (c pgChecker) Check(ctx context.Context) string {
	if err := c.p.Ping(ctx); err != nil {
		// Strip newlines so the JSON envelope stays single-line per check.
		msg := strings.ReplaceAll(err.Error(), "\n", " ")
		return "fail: " + msg
	}
	return "ok"
}

// PostgresChecker is a HARD check: failure → 503. Without a database the
// service cannot serve any endpoint, so removing the pod from rotation is
// strictly safer than serving 5xx for every request.
func PostgresChecker(p Pinger) Checker { return pgChecker{p: p} }
