package obs_test

import (
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/obs"
)

// stubProvider is a trivial PoolStatProvider that returns a fixed PoolStats.
type stubProvider struct{ stats obs.PoolStats }

func (s *stubProvider) Stat() obs.PoolStats { return s.stats }

// gatherMetricValue registers collector in a fresh registry, gathers, and
// returns the first float64 sample value for the metric family named
// metricName.  The test fails immediately if the family is missing.
func gatherMetricValue(t *testing.T, collector prometheus.Collector, metricName string) float64 {
	t.Helper()
	reg := prometheus.NewRegistry()
	require.NoError(t, reg.Register(collector))
	families, err := reg.Gather()
	require.NoError(t, err)

	for _, f := range families {
		if f.GetName() == metricName {
			metrics := f.GetMetric()
			require.NotEmpty(t, metrics)
			m := metrics[0]
			if g := m.GetGauge(); g != nil {
				return g.GetValue()
			}
			if c := m.GetCounter(); c != nil {
				return c.GetValue()
			}
			if u := m.GetUntyped(); u != nil {
				return u.GetValue()
			}
			t.Fatalf("metric %q has no Gauge/Counter/Untyped value", metricName)
		}
	}
	t.Fatalf("metric family %q not found in gathered output", metricName)
	return 0
}

// TestPgxpoolCollector_Describe_CoversAllNineNames asserts that Describe emits
// exactly 9 descriptors and that each of the 9 MetricDBPool* name constants
// appears in exactly one descriptor string.
func TestPgxpoolCollector_Describe_CoversAllNineNames(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{} // zero stats
	collector := obs.NewPgxpoolCollector(provider)

	ch := make(chan *prometheus.Desc, 32)
	collector.Describe(ch)
	close(ch)

	var descs []*prometheus.Desc
	for d := range ch {
		descs = append(descs, d)
	}

	assert.Len(t, descs, 9, "Describe must emit exactly 9 descriptors")

	allNames := []string{
		obs.MetricDBPoolTotalConns,
		obs.MetricDBPoolIdleConns,
		obs.MetricDBPoolAcquiredConns,
		obs.MetricDBPoolMaxConns,
		obs.MetricDBPoolAcquiresTotal,
		obs.MetricDBPoolAcquireWaitSecondsTotal,
		obs.MetricDBPoolEmptyAcquiresTotal,
		obs.MetricDBPoolCancelledAcquiresTotal,
		obs.MetricDBPoolNewConnsTotal,
	}

	for _, name := range allNames {
		found := 0
		for _, d := range descs {
			if strings.Contains(d.String(), name) {
				found++
			}
		}
		assert.Equal(t, 1, found,
			"metric name %q must appear in exactly one descriptor string (found %d)", name, found)
	}
}

// TestPgxpoolCollector_Collect_GaugeValuesReflected asserts that the
// connection-count gauge metrics reflect the values in PoolStats.
func TestPgxpoolCollector_Collect_GaugeValuesReflected(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{stats: obs.PoolStats{
		TotalConns:    7,
		IdleConns:     3,
		AcquiredConns: 4,
		MaxConns:      25,
	}}
	collector := obs.NewPgxpoolCollector(provider)

	assert.Equal(t, float64(7), gatherMetricValue(t, collector, obs.MetricDBPoolTotalConns),
		"db_pool_total_conns must equal 7")

	// Re-create collector each time (Collect is idempotent per stub).
	collector = obs.NewPgxpoolCollector(provider)
	assert.Equal(t, float64(3), gatherMetricValue(t, collector, obs.MetricDBPoolIdleConns),
		"db_pool_idle_conns must equal 3")

	collector = obs.NewPgxpoolCollector(provider)
	assert.Equal(t, float64(4), gatherMetricValue(t, collector, obs.MetricDBPoolAcquiredConns),
		"db_pool_acquired_conns must equal 4")

	collector = obs.NewPgxpoolCollector(provider)
	assert.Equal(t, float64(25), gatherMetricValue(t, collector, obs.MetricDBPoolMaxConns),
		"db_pool_max_conns must equal 25")
}

// TestPgxpoolCollector_Collect_CounterValuesReflected asserts that the
// counter-shaped pool metrics reflect the values in PoolStats.
func TestPgxpoolCollector_Collect_CounterValuesReflected(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{stats: obs.PoolStats{
		AcquireCount:         100,
		EmptyAcquireCount:    5,
		CanceledAcquireCount: 2,
		NewConnsCount:        12,
	}}

	check := func(metricName string, want float64) {
		t.Helper()
		c := obs.NewPgxpoolCollector(provider)
		assert.Equal(t, want, gatherMetricValue(t, c, metricName),
			"metric %q must equal %v", metricName, want)
	}

	check(obs.MetricDBPoolAcquiresTotal, 100)
	check(obs.MetricDBPoolEmptyAcquiresTotal, 5)
	check(obs.MetricDBPoolCancelledAcquiresTotal, 2)
	check(obs.MetricDBPoolNewConnsTotal, 12)
}

// TestPgxpoolCollector_Collect_AcquireWaitSecondsConversion asserts that the
// AcquireWaitDuration field is exposed in seconds (not milliseconds or
// nanoseconds) as db_pool_acquire_wait_seconds_total.
func TestPgxpoolCollector_Collect_AcquireWaitSecondsConversion(t *testing.T) {
	t.Parallel()

	provider := &stubProvider{stats: obs.PoolStats{
		AcquireWaitDuration: 1500 * time.Millisecond,
	}}
	collector := obs.NewPgxpoolCollector(provider)

	val := gatherMetricValue(t, collector, obs.MetricDBPoolAcquireWaitSecondsTotal)

	assert.InDelta(t, 1.5, val, 0.0001,
		"1500ms must be reported as 1.5 seconds (got %v)", val)
}
