package obs

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

// LogJobReserved logs that a job was reserved from the queue.
// base and quote identify the currency pair (e.g. "USD", "EUR").
func LogJobReserved(ctx context.Context, jobID, base, quote string) {
	Logger(ctx).LogAttrs(ctx, slog.LevelInfo, EvJobReserved,
		slog.String("job_id", jobID),
		slog.String("base", base),
		slog.String("quote", quote),
	)
}

// LogJobCompleted logs that a job finished successfully.
// base and quote identify the currency pair (e.g. "USD", "EUR").
func LogJobCompleted(ctx context.Context, jobID, base, quote string, duration time.Duration) {
	Logger(ctx).LogAttrs(ctx, slog.LevelInfo, EvJobCompleted,
		slog.String("job_id", jobID),
		slog.String("base", base),
		slog.String("quote", quote),
		slog.Int64("duration_ms", duration.Milliseconds()),
	)
}

// LogJobRescheduled logs that a job was rescheduled for a future retry.
// base and quote identify the currency pair (e.g. "USD", "EUR").
func LogJobRescheduled(ctx context.Context, jobID, base, quote string, attempts int, nextDelay time.Duration) {
	Logger(ctx).LogAttrs(ctx, slog.LevelWarn, EvJobRescheduled,
		slog.String("job_id", jobID),
		slog.String("base", base),
		slog.String("quote", quote),
		slog.Int("attempts", attempts),
		slog.Int64("next_delay_ms", nextDelay.Milliseconds()),
	)
}

// LogJobFailed logs that a job has exhausted all retries and is permanently failed.
// base and quote identify the currency pair (e.g. "USD", "EUR").
func LogJobFailed(ctx context.Context, jobID, base, quote string, attempts int, err error) {
	Logger(ctx).LogAttrs(ctx, slog.LevelError, EvJobFailed,
		slog.String("job_id", jobID),
		slog.String("base", base),
		slog.String("quote", quote),
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

// LogSchedulerTickFailed logs a scheduler tick that returned an error from the
// queue. The Run loop continues; the next tick will retry.
func LogSchedulerTickFailed(ctx context.Context, err error) {
	Logger(ctx).LogAttrs(ctx, slog.LevelError, EvSchedulerTick,
		slog.String("error", err.Error()),
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

// LogCoalescingCollapsed logs that a duplicate enqueue was collapsed into an
// already-queued job. jobID is the existing job that absorbed the duplicate;
// base and quote identify the currency pair.
func LogCoalescingCollapsed(ctx context.Context, jobID, base, quote string) {
	Logger(ctx).LogAttrs(ctx, slog.LevelDebug, EvCoalescingCollapsed,
		slog.String("job_id", jobID),
		slog.String("base", base),
		slog.String("quote", quote),
	)
}

// LogWorkerOpFailed logs an error from one of the worker's queue operations
// (reserve, complete, reschedule, fail, recover_expired). The op attribute
// lets a single dashboard row break out failures by operation.
func LogWorkerOpFailed(ctx context.Context, op string, err error) {
	Logger(ctx).LogAttrs(ctx, slog.LevelError, EvWorkerOpFailed,
		slog.String("op", op),
		slog.String("error", err.Error()),
	)
}

// LogProviderQuotaExceeded logs that an upstream provider returned a quota-
// exceeded signal and the worker is rescheduling. retryAt is the absolute time
// the job will next be eligible to run.
func LogProviderQuotaExceeded(ctx context.Context, provider string, retryAt time.Time) {
	Logger(ctx).LogAttrs(ctx, slog.LevelWarn, EvProviderQuotaExceeded,
		slog.String("provider", provider),
		slog.Time("retry_at", retryAt),
	)
}

// LogProviderResponseAnomaly logs a per-occurrence anomaly in an upstream
// provider response (e.g. a malformed quotes key). detail carries the offending
// value so a single log line is enough to diagnose without correlating with
// other sources.
func LogProviderResponseAnomaly(ctx context.Context, provider, kind, detail string) {
	Logger(ctx).LogAttrs(ctx, slog.LevelWarn, EvProviderResponseAnomaly,
		slog.String("provider", provider),
		slog.String("kind", kind),
		slog.String("detail", detail),
	)
}

// LogPanicRecovered logs a recovered panic with its value and stack trace.
// recovered is the value passed to panic(); stack is the output of debug.Stack().
// The log record is emitted at ERROR level with fields "panic" and "stack".
// Any request_id present in ctx is automatically attached by Logger.
func LogPanicRecovered(ctx context.Context, recovered any, stack []byte) {
	Logger(ctx).LogAttrs(ctx, slog.LevelError, EvPanicRecovered,
		slog.String("panic", fmt.Sprintf("%+v", recovered)),
		slog.String("stack", string(stack)),
	)
}
