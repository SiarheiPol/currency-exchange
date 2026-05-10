// Package memquoterepo provides an in-memory implementation of
// quoterepo.QuoteRepo used as a test double.
package memquoterepo

import (
	"context"
	"sync"

	"currency-exchange/internal/quoterepo"
	"currency-exchange/internal/ratesprovider"
)

// Compile-time assertion that Repo satisfies QuoteRepo.
var _ quoterepo.QuoteRepo = (*Repo)(nil)

// Repo is a thread-safe in-memory implementation of quoterepo.QuoteRepo.
type Repo struct {
	mu     sync.Mutex
	quotes map[ratesprovider.Pair]ratesprovider.Quote
}

// New returns a new empty Repo.
func New() *Repo {
	return &Repo{
		quotes: make(map[ratesprovider.Pair]ratesprovider.Quote),
	}
}

// UpsertBatch inserts or overwrites the given quotes keyed by their Pair.
func (r *Repo) UpsertBatch(_ context.Context, qs []ratesprovider.Quote) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, q := range qs {
		r.quotes[q.Pair] = q
	}
	return nil
}

// Get returns the stored quote for the given pair, or false if absent.
func (r *Repo) Get(p ratesprovider.Pair) (ratesprovider.Quote, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	q, ok := r.quotes[p]
	return q, ok
}

// Len returns the number of quotes currently stored.
func (r *Repo) Len() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.quotes)
}
