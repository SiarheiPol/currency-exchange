package obs

import (
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// MetricsHandler returns an http.Handler that serves the Prometheus exposition
// of the package-level singleton registry. Mount it on GET /metrics.
//
// EnableOpenMetrics is left off so the default text/plain content-type matches
// what most Prometheus servers and Grafana scrape configs expect without
// negotiation. ErrorHandling defaults to HTTPErrorOnError, which surfaces
// gather failures as 5xx — visible during scraping rather than silently
// dropped.
func MetricsHandler() http.Handler {
	return promhttp.HandlerFor(defaultRegistry, promhttp.HandlerOpts{})
}
