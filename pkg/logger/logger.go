package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New builds a production zap logger with the given log level.
func New(level string) (*zap.Logger, error) {
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		lvl = zapcore.InfoLevel
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	return cfg.Build(zap.AddCaller(), zap.AddCallerSkip(0))
}

// Must panics if logger construction fails — intended for use in main.
func Must(level string) *zap.Logger {
	l, err := New(level)
	if err != nil {
		panic(err)
	}
	return l
}
