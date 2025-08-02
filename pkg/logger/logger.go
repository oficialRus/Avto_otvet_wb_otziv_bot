package logger

import (
	"os"
	"strings"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New returns a sugared zap logger configured for the given log level.
// Supported levels: "debug", "info", "warn", "error", "fatal", "panic".
// Any unknown value falls back to "info".
//
// In development (GO_ENV != "production"), logs are human‑readable; otherwise JSON.
// The caller skip is set to 1 so wrapper functions log the correct line number.
func New(level string) *zap.SugaredLogger {
	lvl := parseLevel(strings.ToLower(level))

	var cfg zap.Config
	if isProd() {
		cfg = zap.NewProductionConfig()
	} else {
		cfg = zap.NewDevelopmentConfig()
	}
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	logger, err := cfg.Build(zap.AddCallerSkip(1))
	if err != nil {
		panic(err) // configuration errors are fatal on startup
	}
	return logger.Sugar()
}

// Sync flushes any buffered log entries. Should be called on shutdown.
// It ignores the error returned by zap.Sync for common "invalid argument" cases
// on Windows.
func Sync(l *zap.SugaredLogger) {
	if l == nil {
		return
	}
	_ = l.Sync()
}

// Helper: map string to zapcore.Level
func parseLevel(lvl string) zapcore.Level {
	switch lvl {
	case "debug":
		return zap.DebugLevel
	case "warn":
		return zap.WarnLevel
	case "error":
		return zap.ErrorLevel
	case "fatal":
		return zap.FatalLevel
	case "panic":
		return zap.PanicLevel
	default:
		return zap.InfoLevel
	}
}

// Helper: detect prod env via GO_ENV var (convention)
func isProd() bool {
	return strings.ToLower(strings.TrimSpace(getenv("GO_ENV"))) == "production"
}

// Wrapper around os.Getenv to avoid direct import in this file
func getenv(k string) string {
	return strings.TrimSpace(strings.ToLower(strings.TrimSpace(strings.Trim(os.Getenv(k), "\""))))
}
