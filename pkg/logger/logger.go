// Package logger wraps the standard log/slog package with opinionated defaults
// for structured, JSON-encoded production output.
package logger

import (
	"log/slog"
	"os"
)

// New returns a *slog.Logger configured for JSON output at the requested level.
// Unrecognised level strings default to INFO.
func New(level string) *slog.Logger {
	var lvl slog.Level
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl = slog.LevelInfo
	}

	h := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level:     lvl,
		AddSource: false,
	})

	return slog.New(h)
}
