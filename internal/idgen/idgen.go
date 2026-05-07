// Package idgen provides an IDGenerator interface and two implementations:
// UUIDGenerator backed by github.com/google/uuid, and SeqIDGenerator for
// deterministic test IDs.
package idgen

import (
	"fmt"
	"sync"

	"github.com/google/uuid"
)

// IDGenerator returns a fresh ID string on each call.
type IDGenerator interface {
	NewID() string
}

// UUIDGenerator returns a random v4 UUID in canonical hyphenated form.
type UUIDGenerator struct{}

// NewID returns a v4 UUID string.
func (UUIDGenerator) NewID() string {
	return uuid.New().String()
}

// New returns an IDGenerator backed by UUIDGenerator.
func New() IDGenerator { return UUIDGenerator{} }

// SeqIDGenerator returns deterministic, sequential IDs of the form "id-NNNNNN".
// Safe for concurrent use.
type SeqIDGenerator struct {
	mu      sync.Mutex
	counter int
}

// NewSeq returns a fresh SeqIDGenerator with counter at zero.
func NewSeq() *SeqIDGenerator {
	return &SeqIDGenerator{}
}

// NewID returns the next sequential ID string.
func (g *SeqIDGenerator) NewID() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.counter++
	return fmt.Sprintf("id-%06d", g.counter)
}
