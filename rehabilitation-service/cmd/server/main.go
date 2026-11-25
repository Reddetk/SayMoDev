// rehabilitation-service — BC#3 Rehabilitation Content Management.
// goca:manual — entrypoint собирает health/metrics/probes вместо GORM AutoMigrate.
//
// @title SayMo Rehabilitation Content Service API
// @version 0.1.0
// @description BC#3 Rehabilitation Content Management (SayMo). S1: health/readiness.
// @host localhost:8003
// @BasePath /
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/di"
	handlerhttp "github.com/Reddetk/SayMoDev/rehabilitation-service/internal/handler/http"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/pkg/config"
)

var (
	Version   = "dev"
	BuildTime = "unknown"
	Commit    = "none"
)

func main() {
	healthcheck := flag.Bool("healthcheck", false, "probe local /health and exit")
	flag.Parse()

	if *healthcheck {
		os.Exit(runHealthcheck())
	}

	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup failed: %v\n", err)
		os.Exit(1)
	}

	app, err := di.New(context.Background(), cfg, handlerhttp.BuildInfo{
		Version:   Version,
		Commit:    Commit,
		BuildTime: BuildTime,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "startup failed: %v\n", err)
		os.Exit(1)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := app.Run(ctx); err != nil {
		// Run уже залогировал причину.
		os.Exit(1)
	}
}

func runHealthcheck() int {
	addr := os.Getenv("HTTP_ADDR")
	if addr == "" {
		addr = ":8003"
	}
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return 1
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}

	client := http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + net.JoinHostPort(host, port) + "/health")
	if err != nil {
		fmt.Fprintf(os.Stderr, "healthcheck: %v\n", err)
		return 1
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "healthcheck: status %d\n", resp.StatusCode)
		return 1
	}
	return 0
}
