package health

import (
	"context"
	"fmt"
	"time"
)

// SchedulerHeartbeater is implemented by anything that records the wall-clock
// time of its most recent tick. *scheduler.Scheduler satisfies it. Returning
// the zero time means "no tick observed yet" — treated as degraded.
type SchedulerHeartbeater interface {
	LastTick() time.Time
}

type schedulerChecker struct {
	hb        SchedulerHeartbeater
	threshold time.Duration
}

func (c schedulerChecker) Name() string { return "scheduler" }
func (c schedulerChecker) Check(_ context.Context) string {
	last := c.hb.LastTick()
	if last.IsZero() {
		return "degraded: no tick observed"
	}
	since := time.Since(last)
	if since > c.threshold {
		return fmt.Sprintf("degraded: last tick %s ago", since.Truncate(time.Second))
	}
	return "ok"
}

// SchedulerChecker is a SOFT check: a scheduler that has not ticked recently
// means fresh jobs are not being enqueued, but cached reads can still be served.
// Removing the pod from rotation would make the situation worse.
func SchedulerChecker(hb SchedulerHeartbeater, threshold time.Duration) Checker {
	return schedulerChecker{hb: hb, threshold: threshold}
}
