package clock

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Compile-time interface assertions: both FakeClock and RealClock must satisfy Clock.
var _ Clock = (*FakeClock)(nil)
var _ Clock = RealClock{}

// TestRealClock_NowReturnsApproximateWallTime confirms that RealClock.Now() returns
// a time that is within the wall-clock interval measured around the call.
func TestRealClock_NowReturnsApproximateWallTime(t *testing.T) {
	t.Parallel()

	c := New()

	before := time.Now()
	got := c.Now()
	after := time.Now()

	require.False(t, got.Before(before), "Now() must not be before the pre-call measurement")
	require.False(t, got.After(after), "Now() must not be after the post-call measurement")
	require.Less(t, after.Sub(before), 100*time.Millisecond, "measurement window must be <100ms")
}

// TestFakeClock_AdvanceMovesNow confirms that successive Advance calls accumulate
// and are reflected in Now().
func TestFakeClock_AdvanceMovesNow(t *testing.T) {
	t.Parallel()

	initial := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	c := NewFake(initial)

	c.Advance(5 * time.Second)
	require.Equal(t, initial.Add(5*time.Second), c.Now())

	c.Advance(2 * time.Hour)
	require.Equal(t, initial.Add(5*time.Second+2*time.Hour), c.Now())
}

// TestFakeClock_SetReplacesNow confirms that Set replaces the current time
// with the given value, independent of what Now() returned before.
func TestFakeClock_SetReplacesNow(t *testing.T) {
	t.Parallel()

	initial := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	target := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	c := NewFake(initial)

	c.Set(target)

	require.Equal(t, target, c.Now())
}

// TestFakeClock_ConcurrentAdvanceAndNow confirms that FakeClock is safe for
// concurrent use.  10 goroutines each call Advance(1ms) 100 times; the test
// goroutine calls Now() 1000 times in parallel for maximal interleaving.
// After all advances the total must equal exactly 1000ms.
//
// Note: running with -race makes the missing-mutex case fail deterministically.
// Without -race, the assertion on the final value still catches lost updates
// probabilistically.
func TestFakeClock_ConcurrentAdvanceAndNow(t *testing.T) {
	t.Parallel()

	initial := time.Date(2026, 5, 6, 12, 0, 0, 0, time.UTC)
	c := NewFake(initial)

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				c.Advance(1 * time.Millisecond)
			}
		}()
	}

	// Interleave Now() calls with the concurrent Advance calls.
	for i := 0; i < 1000; i++ {
		_ = c.Now()
	}

	wg.Wait()

	require.Equal(t, initial.Add(1000*time.Millisecond), c.Now())
}
