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

// TestNewRegistry_AllMetricsGatherable asserts that every name in
// obs.AllMetricNames is represented in the gathered MetricFamily list.
func TestNewRegistry_AllMetricsGatherable(t *testing.T) {
	t.Parallel()

	reg := obs.NewRegistry()
	families, err := reg.Gather()

	require.NoError(t, err, "Gather must not return an error")

	gathered := make(map[string]bool, len(families))
	for _, f := range families {
		gathered[f.GetName()] = true
	}
	for _, name := range obs.AllMetricNames {
		assert.True(t, gathered[name], "metric %q not registered", name)
	}
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

func TestAllMetricNames_ContainsQuoteJobsCompletionSeconds(t *testing.T) {
	t.Parallel()

	found := false
	for _, name := range obs.AllMetricNames {
		if name == obs.MetricQuoteJobsCompletionSeconds {
			found = true
			break
		}
	}
	assert.True(t, found,
		"AllMetricNames must contain MetricQuoteJobsCompletionSeconds (%q)",
		obs.MetricQuoteJobsCompletionSeconds)
}

// TestNewRegistry_IncludesGoAndProcessCollectors asserts that the registry
// returned by obs.NewRegistry() gathers both the standard Go runtime (go_*)
// and process (process_*) metric families in addition to the service metrics.
//
// CI runs only on ubuntu-latest (Linux), so no OS skip guard is needed: the
// ProcessCollector emits process_* families on Linux unconditionally.
func TestNewRegistry_IncludesGoAndProcessCollectors(t *testing.T) {
	t.Parallel()

	reg := obs.NewRegistry()
	families, err := reg.Gather()

	require.NoError(t, err, "Gather must not return an error")

	gathered := make(map[string]bool, len(families))
	for _, f := range families {
		gathered[f.GetName()] = true
	}

	assert.True(t, gathered["go_goroutines"],
		"metric %q must be present in gathered families", "go_goroutines")
	assert.True(t, gathered["go_memstats_alloc_bytes"],
		"metric %q must be present in gathered families", "go_memstats_alloc_bytes")
	assert.True(t, gathered["process_resident_memory_bytes"],
		"metric %q must be present in gathered families", "process_resident_memory_bytes")
	assert.True(t, gathered["process_open_fds"],
		"metric %q must be present in gathered families", "process_open_fds")
}
