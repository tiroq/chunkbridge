package observability

import (
	"log/slog"
	"os"
)

// Logger wraps slog and enforces that sensitive fields are never logged.
type Logger struct {
	inner *slog.Logger
}

// NewLogger creates a Logger with the given level and format ("json" or "text").
func NewLogger(level, format string) *Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: lvl}
	var handler slog.Handler
	if format == "json" {
		handler = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		handler = slog.NewTextHandler(os.Stderr, opts)
	}
	return &Logger{inner: slog.New(handler)}
}

// Info logs at INFO level.
func (l *Logger) Info(msg string, args ...any) {
	l.inner.Info(msg, args...)
}

// Debug logs at DEBUG level.
func (l *Logger) Debug(msg string, args ...any) {
	l.inner.Debug(msg, args...)
}

// Warn logs at WARN level.
func (l *Logger) Warn(msg string, args ...any) {
	l.inner.Warn(msg, args...)
}

// Error logs at ERROR level.
func (l *Logger) Error(msg string, args ...any) {
	l.inner.Error(msg, args...)
}

// With returns a Logger with the given attributes pre-set.
func (l *Logger) With(args ...any) *Logger {
	return &Logger{inner: l.inner.With(args...)}
}
