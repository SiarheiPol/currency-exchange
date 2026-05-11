// Package main runs the currency quote service HTTP server.
//
// Bootstrap order on startup:
//  1. Logging — JSON handler set as slog default.
//  2. Postgres pool — pgxpool.New, then Ping. Failure here aborts startup.
//  3. Startup probe — apilayer.Provider.FetchPairs([{USD,EUR}]) sanity check.
//  4. Worker — pgqueue + worker.Run in a goroutine, ctx cancelled on shutdown.
//  5. Scheduler — scheduler.Run in a goroutine, ticks the queue from the whitelist.
//  6. HTTP server — /healthz, /metrics, /readyz mounted; the handler chain is
//     RequestID → PanicRecover → Metrics → mux. In dev/test profiles
//     (cfg.Env != "production"), an OpenAPIValidate middleware wraps the mux
//     to enforce request/response schema compliance at runtime.
//
// Shutdown order on SIGINT/SIGTERM:
//
//	HTTP (Shutdown) → scheduler (ctx cancel + drain) → worker (ctx cancel + drain) → pool.Close.
//
// This is the order from docs/discussions/implementation-roadmap.md Stage 5.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"runtime/debug"
	"syscall"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/jackc/pgx/v5/pgxpool"

	"currency-exchange/internal/api"
	"currency-exchange/internal/clock"
	"currency-exchange/internal/health"
	"currency-exchange/internal/httpmw"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue/pgqueue"
	"currency-exchange/internal/quoterepo/pgquoterepo"
	"currency-exchange/internal/ratesprovider/apilayer"
	"currency-exchange/internal/scheduler"
	"currency-exchange/internal/startup"
	"currency-exchange/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	// Bootstrap with a plain handler so any Load() error can be logged.
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := Load()
	if err != nil {
		return err
	}

	// Rewire the default logger now that we know the desired level.
	var lvl slog.Level
	switch cfg.LogLevel {
	case "debug":
		lvl = slog.LevelDebug
	case "info":
		lvl = slog.LevelInfo
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	}
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})))

	// Load the embedded OpenAPI spec early so startup fails fast before any
	// goroutines are launched. Only used when Env != "production".
	var openAPISpec *openapi3.T
	if cfg.Env != "production" {
		openAPISpec, err = api.GetSpec()
		if err != nil {
			return fmt.Errorf("load embedded openapi spec: %w", err)
		}
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	workerHeartbeatThreshold := 6 * cfg.PollInterval

	obs.Logger(rootCtx).Info(obs.EvWorkerConfigDerived,
		slog.Int64("poll_interval_ms", cfg.PollInterval.Milliseconds()),
		slog.Int("batch_size", cfg.BatchSize),
	)

	pool, err := pgxpool.New(rootCtx, cfg.DBDSN)
	if err != nil {
		return fmt.Errorf("pgxpool.New: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(rootCtx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	obs.Logger(rootCtx).Info(obs.EvPostgresConnected)

	clk := clock.New()
	q := pgqueue.New(pool, clk)
	provider := &apilayer.Provider{
		APIKey:  cfg.ProviderAPIKey,
		BaseURL: cfg.ProviderBaseURL,
		Clock:   clk,
	}

	// Probe the upstream provider before accepting traffic. Any failure aborts startup.
	probeCtx, probeCancel := context.WithTimeout(rootCtx, 5*time.Second)
	defer probeCancel()
	if err := startup.Probe(probeCtx, provider); err != nil {
		return fmt.Errorf("startup probe: %w", err)
	}
	obs.Logger(rootCtx).Info(obs.EvStartupProbeOK)

	repo := pgquoterepo.New(pool)
	w := worker.New(q, q, provider, repo, clk,
		worker.WithPollInterval(cfg.PollInterval),
		worker.WithBatchSize(cfg.BatchSize),
	)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		defer func() {
			if r := recover(); r != nil {
				obs.LogPanicRecovered(workerCtx, r, debug.Stack())
				stop()
			}
		}()
		obs.Logger(workerCtx).Info(obs.EvWorkerStarted)
		if err := w.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			obs.Logger(workerCtx).Error(obs.EvWorkerExitedUnexpectedly, "error", err)
			stop() // trigger graceful shutdown of the rest of the process
		}
	}()

	schedHeartbeatThreshold := 6 * cfg.SchedulerTickInterval
	sched := scheduler.New(
		scheduler.WithInterval(cfg.SchedulerTickInterval),
		scheduler.WithBucketSize(cfg.CoalescingWindow),
		scheduler.WithPairs(expandWhitelist(cfg.WhitelistCurrencies)),
		scheduler.WithQueue(q),
		scheduler.WithClock(clk),
		scheduler.WithIDGen(idgen.New()),
	)

	schedCtx, schedCancel := context.WithCancel(context.Background())
	schedDone := make(chan struct{})
	go func() {
		defer close(schedDone)
		defer func() {
			if r := recover(); r != nil {
				obs.LogPanicRecovered(schedCtx, r, debug.Stack())
				stop()
			}
		}()
		obs.Logger(schedCtx).Info(obs.EvSchedulerStarted)
		if err := sched.Run(schedCtx); err != nil && !errors.Is(err, context.Canceled) {
			obs.Logger(schedCtx).Error(obs.EvSchedulerExitedUnexpectedly, "error", err)
			stop()
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("/openapi.json", api.OpenAPIJSONHandler)
	mux.Handle("/docs/", api.SwaggerUIHandler)
	mux.Handle("GET /healthz", health.Healthz())
	mux.Handle("GET /metrics", obs.MetricsHandler())
	mux.Handle("GET /readyz", health.Readyz(
		[]health.Checker{health.PostgresChecker(pool)},
		[]health.Checker{
			health.WorkerChecker(w, workerHeartbeatThreshold),
			health.SchedulerChecker(sched, schedHeartbeatThreshold),
		},
	))

	handlers := api.NewHandlers(cfg.WhitelistCurrencies, q, clk, idgen.New(), cfg.CoalescingWindow, repo)
	api.HandlerWithOptions(handlers, api.StdHTTPServerOptions{
		BaseRouter:       mux,
		ErrorHandlerFunc: api.JSONErrorHandler,
	})

	// Wire OpenAPI runtime validation middleware (dev/test only).
	var inner http.Handler = mux
	if openAPISpec != nil {
		inner = httpmw.OpenAPIValidate(openAPISpec, mux)
		obs.Logger(rootCtx).Info(obs.EvOpenAPIValidateEnabled, "env", cfg.Env)
	}

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpmw.RequestID(httpmw.PanicRecover(httpmw.Metrics(inner))),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		obs.Logger(rootCtx).Info(obs.EvHTTPServerStarted, "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	const shutdownTimeout = 10 * time.Second
	shutdownCtx := context.WithoutCancel(rootCtx)

	select {
	case <-rootCtx.Done():
		obs.Logger(shutdownCtx).Info(obs.EvShutdownSignalReceived)
	case err := <-serverErr:
		if gsErr := gracefulShutdown(shutdownCtx, srv, schedCancel, schedDone, workerCancel, workerDone, shutdownTimeout); gsErr != nil {
			obs.Logger(shutdownCtx).Error(obs.EvHTTPShutdownFailed, "error", gsErr)
		}
		return fmt.Errorf("http server: %w", err)
	}

	// Signal branch: HTTP server is still up; drain it then drain scheduler and worker.
	if err := gracefulShutdown(shutdownCtx, srv, schedCancel, schedDone, workerCancel, workerDone, shutdownTimeout); err != nil {
		obs.Logger(shutdownCtx).Error(obs.EvHTTPShutdownFailed, "error", err)
	}

	// pool.Close runs via defer on return.
	return nil
}

// gracefulShutdown drains the running server in HTTP → scheduler → worker
// order. Each stage is bounded by the supplied timeout; if a stage exceeds
// it, the helper logs a warning via obs.Logger(ctx) and proceeds rather
// than hanging the process.
//
// The ctx parameter should be derived from rootCtx via context.WithoutCancel
// so log calls retain context values (request_id, logger) even though rootCtx
// is already canceled at shutdown time.
func gracefulShutdown(
	ctx context.Context,
	srv *http.Server,
	schedCancel context.CancelFunc,
	schedDone <-chan struct{},
	workerCancel context.CancelFunc,
	workerDone <-chan struct{},
	timeout time.Duration,
) error {
	// 1. Stop accepting new HTTP requests; let in-flight ones drain.
	shutdownCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	var firstErr error
	if err := srv.Shutdown(shutdownCtx); err != nil {
		firstErr = err
	}

	// 2. Stop the scheduler and wait for it to drain.
	schedCancel()
	select {
	case <-schedDone:
	case <-time.After(timeout):
		obs.Logger(ctx).Warn(obs.EvSchedulerStopTimeout)
	}

	// 3. Stop the worker and wait for it to drain.
	workerCancel()
	select {
	case <-workerDone:
	case <-time.After(timeout):
		obs.Logger(ctx).Warn(obs.EvWorkerStopTimeout)
	}

	return firstErr
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
