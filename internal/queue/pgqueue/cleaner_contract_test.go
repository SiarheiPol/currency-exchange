//go:build integration

package pgqueue_test

import (
	"testing"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/pgqueue"
	"currency-exchange/internal/queue/queuetest"
	"currency-exchange/internal/testhelper/pgtest"
)

func TestPgQueueCleanerContract(t *testing.T) {
	queuetest.RunCleanerContractTests(t, func(t *testing.T, clk clock.Clock) (queue.JobQueue, queue.Cleaner) {
		t.Helper()
		pool := pgtest.NewDB(t)
		q := pgqueue.New(pool, clk)
		return q, q
	})
}
