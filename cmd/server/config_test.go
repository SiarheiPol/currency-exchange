package main

import (
	"testing"

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
