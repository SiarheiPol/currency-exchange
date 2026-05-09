// Package health serves liveness (/healthz) and readiness (/readyz) HTTP
// endpoints. Liveness is a process-level signal — 200 if the process can
// answer at all. Readiness is a dependency-level signal — see readyz.go.
package health

import (
	"net/http"
)

// healthzBody is the always-200 liveness response. Kept as a constant byte
// slice so we never re-marshal a struct on a path that orchestrators hit
// every few seconds.
var healthzBody = []byte(`{"status":"ok"}`)

// Healthz returns the liveness handler. It writes 200 with body
// {"status":"ok"} unconditionally — the contract is "if the process can
// answer this, it is alive".
//
// Do not add dependency checks here; that is /readyz' job. Liveness must
// not flap on transient downstream issues, otherwise orchestrators kill
// healthy pods.
func Healthz() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(healthzBody)
	})
}
