package obs

import (
	"context"
	"log/slog"
	"time"
)

// LogJobReserved logs that a job was reserved from the queue.
func LogJobReserved(ctx context.Context, jobID, currency string) {
	Logger(ctx).LogAttrs(ctx, slog.LevelInfo, EvJobReserved,
		slog.String("job_id", jobID),
		slog.String("currency", currency),
	)
}

// LogJobCompleted logs that a job finished successfully.
func LogJobCompleted(ctx context.Context, jobID, currency string, duration time.Duration) {
	Logger(ctx).LogAttrs(ctx, slog.LevelInfo, EvJobCompleted,
		slog.String("job_id", jobID),
		slog.String("currency", currency),
		slog.Int64("duration_ms", duration.Milliseconds()),
	)
}

// LogJobRescheduled logs that a job was rescheduled for a future retry.
func LogJobRescheduled(ctx context.Context, jobID, currency string, attempts int, nextDelay time.Duration) {
	Logger(ctx).LogAttrs(ctx, slog.LevelWarn, EvJobRescheduled,
		slog.String("job_id", jobID),
		slog.String("currency", currency),
		slog.Int("attempts", attempts),
		slog.Int64("next_delay_ms", nextDelay.Milliseconds()),
	)
}

// LogJobFailed logs that a job has exhausted all retries and is permanently failed.
func LogJobFailed(ctx context.Context, jobID, currency string, attempts int, err error) {
	Logger(ctx).LogAttrs(ctx, slog.LevelError, EvJobFailed,
		slog.String("job_id", jobID),
		slog.String("currency", currency),
		slog.Int("attempts", attempts),
		slog.String("error", err.Error()),
	)
}

// LogSchedulerTick logs each scheduler tick with the currencies queued.
func LogSchedulerTick(ctx context.Context, currencies []string) {
	Logger(ctx).LogAttrs(ctx, slog.LevelDebug, EvSchedulerTick,
		slog.Any("currencies", currencies),
	)
}

// LogUpstreamCallStarted logs the beginning of an upstream provider call.
func LogUpstreamCallStarted(ctx context.Context, provider string, currencies []string) {
	Logger(ctx).LogAttrs(ctx, slog.LevelInfo, EvUpstreamCallStarted,
		slog.String("provider", provider),
		slog.Any("currencies", currencies),
	)
}

// LogUpstreamCallFinished logs the result of an upstream provider call.
// Level is INFO when err is nil, WARN when err is non-nil.
func LogUpstreamCallFinished(ctx context.Context, provider string, currencies []string, duration time.Duration, err error) {
	attrs := []slog.Attr{
		slog.String("provider", provider),
		slog.Any("currencies", currencies),
		slog.Int64("duration_ms", duration.Milliseconds()),
	}
	level := slog.LevelInfo
	if err != nil {
		level = slog.LevelWarn
		attrs = append(attrs, slog.String("error", err.Error()))
	}
	Logger(ctx).LogAttrs(ctx, level, EvUpstreamCallFinished, attrs...)
}

// LogHTTPRequestReceived logs an incoming HTTP request.
func LogHTTPRequestReceived(ctx context.Context, method, path string) {
	Logger(ctx).LogAttrs(ctx, slog.LevelInfo, EvHTTPRequestReceived,
		slog.String("method", method),
		slog.String("path", path),
	)
}

// LogHTTPRequestCompleted logs the completion of an HTTP request.
func LogHTTPRequestCompleted(ctx context.Context, method, path string, statusCode int, duration time.Duration) {
	Logger(ctx).LogAttrs(ctx, slog.LevelInfo, EvHTTPRequestCompleted,
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("status_code", statusCode),
		slog.Int64("duration_ms", duration.Milliseconds()),
	)
}

// LogCoalescingCollapsed logs that a duplicate request was collapsed into an in-flight request.
func LogCoalescingCollapsed(ctx context.Context, currency, dedupKey string) {
	Logger(ctx).LogAttrs(ctx, slog.LevelDebug, EvCoalescingCollapsed,
		slog.String("currency", currency),
		slog.String("dedup_key", dedupKey),
	)
}
