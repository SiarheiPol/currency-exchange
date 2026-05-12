package obs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// NewSchedulerLastTickGaugeFunc returns a prometheus.GaugeFunc that, on every
// scrape, computes nowFn().Sub(lastTickFn()).Seconds() — the number of seconds
// since the scheduler last ticked. Both function parameters enable deterministic
// testing; production callers pass scheduler.LastTick and time.Now.
//
// Registered dynamically in cmd/server/main.go after the scheduler is constructed,
// not in obs.init(), because the lastTickFn closure depends on a runtime-built
// scheduler instance.
func NewSchedulerLastTickGaugeFunc(
	lastTickFn func() time.Time,
	nowFn func() time.Time,
) prometheus.GaugeFunc {
	return prometheus.NewGaugeFunc(
		prometheus.GaugeOpts{
			Name: MetricSchedulerLastTickSecondsAgo,
			Help: "Seconds since the scheduler last ticked. Computed at scrape time.",
		},
		func() float64 {
			return nowFn().Sub(lastTickFn()).Seconds()
		},
	)
}
