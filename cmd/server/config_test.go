package main

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

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
