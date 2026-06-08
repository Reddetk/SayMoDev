package config

import (
	"github.com/Reddetk/SayMoDev/identy-service/adapter/primary/http/middleware"
	"github.com/Reddetk/SayMoDev/identy-service/adapter/secondary"
	"github.com/Reddetk/SayMoDev/identy-service/adapter/telemetry"
)

type Config struct {
	HTTPAddr     string // :8080
	DatabaseURL  string // postgres://...
	RedisURL     string // redis://...
	RSAKeyPath   string // путь к PEM приватного ключа
	JWKSPath     string // путь к JWKS JSON (публичные ключи)
	OTelEndpoint string // gRPC OTLP endpoint (Jaeger/Tempo)
	GoogleOAuth  secondary.GoogleOAuthConfig
	Postbox      secondary.PostboxConfig
	CORS         middleware.CORSConfig
	Logger       telemetry.LoggerConfig
	Trace        telemetry.TracerConfig
}
