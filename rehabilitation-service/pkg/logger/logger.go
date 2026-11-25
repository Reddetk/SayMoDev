// Package logger создаёт структурированный zap-логгер.
// goca:manual — шаблон даёт log.Printf; docs/AGENTS требуют структурированные логи.
package logger

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// New возвращает JSON-логгер (console — только в local).
func New(level, env string) (*zap.Logger, error) {
	lvl, err := zapcore.ParseLevel(level)
	if err != nil {
		return nil, err
	}

	cfg := zap.NewProductionConfig()
	cfg.Level = zap.NewAtomicLevelAt(lvl)
	cfg.OutputPaths = []string{"stdout"}
	cfg.ErrorOutputPaths = []string{"stderr"}
	cfg.EncoderConfig.TimeKey = "ts"
	cfg.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder

	if env == "local" {
		cfg.Encoding = "console"
		cfg.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	log, err := cfg.Build()
	if err != nil {
		return nil, err
	}

	host, _ := os.Hostname()
	return log.With(zap.String("service", "rehabilitation-service"), zap.String("host", host)), nil
}
