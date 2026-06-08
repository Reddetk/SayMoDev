package telemetry

import (
	"context"

	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// LoggerConfig -- настройки логгера. Передаётся из cmd/api/main.go.
// Приоритет переиспользования: каждый BC передаёт свои ServiceName / BC.
type LoggerConfig struct {
	ServiceName string       // "identity-service" | "billing-service" | ...
	BC          string       // "bc1" | "bc2" | "bc3" | "bc4"
	Level       zapcore.Level // zap.InfoLevel в prod, zap.DebugLevel в dev
	Development bool
}

// Logger -- обёртка над *zap.Logger с trace-aware методами.
// Доменный код получает интерфейс Logger; zap не импортируется в core/.
type Logger struct {
	base *zap.Logger
}

// NewLogger строит production-ready zap.Logger согласно Log Format Standard
// из Observability.md: поля timestamp, level, service, bc обязательны.
func NewLogger(cfg LoggerConfig) (*Logger, error) {
	zapCfg := zap.NewProductionConfig()
	if cfg.Development {
		zapCfg = zap.NewDevelopmentConfig()
	}
	zapCfg.Level = zap.NewAtomicLevelAt(cfg.Level)
	// JSON-формат: совместим с Loki label-парсингом {job, level, bc}.
	zapCfg.Encoding = "json"
	zapCfg.EncoderConfig.TimeKey = "timestamp"
	zapCfg.EncoderConfig.EncodeTime = zapcore.RFC3339NanoTimeEncoder
	zapCfg.EncoderConfig.LevelKey = "level"
	zapCfg.EncoderConfig.EncodeLevel = zapcore.CapitalLevelEncoder

	base, err := zapCfg.Build(
		zap.Fields(
			zap.String("service", cfg.ServiceName),
			zap.String("bc", cfg.BC),
		),
		// AddCallerSkip(1): caller указывает на вызывающий код,
		// а не на методы-обёртки этого пакета.
		zap.AddCallerSkip(1),
	)
	if err != nil {
		return nil, err
	}
	return &Logger{base: base}, nil
}

// WithContext извлекает trace_id и span_id из OTel контекста и возвращает
// *zap.Logger с этими полями. Вызывается в начале каждого handler/use-case.
//
// Использование:
//
//	log := logger.WithContext(ctx)
//	log.Info("session_created", zap.String("account_id", id))
func (l *Logger) WithContext(ctx context.Context) *zap.Logger {
	span := trace.SpanFromContext(ctx)
	if !span.SpanContext().IsValid() {
		return l.base
	}
	sc := span.SpanContext()
	return l.base.With(
		zap.String("trace_id", sc.TraceID().String()),
		zap.String("span_id", sc.SpanID().String()),
	)
}

// Sync сбрасывает буферы zap. Вызывается в graceful shutdown.
func (l *Logger) Sync() error {
	return l.base.Sync()
}

// Base возвращает raw *zap.Logger для случаев инициализации
// до появления контекста (startup, shutdown).
func (l *Logger) Base() *zap.Logger {
	return l.base
}
