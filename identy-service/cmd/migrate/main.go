// Package main  утилита миграций БД identity-service.
//
// Отдельный бинарник  не зависит от пакетов core/ или adapter/.
// Единственная внешняя зависимость: golang-migrate/v4.
//
// Порядок загрузки env:
//  1. Реальное окружение процесса (Docker environment:, K8s Secrets)
//  2. .env.${ENV} если ENV установлен
//  3. .env.local как fallback
//
// Обязательные переменные:
//
//	POSTGRES_DSN       postgres://user:pass@host:5432/db?sslmode=disable
//	MIGRATIONS_PATH    путь к директории с *.up.sql / *.down.sql (default: ./migrations)
//
// Флаги:
//
//	--action up              применить все pending миграции
//	--action down            откатить последнюю миграцию
//	--action force -version N  выставить версию принудительно (фикс dirty state)
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

	dotenv "github.com/Reddetk/SayMoDev/identy-service/cmd/config"
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
	//  env loading
	// Приоритет: реальное окружение > .env.${ENV} > .env.local
	// dotenv.Load не перезаписывает уже установленные переменные.
	if env := os.Getenv("ENV"); env != "" {
		envFile := fmt.Sprintf(".env.%s", env)
		if err := dotenv.ENVLoad(envFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("load %s: %w", envFile, err)
		}
	}
	if err := dotenv.ENVLoad(".env.local"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load .env.local: %w", err)
	}

	//  flags
	action := flag.String("action", "up", "up | down | force")
	version := flag.Int("version", 0, "target version for -action force")
	flag.Parse()

	//  config
	dsn, err := requireEnv("POSTGRES_DSN")
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
	if _, err := os.Stat(abs); os.IsNotExist(err) {
		return fmt.Errorf("migrations directory not found: %s", abs)
	}

	sourceURL := fmt.Sprintf("file://%s", filepath.ToSlash(abs))

	logger.Info("migrate: initializing",
		zap.String("action", *action),
		zap.String("migrations", abs),
	)

	//  migrate
	m, err := migrate.New(sourceURL, dsn)
	if err != nil {
		return fmt.Errorf("migrate.New: %w", err)
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
		v, dirty, _ := m.Version()
		logger.Info("migrate up: done", zap.Uint("version", v), zap.Bool("dirty", dirty))

	case "down":
		err = m.Steps(-1)
		if errors.Is(err, migrate.ErrNoChange) {
			logger.Info("migrate down: already at base")
			return nil
		}
		if err != nil {
			return fmt.Errorf("migrate down: %w", err)
		}
		v, dirty, _ := m.Version()
		logger.Info("migrate down: done", zap.Uint("version", v), zap.Bool("dirty", dirty))

	case "force":
		if *version == 0 {
			return fmt.Errorf("-version required for force and must be > 0")
		}
		if err := m.Force(*version); err != nil {
			return fmt.Errorf("migrate force: %w", err)
		}
		logger.Info("migrate force: done", zap.Int("version", *version))

	default:
		return fmt.Errorf("unknown action %q: use up | down | force", *action)
	}

	return nil
}

func requireEnv(key string) (string, error) {
	v := os.Getenv(key)
	if v == "" {
		return "", fmt.Errorf("required env variable %q is not set", key)
	}
	return v, nil
}
