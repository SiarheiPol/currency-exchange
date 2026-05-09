package obs_test

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"currency-exchange/internal/obs"
)

func TestLogger_FallsBackToSlogDefault(t *testing.T) {
	// Not parallel because it mutates slog.Default()
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	ctx := context.Background()
	obs.Logger(ctx).Info("fallback")

	assert.Contains(t, buf.String(), `"msg":"fallback"`)
}

func TestLogger_UsesStoredLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	stored := slog.New(slog.NewJSONHandler(&buf, nil))
	ctx := obs.WithLogger(context.Background(), stored)

	obs.Logger(ctx).Info("stored")

	assert.Contains(t, buf.String(), `"msg":"stored"`)
	assert.NotContains(t, buf.String(), "request_id")
}

func TestLogger_WithLogger_DoesNotMutateParentCtx(t *testing.T) {
	// Not parallel because it relies on slog.Default() check
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	parent := context.Background()
	_ = obs.WithLogger(parent, slog.New(slog.NewTextHandler(io.Discard, nil)))

	obs.Logger(parent).Info("parent")
	assert.Contains(t, buf.String(), `"msg":"parent"`)
}

func TestLogger_NilLogger_FallsBackToDefault(t *testing.T) {
	// Not parallel because it mutates slog.Default()
	var buf bytes.Buffer
	original := slog.Default()
	slog.SetDefault(slog.New(slog.NewJSONHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(original) })

	ctx := obs.WithLogger(context.Background(), nil)
	obs.Logger(ctx).Info("nil_fallback")

	assert.Contains(t, buf.String(), `"msg":"nil_fallback"`)
}

func TestWithLogger_CancelledCtx_StillCarriesLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	custom := slog.New(slog.NewJSONHandler(&buf, nil))
	ctx, cancel := context.WithCancel(context.Background())
	ctx = obs.WithLogger(ctx, custom)
	cancel()

	obs.Logger(ctx).Info("cancelled")

	assert.Contains(t, buf.String(), `"msg":"cancelled"`)
}
