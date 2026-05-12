package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsHandler returns an http.Handler that serves the Prometheus exposition
// of all service metrics. It combines defaultRegistry (service metrics,
// Go runtime, process) and httpRegistry (HTTPRequestsTotal) via
// prometheus.Gatherers so that high-cardinality HTTP metrics appear in the
// exposition after the first real request without polluting the registry used
// by NewRegistry().
//
// EnableOpenMetrics is left off so the default text/plain content-type matches
// what most Prometheus servers and Grafana scrape configs expect without
// negotiation. ErrorHandling defaults to HTTPErrorOnError, which surfaces
// gather failures as 5xx — visible during scraping rather than silently
// dropped.
func MetricsHandler() http.Handler {
	g := prometheus.Gatherers{defaultRegistry, httpRegistry}
	return promhttp.HandlerFor(g, promhttp.HandlerOpts{})
}
