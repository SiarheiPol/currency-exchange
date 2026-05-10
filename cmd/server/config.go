package main

import (
	"errors"
	"os"
)

// Config holds the runtime configuration loaded from the environment.
type Config struct {
	HTTPAddr        string
	DBDSN           string
	ProviderAPIKey  string
	ProviderBaseURL string
}

const defaultProviderBaseURL = "https://api.currencylayer.com"
const defaultHTTPAddr = ":8080"

// Load reads configuration from the environment and validates required
// values. Returns an error if a required value is missing.
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:        envOr("HTTP_ADDR", defaultHTTPAddr),
		DBDSN:           os.Getenv("DB_DSN"),
		ProviderAPIKey:  os.Getenv("PROVIDER_API_KEY"),
		ProviderBaseURL: envOr("PROVIDER_BASE_URL", defaultProviderBaseURL),
	}
	if cfg.DBDSN == "" {
		return nil, errors.New("DB_DSN environment variable is required")
	}
	if cfg.ProviderAPIKey == "" {
		return nil, errors.New("PROVIDER_API_KEY environment variable is required")
	}
	return cfg, nil
}
