// Package main -- утилита миграций БД identity-service.
//
// Порядок загрузки env:
//  1. Реальное окружение процесса (Docker env_file, Kubernetes Secrets)
//  2. Файл .env.${ENV} если ENV установлен (local / test)
//  3. Файл .env.local как fallback для локальной разработки
//
// Флаги:
//
//	-action up     -- применить все pending миграции
//	-action down   -- откатить последнюю миграцию
//	-action force  -- принудительно выставить версию (требует -version N)
//
// Переменные окружения:
//
//	POSTGRES_DSN     -- DSN вида postgres://user:pass@host:port/db?sslmode=disable
//	MIGRATIONS_PATH  -- путь к директории миграций (по умолчанию ./migrations)
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

	"github.com/Reddetk/SayMoDev/identy-service/cmd/config"
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
	// --- env loading -----------------------------------------------------------
	// Приоритет: реальное окружение > .env.${ENV} > .env.local
	// LoadDotEnv не перезаписывает уже установленные переменные.
	env := os.Getenv("ENV")
	if env != "" {
		envFile := fmt.Sprintf(".env.%s", env)
		if err := config.LoadDotEnv(envFile); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("load %s: %w", envFile, err)
		}
	}
	// Fallback для локальной разработки без ENV.
	if err := config.LoadDotEnv(".env.local"); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("load .env.local: %w", err)
	}

	// --- flags -----------------------------------------------------------------
	action := flag.String("action", "up", "Migration action: up | down | force")
	version := flag.Int("version", 0, "Target version for -action force")
	flag.Parse()

	// --- config ----------------------------------------------------------------
	dsn := requireEnv("POSTGRES_DSN")

	migrationsPath := os.Getenv("MIGRATIONS_PATH")
	if migrationsPath == "" {
		migrationsPath = "./migrations"
	}

	abs, err := filepath.Abs(migrationsPath)
	if err != nil {
		return fmt.Errorf("migrations path abs: %w", err)
	}

	if _, err := os.Stat(abs); os.IsNotExist(err) {
		return fmt.Errorf("migrations directory not found: %s", abs)
	}

	sourceURL := fmt.Sprintf("file://%s", filepath.ToSlash(abs))

	logger.Info("migrate: initializing",
		zap.String("action", *action),
		zap.String("migrations", abs),
	)

	// --- migrate ---------------------------------------------------------------
	m, err := migrate.New(sourceURL, dsn)
	if err != nil {
		return fmt.Errorf("migrate.New: %w", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			logger.Warn("migrate: source close error", zap.Error(srcErr))
		}
		if dbErr != nil {
			logger.Warn("migrate: db close error", zap.Error(dbErr))
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
			return fmt.Errorf("-version is required for action=force and must be > 0")
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

// requireEnv читает обязательную переменную окружения.
// Возвращает ошибку вместо паники -- migrate завершится через os.Exit(1) в main.
func requireEnv(key string) string {
	v := os.Getenv(key)
	if v == "" {
		fmt.Fprintf(os.Stderr, "migrate: required env variable %q is not set\n", key)
		os.Exit(1)
	}
	return v
}
