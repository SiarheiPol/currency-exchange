package health

import (
	"context"
	"fmt"
	"time"
)

// Heartbeater is implemented by anything that records a wall-clock time of
// its most recent loop iteration. *worker.Worker satisfies it. Returning
// the zero time means "no iteration observed yet" — treated as degraded.
type Heartbeater interface {
	LastIteration() time.Time
}

type workerChecker struct {
	hb        Heartbeater
	threshold time.Duration
}

func (c workerChecker) Name() string { return "worker" }
func (c workerChecker) Check(_ context.Context) string {
	last := c.hb.LastIteration()
	if last.IsZero() {
		return "degraded: no iteration observed"
	}
	since := time.Since(last)
	if since > c.threshold {
		return fmt.Sprintf("degraded: last iteration %s ago", since.Truncate(time.Second))
	}
	return "ok"
}

// WorkerChecker is a SOFT check: a stuck worker means refresh-driven jobs
// queue up but the pod can still serve cached reads, so removing it from
// rotation makes the situation worse, not better. The metric
// quote_jobs_pending_count + alerting on a stale heartbeat is the right
// signal — not LB removal.
func WorkerChecker(hb Heartbeater, threshold time.Duration) Checker {
	return workerChecker{hb: hb, threshold: threshold}
}
