package obs_test

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/obs"
)

// gatherFamilies calls obs.NewRegistry().Gather() and returns the families
// indexed by name. It fails the test immediately if Gather returns an error.
func gatherFamilies(t *testing.T) map[string]*dto.MetricFamily {
	t.Helper()
	reg := obs.NewRegistry()
	families, err := reg.Gather()
	require.NoError(t, err, "Gather must not return an error")
	m := make(map[string]*dto.MetricFamily, len(families))
	for _, f := range families {
		m[f.GetName()] = f
	}
	return m
}

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

// seriesID returns a human-readable identifier for a metric sample, formatted
// as "name{label=value,...}" with labels in sorted order. Used in failure
// messages for TestNewRegistry_NoEmptyLabelValues.
func seriesID(familyName string, m *dto.Metric) string {
	pairs := m.GetLabel()
	parts := make([]string, 0, len(pairs))
	for _, lp := range pairs {
		parts = append(parts, lp.GetName()+"="+lp.GetValue())
	}
	sort.Strings(parts)
	return familyName + "{" + strings.Join(parts, ",") + "}"
}

// TestNewRegistry_NoEmptyLabelValues asserts that no metric series gathered
// from obs.NewRegistry() has a label pair whose value is the empty string.
// Empty-label series arise from incorrect pre-initialisation calls such as
// WithLabelValues("", "") and pollute dashboards and alerting rules.
func TestNewRegistry_NoEmptyLabelValues(t *testing.T) {
	t.Parallel()

	families := gatherFamilies(t)

	var offenders []string
	for name, fam := range families {
		for _, m := range fam.GetMetric() {
			for _, lp := range m.GetLabel() {
				if lp.GetValue() == "" {
					offenders = append(offenders, seriesID(name, m))
					break
				}
			}
		}
	}
	sort.Strings(offenders)

	if len(offenders) > 0 {
		require.Failf(t, "found metric series with empty label values",
			"offending series (%d):\n  %s",
			len(offenders), strings.Join(offenders, "\n  "))
	}
}

// TestNewRegistry_QuoteJobsTotal_PreInitialized asserts that the
// quote_jobs_total family is pre-initialised with exactly the two expected
// status label values ("done" and "failed") and that both counters are zero.
func TestNewRegistry_QuoteJobsTotal_PreInitialized(t *testing.T) {
	t.Parallel()

	families := gatherFamilies(t)

	fam, ok := families[obs.MetricQuoteJobsTotal]
	require.True(t, ok, "family %q must be present after NewRegistry()", obs.MetricQuoteJobsTotal)

	metrics := fam.GetMetric()
	require.Len(t, metrics, 2, "expected exactly 2 pre-initialised series for %q", obs.MetricQuoteJobsTotal)

	got := make([]string, 0, 2)
	for _, m := range metrics {
		labels := m.GetLabel()
		require.Len(t, labels, 1, "each series must have exactly one label (status)")
		require.Equal(t, "status", labels[0].GetName(), "label name must be \"status\"")
		got = append(got, labels[0].GetValue())
		require.NotNil(t, m.GetCounter(), "series must be a counter")
		assert.Equal(t, 0.0, m.GetCounter().GetValue(), "initial counter value must be 0")
	}

	sort.Strings(got)
	assert.Equal(t, []string{"done", "failed"}, got,
		"status label values must be exactly [done failed]")
}

