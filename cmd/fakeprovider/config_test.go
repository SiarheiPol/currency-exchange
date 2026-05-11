package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestConfig_NewVars_DefaultsWhenUnset verifies that when none of the three new
// env vars are set, Load() returns zero values for CadenceSeconds, LatencyMinMS,
// and LatencyMaxMS.
func TestConfig_NewVars_DefaultsWhenUnset(t *testing.T) {
	// Unset any env vars that might bleed from the outer process.
	t.Setenv("FAKE_UPSTREAM_CADENCE_SECONDS", "")
	t.Setenv("FAKE_LATENCY_MIN_MS", "")
	t.Setenv("FAKE_LATENCY_MAX_MS", "")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, int64(0), cfg.CadenceSeconds, "CadenceSeconds must default to 0")
	require.Equal(t, int64(0), cfg.LatencyMinMS, "LatencyMinMS must default to 0")
	require.Equal(t, int64(0), cfg.LatencyMaxMS, "LatencyMaxMS must default to 0")
}

// TestConfig_NewVars_ParseCorrectly verifies that when all three new env vars
// are set to valid values, Load() parses them correctly.
func TestConfig_NewVars_ParseCorrectly(t *testing.T) {
	t.Setenv("FAKE_UPSTREAM_CADENCE_SECONDS", "600")
	t.Setenv("FAKE_LATENCY_MIN_MS", "100")
	t.Setenv("FAKE_LATENCY_MAX_MS", "500")

	cfg, err := Load()
	require.NoError(t, err)
	require.Equal(t, int64(600), cfg.CadenceSeconds)
	require.Equal(t, int64(100), cfg.LatencyMinMS)
	require.Equal(t, int64(500), cfg.LatencyMaxMS)
}

// TestConfig_Validation_NegativeLatencyMin verifies that a negative
// FAKE_LATENCY_MIN_MS value causes Load() to return an error containing the
// expected message.
func TestConfig_Validation_NegativeLatencyMin(t *testing.T) {
	t.Setenv("FAKE_LATENCY_MIN_MS", "-1")
	t.Setenv("FAKE_LATENCY_MAX_MS", "")

	_, err := Load()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "FAKE_LATENCY_MIN_MS must be non-negative"),
		"error message must mention FAKE_LATENCY_MIN_MS must be non-negative; got: %v", err)
}

// TestConfig_Validation_NegativeLatencyMax verifies that a negative
// FAKE_LATENCY_MAX_MS value causes Load() to return an error containing the
// expected message.
func TestConfig_Validation_NegativeLatencyMax(t *testing.T) {
	t.Setenv("FAKE_LATENCY_MIN_MS", "")
	t.Setenv("FAKE_LATENCY_MAX_MS", "-1")

	_, err := Load()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "FAKE_LATENCY_MAX_MS must be non-negative"),
		"error message must mention FAKE_LATENCY_MAX_MS must be non-negative; got: %v", err)
}

// TestConfig_Validation_MaxLessThanMin verifies that when FAKE_LATENCY_MAX_MS
// is positive but less than FAKE_LATENCY_MIN_MS, Load() returns an error
// containing the expected message.
func TestConfig_Validation_MaxLessThanMin(t *testing.T) {
	t.Setenv("FAKE_LATENCY_MIN_MS", "100")
	t.Setenv("FAKE_LATENCY_MAX_MS", "50")

	_, err := Load()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "FAKE_LATENCY_MAX_MS must be >= FAKE_LATENCY_MIN_MS"),
		"error message must mention FAKE_LATENCY_MAX_MS must be >= FAKE_LATENCY_MIN_MS; got: %v", err)
}

// TestConfig_Validation_MinPositiveMaxZero verifies that setting MIN > 0 with
// MAX unset (i.e. defaulting to 0) is rejected, preventing a runtime panic in
// sampleLatency where Int64N(span) would receive a negative span.
func TestConfig_Validation_MinPositiveMaxZero(t *testing.T) {
	t.Setenv("FAKE_LATENCY_MIN_MS", "100")
	t.Setenv("FAKE_LATENCY_MAX_MS", "")

	_, err := Load()
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "FAKE_LATENCY_MAX_MS must be >= FAKE_LATENCY_MIN_MS"),
		"error message must mention FAKE_LATENCY_MAX_MS must be >= FAKE_LATENCY_MIN_MS; got: %v", err)
}
