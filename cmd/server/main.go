// Package main runs the currency quote service HTTP server.
//
// This binary is intentionally minimal: it mounts the observability and
// liveness endpoints that exist today and exits cleanly on SIGINT/SIGTERM.
// Database pool, worker, scheduler, and /readyz wiring will be added as the
// corresponding components land (see docs/discussions/implementation-roadmap.md
// Stage 2 retrofit and Stage 5 packaging items).
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

	"currency-exchange/internal/health"
	"currency-exchange/internal/httpmw"
	"currency-exchange/internal/obs"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, nil)))

	addr := envOr("HTTP_ADDR", ":8080")

	mux := http.NewServeMux()
	mux.Handle("GET /healthz", health.Healthz())
	mux.Handle("GET /metrics", obs.MetricsHandler())
	// GET /readyz is deliberately not mounted yet: it requires a Pinger
	// (DB pool) and a Heartbeater (running worker). Both arrive together
	// with the queue retrofit + worker bootstrap commit.

	srv := &http.Server{
		Addr:              addr,
		Handler:           httpmw.RequestID(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	serverErr := make(chan error, 1)
	go func() {
		obs.Logger(ctx).Info("http server starting", "addr", addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case <-ctx.Done():
		obs.Logger(context.Background()).Info("http server shutting down")
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("http shutdown: %w", err)
	}
	return nil
}

func envOr(key, def string) string {
	if v, ok := os.LookupEnv(key); ok && v != "" {
		return v
	}
	return def
}