// TestNewRegistry_WorkerIterationsTotal_PreInitialized asserts that the
// worker_iterations_total family is pre-initialised with exactly four series,
// one for each expected outcome label value, all with counter value 0.
func TestNewRegistry_WorkerIterationsTotal_PreInitialized(t *testing.T) {
	t.Parallel()

	families := gatherFamilies(t)

	fam, ok := families[obs.MetricWorkerIterationsTotal]
	require.True(t, ok, "family %q must be present after NewRegistry()", obs.MetricWorkerIterationsTotal)

	metrics := fam.GetMetric()
	require.Len(t, metrics, 4, "expected exactly 4 pre-initialised series for %q", obs.MetricWorkerIterationsTotal)

	got := make([]string, 0, 4)
	for _, m := range metrics {
		labels := m.GetLabel()
		require.Len(t, labels, 1, "each series must have exactly one label (outcome)")
		require.Equal(t, "outcome", labels[0].GetName(), "label name must be \"outcome\"")
		got = append(got, labels[0].GetValue())
		require.NotNil(t, m.GetCounter(), "series must be a counter")
		assert.Equal(t, 0.0, m.GetCounter().GetValue(), "initial counter value must be 0")
	}

	sort.Strings(got)
	assert.Equal(t, []string{"error", "idle", "ok", "work"}, got,
		"outcome label values must be exactly [error idle ok work]")
}

// TestNewRegistry_RatesProviderRequestsTotal_PreInitialized asserts that the
// rates_provider_requests_total family is pre-initialised with exactly four
// series — one per outcome, all with provider="apilayer" — and counter value 0.
func TestNewRegistry_RatesProviderRequestsTotal_PreInitialized(t *testing.T) {
	t.Parallel()

	families := gatherFamilies(t)

	fam, ok := families[obs.MetricRatesProviderRequestsTotal]
	require.True(t, ok, "family %q must be present after NewRegistry()", obs.MetricRatesProviderRequestsTotal)

	metrics := fam.GetMetric()
	require.Len(t, metrics, 4, "expected exactly 4 pre-initialised series for %q", obs.MetricRatesProviderRequestsTotal)

	outcomes := make([]string, 0, 4)
	for _, m := range metrics {
		labelMap := make(map[string]string, 2)
		for _, lp := range m.GetLabel() {
			labelMap[lp.GetName()] = lp.GetValue()
		}
		assert.Equal(t, "apilayer", labelMap["provider"],
			"provider label must be \"apilayer\" for series %s", seriesID(obs.MetricRatesProviderRequestsTotal, m))
		outcomes = append(outcomes, labelMap["outcome"])
		require.NotNil(t, m.GetCounter(), "series must be a counter")
		assert.Equal(t, 0.0, m.GetCounter().GetValue(), "initial counter value must be 0")
	}

	sort.Strings(outcomes)
	assert.Equal(t, []string{"ok", "permanent", "quota_exceeded", "transient"}, outcomes,
		"outcome label values must be exactly [ok permanent quota_exceeded transient]")
}

// TestNewRegistry_RatesProviderRequestDurationSeconds_NoSyntheticSamples
// asserts that the rates_provider_request_duration_seconds histogram is
// pre-initialised with exactly one series (provider="apilayer") but that no
// synthetic observation was recorded — SampleCount must be 0, not 1.
func TestNewRegistry_RatesProviderRequestDurationSeconds_NoSyntheticSamples(t *testing.T) {
	t.Parallel()

	families := gatherFamilies(t)

	fam, ok := families[obs.MetricRatesProviderRequestDurationSeconds]
	require.True(t, ok, "family %q must be present after NewRegistry()", obs.MetricRatesProviderRequestDurationSeconds)

	metrics := fam.GetMetric()
	require.Len(t, metrics, 1, "expected exactly 1 pre-initialised series for %q", obs.MetricRatesProviderRequestDurationSeconds)

	m := metrics[0]
	labelMap := make(map[string]string, 1)
	for _, lp := range m.GetLabel() {
		labelMap[lp.GetName()] = lp.GetValue()
	}
	assert.Equal(t, "apilayer", labelMap["provider"],
		"provider label must be \"apilayer\"")

	h := m.GetHistogram()
	require.NotNil(t, h, "series must be a histogram")
	assert.Equal(t, uint64(0), h.GetSampleCount(),
		"SampleCount must be 0: WithLabelValues alone must not record a synthetic Observe(0)")
}

