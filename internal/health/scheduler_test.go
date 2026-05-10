package health_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"currency-exchange/internal/health"
)

// stubSchedulerHeartbeater satisfies health.SchedulerHeartbeater for tests.
type stubSchedulerHeartbeater struct{ t time.Time }

func (s stubSchedulerHeartbeater) LastTick() time.Time { return s.t }

// TestSchedulerChecker_Statuses covers the three observable states of
// SchedulerChecker:
//
//   - Zero LastTick (no tick observed yet) → "degraded: no tick observed".
//   - Stale LastTick (beyond threshold) → "degraded: last tick <duration> ago".
//   - Fresh LastTick (within threshold) → "ok".
func TestSchedulerChecker_Statuses(t *testing.T) {
	t.Parallel()

	threshold := 10 * time.Second

	t.Run("NeverTicked", func(t *testing.T) {
		t.Parallel()
		hb := stubSchedulerHeartbeater{t: time.Time{}}
		c := health.SchedulerChecker(hb, threshold)
		got := c.Check(context.Background())
		if !strings.HasPrefix(got, "degraded") {
			t.Errorf("Check: got %q, want degraded:* prefix", got)
		}
		if !strings.Contains(got, "no tick observed") {
			t.Errorf("Check: got %q, want message to contain %q", got, "no tick observed")
		}
	})

	t.Run("StaleTick", func(t *testing.T) {
		t.Parallel()
		hb := stubSchedulerHeartbeater{t: time.Now().Add(-2 * threshold)}
		c := health.SchedulerChecker(hb, threshold)
		got := c.Check(context.Background())
		if !strings.HasPrefix(got, "degraded") {
			t.Errorf("Check: got %q, want degraded:* prefix", got)
		}
		if !strings.Contains(got, "last tick") {
			t.Errorf("Check: got %q, want message to contain %q", got, "last tick")
		}
	})

	t.Run("FreshTick", func(t *testing.T) {
		t.Parallel()
		hb := stubSchedulerHeartbeater{t: time.Now().Add(-threshold / 2)}
		c := health.SchedulerChecker(hb, threshold)
		got := c.Check(context.Background())
		if got != "ok" {
			t.Errorf("Check: got %q, want %q", got, "ok")
		}
	})
}
