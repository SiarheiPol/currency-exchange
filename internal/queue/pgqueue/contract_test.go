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

// TestJobQueueContract_PgQueue runs the shared queue contract suite against
// the Postgres-backed implementation.
//
// Note: Reserve/Complete/Reschedule/Fail subtests are expected to fail until
// the pgqueue implementation lands (C4/C5). The Enqueue subtests are the
// primary target for C3.
func TestJobQueueContract_PgQueue(t *testing.T) {
	queuetest.RunJobQueueContractTests(t, func(t *testing.T, clk clock.Clock) queue.JobQueue {
		pool := pgtest.NewDB(t)
		return pgqueue.New(pool, clk)
	})
}
