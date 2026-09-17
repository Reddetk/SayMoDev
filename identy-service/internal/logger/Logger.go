// Package logger provides a thin wrapper around zap.Logger.
//
// Logger interface is used across all layers (core, adapters) to avoid
// importing zap directly outside of cmd/ and infrastructure packages.
package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger  минимальный интерфейс логирования для всех слоёв сервиса.
// Используется в core-сервисах и адаптерах вместо прямого импорта zap.
type Logger interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	// With возвращает child-логгер с предустановленными полями.
	// Используется для прокидывания trace_id, account_id и т.п. в core.
	With(fields ...Field) Logger
}

// Field  псевдоним zap.Field чтобы импортирующие пакеты не тащили zap напрямую.
type Field = zap.Field

// Реэкспорт часто используемых конструкторов zap.Field.
// Добавляй по мере необходимости  не импортируй zap в core напрямую.
var (
	String   = zap.String
	Int      = zap.Int
	Int64    = zap.Int64
	Bool     = zap.Bool
	Error    = zap.Error
	Duration = zap.Duration
	Any      = zap.Any
)

// 
// zapLogger  внутренняя реализация Logger поверх *zap.Logger.
// 

type zapLogger struct {
	l *zap.Logger
}

// NewZapLogger оборачивает готовый *zap.Logger в Logger.
// Используется в cmd/main.go для передачи production-логгера в адаптеры.
func NewZapLogger(l *zap.Logger) Logger {
	return &zapLogger{l: l}
}

func (z *zapLogger) Debug(msg string, fields ...Field) { z.l.Debug(msg, fields...) }
func (z *zapLogger) Info(msg string, fields ...Field)  { z.l.Info(msg, fields...) }
func (z *zapLogger) Warn(msg string, fields ...Field)  { z.l.Warn(msg, fields...) }
func (z *zapLogger) Error(msg string, fields ...Field) { z.l.Error(msg, fields...) }

func (z *zapLogger) With(fields ...Field) Logger {
	return &zapLogger{l: z.l.With(fields...)}
}

// 
// New  конструктор для локальной разработки.
// Читаемый console-формат с цветными уровнями и временем HH:MM:SS.
// В prod используй NewZapLogger(zap.NewProduction()).
// 

// New создаёт Logger с человекочитаемым console-форматом.
// Уровень: Info. Stacktrace: только для Error и выше.
func New() Logger {
	cfg := zap.Config{
		Level:       zap.NewAtomicLevelAt(zap.DebugLevel),
		Development: true,
		Encoding:    "console",
		EncoderConfig: zapcore.EncoderConfig{
			TimeKey:        "ts",
			LevelKey:       "level",
			CallerKey:      "caller",
			MessageKey:     "msg",
			StacktraceKey:  "stacktrace",
			EncodeTime:     zapcore.TimeEncoderOfLayout("15:04:05"),
			EncodeLevel:    zapcore.CapitalColorLevelEncoder,
			EncodeCaller:   zapcore.ShortCallerEncoder,
			EncodeDuration: zapcore.StringDurationEncoder,
		},
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	l, _ := cfg.Build(
		zap.AddCaller(),
		zap.AddCallerSkip(1),
		zap.AddStacktrace(zap.ErrorLevel),
	)

	return &zapLogger{l: l}
}
