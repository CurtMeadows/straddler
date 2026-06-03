// Package telemetry provides structured logging via the standard library's slog package.
// Loggers are stored in and retrieved from context.Context so that call sites
// deep in the call stack (e.g. inside a worker goroutine) can attach job-scoped
// fields without needing the logger threaded through every function signature.
//
// Usage:
//
//	// At startup:
//	logger := telemetry.New("info", "json")
//	ctx = telemetry.WithLogger(ctx, logger)
//
//	// Deep inside a worker:
//	log := telemetry.FromContext(ctx).With("job_id", 42, "source", "docker.io/library/nginx:1.25")
//	log.Info("copying image")
package telemetry

import (
	"context"
	"log/slog"
	"os"
)

// contextKey is an unexported type so our key never collides with keys from
// other packages that also store values in context.
type contextKey struct{}

// New creates a *slog.Logger writing to stderr.
// level must be one of: debug, info, warn, error.
// format must be one of: json, text.
func New(level, format string) *slog.Logger {
	var lvl slog.Level
	// UnmarshalText accepts "DEBUG", "INFO", "WARN", "ERROR" (case-insensitive).
	// On any parse error we fall back to Info so the tool stays usable.
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}

	var handler slog.Handler
	switch format {
	case "text":
		handler = slog.NewTextHandler(os.Stderr, opts)
	default:
		handler = slog.NewJSONHandler(os.Stderr, opts)
	}

	return slog.New(handler)
}

// WithLogger returns a copy of ctx with the given logger stored inside it.
func WithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, contextKey{}, logger)
}

// FromContext retrieves the logger stored by WithLogger.
// If no logger is found it returns slog.Default() so callers never get nil.
func FromContext(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(contextKey{}).(*slog.Logger); ok {
		return l
	}
	return slog.Default()
}
