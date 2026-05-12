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

func TestConfig_Load_DefaultSLAValues(t *testing.T) {
	setValidMinimalEnv(t)

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 2*time.Second, cfg.RefreshMaxLatency)
	require.Equal(t, 1, cfg.WorkerCount)
}

func TestConfig_Load_DerivedPollInterval_DefaultSLA(t *testing.T) {
	setValidMinimalEnv(t)

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 1*time.Second, cfg.PollInterval)
}

// N*(N-1) = 6 pairs from a 3-currency whitelist, ceil(6/1) = 6.
func TestConfig_Load_DerivedBatchSize_DefaultWhitelistOneWorker(t *testing.T) {
	setValidMinimalEnv(t)

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 6, cfg.BatchSize)
}

// ceil(6/2) = 3.
func TestConfig_Load_DerivedBatchSize_TwoWorkers(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("WORKER_COUNT", "2")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 3, cfg.BatchSize)
}

// 4-currency whitelist → 12 pairs; WorkerCount=5; ceil(12/5) = 3 (not 2).
func TestConfig_Load_DerivedBatchSize_CeilNonDivisible(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("WHITELIST_CURRENCIES", "USD,EUR,MXN,GBP")
	t.Setenv("WORKER_COUNT", "5")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 3, cfg.BatchSize)
}

func TestConfig_Load_SLABelowFloor_ReturnsError(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "999")

	_, err := Load()

	require.Error(t, err)
}

func TestConfig_Load_SLAAtFloor_AcceptedAndPollIntervalZero(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "1000")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 0*time.Millisecond, cfg.PollInterval)
}

func TestConfig_Load_InvalidRefreshMaxLatencyMS_NonInteger(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "abc")

	_, err := Load()

	require.Error(t, err)
}

func TestConfig_Load_WorkerCountZero_ReturnsError(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("WORKER_COUNT", "0")

	_, err := Load()

	require.Error(t, err)
}

// PollInterval = REFRESH_MAX_LATENCY − floor = 3000ms − 1000ms = 2s.
func TestConfig_Load_RefreshMaxLatencyOverride_PropagesToPollInterval(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "3000")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 3*time.Second, cfg.RefreshMaxLatency)
	require.Equal(t, 2*time.Second, cfg.PollInterval)
}

func TestConfig_Load_RefreshMaxLatencyMS_ZeroRejected(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "0")

	_, err := Load()

	require.Error(t, err)
}

func TestConfig_Load_SLABelowFloor_ErrorMessageMentionsValues(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("REFRESH_MAX_LATENCY_MS", "500")

	_, err := Load()

	require.Error(t, err)
	require.Contains(t, err.Error(), "500")
	require.Contains(t, err.Error(), "1000")
}

func TestConfig_LogLevel_DefaultsToInfo(t *testing.T) {
	setValidMinimalEnv(t)
	// LOG_LEVEL is not set; Load() must default to "info".
	t.Setenv("LOG_LEVEL", "")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, "info", cfg.LogLevel)
}

func TestConfig_LogLevel_ValidValuesAccepted(t *testing.T) {
	cases := []struct {
		input     string
		wantLower string
	}{
		{"debug", "debug"},
		{"info", "info"},
		{"warn", "warn"},
		{"error", "error"},
		{"DEBUG", "debug"},
		{"Info", "info"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.input, func(t *testing.T) {
			setValidMinimalEnv(t)
			t.Setenv("LOG_LEVEL", tc.input)

			cfg, err := Load()

			require.NoError(t, err)
			require.Equal(t, tc.wantLower, cfg.LogLevel)
		})
	}
}

func TestConfig_LogLevel_InvalidValueRejected(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("LOG_LEVEL", "garbage")

	_, err := Load()

	require.Error(t, err)
	require.Contains(t, err.Error(), "LOG_LEVEL")
}

func TestConfig_DBPoolMaxConns_DefaultsTo25(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("DB_POOL_MAX_CONNS", "")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 25, cfg.DBPoolMaxConns)
}

func TestConfig_DBPoolMaxConns_ParsesEnvVar(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("DB_POOL_MAX_CONNS", "50")

	cfg, err := Load()

	require.NoError(t, err)
	require.Equal(t, 50, cfg.DBPoolMaxConns)
}

func TestConfig_DBPoolMaxConns_RejectsZero(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("DB_POOL_MAX_CONNS", "0")

	_, err := Load()

	require.Error(t, err)
	require.ErrorContains(t, err, "DB_POOL_MAX_CONNS must be >= 1")
}

func TestConfig_DBPoolMaxConns_RejectsNegative(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("DB_POOL_MAX_CONNS", "-5")

	_, err := Load()

	require.Error(t, err)
	require.ErrorContains(t, err, "DB_POOL_MAX_CONNS must be >= 1")
}

func TestConfig_DBPoolMaxConns_RejectsNonNumeric(t *testing.T) {
	setValidMinimalEnv(t)
	t.Setenv("DB_POOL_MAX_CONNS", "abc")

	_, err := Load()

	require.Error(t, err)
	require.ErrorContains(t, err, "DB_POOL_MAX_CONNS")
}