// TestNewRegistry_RatesProviderResponseAnomaliesTotal_PreInitialized asserts
// that rates_provider_response_anomalies_total is pre-initialised with exactly
// one series: provider="apilayer", kind="malformed_quote_key", counter=0.
func TestNewRegistry_RatesProviderResponseAnomaliesTotal_PreInitialized(t *testing.T) {
	t.Parallel()

	families := gatherFamilies(t)

	fam, ok := families[obs.MetricRatesProviderResponseAnomaliesTotal]
	require.True(t, ok, "family %q must be present after NewRegistry()", obs.MetricRatesProviderResponseAnomaliesTotal)

	metrics := fam.GetMetric()
	require.Len(t, metrics, 1, "expected exactly 1 pre-initialised series for %q", obs.MetricRatesProviderResponseAnomaliesTotal)

	m := metrics[0]
	labelMap := make(map[string]string, 2)
	for _, lp := range m.GetLabel() {
		labelMap[lp.GetName()] = lp.GetValue()
	}
	assert.Equal(t, "apilayer", labelMap["provider"], "provider label must be \"apilayer\"")
	assert.Equal(t, "malformed_quote_key", labelMap["kind"], "kind label must be \"malformed_quote_key\"")

	require.NotNil(t, m.GetCounter(), "series must be a counter")
	assert.Equal(t, 0.0, m.GetCounter().GetValue(), "initial counter value must be 0")
}

// TestNewRegistry_QuoteJobsCompletionSeconds_NoSyntheticSamples asserts that
// the quote_jobs_completion_seconds histogram is pre-initialised with exactly
// two series (source="scheduler" and source="refresh") and that neither has any
// recorded observations — SampleCount must be 0 for each.
//
// This test goes RED against the current implementation because lines 236–237
// of metrics.go call .Observe(0) which sets SampleCount=1.
func TestNewRegistry_QuoteJobsCompletionSeconds_NoSyntheticSamples(t *testing.T) {
	t.Parallel()

	families := gatherFamilies(t)

	fam, ok := families[obs.MetricQuoteJobsCompletionSeconds]
	require.True(t, ok, "family %q must be present after NewRegistry()", obs.MetricQuoteJobsCompletionSeconds)

	metrics := fam.GetMetric()
	require.Len(t, metrics, 2, "expected exactly 2 pre-initialised series for %q", obs.MetricQuoteJobsCompletionSeconds)

	sources := make([]string, 0, 2)
	for _, m := range metrics {
		labelMap := make(map[string]string, 1)
		for _, lp := range m.GetLabel() {
			labelMap[lp.GetName()] = lp.GetValue()
		}
		src := labelMap["source"]
		sources = append(sources, src)

		h := m.GetHistogram()
		require.NotNil(t, h, "series source=%q must be a histogram", src)
		assert.Equal(t, uint64(0), h.GetSampleCount(),
			"SampleCount for source=%q must be 0: use WithLabelValues without Observe(0)", src)
	}

	sort.Strings(sources)
	assert.Equal(t, []string{"refresh", "scheduler"}, sources,
		"source label values must be exactly [refresh scheduler]")
}

// TestNewRegistry_HTTPMetrics_NotPreInitialized asserts that neither
// http_requests_total nor http_request_duration_seconds appears in the gathered
// output immediately after obs.NewRegistry(). These metrics are
// dynamic-cardinality and must only appear after real HTTP traffic is recorded.
func TestNewRegistry_HTTPMetrics_NotPreInitialized(t *testing.T) {
	t.Parallel()

	families := gatherFamilies(t)

	_, httpTotalPresent := families[obs.MetricHTTPRequestsTotal]
	assert.False(t, httpTotalPresent,
		"family %q must NOT be present at registry init; it appears only after first HTTP request",
		obs.MetricHTTPRequestsTotal)

	_, httpDurationPresent := families[obs.MetricHTTPRequestDurationSeconds]
	assert.False(t, httpDurationPresent,
		"family %q must NOT be present at registry init; it appears only after first HTTP request",
		obs.MetricHTTPRequestDurationSeconds)
}
