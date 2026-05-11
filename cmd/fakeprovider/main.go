// Package main runs the fake rates provider HTTP server. Used for local
// development and reviewer end-to-end testing without burning real upstream
// quota. See docs/discussions/implementation-roadmap.md Stage 5.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"currency-exchange/internal/clock"
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

	cfg, err := Load()
	if err != nil {
		return err
	}

	rootCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	state := NewState(cfg.Seed, cfg.MonthlyQuota, cfg.CadenceSeconds, clock.New())
	latencyCfg := LatencyConfig{
		MinMS: cfg.LatencyMinMS,
		MaxMS: cfg.LatencyMaxMS,
		RNG:   rand.New(rand.NewPCG(cfg.Seed+1, 0)),
	}
	server := NewServer(state, cfg.AccessKey, clock.New(), latencyCfg)

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           server,
		ReadHeaderTimeout: 5 * time.Second,
	}

	serverErr := make(chan error, 1)
	go func() {
		obs.Logger(rootCtx).Info(obs.EvFakeproviderStarted, "addr", cfg.Addr, "seed", cfg.Seed, "quota", cfg.MonthlyQuota)
		obs.Logger(rootCtx).Info(obs.EvFakeproviderConfig,
			"cadence", fmt.Sprintf("%ds", cfg.CadenceSeconds),
			"latency", fmt.Sprintf("[%d,%d]ms", cfg.LatencyMinMS, cfg.LatencyMaxMS),
			"quota", cfg.MonthlyQuota,
		)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	select {
	case <-rootCtx.Done():
		obs.Logger(context.WithoutCancel(rootCtx)).Info(obs.EvShutdownSignalReceived)
	case err := <-serverErr:
		return fmt.Errorf("http server: %w", err)
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}
