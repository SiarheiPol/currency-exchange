package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// validMinimalEnv sets the minimum env vars required for Load() to succeed,
// leaving REFRESH_MAX_LATENCY_MS and WORKER_COUNT unset so defaults apply.
func setValidMinimalEnv(t *testing.T) {
	t.Helper()
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("PROVIDER_API_KEY", "k")
	t.Setenv("WHITELIST_CURRENCIES", "")
	t.Setenv("SCHEDULER_TICK_SECONDS", "")
	t.Setenv("COALESCING_WINDOW_SECONDS", "")
	t.Setenv("REFRESH_MAX_LATENCY_MS", "")
	t.Setenv("WORKER_COUNT", "")
}

func TestConfig_Load_MissingProviderAPIKey_ReturnsError(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("PROVIDER_API_KEY", "")

	_, err := Load()

	require.Error(t, err)
}

func TestConfig_Load_MissingDBDSN_ReturnsError(t *testing.T) {
	t.Setenv("DB_DSN", "")
	t.Setenv("PROVIDER_API_KEY", "test-key")

	_, err := Load()

	require.Error(t, err)
}

func TestConfig_Load_DefaultBaseURL_AppliedWhenAbsent(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("PROVIDER_API_KEY", "test-key")
	t.Setenv("PROVIDER_BASE_URL", "")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "https://api.currencylayer.com", cfg.ProviderBaseURL)
	require.Equal(t, "test-key", cfg.ProviderAPIKey)
}

func TestConfig_Load_SchedulerDefaults(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("PROVIDER_API_KEY", "k")
	t.Setenv("WHITELIST_CURRENCIES", "")
	t.Setenv("SCHEDULER_TICK_SECONDS", "")
	t.Setenv("COALESCING_WINDOW_SECONDS", "")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, []string{"USD", "EUR", "MXN"}, cfg.WhitelistCurrencies)
	require.Equal(t, 30*time.Second, cfg.SchedulerTickInterval)
	require.Equal(t, 30*time.Second, cfg.CoalescingWindow)
}

func TestConfig_Load_SchedulerFieldsParsed(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("PROVIDER_API_KEY", "k")
	t.Setenv("WHITELIST_CURRENCIES", "USD,EUR")
	t.Setenv("SCHEDULER_TICK_SECONDS", "60")
	t.Setenv("COALESCING_WINDOW_SECONDS", "120")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, []string{"USD", "EUR"}, cfg.WhitelistCurrencies)
	require.Equal(t, 60*time.Second, cfg.SchedulerTickInterval)
	require.Equal(t, 120*time.Second, cfg.CoalescingWindow)
}

func TestConfig_Load_WhitelistTooShort_ReturnsError(t *testing.T) {
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("PROVIDER_API_KEY", "k")
	t.Setenv("WHITELIST_CURRENCIES", "USD")
	t.Setenv("SCHEDULER_TICK_SECONDS", "")
	t.Setenv("COALESCING_WINDOW_SECONDS", "")

	_, err := Load()

	require.Error(t, err)
}

func TestConfig_Load_NonPositiveIntervalsRejected(t *testing.T) {
	t.Run("zero_tick", func(t *testing.T) {
		t.Setenv("DB_DSN", "postgres://test")
		t.Setenv("PROVIDER_API_KEY", "k")
		t.Setenv("WHITELIST_CURRENCIES", "")
		t.Setenv("SCHEDULER_TICK_SECONDS", "0")
		t.Setenv("COALESCING_WINDOW_SECONDS", "30")

		_, err := Load()

		require.Error(t, err)
	})

	t.Run("zero_coalescing", func(t *testing.T) {
		t.Setenv("DB_DSN", "postgres://test")
		t.Setenv("PROVIDER_API_KEY", "k")
		t.Setenv("WHITELIST_CURRENCIES", "")
		t.Setenv("SCHEDULER_TICK_SECONDS", "30")
		t.Setenv("COALESCING_WINDOW_SECONDS", "0")

		_, err := Load()

		require.Error(t, err)
	})
}

// T-1. Default SLA values: RefreshMaxLatency=2s, WorkerCount=1 when env vars absent.
func TestConfig_Load_DefaultSLAValues(t *testing.T) {
	setValidMinimalEnv(t)

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 2*time.Second, cfg.RefreshMaxLatency)
	require.Equal(t, 1, cfg.WorkerCount)
}

// T-2. Derived PollInterval with default SLA: 2000ms − 500ms − 100ms − 400ms = 1000ms.
func TestConfig_Load_DerivedPollInterval_DefaultSLA(t *testing.T) {
	setValidMinimalEnv(t)

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 1*time.Second, cfg.PollInterval)
}

// T-3. Derived BatchSize with default whitelist [USD,EUR,MXN] and WorkerCount=1:
// N*(N-1)=6 pairs, ceil(6/1)=6.
func TestConfig_Load_DerivedBatchSize_DefaultWhitelistOneWorker(t *testing.T) {
	setValidMinimalEnv(t)

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 6, cfg.BatchSize)
}

// T-4. Derived BatchSize with WorkerCount=2: ceil(6/2)=3.
func TestConfig_Load_DerivedBatchSize_TwoWorkers(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("WORKER_COUNT", "2")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 3, cfg.BatchSize)
}

// T-5. Derived BatchSize rounds up with 4-currency whitelist (12 pairs) and
// WorkerCount=5: ceil(12/5)=3.
func TestConfig_Load_DerivedBatchSize_CeilNonDivisible(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("WHITELIST_CURRENCIES", "USD,EUR,MXN,GBP")
	t.Setenv("WORKER_COUNT", "5")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 3, cfg.BatchSize)
}

// T-6. SLA below floor (999ms < 1000ms) causes Load() to return an error
// mentioning configured value and floor.
func TestConfig_Load_SLABelowFloor_ReturnsError(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "999")

	_, err := Load()

	require.Error(t, err)
}

// T-7. SLA at exactly the floor (1000ms) is accepted; PollInterval == 0.
func TestConfig_Load_SLAAtFloor_AcceptedAndPollIntervalZero(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "1000")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 0*time.Millisecond, cfg.PollInterval)
}

// T-8. Non-integer string for REFRESH_MAX_LATENCY_MS returns an error.
func TestConfig_Load_InvalidRefreshMaxLatencyMS_NonInteger(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "abc")

	_, err := Load()

	require.Error(t, err)
}

// T-9. WORKER_COUNT=0 returns an error (K >= 1 required).
func TestConfig_Load_WorkerCountZero_ReturnsError(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("WORKER_COUNT", "0")

	_, err := Load()

	require.Error(t, err)
}

// T-10. REFRESH_MAX_LATENCY_MS=3000 sets RefreshMaxLatency=3s and
// PollInterval=2s (3000ms − 1000ms floor).
func TestConfig_Load_RefreshMaxLatencyOverride_PropagesToPollInterval(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "3000")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 3*time.Second, cfg.RefreshMaxLatency)
	require.Equal(t, 2*time.Second, cfg.PollInterval)
}

// T-12. REFRESH_MAX_LATENCY_MS=0 returns an error (zero is not a positive integer).
func TestConfig_Load_RefreshMaxLatencyMS_ZeroRejected(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "0")

	_, err := Load()

	require.Error(t, err)
}

// T-11. Sub-floor error message mentions configured value (500/500ms) and
// floor (1000/1000ms) so an operator knows what to fix.
func TestConfig_Load_SLABelowFloor_ErrorMessageMentionsValues(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "500")

	_, err := Load()

	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
	require.Contains(t, err.Error(), "1000")
}
