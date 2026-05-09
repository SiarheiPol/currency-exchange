package obs

import "github.com/prometheus/client_golang/prometheus"

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
)

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

// SchedulerLastTickSecondsAgo records how many seconds have elapsed since the
// last scheduler tick.
var SchedulerLastTickSecondsAgo prometheus.Gauge

// CoalescingCollapsedTotal counts the total number of duplicate requests
// collapsed by the coalescing layer.
var CoalescingCollapsedTotal prometheus.Counter

// RatesProviderRequestsTotal counts upstream rates provider calls partitioned
// by provider name and outcome.
var RatesProviderRequestsTotal *prometheus.CounterVec

// RatesProviderRequestDurationSeconds measures upstream rates provider call
// latency in seconds partitioned by provider name.
var RatesProviderRequestDurationSeconds *prometheus.HistogramVec

// RatesProviderQuotaUsed tracks the quota consumed per rates provider and
// quota period.
var RatesProviderQuotaUsed *prometheus.GaugeVec

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

	QuoteJobsAttempts = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name: MetricQuoteJobsAttempts,
		Help: "Number of delivery attempts per quote job.",
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

	SchedulerLastTickSecondsAgo = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: MetricSchedulerLastTickSecondsAgo,
		Help: "Seconds elapsed since the last scheduler tick.",
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

	RatesProviderQuotaUsed = prometheus.NewGaugeVec(prometheus.GaugeOpts{
		Name: MetricRatesProviderQuotaUsed,
		Help: "Quota consumed per rates provider and period.",
	}, []string{"provider", "period"})
	RatesProviderQuotaUsed.WithLabelValues("", "")

	defaultRegistry.MustRegister(
		HTTPRequestsTotal,
		HTTPRequestDurationSeconds,
		HTTPInFlightRequests,
		QuoteJobsPendingCount,
		QuoteJobsTotal,
		QuoteJobsAttempts,
		WorkerIterationsTotal,
		SchedulerTicksTotal,
		SchedulerLastTickSecondsAgo,
		CoalescingCollapsedTotal,
		RatesProviderRequestsTotal,
		RatesProviderRequestDurationSeconds,
		RatesProviderQuotaUsed,
	)
}

// NewRegistry returns the package-level singleton *prometheus.Registry. All 13
// service collectors are pre-registered and pre-initialised. Callers must not
// register additional collectors into this registry; use the exported package
// variables (e.g. obs.HTTPRequestsTotal) to record observations.
func NewRegistry() *prometheus.Registry { return defaultRegistry }
