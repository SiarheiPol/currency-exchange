package main

import (
	"errors"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds the runtime configuration loaded from the environment.
type Config struct {
	HTTPAddr              string
	DBDSN                 string
	ProviderAPIKey        string
	ProviderBaseURL       string
	WhitelistCurrencies   []string
	SchedulerTickInterval time.Duration
	CoalescingWindow      time.Duration
}

const defaultProviderBaseURL = "https://api.currencylayer.com"
const defaultHTTPAddr = ":8080"
const defaultSchedulerTickSeconds = 30
const defaultCoalescingSeconds = 30

var defaultWhitelist = []string{"USD", "EUR", "MXN"}

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

	// Whitelist
	if v := os.Getenv("WHITELIST_CURRENCIES"); v != "" {
		cfg.WhitelistCurrencies = strings.Split(v, ",")
	} else {
		cfg.WhitelistCurrencies = defaultWhitelist
	}
	if len(cfg.WhitelistCurrencies) < 2 {
		return nil, errors.New("WHITELIST_CURRENCIES must contain at least 2 currencies")
	}

	// SchedulerTickInterval
	tickSeconds := defaultSchedulerTickSeconds
	if v := os.Getenv("SCHEDULER_TICK_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, errors.New("SCHEDULER_TICK_SECONDS must be an integer")
		}
		tickSeconds = n
	}
	if tickSeconds <= 0 {
		return nil, errors.New("SCHEDULER_TICK_SECONDS must be > 0")
	}
	cfg.SchedulerTickInterval = time.Duration(tickSeconds) * time.Second

	// CoalescingWindow
	coalescingSeconds := defaultCoalescingSeconds
	if v := os.Getenv("COALESCING_WINDOW_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, errors.New("COALESCING_WINDOW_SECONDS must be an integer")
		}
		coalescingSeconds = n
	}
	if coalescingSeconds <= 0 {
		return nil, errors.New("COALESCING_WINDOW_SECONDS must be > 0")
	}
	cfg.CoalescingWindow = time.Duration(coalescingSeconds) * time.Second

	return cfg, nil
}
