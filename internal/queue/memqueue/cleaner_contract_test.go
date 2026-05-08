package memqueue_test

import (
	"testing"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/memqueue"
	"currency-exchange/internal/queue/queuetest"
)

func TestMemQueueCleanerContract(t *testing.T) {
	queuetest.RunCleanerContractTests(t, func(t *testing.T, clk clock.Clock) (queue.JobQueue, queue.Cleaner) {
		t.Helper()
		q := memqueue.New(clk)
		return q, q
	})
}
