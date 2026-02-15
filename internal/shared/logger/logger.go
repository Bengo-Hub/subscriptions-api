package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New creates a zap.Logger configured for the environment.
func New(env string) (*zap.Logger, error) {
	cfg := zap.Config{
		Encoding:          "json",
		Level:             zap.NewAtomicLevelAt(zapcore.InfoLevel),
		Development:       env == "development",
		DisableCaller:     false,
		DisableStacktrace: env != "development",
		EncoderConfig: zapcore.EncoderConfig{
			MessageKey:    "message",
			LevelKey:      "level",
			TimeKey:       "timestamp",
			NameKey:       "logger",
			CallerKey:     "caller",
			StacktraceKey: "stack",
			LineEnding:    zapcore.DefaultLineEnding,
			EncodeLevel: func(l zapcore.Level, enc zapcore.PrimitiveArrayEncoder) {
				enc.AppendString(l.String())
			},
			EncodeTime:     zapcore.ISO8601TimeEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	if env == "development" {
		cfg.Encoding = "console"
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	return cfg.Build()
}
