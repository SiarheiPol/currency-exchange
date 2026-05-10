package queue_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"currency-exchange/internal/queue"
)

// TestDedupKey_DeterministicCaseInsensitiveBucketAligned covers three
// properties of queue.DedupKey:
//
//   - Case-insensitive: upper and lower inputs hash identically.
//   - Within-bucket stability: two timestamps in the same bucket window produce
//     the same key.
//   - Cross-bucket uniqueness: timestamps in adjacent buckets produce different
//     keys.
//
// Uses t0=Unix(960,0) (already floor-aligned to d=60s) so t0 and t0+d/2 fall
// in the same bucket [960, 1020), and t0+d crosses into the next bucket [1020, 1080).
func TestDedupKey_DeterministicCaseInsensitiveBucketAligned(t *testing.T) {
	t.Parallel()

	t0 := time.Unix(960, 0)
	d := 60 * time.Second

	t.Run("CaseInsensitive", func(t *testing.T) {
		t.Parallel()
		lower := queue.DedupKey("usd", "eur", t0, d)
		upper := queue.DedupKey("USD", "EUR", t0, d)
		require.Equal(t, lower, upper,
			"DedupKey must be case-insensitive: lower=%q upper=%q", lower, upper)
		require.Len(t, upper, 64,
			"sha256 hex digest must be 64 characters long")
	})

	t.Run("WithinBucketStable", func(t *testing.T) {
		t.Parallel()
		// t0 and t0+d/2 fall in the same bucket window.
		k1 := queue.DedupKey("USD", "EUR", t0, d)
		k2 := queue.DedupKey("USD", "EUR", t0.Add(d/2), d)
		require.Equal(t, k1, k2,
			"timestamps within the same bucket must produce the same key")
	})

	t.Run("AcrossBucketBoundaryDiffers", func(t *testing.T) {
		t.Parallel()
		// t0 and t0+d fall in adjacent bucket windows.
		k1 := queue.DedupKey("USD", "EUR", t0, d)
		k2 := queue.DedupKey("USD", "EUR", t0.Add(d), d)
		require.NotEqual(t, k1, k2,
			"timestamps in adjacent buckets must produce different keys")
	})
}
