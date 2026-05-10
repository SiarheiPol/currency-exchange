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

	obs.LogJobFailed(ctx, "j-4", "USD", "MXN", 5, errors.New("upstream timeout"))

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	assert.Equal(t, obs.EvJobFailed, rec["msg"])
	assert.Equal(t, "upstream timeout", rec["error"])
	assert.Equal(t, "ERROR", rec["level"])
	assert.Equal(t, float64(5), rec["attempts"])
	assert.Equal(t, "USD", rec["base"])
	assert.Equal(t, "MXN", rec["quote"])
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

// TestLogJobCompleted_FieldsPresent asserts that LogJobCompleted emits the
// expected structured fields with correct types and values.
func TestLogJobCompleted_FieldsPresent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	obs.LogJobCompleted(ctx, "j-1", "EUR", "USD", 214*time.Millisecond)

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	assert.Equal(t, obs.EvJobCompleted, rec["msg"])
	assert.Equal(t, "j-1", rec["job_id"])
	assert.Equal(t, "EUR", rec["base"])
	assert.Equal(t, "USD", rec["quote"])
	assert.Equal(t, float64(214), rec["duration_ms"])
	assert.Equal(t, "INFO", rec["level"])
}

// TestLogJobReserved_FieldsPresent asserts that LogJobReserved emits level INFO
// with job_id, base, and quote attributes.
func TestLogJobReserved_FieldsPresent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	obs.LogJobReserved(ctx, "j-2", "GBP", "USD")

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	assert.Equal(t, obs.EvJobReserved, rec["msg"])
	assert.Equal(t, "j-2", rec["job_id"])
	assert.Equal(t, "GBP", rec["base"])
	assert.Equal(t, "USD", rec["quote"])
	assert.Equal(t, "INFO", rec["level"])
}

// TestLogJobRescheduled_FieldsPresent asserts that LogJobRescheduled emits
// level WARN with job_id, base, quote, attempts, and next_delay_ms attributes.
func TestLogJobRescheduled_FieldsPresent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	obs.LogJobRescheduled(ctx, "j-3", "USD", "JPY", 2, 500*time.Millisecond)

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	assert.Equal(t, obs.EvJobRescheduled, rec["msg"])
	assert.Equal(t, "j-3", rec["job_id"])
	assert.Equal(t, "USD", rec["base"])
	assert.Equal(t, "JPY", rec["quote"])
	assert.Equal(t, float64(2), rec["attempts"])
	assert.Equal(t, float64(500), rec["next_delay_ms"])
	assert.Equal(t, "WARN", rec["level"])
}

// TestLogCoalescingCollapsed_FieldsPresent asserts that LogCoalescingCollapsed
// emits level DEBUG with job_id, base, and quote attributes.
func TestLogCoalescingCollapsed_FieldsPresent(t *testing.T) {
	t.Parallel()

	var buf bytes.Buffer
	logger := slog.New(slog.NewJSONHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug}))
	ctx := obs.WithLogger(context.Background(), logger)

	obs.LogCoalescingCollapsed(ctx, "j-5", "EUR", "CHF")

	var rec map[string]any
	assert.NoError(t, json.Unmarshal(buf.Bytes(), &rec))

	assert.Equal(t, obs.EvCoalescingCollapsed, rec["msg"])
	assert.Equal(t, "j-5", rec["job_id"])
	assert.Equal(t, "EUR", rec["base"])
	assert.Equal(t, "CHF", rec["quote"])
	assert.Equal(t, "DEBUG", rec["level"])
}
