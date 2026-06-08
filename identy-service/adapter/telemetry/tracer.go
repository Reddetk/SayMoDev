package telemetry

import (
	"context"
	"fmt"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.21.0"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// TracerConfig -- настройки OTel SDK.
// Jaeger endpoint фиксирован спецификацией: OTLP gRPC :4317.
// Каждый BC передаёт свои ServiceName / Environment.
type TracerConfig struct {
	ServiceName    string // "identity-service" | "billing-service" | ...
	ServiceVersion string // из build-переменной, например "v1.2.3"
	Environment    string // "production" | "staging" | "development"
	JaegerEndpoint string // default: "localhost:4317"
	// ConnectTimeout -- таймаут установки gRPC-соединения при старте.
	// 0 использует дефолт 5s.
	ConnectTimeout time.Duration
}

// ShutdownFunc -- вызывается в graceful shutdown для дрейна spans.
type ShutdownFunc func(ctx context.Context) error

// NewTracer инициализирует OTel TracerProvider с OTLP gRPC экспортером
// согласно Observability.md: W3C TraceContext + Baggage propagators.
//
// Возвращает ShutdownFunc -- должна быть вызвана с таймаутом до закрытия процесса.
func NewTracer(cfg TracerConfig) (ShutdownFunc, error) {
	if cfg.JaegerEndpoint == "" {
		cfg.JaegerEndpoint = "localhost:4317"
	}
	timeout := cfg.ConnectTimeout
	if timeout == 0 {
		timeout = 5 * time.Second
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// Insecure: внутренняя сеть кластера; TLS терминируется на ingress.
	//nolint:staticcheck // grpc.DialContext deprecated в 1.65+; заменить на grpc.NewClient при обновлении.
	conn, err := grpc.DialContext(ctx, cfg.JaegerEndpoint,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithBlock(),
	)
	if err != nil {
		return nil, fmt.Errorf("telemetry: dial jaeger %s: %w", cfg.JaegerEndpoint, err)
	}

	exporter, err := otlptracegrpc.New(ctx, otlptracegrpc.WithGRPCConn(conn))
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("telemetry: create otlp exporter: %w", err)
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceName(cfg.ServiceName),
			semconv.ServiceVersion(cfg.ServiceVersion),
			semconv.DeploymentEnvironment(cfg.Environment),
		),
	)
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("telemetry: build resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
		// AlwaysSample подходит для dev/staging.
		// В production передавай sdktrace.ParentBased(sdktrace.TraceIDRatioBased(0.1)).
		sdktrace.WithSampler(sdktrace.AlwaysSample()),
	)

	// W3C TraceContext + Baggage (account_id, correlation_id) согласно спецификации.
	otel.SetTracerProvider(tp)
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	return func(ctx context.Context) error {
		if err := tp.Shutdown(ctx); err != nil {
			return fmt.Errorf("telemetry: tracer shutdown: %w", err)
		}
		return conn.Close()
	}, nil
}
