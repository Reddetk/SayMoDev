// Package logger builds the structured zap logger used across the service.
package logger

import (
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

// Logger is the minimal logging surface used by adapters and services.
type Logger interface {
	Debug(msg string, fields ...zap.Field)
	Info(msg string, fields ...zap.Field)
	Warn(msg string, fields ...zap.Field)
	Error(msg string, fields ...zap.Field)
	Fatal(msg string, fields ...zap.Field)
}

// New returns a production-style logger: JSON by default, console for local debugging.
func New(level, format string) (*zap.Logger, error) {
	lvl := zap.NewAtomicLevel()
	if err := lvl.UnmarshalText([]byte(level)); err != nil {
		lvl.SetLevel(zap.InfoLevel)
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.TimeKey = "ts"
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder
	encCfg.MessageKey = "msg"

	var enc zapcore.Encoder
	if format == "console" {
		enc = zapcore.NewConsoleEncoder(encCfg)
	} else {
		enc = zapcore.NewJSONEncoder(encCfg)
	}

	core := zapcore.NewCore(enc, zapcore.Lock(zapcore.AddSync(stdout{})), lvl)
	return zap.New(core, zap.AddCaller(), zap.AddStacktrace(zapcore.ErrorLevel)), nil
}

type stdout struct{}

func (stdout) Write(p []byte) (int, error) { return writeStdout(p) }
