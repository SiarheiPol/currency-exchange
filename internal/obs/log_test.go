package obs_test

import (
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"currency-exchange/internal/obs"
)

// TestLogger_NoLogger_ReturnsSlogDefault asserts that Logger returns a pointer
// equal to slog.Default() when no logger has been stored in the context.
func TestLogger_NoLogger_ReturnsSlogDefault(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	got := obs.Logger(ctx)
	assert.Same(t, slog.Default(), got)
}

// TestLogger_WithLogger_ReturnsSameLogger asserts that Logger returns the exact
// same pointer that was stored via WithLogger.
func TestLogger_WithLogger_ReturnsSameLogger(t *testing.T) {
	t.Parallel()

	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx := obs.WithLogger(context.Background(), custom)
	got := obs.Logger(ctx)
	assert.Same(t, custom, got)
}

// TestLogger_WithLogger_DoesNotMutateParentCtx asserts that the parent context
// is unaffected after deriving a child with WithLogger.
func TestLogger_WithLogger_DoesNotMutateParentCtx(t *testing.T) {
	t.Parallel()

	parent := context.Background()
	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	_ = obs.WithLogger(parent, custom)

	got := obs.Logger(parent)
	assert.Same(t, slog.Default(), got)
}

// TestLogger_NilLogger_ReturnsDefault asserts that passing nil to WithLogger
// results in Logger returning slog.Default() with no panic.
func TestLogger_NilLogger_ReturnsDefault(t *testing.T) {
	t.Parallel()

	ctx := obs.WithLogger(context.Background(), nil)
	got := obs.Logger(ctx)
	assert.Same(t, slog.Default(), got)
}

// TestWithLogger_CancelledCtx_StillCarriesLogger asserts that cancelling a
// context does not lose the logger value stored in it.
func TestWithLogger_CancelledCtx_StillCarriesLogger(t *testing.T) {
	t.Parallel()

	custom := slog.New(slog.NewTextHandler(io.Discard, nil))
	ctx, cancel := context.WithCancel(context.Background())
	ctx = obs.WithLogger(ctx, custom)
	cancel()

	got := obs.Logger(ctx)
	assert.Same(t, custom, got)
}
