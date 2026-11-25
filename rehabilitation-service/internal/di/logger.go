package di

import (
	"github.com/Reddetk/SayMoDev/rehabilitation-service/pkg/config"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/pkg/logger"
	"go.uber.org/zap"
)

func newLogger(cfg *config.Config) (*zap.Logger, error) {
	return logger.New(cfg.LogLevel, string(cfg.Env))
}
