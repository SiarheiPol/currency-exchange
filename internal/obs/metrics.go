package obs

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"
)

// Metric name constants are the canonical names for all Prometheus metrics
// exposed by the service. Callers must reference these constants rather than
// string literals so that renames are caught at compile time.
const (
	// MetricHTTPRequestsTotal counts HTTP requests partitioned by method, path,
	// and response status code.
	MetricHTTPRequestsTotal = "http_requests_total"

	// MetricHTTPRequestDurationSeconds measures HTTP request latency partitioned
	// by method and path.
	MetricHTTPRequestDurationSeconds = "http_request_duration_seconds"

	// MetricHTTPInFlightRequests tracks the number of HTTP requests currently
	// being processed.
	MetricHTTPInFlightRequests = "http_in_flight_requests"

	// MetricQuoteJobsPendingCount tracks the number of quote jobs currently
	// waiting in the queue.
	MetricQuoteJobsPendingCount = "quote_jobs_pending_count"

	// MetricQuoteJobsTotal counts quote jobs partitioned by terminal status.
	MetricQuoteJobsTotal = "quote_jobs_total"

	// MetricQuoteJobsAttempts records the number of delivery attempts per job.
	MetricQuoteJobsAttempts = "quote_jobs_attempts"

	// MetricWorkerIterationsTotal counts worker loop iterations partitioned by
	// outcome.
	MetricWorkerIterationsTotal = "worker_iterations_total"

	// MetricSchedulerTicksTotal counts the number of scheduler ticks fired.
	MetricSchedulerTicksTotal = "scheduler_ticks_total"

	// MetricSchedulerLastTickSecondsAgo records how many seconds ago the last
	// scheduler tick occurred.
	MetricSchedulerLastTickSecondsAgo = "scheduler_last_tick_seconds_ago"

	// MetricCoalescingCollapsedTotal counts the number of duplicate requests
	// collapsed by the coalescing layer.
	MetricCoalescingCollapsedTotal = "coalescing_collapsed_total"

	// MetricRatesProviderRequestsTotal counts upstream rates provider calls
	// partitioned by provider name and outcome.
	MetricRatesProviderRequestsTotal = "rates_provider_requests_total"

	// MetricRatesProviderRequestDurationSeconds measures upstream rates provider
	// call latency partitioned by provider name.
	MetricRatesProviderRequestDurationSeconds = "rates_provider_request_duration_seconds"

	// MetricRatesProviderQuotaUsed tracks the quota consumed per provider and
	// quota period.
	MetricRatesProviderQuotaUsed = "rates_provider_quota_used"

	// MetricDBPool* — pgxpool connection-pool state. Registered dynamically via
	// PgxpoolCollector in cmd/server/main.go after the pool is constructed.
	MetricDBPoolTotalConns              = "db_pool_total_conns"
	MetricDBPoolIdleConns               = "db_pool_idle_conns"
	MetricDBPoolAcquiredConns           = "db_pool_acquired_conns"
	MetricDBPoolMaxConns                = "db_pool_max_conns"
	MetricDBPoolAcquiresTotal           = "db_pool_acquires_total"
	MetricDBPoolAcquireWaitSecondsTotal = "db_pool_acquire_wait_seconds_total"
	MetricDBPoolEmptyAcquiresTotal      = "db_pool_empty_acquires_total"
	MetricDBPoolCancelledAcquiresTotal  = "db_pool_cancelled_acquires_total"
	MetricDBPoolNewConnsTotal           = "db_pool_new_conns_total"

	// MetricRatesProviderResponseAnomaliesTotal counts response anomalies in
	// upstream rates provider responses, partitioned by provider and anomaly kind.
	MetricRatesProviderResponseAnomaliesTotal = "rates_provider_response_anomalies_total"

	// MetricQuoteJobsCompletionSeconds measures the end-to-end latency from job
	// creation (created_at) to first successful completion, partitioned by source.
	// Only jobs that complete on their first attempt (Attempts==0) are observed.
	MetricQuoteJobsCompletionSeconds = "quote_jobs_completion_seconds"
)

