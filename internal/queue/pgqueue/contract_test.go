//go:build integration

package pgqueue_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/shopspring/decimal"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/pgqueue"
	"currency-exchange/internal/queue/queuetest"
	"currency-exchange/internal/testhelper/pgtest"
)

// TestJobQueueContract_PgQueue runs the shared queue contract suite against
// the Postgres-backed implementation.
func TestJobQueueContract_PgQueue(t *testing.T) {
	queuetest.RunJobQueueContractTests(t,
		func(t *testing.T, clk clock.Clock) (queue.JobQueue, queuetest.ReadBackFn) {
			pool := pgtest.NewDB(t)
			q := pgqueue.New(pool, clk)
			readBack := func(ctx context.Context, id queue.JobID) (decimal.Decimal, time.Time, string, error) {
				var (
					priceStr       string
					quoteUpdatedAt time.Time
					status         string
				)
				err := pool.QueryRow(ctx,
					`SELECT price::text, quote_updated_at, status
					   FROM quote_jobs
					  WHERE id = $1`,
					string(id),
				).Scan(&priceStr, &quoteUpdatedAt, &status)
				if err != nil {
					return decimal.Zero, time.Time{}, "", fmt.Errorf("readBack: %w", err)
				}
				price, pErr := decimal.NewFromString(priceStr)
				if pErr != nil {
					return decimal.Zero, time.Time{}, "", fmt.Errorf("readBack: parse price: %w", pErr)
				}
				return price, quoteUpdatedAt, status, nil
			}
			return q, readBack
		},
	)
}
