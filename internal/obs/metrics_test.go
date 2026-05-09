package obs_test

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/obs"
)

// Compilation guard — each Metric* constant must exist, be exported, and be a string.
var (
	_ string = obs.MetricHTTPRequestsTotal
	_ string = obs.MetricHTTPRequestDurationSeconds
	_ string = obs.MetricHTTPInFlightRequests
	_ string = obs.MetricQuoteJobsPendingCount
	_ string = obs.MetricQuoteJobsTotal
	_ string = obs.MetricQuoteJobsAttempts
	_ string = obs.MetricWorkerIterationsTotal
	_ string = obs.MetricSchedulerTicksTotal
	_ string = obs.MetricSchedulerLastTickSecondsAgo
	_ string = obs.MetricCoalescingCollapsedTotal
	_ string = obs.MetricRatesProviderRequestsTotal
	_ string = obs.MetricRatesProviderRequestDurationSeconds
	_ string = obs.MetricRatesProviderQuotaUsed
)

// TestNewRegistry_NoRegistrationPanic asserts that obs.NewRegistry returns a
// non-nil *prometheus.Registry without panicking.
func TestNewRegistry_NoRegistrationPanic(t *testing.T) {
	t.Parallel()

	reg := obs.NewRegistry()
	assert.NotNil(t, reg, "NewRegistry must return a non-nil *prometheus.Registry")
}

// TestNewRegistry_AllMetricsGatherable asserts that the registry exposes exactly
// 13 distinct MetricFamily entries (one per declared metric) via Gather.
func TestNewRegistry_AllMetricsGatherable(t *testing.T) {
	t.Parallel()

	reg := obs.NewRegistry()
	families, err := reg.Gather()

	require.NoError(t, err, "Gather must not return an error")
	assert.Len(t, families, 13, "registry must expose exactly 13 metric families")
}

// descLabelRE extracts the fqName and variableLabels portion from a Desc.String().
// Format: Desc{fqName: "name", help: "...", constLabels: {...}, variableLabels: {l1,l2,...}}
var descFQNameRE = regexp.MustCompile(`fqName: "([^"]+)"`)
var descVarLabelsRE = regexp.MustCompile(`variableLabels: \{([^}]*)\}`)

// descLabels parses a *prometheus.Desc and returns its fqName and sorted
// variable label names.
func descLabels(d *prometheus.Desc) (fqName string, labels []string) {
	s := d.String()

	m := descFQNameRE.FindStringSubmatch(s)
	if len(m) == 2 {
		fqName = m[1]
	}

	m = descVarLabelsRE.FindStringSubmatch(s)
	if len(m) == 2 && m[1] != "" {
		raw := strings.Split(m[1], ",")
		for _, l := range raw {
			l = strings.TrimSpace(l)
			if l != "" {
				labels = append(labels, l)
			}
		}
	}
	sort.Strings(labels)
	return fqName, labels
}

// TestNewRegistry_LabelNames asserts that each metric in the registry has
// exactly the variable labels declared in monitoring.md.
func TestNewRegistry_LabelNames(t *testing.T) {
	t.Parallel()

	reg := obs.NewRegistry()

	// Collect all Desc objects from the registry.
	ch := make(chan *prometheus.Desc, 64)
	go func() {
		reg.Describe(ch)
		close(ch)
	}()

	// Build fqName → sorted label names map.
	got := make(map[string][]string)
	for d := range ch {
		fqName, labels := descLabels(d)
		if fqName != "" {
			got[fqName] = labels
		}
	}

	// Expected label sets per metric.
	want := map[string][]string{
		obs.MetricHTTPRequestsTotal:                   {"method", "path", "status"},
		obs.MetricHTTPRequestDurationSeconds:          {"method", "path"},
		obs.MetricHTTPInFlightRequests:                {},
		obs.MetricQuoteJobsTotal:                      {"status"},
		obs.MetricWorkerIterationsTotal:               {"outcome"},
		obs.MetricRatesProviderRequestsTotal:          {"outcome", "provider"},
		obs.MetricRatesProviderRequestDurationSeconds: {"provider"},
		obs.MetricRatesProviderQuotaUsed:              {"period", "provider"},
	}

	for metric, wantLabels := range want {
		t.Run(metric, func(t *testing.T) {
			t.Parallel()
			gotLabels, ok := got[metric]
			require.True(t, ok, "metric %q not found in registry", metric)

			// Normalize: sort both sides before comparing.
			wSorted := make([]string, len(wantLabels))
			copy(wSorted, wantLabels)
			sort.Strings(wSorted)

			gSorted := make([]string, len(gotLabels))
			copy(gSorted, gotLabels)
			sort.Strings(gSorted)

			assert.Equal(t, wSorted, gSorted, "metric %q label mismatch", metric)
		})
	}
}
