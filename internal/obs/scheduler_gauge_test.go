package obs_test

import (
	"testing"
	"time"

	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/prometheus/client_golang/prometheus"

	"currency-exchange/internal/obs"
)

// gatherSingleValue registers g into a fresh registry, gathers, and returns the
// single float64 sample value for the first metric family whose name matches
// metricName.
func gatherSingleValue(t *testing.T, g prometheus.Collector, metricName string) float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(g), "Register must not return an error")
	families, err := reg.Gather()
	require.NoError(t, err, "Gather must not return an error")

	for _, f := range families {
		if f.GetName() == metricName {
			metrics := f.GetMetric()
			require.Len(t, metrics, 1, "expected exactly one metric sample")
			return gaugeValue(t, metrics[0])
		}
	}
	t.Fatalf("metric family %q not found in gathered output", metricName)
	return 0
}

// gaugeValue extracts the float64 value from a dto.Metric, preferring Gauge
// over Untyped.
func gaugeValue(t *testing.T, m *dto.Metric) float64 {
	t.Helper()
	if g := m.GetGauge(); g != nil {
		return g.GetValue()
	}
	if u := m.GetUntyped(); u != nil {
		return u.GetValue()
	}
	t.Fatalf("metric has neither Gauge nor Untyped value")
	return 0
}

// TestSchedulerGaugeFunc_BeforeTick_ReturnsPositive asserts that when the
// scheduler has never ticked (lastTickFn returns zero time.Time{}), the gauge
// returns a large positive value indicating a very long time since the last
// tick.
func TestSchedulerGaugeFunc_BeforeTick_ReturnsPositive(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	g := obs.NewSchedulerLastTickGaugeFunc(
		func() time.Time { return time.Time{} }, // zero — never ticked
		func() time.Time { return now },
	)

	val := gatherSingleValue(t, g, obs.MetricSchedulerLastTickSecondsAgo)

	// now.Sub(time.Time{}) is ~56 years in seconds (> 1e9).
	assert.Greater(t, val, 1e9,
		"before any tick the gauge must return a value > 1e9 seconds (got %v)", val)
}

// TestSchedulerGaugeFunc_AfterTick_ReflectsElapsed asserts that when the
// scheduler last ticked at t0 and now is t0+5s, the gauge value is 5.0 (±0.01).
func TestSchedulerGaugeFunc_AfterTick_ReflectsElapsed(t *testing.T) {
	t.Parallel()

	t0 := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	now := t0.Add(5 * time.Second)

	g := obs.NewSchedulerLastTickGaugeFunc(
		func() time.Time { return t0 },
		func() time.Time { return now },
	)

	val := gatherSingleValue(t, g, obs.MetricSchedulerLastTickSecondsAgo)

	assert.InDelta(t, 5.0, val, 0.01,
		"5 seconds after last tick the gauge must read 5.0 (got %v)", val)
}
