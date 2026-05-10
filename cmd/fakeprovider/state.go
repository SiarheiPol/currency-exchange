package main

import (
	"math/rand/v2"
	"sync"
)

// Pair identifies a directional currency pair for the fake provider.
type Pair struct {
	Base, Quote string
}

// State holds the in-memory rate state and quota counter for the fake provider.
type State struct {
	mu           sync.Mutex
	rng          *rand.Rand
	monthlyQuota int
	rates        map[Pair]float64
}

// NewState returns a new State seeded with the given seed and quota.
func NewState(seed uint64, monthlyQuota int) *State {
	s := &State{
		rng:          rand.New(rand.NewPCG(seed, 0)),
		monthlyQuota: monthlyQuota,
		rates:        make(map[Pair]float64),
	}
	// Hardcoded initial rates for whitelist pairs.
	s.rates[Pair{"USD", "EUR"}] = 0.85
	s.rates[Pair{"EUR", "USD"}] = 1.18
	s.rates[Pair{"USD", "MXN"}] = 17.18
	s.rates[Pair{"MXN", "USD"}] = 0.058
	s.rates[Pair{"EUR", "MXN"}] = 20.26
	s.rates[Pair{"MXN", "EUR"}] = 0.049
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

// Rate returns the current rate for p, advancing the random walk. Returns
// (0, false) for self-pairs; (rate, true) for all other pairs.
func (s *State) Rate(p Pair) (float64, bool) {
	if p.Base == p.Quote {
		return 0, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.rateLocked(p), true
}

// rateLocked advances the random walk for p and returns the new value.
// Caller must hold s.mu.
func (s *State) rateLocked(p Pair) float64 {
	rate, ok := s.rates[p]
	if !ok {
		// Uniform random in [0.5, 20.0] for unknown pairs.
		rate = 0.5 + s.rng.Float64()*19.5
	}
	// Random walk: ±5%, clamped > 0.
	factor := 1 + s.rng.Float64()*0.1 - 0.05
	rate *= factor
	if rate <= 0 {
		rate = 0.0001
	}
	s.rates[p] = rate
	return rate
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
