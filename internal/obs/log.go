package obs

import (
	"context"
	"log/slog"
)

// loggerKey is an unexported context key type for the stored *slog.Logger.
// Using a named struct prevents collisions with keys from other packages.
type loggerKey struct{}

// requestIDKey is an unexported context key type for the request ID.
type requestIDKey struct{}

// WithLogger returns a new context derived from ctx with l stored as the logger.
// Passing nil is allowed; Logger will return slog.Default() in that case.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// WithRequestID returns a new context derived from ctx with id stored as the request ID.
func WithRequestID(ctx context.Context, id string) context.Context {
	return context.WithValue(ctx, requestIDKey{}, id)
}

// RequestID returns the request ID stored in ctx by WithRequestID.
// If no ID is present, it returns an empty string.
func RequestID(ctx context.Context) string {
	id, _ := ctx.Value(requestIDKey{}).(string)
	return id
}

// Logger returns the *slog.Logger stored in ctx by WithLogger.
// If no logger is present, or if nil was stored, it returns slog.Default().
// If a request_id is present in the context, it is attached to the logger.
func Logger(ctx context.Context) *slog.Logger {
	l, _ := ctx.Value(loggerKey{}).(*slog.Logger)
	if l == nil {
		l = slog.Default()
	}

	if id := RequestID(ctx); id != "" {
		l = l.With(slog.String("request_id", id))
	}

	return l
}
