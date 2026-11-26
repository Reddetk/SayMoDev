// Package main — утилита миграций billing-service (BC#2).
//
// Отдельный бинарник: не зависит от домена и адаптеров.
// Флаги: -action up|down|force, -version N (для force).
// Обязательные переменные: POSTGRES_DSN, MIGRATIONS_PATH (default ./migrations).
// goca:manual - отдельный бинарник миграций, goca init его не генерирует.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	"go.uber.org/zap"

	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/config"
)

func main() {
	logger, _ := zap.NewProduction()
	defer func() { _ = logger.Sync() }()

	if err := run(logger); err != nil {
		logger.Error("migrate: fatal error", zap.Error(err))
		os.Exit(1)
	}
}

func run(logger *zap.Logger) error {
	if err := config.LoadEnvFiles(); err != nil {
		return err
	}

	action := flag.String("action", "up", "up | down | force")
	version := flag.Int("version", 0, "target version for -action force")
	flag.Parse()

	dsn, err := config.LoadDSN(os.LookupEnv)
	if err != nil {
		return err
	}

	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = "./migrations"
	}
	abs, err := filepath.Abs(migrationsPath)
	if err != nil {
		return fmt.Errorf("migrations path: %w", err)
	}
	if _, err := os.Stat(abs); err != nil {
		return fmt.Errorf("migrations directory %s: %w", abs, err)
	}

	logger.Info("migrate: initializing", zap.String("action", *action), zap.String("migrations", abs))

	m, err := migrate.New("file://"+filepath.ToSlash(abs), dsn)
	if err != nil {
		return fmt.Errorf("migrate.New: %w", config.RedactError(err, dsn))
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			logger.Warn("migrate: source close", zap.Error(srcErr))
		}
		if dbErr != nil {
			logger.Warn("migrate: db close", zap.Error(dbErr))
		}
	}()

	switch *action {
	case "up":
		err = m.Up()
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("migrate up: no changes")
			return nil
		}
		if err != nil {
			return fmt.Errorf("migrate up: %w", err)
		}
	case "down":
		err = m.Steps(-1)
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("migrate down: already at base")
			return nil
		}
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
	case "force":
		if *version <= 0 {
			return fmt.Errorf("-version required for force and must be > 0")
		}
		if err := m.Force(*version); err != nil {
			return fmt.Errorf("migrate force: %w", err)
		}
		logger.Info("migrate force: done", zap.Int("version", *version))
		return nil
	default:
		return fmt.Errorf("unknown action %q: use up | down | force", *action)
	}

	v, dirty, _ := m.Version()
	logger.Info("migrate: done", zap.Uint("version", v), zap.Bool("dirty", dirty))
	return nil
}
