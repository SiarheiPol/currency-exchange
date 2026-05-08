// Package backoff provides exponential retry delay computation with full
// jitter, used by the worker when rescheduling jobs after transient upstream
// failures.
package backoff

import (
	"math/rand/v2"
	"time"
)

const (
	baseDelay         = time.Second
	maxBackoff        = 60 * time.Second
	saturationAttempt = 6 // 1s << 6 = 64s, exceeds maxBackoff
)

// Compute returns a uniformly random backoff duration in [0, window),
// where window = min(baseDelay << attempt, maxBackoff).
//
// The full-jitter strategy spreads concurrent retries across the window to
// prevent thundering-herd stampedes against a recovering upstream. Negative
// attempt values are clamped to zero.
func Compute(attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}

	var window time.Duration
	if attempt >= saturationAttempt {
		window = maxBackoff
	} else {
		window = baseDelay << attempt
	}

	return time.Duration(rand.Int64N(int64(window)))
}