// AllMetricNames is the canonical enumeration of every Prometheus metric name
// registered by this package. Tests use it to assert exhaustive coverage
// without hardcoding a count.
var AllMetricNames = []string{
	MetricHTTPRequestsTotal,
	MetricHTTPRequestDurationSeconds,
	MetricHTTPInFlightRequests,
	MetricQuoteJobsPendingCount,
	MetricQuoteJobsTotal,
	MetricQuoteJobsAttempts,
	MetricWorkerIterationsTotal,
	MetricSchedulerTicksTotal,
	MetricCoalescingCollapsedTotal,
	MetricRatesProviderRequestsTotal,
	MetricRatesProviderRequestDurationSeconds,
	MetricRatesProviderResponseAnomaliesTotal,
	MetricQuoteJobsCompletionSeconds,
}

// HTTPRequestsTotal counts HTTP requests partitioned by method, path, and
// response status code. Use WithLabelValues to record observations.
var HTTPRequestsTotal *prometheus.CounterVec

// HTTPRequestDurationSeconds measures HTTP request latency in seconds
// partitioned by method and path.
var HTTPRequestDurationSeconds *prometheus.HistogramVec

// HTTPInFlightRequests tracks the current number of HTTP requests being
// processed.
var HTTPInFlightRequests prometheus.Gauge

// QuoteJobsPendingCount tracks the current number of quote jobs waiting in the
// queue.
var QuoteJobsPendingCount prometheus.Gauge

// QuoteJobsTotal counts quote jobs partitioned by terminal status.
var QuoteJobsTotal *prometheus.CounterVec

// QuoteJobsAttempts records the number of delivery attempts per quote job.
var QuoteJobsAttempts prometheus.Histogram

// WorkerIterationsTotal counts worker loop iterations partitioned by outcome.
var WorkerIterationsTotal *prometheus.CounterVec

// SchedulerTicksTotal counts the total number of scheduler ticks fired.
var SchedulerTicksTotal prometheus.Counter

// CoalescingCollapsedTotal counts the total number of duplicate requests
// collapsed by the coalescing layer.
var CoalescingCollapsedTotal prometheus.Counter

// RatesProviderRequestsTotal counts upstream rates provider calls partitioned
// by provider name and outcome.
var RatesProviderRequestsTotal *prometheus.CounterVec

// RatesProviderRequestDurationSeconds measures upstream rates provider call
// latency in seconds partitioned by provider name.
var RatesProviderRequestDurationSeconds *prometheus.HistogramVec

// RatesProviderResponseAnomaliesTotal counts response anomalies in upstream
// rates provider responses partitioned by provider and anomaly kind.
var RatesProviderResponseAnomaliesTotal *prometheus.CounterVec

// QuoteJobsCompletionSeconds measures end-to-end job completion latency in
// seconds from created_at to first successful Complete, partitioned by source.
// Only jobs with Attempts==0 at the moment Complete succeeds are observed.
var QuoteJobsCompletionSeconds *prometheus.HistogramVec

// defaultRegistry is the package-level singleton registry. It is created once
// in init and returned by every NewRegistry call.
var defaultRegistry *prometheus.Registry

