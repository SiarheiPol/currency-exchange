package obs

import (
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

// PoolStats is a value-type snapshot of the pgxpool fields we emit as metrics.
// It decouples PgxpoolCollector from pgxpool.Stat, whose lack of an exported
// constructor would force integration testing for unit-level coverage.
type PoolStats struct {
	TotalConns           int32
	IdleConns            int32
	AcquiredConns        int32
	MaxConns             int32
	AcquireCount         int64
	AcquireWaitDuration  time.Duration
	EmptyAcquireCount    int64
	CanceledAcquireCount int64
	NewConnsCount        int64
}

// PoolStatProvider is the seam between PgxpoolCollector and the real pgxpool.
// In production, cmd/server/main.go wires a thin adapter around *pgxpool.Pool
// that copies pool.Stat() getters into PoolStats.
type PoolStatProvider interface {
	Stat() PoolStats
}

// PgxpoolCollector implements prometheus.Collector and emits the 9 pool-state
// metrics whose names are declared as MetricDBPool* constants in metrics.go.
type PgxpoolCollector struct {
	provider PoolStatProvider

	// descriptors created once at construction so Describe is cheap.
	descTotalConns             *prometheus.Desc
	descIdleConns              *prometheus.Desc
	descAcquiredConns          *prometheus.Desc
	descMaxConns               *prometheus.Desc
	descAcquiresTotal          *prometheus.Desc
	descAcquireWaitSeconds     *prometheus.Desc
	descEmptyAcquiresTotal     *prometheus.Desc
	descCancelledAcquiresTotal *prometheus.Desc
	descNewConnsTotal          *prometheus.Desc
}

// NewPgxpoolCollector constructs a PgxpoolCollector backed by the given provider.
func NewPgxpoolCollector(provider PoolStatProvider) *PgxpoolCollector {
	return &PgxpoolCollector{
		provider:                   provider,
		descTotalConns:             prometheus.NewDesc(MetricDBPoolTotalConns, "Current total number of connections in the pgxpool.", nil, nil),
		descIdleConns:              prometheus.NewDesc(MetricDBPoolIdleConns, "Current idle connections in the pgxpool.", nil, nil),
		descAcquiredConns:          prometheus.NewDesc(MetricDBPoolAcquiredConns, "Current acquired (in-use) connections in the pgxpool.", nil, nil),
		descMaxConns:               prometheus.NewDesc(MetricDBPoolMaxConns, "Maximum connections configured on the pgxpool.", nil, nil),
		descAcquiresTotal:          prometheus.NewDesc(MetricDBPoolAcquiresTotal, "Total number of successful pool.Acquire calls.", nil, nil),
		descAcquireWaitSeconds:     prometheus.NewDesc(MetricDBPoolAcquireWaitSecondsTotal, "Cumulative wait time for pool.Acquire (seconds).", nil, nil),
		descEmptyAcquiresTotal:     prometheus.NewDesc(MetricDBPoolEmptyAcquiresTotal, "Acquire calls that had to wait for a new connection.", nil, nil),
		descCancelledAcquiresTotal: prometheus.NewDesc(MetricDBPoolCancelledAcquiresTotal, "Acquire calls cancelled before they completed.", nil, nil),
		descNewConnsTotal:          prometheus.NewDesc(MetricDBPoolNewConnsTotal, "Total number of connections ever constructed by the pool.", nil, nil),
	}
}

// Describe sends all descriptors to the provided channel.
func (c *PgxpoolCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.descTotalConns
	ch <- c.descIdleConns
	ch <- c.descAcquiredConns
	ch <- c.descMaxConns
	ch <- c.descAcquiresTotal
	ch <- c.descAcquireWaitSeconds
	ch <- c.descEmptyAcquiresTotal
	ch <- c.descCancelledAcquiresTotal
	ch <- c.descNewConnsTotal
}

// Collect gathers a fresh snapshot from the provider and sends metrics.
func (c *PgxpoolCollector) Collect(ch chan<- prometheus.Metric) {
	s := c.provider.Stat()
	ch <- prometheus.MustNewConstMetric(c.descTotalConns, prometheus.GaugeValue, float64(s.TotalConns))
	ch <- prometheus.MustNewConstMetric(c.descIdleConns, prometheus.GaugeValue, float64(s.IdleConns))
	ch <- prometheus.MustNewConstMetric(c.descAcquiredConns, prometheus.GaugeValue, float64(s.AcquiredConns))
	ch <- prometheus.MustNewConstMetric(c.descMaxConns, prometheus.GaugeValue, float64(s.MaxConns))
	ch <- prometheus.MustNewConstMetric(c.descAcquiresTotal, prometheus.CounterValue, float64(s.AcquireCount))
	ch <- prometheus.MustNewConstMetric(c.descAcquireWaitSeconds, prometheus.CounterValue, s.AcquireWaitDuration.Seconds())
	ch <- prometheus.MustNewConstMetric(c.descEmptyAcquiresTotal, prometheus.CounterValue, float64(s.EmptyAcquireCount))
	ch <- prometheus.MustNewConstMetric(c.descCancelledAcquiresTotal, prometheus.CounterValue, float64(s.CanceledAcquireCount))
	ch <- prometheus.MustNewConstMetric(c.descNewConnsTotal, prometheus.CounterValue, float64(s.NewConnsCount))
}
