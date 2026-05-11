package main

import (
	"errors"
	"fmt"
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
	// Env is the runtime profile. Set to "production" to disable the
	// kin-openapi request/response validation middleware. Default: "development".
	Env string

	// SLA and worker capacity fields (capacity.md § Derived worker parameters).
	RefreshMaxLatency time.Duration
	WorkerCount       int
	PollInterval      time.Duration
	BatchSize         int
}

const defaultProviderBaseURL = "https://api.currencylayer.com"
const defaultHTTPAddr = ":8080"
const defaultSchedulerTickSeconds = 30
const defaultCoalescingSeconds = 30

// SLA budget constants (capacity.md § Budget decomposition).
const (
	upstreamP99 = 500 * time.Millisecond
	dbP99       = 100 * time.Millisecond
	slaMargin   = 400 * time.Millisecond
	minSLAFloor = upstreamP99 + dbP99 + slaMargin // 1000ms
)

const defaultRefreshMaxLatencyMS = 2000
const defaultWorkerCount = 1

var defaultWhitelist = []string{"USD", "EUR", "MXN"}

// Load reads configuration from the environment and validates required
// values. Returns an error if a required value is missing.
func Load() (*Config, error) {
	cfg := &Config{
		HTTPAddr:        envOr("HTTP_ADDR", defaultHTTPAddr),
		DBDSN:           os.Getenv("DB_DSN"),
		ProviderAPIKey:  os.Getenv("PROVIDER_API_KEY"),
		ProviderBaseURL: envOr("PROVIDER_BASE_URL", defaultProviderBaseURL),
		Env:             envOr("APP_ENV", "development"),
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

	// REFRESH_MAX_LATENCY_MS
	refreshMS := defaultRefreshMaxLatencyMS
	if v := os.Getenv("REFRESH_MAX_LATENCY_MS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, errors.New("REFRESH_MAX_LATENCY_MS must be an integer")
		}
		refreshMS = n
	}
	cfg.RefreshMaxLatency = time.Duration(refreshMS) * time.Millisecond
	if cfg.RefreshMaxLatency < minSLAFloor {
		return nil, fmt.Errorf(
			"REFRESH_MAX_LATENCY_MS=%d is below the minimum achievable SLA of %dms (upstream_p99=%s + db_p99=%s + margin=%s)",
			refreshMS, minSLAFloor.Milliseconds(), upstreamP99, dbP99, slaMargin,
		)
	}

	// WORKER_COUNT
	workerCount := defaultWorkerCount
	if v := os.Getenv("WORKER_COUNT"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			return nil, errors.New("WORKER_COUNT must be an integer")
		}
		workerCount = n
	}
	if workerCount < 1 {
		return nil, errors.New("WORKER_COUNT must be >= 1")
	}
	cfg.WorkerCount = workerCount

	// Derived fields.
	cfg.PollInterval = cfg.RefreshMaxLatency - upstreamP99 - dbP99 - slaMargin

	pairs := len(cfg.WhitelistCurrencies) * (len(cfg.WhitelistCurrencies) - 1)
	cfg.BatchSize = (pairs + cfg.WorkerCount - 1) / cfg.WorkerCount
	if cfg.BatchSize < 1 {
		cfg.BatchSize = 1
	}

	return cfg, nil
}
