package memqueue_test

import (
	"testing"

	"currency-exchange/internal/clock"
	"currency-exchange/internal/queue"
	"currency-exchange/internal/queue/memqueue"
	"currency-exchange/internal/queue/queuetest"
)

// TestJobQueueContract_MemQueue runs the shared queue contract suite against
// the in-memory implementation.
func TestJobQueueContract_MemQueue(t *testing.T) {
	queuetest.RunJobQueueContractTests(t,
		func(t *testing.T, clk clock.Clock) (queue.JobQueue, queuetest.ReadBackFn) {
			q := memqueue.New(clk)
			return q, q.ReadBack
		},
	)
}
