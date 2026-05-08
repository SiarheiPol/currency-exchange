package obs

import (
	"context"
	"log/slog"
)

// loggerKey is an unexported context key type for the stored *slog.Logger.
// Using a named struct prevents collisions with keys from other packages.
type loggerKey struct{}

// WithLogger returns a new context derived from ctx with l stored as the logger.
// Passing nil is allowed; Logger will return slog.Default() in that case.
func WithLogger(ctx context.Context, l *slog.Logger) context.Context {
	return context.WithValue(ctx, loggerKey{}, l)
}

// Logger returns the *slog.Logger stored in ctx by WithLogger.
// If no logger is present, or if nil was stored, it returns slog.Default().
func Logger(ctx context.Context) *slog.Logger {
	l, _ := ctx.Value(loggerKey{}).(*slog.Logger)
	if l == nil {
		return slog.Default()
	}
	return l
}
