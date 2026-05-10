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

// TestLogJobFailed_ErrorField asserts that LogJobFailed emits level ERROR
// and carries the error text and attempts count.
func TestLogJobFailed_ErrorField(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	obs.LogJobFailed(ctx, "j-4", "USD", "MXN", 5, errors.New("upstream timeout"))

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	assert.Equal(t, "ERROR", rec["level"])
	assert.Equal(t, "upstream timeout", rec["error"])
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
	obs.LogJobCompleted(ctx, "j-1", "EUR", "USD", 100*time.Millisecond)

	line := buf.Bytes()
	assert.NotEmpty(t, line, "default logger must have received a log line")

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(line, &rec))
	assert.Equal(t, obs.EvJobCompleted, rec["msg"])
}

// TestLogJobRescheduled_FieldsPresent asserts that LogJobRescheduled emits
// level WARN and converts the delay duration to milliseconds in next_delay_ms.
func TestLogJobRescheduled_FieldsPresent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	obs.LogJobRescheduled(ctx, "j-3", "USD", "JPY", 2, 500*time.Millisecond)

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	assert.Equal(t, "WARN", rec["level"])
	assert.Equal(t, float64(500), rec["next_delay_ms"])
}
