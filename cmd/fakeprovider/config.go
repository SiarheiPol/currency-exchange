package main

import (
	"errors"
	"os"
	"strconv"
)

// Config holds runtime configuration for the fake rates provider, loaded from
// environment variables.
type Config struct {
	Addr           string
	Seed           uint64
	MonthlyQuota   int
	AccessKey      string
	CadenceSeconds int64
	LatencyMinMS   int64
	LatencyMaxMS   int64
}

const (
	defaultAddr         = ":9090"
	defaultSeed         = uint64(42)
	defaultMonthlyQuota = 100
)

// Load reads configuration from the environment and returns an error if any
// value is malformed.
func Load() (*Config, error) {
	cfg := &Config{
		Addr:         envOr("FAKE_ADDR", defaultAddr),
		Seed:         defaultSeed,
		MonthlyQuota: defaultMonthlyQuota,
		AccessKey:    os.Getenv("FAKE_ACCESS_KEY"),
	}
	if v := os.Getenv("FAKE_SEED"); v != "" {
		n, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return nil, errors.New("FAKE_SEED must be a uint64")
		}
		cfg.Seed = n
	}
	if v := os.Getenv("FAKE_MONTHLY_QUOTA"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 {
			return nil, errors.New("FAKE_MONTHLY_QUOTA must be a non-negative integer")
		}
		cfg.MonthlyQuota = n
	}
	if v := os.Getenv("FAKE_UPSTREAM_CADENCE_SECONDS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, errors.New("FAKE_UPSTREAM_CADENCE_SECONDS must be a non-negative integer")
		}
		if n < 0 {
			return nil, errors.New("FAKE_UPSTREAM_CADENCE_SECONDS must be non-negative")
		}
		cfg.CadenceSeconds = n
	}
	if v := os.Getenv("FAKE_LATENCY_MIN_MS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, errors.New("FAKE_LATENCY_MIN_MS must be non-negative")
		}
		if n < 0 {
			return nil, errors.New("FAKE_LATENCY_MIN_MS must be non-negative")
		}
		cfg.LatencyMinMS = n
	}
	if v := os.Getenv("FAKE_LATENCY_MAX_MS"); v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return nil, errors.New("FAKE_LATENCY_MAX_MS must be non-negative")
		}
		if n < 0 {
			return nil, errors.New("FAKE_LATENCY_MAX_MS must be non-negative")
		}
		cfg.LatencyMaxMS = n
	}
	if cfg.LatencyMaxMS < cfg.LatencyMinMS {
		return nil, errors.New("FAKE_LATENCY_MAX_MS must be >= FAKE_LATENCY_MIN_MS")
	}
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
