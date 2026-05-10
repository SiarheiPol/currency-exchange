// Package main runs the currency quote service HTTP server.
//
// Bootstrap order on startup:
//  1. Logging — JSON handler set as slog default.
//  2. Postgres pool — pgxpool.New, then Ping. Failure here aborts startup.
//  3. Worker — pgqueue + worker.Run in a goroutine, ctx cancelled on shutdown.
//  4. HTTP server — /healthz, /metrics, /readyz mounted; RequestID middleware.
//
// Shutdown order on SIGINT/SIGTERM:
//
//	HTTP (Shutdown) → worker (ctx cancel + drain) → pool.Close.
//
// This is the order from docs/discussions/implementation-roadmap.md Stage 5;
// scheduler will slot between HTTP and worker when it lands.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/health"
	"currency-exchange/internal/httpmw"
	"currency-exchange/internal/obs"
	"currency-exchange/internal/queue/pgqueue"
	"currency-exchange/internal/quoterepo/pgquoterepo"
	"currency-exchange/internal/ratesprovider/apilayer"
	"currency-exchange/internal/startup"
	"currency-exchange/internal/worker"
)

// workerHeartbeatThreshold bounds how stale the worker's last-iteration
// timestamp can be before /readyz reports it as degraded. Six times the
// default poll interval (5s) gives room for missed ticks under load without
// false positives.
const workerHeartbeatThreshold = 30 * time.Second

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	cfg, err := Load()
	if err != nil {
		return err
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := pgxpool.New(rootCtx, cfg.DBDSN)
	if err != nil {
		return fmt.Errorf("pgxpool.New: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(rootCtx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}
	obs.Logger(rootCtx).Info("postgres connected")

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
	w := worker.New(q, q, provider, repo, clk)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		obs.Logger(workerCtx).Info("worker starting")
		if err := w.Run(workerCtx); err != nil && !errors.Is(err, context.Canceled) {
			obs.Logger(workerCtx).Error("worker exited unexpectedly", "error", err)
			stop() // trigger graceful shutdown of the rest of the process
		}
	}()

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", health.Healthz())
	mux.Handle("GET /metrics", obs.MetricsHandler())
	mux.Handle("GET /readyz", health.Readyz(
		[]health.Checker{health.PostgresChecker(pool)},
		[]health.Checker{health.WorkerChecker(w, workerHeartbeatThreshold)},
	))

	srv := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           httpmw.RequestID(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		obs.Logger(rootCtx).Info("http server starting", "addr", cfg.HTTPAddr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case <-rootCtx.Done():
		obs.Logger(context.Background()).Info("shutdown signal received")
	case err := <-serverErr:
		workerCancel()
		<-workerDone
		return fmt.Errorf("http server: %w", err)
	}

	// 1. Stop accepting new HTTP requests; let in-flight ones drain.
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		obs.Logger(context.Background()).Error("http shutdown failed", "error", err)
	}

	// 2. Stop the worker and wait for it to drain.
	workerCancel()
	select {
	case <-workerDone:
	case <-time.After(10 * time.Second):
		obs.Logger(context.Background()).Warn("worker did not stop within 10s")
	}

	// 3. pool.Close runs via defer on return.
	return nil
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
