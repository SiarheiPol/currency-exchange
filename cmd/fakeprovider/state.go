package main

import (
	"math/rand/v2"
	"sync"

	"currency-exchange/internal/clock"
)

// Pair identifies a directional currency pair for the fake provider.
type Pair struct {
	Base, Quote string
}

// pairState holds the current rate and the cadence window in which it was last
// advanced. lastWindow is initialised to -1 (never advanced) so that the first
// call in any window — including window 0 (Unix epoch) — always advances the walk.
type pairState struct {
	rate       float64
	lastWindow int64 // -1 = never advanced
}

// State holds the in-memory rate state and quota counter for the fake provider.
type State struct {
	mu             sync.Mutex
	rng            *rand.Rand
	monthlyQuota   int
	rates          map[Pair]pairState
	cadenceSeconds int64
	clk            clock.Clock
}

// NewState returns a new State seeded with the given seed, quota, and cadence.
// When cadenceSeconds is 0 the rate walk advances on every call (previous
// behaviour). When cadenceSeconds > 0 the walk advances at most once per
// cadence window.
func NewState(seed uint64, monthlyQuota int, cadenceSeconds int64, clk clock.Clock) *State {
	s := &State{
		rng:            rand.New(rand.NewPCG(seed, 0)),
		monthlyQuota:   monthlyQuota,
		rates:          make(map[Pair]pairState),
		cadenceSeconds: cadenceSeconds,
		clk:            clk,
	}
	// Hardcoded initial rates for whitelist pairs. lastWindow=-1 means "never
	// advanced", so the first Rate call within any window will run the walk.
	s.rates[Pair{"USD", "EUR"}] = pairState{rate: 0.85, lastWindow: -1}
	s.rates[Pair{"EUR", "USD"}] = pairState{rate: 1.18, lastWindow: -1}
	s.rates[Pair{"USD", "MXN"}] = pairState{rate: 17.18, lastWindow: -1}
	s.rates[Pair{"MXN", "USD"}] = pairState{rate: 0.058, lastWindow: -1}
	s.rates[Pair{"EUR", "MXN"}] = pairState{rate: 20.26, lastWindow: -1}
	s.rates[Pair{"MXN", "EUR"}] = pairState{rate: 0.049, lastWindow: -1}
	return s
}

// Consume decrements the monthly quota and returns true if a quota unit was
// available. Once exhausted, every subsequent call returns false.
func (s *State) Consume() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.monthlyQuota <= 0 {
		return false
	}
	s.monthlyQuota--
	return true
}

// Rate returns the current rate for p, advancing the random walk according to
// the cadence policy. Returns (0, false) for self-pairs; (rate, true) otherwise.
func (s *State) Rate(p Pair) (float64, bool) {
	if p.Base == p.Quote {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rateLocked(p), true
}

// rateLocked advances the random walk for p according to the cadence policy and
// returns the new (or cached) value. Caller must hold s.mu.
func (s *State) rateLocked(p Pair) float64 {
	ps, ok := s.rates[p]
	if !ok {
		// Unknown pair: uniform random in [0.5, 20.0]; treat as never advanced.
		ps = pairState{rate: 0.5 + s.rng.Float64()*19.5, lastWindow: -1}
	}

	if s.cadenceSeconds > 0 {
		windowKey := s.clk.Now().Unix() / s.cadenceSeconds
		if ps.lastWindow == windowKey {
			// Already advanced in this window — return cached rate.
			s.rates[p] = ps
			return ps.rate
		}
		// Advance the walk (first call in this window, or never-advanced sentinel).
		ps.rate = s.advanceRate(ps.rate)
		ps.lastWindow = windowKey
	} else {
		// cadence=0: advance on every call.
		ps.rate = s.advanceRate(ps.rate)
	}

	s.rates[p] = ps
	return ps.rate
}

// advanceRate applies one step of the random walk to rate and returns the result.
func (s *State) advanceRate(rate float64) float64 {
	factor := 1 + s.rng.Float64()*0.1 - 0.05
	rate *= factor
	if rate <= 0 {
		rate = 0.0001
	}
	return rate
}

// WindowTimestamp returns the timestamp the server should report for the current
// window. When cadenceSeconds=0 it returns the current Unix time. Otherwise it
// returns the start of the current window (windowKey * cadenceSeconds).
func (s *State) WindowTimestamp() int64 {
	nowUnix := s.clk.Now().Unix()
	if s.cadenceSeconds <= 0 {
		return nowUnix
	}
	return (nowUnix / s.cadenceSeconds) * s.cadenceSeconds
}

// AllRatesForBase returns a map of concatenated pair keys (e.g. "USDEUR") to
// rates for the given base and all requested targets, excluding self-pairs.
func (s *State) AllRatesForBase(base string, targets []string) map[string]float64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]float64)
	for _, t := range targets {
		if t == base {
			continue
		}
		rate := s.rateLocked(Pair{Base: base, Quote: t})
		out[base+t] = rate
	}
	return out
}
