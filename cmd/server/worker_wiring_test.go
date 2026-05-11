package main

import (
	"context"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/idgen"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/memqueue"
	"currency-exchange/internal/quoterepo/memquoterepo"
	"currency-exchange/internal/ratesprovider"
	"currency-exchange/internal/ratesprovider/fake"
	"currency-exchange/internal/worker"
)

// Config: REFRESH_MAX_LATENCY_MS=2000, default whitelist [USD,EUR,MXN],
// WORKER_COUNT=1 → PollInterval=1s, BatchSize=6. Enqueues 6 distinct-pair
// jobs and asserts a single FetchPairs call carries all 6 pairs — proving
// derived values flow from Load() through run() into worker.New.
func TestWorkerWiring_DerivedConfigReachesWorker(t *testing.T) {
	// NOT t.Parallel: uses t.Setenv which mutates process environment.

	// Required env for Load() to succeed.
	t.Setenv("DB_DSN", "postgres://test")
	t.Setenv("PROVIDER_API_KEY", "k")
	// Pin the SLA/whitelist/worker fields so the derived values are deterministic.
	t.Setenv("REFRESH_MAX_LATENCY_MS", "2000")
	t.Setenv("WHITELIST_CURRENCIES", "USD,EUR,MXN")
	t.Setenv("WORKER_COUNT", "1")
	// Silence unrelated config knobs.
	t.Setenv("SCHEDULER_TICK_SECONDS", "")
	t.Setenv("COALESCING_WINDOW_SECONDS", "")

	cfg, err := Load()
	require.NoError(t, err)

	// Derived values asserted here so the test fails with a clear message if
	// Load() changes the derivation formula before worker.New is updated.
	require.Equal(t, 1*time.Second, cfg.PollInterval,
		"derived PollInterval must be 1s for REFRESH_MAX_LATENCY_MS=2000")
	require.Equal(t, 6, cfg.BatchSize,
		"derived BatchSize must be 6 for 3-currency whitelist and WORKER_COUNT=1")

	t0 := time.Date(2026, 5, 10, 12, 0, 0, 0, time.UTC)
	clk := clock.NewFake(t0)
	q := memqueue.New(clk)
	repo := memquoterepo.New()

	// Build a fake provider that succeeds for all 6 pairs of [USD, EUR, MXN].
	currencies := []string{"USD", "EUR", "MXN"}
	allPairs := make(map[ratesprovider.Pair]ratesprovider.Quote)
	for _, base := range currencies {
		for _, quote := range currencies {
			if base == quote {
				continue
			}
			p := ratesprovider.Pair{Base: base, Quote: quote}
			allPairs[p] = ratesprovider.Quote{
				Pair:  p,
				Price: decimal.NewFromFloat(1.0),
			}
		}
	}
	provider := &fake.Fake{
		Clock:  clk,
		Quotes: allPairs,
	}

	// Enqueue all 6 pairs — same set the scheduler would produce.
	gen := idgen.NewSeq()
	for p := range allPairs {
		enqueueJobWiring(t, q, queue.Job{
			ID:        queue.JobID(gen.NewID()),
			Base:      p.Base,
			Quote:     p.Quote,
			NextRunAt: clk.Now(),
			Source:    "scheduler",
		})
	}

	// Construct the worker exactly as run() in main.go will, using derived cfg.
	w := worker.New(q, q, provider, repo, clk,
		worker.WithPollInterval(cfg.PollInterval),
		worker.WithBatchSize(cfg.BatchSize),
		worker.WithLeaseTime(1*time.Second),
	)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_ = w.Run(ctx)

	require.Equal(t, 1, provider.Calls,
		"worker must call FetchPairs exactly once for the full 6-job batch")
	require.Equal(t, 6, len(provider.LastPairs),
		"FetchPairs must receive all 6 pairs in the single call")
}

// enqueueJobWiring is a local helper mirroring enqueueJob from worker_test.go
// for use within the cmd/server package test.
func enqueueJobWiring(t *testing.T, q queue.JobQueue, job queue.Job) queue.JobID {
	t.Helper()
	id, _, err := q.Enqueue(context.Background(), job)
	require.NoError(t, err)
	return id
}
