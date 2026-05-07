// Package clock provides a Clock interface and two implementations: RealClock
// backed by time.Now(), and FakeClock for deterministic tests.
package clock

import (
	"sync"
	"time"
)

// Clock is the seam for time access. Production code reads time via Clock.Now()
// so unit tests can substitute a FakeClock and advance time deterministically.
type Clock interface {
	Now() time.Time
}

// RealClock returns the wall-clock time.
type RealClock struct{}

// Now returns time.Now().
func (RealClock) Now() time.Time { return time.Now() }

// New returns a Clock backed by RealClock.
func New() Clock { return RealClock{} }

// FakeClock is a Clock whose time is set explicitly via Advance or Set.
// Safe for concurrent use.
type FakeClock struct {
	mu  sync.Mutex
	now time.Time
}

// NewFake returns a FakeClock initialised at the given time.
func NewFake(initial time.Time) *FakeClock {
	return &FakeClock{now: initial}
}

// Now returns the current fake time.
func (c *FakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

// Advance moves the fake time forward by d.
func (c *FakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

// Set replaces the fake time with t.
func (c *FakeClock) Set(t time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = t
}
