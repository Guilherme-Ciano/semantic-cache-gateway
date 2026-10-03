package logger

import (
	"log/slog"
	"os"
)

// New initializes a JSON slog.Logger. Defaults to INFO if level is unparseable.
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
