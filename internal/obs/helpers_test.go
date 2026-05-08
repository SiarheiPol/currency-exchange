package obs_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"

	"currency-exchange/internal/obs"
)

// TestLogUpstreamCallFinished_LevelByError is table-driven.
// - err=nil  → level "INFO", no "error" key in JSON
// - err!=nil → level "WARN", rec["error"] == err.Error()
func TestLogUpstreamCallFinished_LevelByError(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name          string
		err           error
		wantLevel     string
		wantErrorKey  bool
		wantErrorText string
	}{
		{
			name:         "nil error logs INFO without error field",
			err:          nil,
			wantLevel:    "INFO",
			wantErrorKey: false,
		},
		{
			name:          "non-nil error logs WARN with error field",
			err:           errors.New("quota exceeded"),
			wantLevel:     "WARN",
			wantErrorKey:  true,
			wantErrorText: "quota exceeded",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			var buf bytes.Buffer
			logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
			ctx := obs.WithLogger(context.Background(), logger)

			obs.LogUpstreamCallFinished(ctx, "test-provider", []string{"USD", "EUR"}, 42*time.Millisecond, tc.err)

			var rec map[string]any
			assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

			assert.Equal(t, tc.wantLevel, rec["level"])

			if tc.wantErrorKey {
				assert.Equal(t, tc.wantErrorText, rec["error"])
			} else {
				assert.NotContains(t, rec, "error", "nil-error call must not emit an error field")
			}
		})
	}
}

// TestLogJobFailed_ErrorField asserts that LogJobFailed emits level ERROR,
// sets msg == obs.EvJobFailed, and carries the error text and attempts count.
func TestLogJobFailed_ErrorField(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	obs.LogJobFailed(ctx, "j-4", "USD", 5, errors.New("upstream timeout"))

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	assert.Equal(t, obs.EvJobFailed, rec["msg"])
	assert.Equal(t, "upstream timeout", rec["error"])
	assert.Equal(t, "ERROR", rec["level"])
	assert.Equal(t, float64(5), rec["attempts"])
}

// TestHelpers_FallBackToDefaultLogger asserts that helpers use slog.Default()
// when no logger is stored in the context.
func TestHelpers_FallBackToDefaultLogger(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	fallback := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	prev := slog.Default()
	slog.SetDefault(fallback)
	defer slog.SetDefault(prev)

	ctx := context.Background() // no logger stored
	obs.LogJobCompleted(ctx, "j-1", "EUR", 100*time.Millisecond)

	line := buf.Bytes()
	assert.NotEmpty(t, line, "default logger must have received a log line")

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(line, &rec))
	assert.Equal(t, obs.EvJobCompleted, rec["msg"])
}

// TestLogJobCompleted_FieldsPresent asserts that LogJobCompleted emits the
// expected structured fields with correct types and values.
func TestLogJobCompleted_FieldsPresent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	obs.LogJobCompleted(ctx, "j-1", "EUR", 214*time.Millisecond)

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	assert.Equal(t, obs.EvJobCompleted, rec["msg"])
	assert.Equal(t, "j-1", rec["job_id"])
	assert.Equal(t, "EUR", rec["currency"])
	assert.Equal(t, float64(214), rec["duration_ms"])
	assert.Equal(t, "INFO", rec["level"])
}