func init() {
	defaultRegistry = prometheus.NewRegistry()

	HTTPRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: MetricHTTPRequestsTotal,
		Help: "Total number of HTTP requests partitioned by method, path, and status.",
	}, []string{"method", "path", "status"})
	// Pre-initialise so Gather returns the family even before any real observations.
	HTTPRequestsTotal.WithLabelValues("", "", "")

	HTTPRequestDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: MetricHTTPRequestDurationSeconds,
		Help: "HTTP request latency in seconds partitioned by method and path.",
	}, []string{"method", "path"})
	HTTPRequestDurationSeconds.WithLabelValues("", "")

	HTTPInFlightRequests = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: MetricHTTPInFlightRequests,
		Help: "Current number of HTTP requests being processed.",
	})

	QuoteJobsPendingCount = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: MetricQuoteJobsPendingCount,
		Help: "Current number of quote jobs waiting in the queue.",
	})

	QuoteJobsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: MetricQuoteJobsTotal,
		Help: "Total number of quote jobs partitioned by terminal status.",
	}, []string{"status"})
	QuoteJobsTotal.WithLabelValues("")

	// Integer-shaped buckets: jobs use 1..maxAttempts (default 5), with 10 as
	// the overflow boundary. Default Prometheus buckets are seconds-shaped and
	// would conflate 2/3 attempts and 4/5 attempts into the same bucket.
	QuoteJobsAttempts = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    MetricQuoteJobsAttempts,
		Help:    "Number of delivery attempts per quote job.",
		Buckets: []float64{1, 2, 3, 4, 5, 10},
	})

	WorkerIterationsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: MetricWorkerIterationsTotal,
		Help: "Total number of worker loop iterations partitioned by outcome.",
	}, []string{"outcome"})
	WorkerIterationsTotal.WithLabelValues("")

	SchedulerTicksTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: MetricSchedulerTicksTotal,
		Help: "Total number of scheduler ticks fired.",
	})

	CoalescingCollapsedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: MetricCoalescingCollapsedTotal,
		Help: "Total number of duplicate requests collapsed by the coalescing layer.",
	})

	RatesProviderRequestsTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: MetricRatesProviderRequestsTotal,
		Help: "Total number of upstream rates provider calls partitioned by provider and outcome.",
	}, []string{"provider", "outcome"})
	RatesProviderRequestsTotal.WithLabelValues("", "")

	RatesProviderRequestDurationSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name: MetricRatesProviderRequestDurationSeconds,
		Help: "Upstream rates provider call latency in seconds partitioned by provider.",
	}, []string{"provider"})
	RatesProviderRequestDurationSeconds.WithLabelValues("")

	RatesProviderResponseAnomaliesTotal = prometheus.NewCounterVec(prometheus.CounterOpts{
		Name: MetricRatesProviderResponseAnomaliesTotal,
		Help: "Total number of response anomalies in upstream rates provider responses partitioned by provider and kind.",
	}, []string{"provider", "kind"})
	RatesProviderResponseAnomaliesTotal.WithLabelValues("", "")

	QuoteJobsCompletionSeconds = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    MetricQuoteJobsCompletionSeconds,
		Help:    "End-to-end job completion latency in seconds from created_at to first successful Complete, partitioned by source.",
		Buckets: []float64{0.05, 0.1, 0.25, 0.5, 1, 2, 5, 10, 30},
	}, []string{"source"})
	// Pre-initialise so Gather returns the family before any real observations.
	QuoteJobsCompletionSeconds.WithLabelValues("scheduler").Observe(0)
	QuoteJobsCompletionSeconds.WithLabelValues("refresh").Observe(0)

	defaultRegistry.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDurationSeconds,
		HTTPInFlightRequests,
		QuoteJobsPendingCount,
		QuoteJobsTotal,
		QuoteJobsAttempts,
		WorkerIterationsTotal,
		SchedulerTicksTotal,
		CoalescingCollapsedTotal,
		RatesProviderRequestsTotal,
		RatesProviderRequestDurationSeconds,
		RatesProviderResponseAnomaliesTotal,
		QuoteJobsCompletionSeconds,
	)
	defaultRegistry.MustRegister(
		collectors.NewGoCollector(),
		collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}),
	)
}

// NewRegistry returns the package-level singleton *prometheus.Registry. The
// registry contains the service collectors plus the standard Go runtime and
// process collectors, all pre-registered.
func NewRegistry() *prometheus.Registry { return defaultRegistry }

// MustRegister registers one or more collectors into the package-level
// singleton registry. It panics if any collector fails to register (e.g. name
// collision). Use this from cmd/server/main.go to wire dynamic collectors such
// as PgxpoolCollector and NewSchedulerLastTickGaugeFunc after the runtime
// dependencies (pool, scheduler) are constructed.
func MustRegister(cs ...prometheus.Collector) {
	defaultRegistry.MustRegister(cs...)
}
