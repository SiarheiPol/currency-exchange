package main

import (
	"errors"
	"os"
	"strconv"
)

// Config holds runtime configuration for the fake rates provider, loaded from
// environment variables.
type Config struct {
	Addr         string
	Seed         uint64
	MonthlyQuota int
	AccessKey    string
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
	return cfg, nil
}

func envOr(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}
