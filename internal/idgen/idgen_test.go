package idgen

import (
	"sync"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Compile-time interface assertions: both UUIDGenerator and SeqIDGenerator must satisfy IDGenerator.
var _ IDGenerator = UUIDGenerator{}
var _ IDGenerator = (*SeqIDGenerator)(nil)

// TestUUIDGenerator_NewIDReturnsValidUUIDv4 confirms real generator wires to google/uuid v4.
func TestUUIDGenerator_NewIDReturnsValidUUIDv4(t *testing.T) {
	t.Parallel()

	g := New()
	id := g.NewID()

	require.Len(t, id, 36)

	parsed, err := uuid.Parse(id)
	require.NoError(t, err)
	require.Equal(t, uuid.Version(4), parsed.Version())
}

// TestSeqIDGenerator_IDsIncrementSequentially confirms monotonic increment with consistent zero-padded format.
func TestSeqIDGenerator_IDsIncrementSequentially(t *testing.T) {
	t.Parallel()

	g := NewSeq()
	got := make([]string, 0, 5)

	for i := 0; i < 5; i++ {
		got = append(got, g.NewID())
	}

	require.Equal(t, []string{
		"00000000-0000-0000-0000-000000000001",
		"00000000-0000-0000-0000-000000000002",
		"00000000-0000-0000-0000-000000000003",
		"00000000-0000-0000-0000-000000000004",
		"00000000-0000-0000-0000-000000000005",
	}, got)
}

// TestSeqIDGenerator_ConcurrentNewID confirms that SeqIDGenerator is safe for concurrent use.
// With -race, missing mutex on the counter is detected; without -race, lost increments still
// cause collisions detectable by the uniqueness check.
func TestSeqIDGenerator_ConcurrentNewID(t *testing.T) {
	t.Parallel()

	g := NewSeq()
	var (
		wg      sync.WaitGroup
		mu      sync.Mutex
		results []string
	)

	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				id := g.NewID()
				mu.Lock()
				results = append(results, id)
				mu.Unlock()
			}
		}()
	}

	wg.Wait()

	require.Len(t, results, 1000)

	set := make(map[string]struct{}, len(results))
	for _, r := range results {
		set[r] = struct{}{}
	}
	require.Len(t, set, 1000)
}
