// cmd/migrate — отдельный бинарник миграций (docs/deployment/migrations.md).
// goca:manual — goca init не генерирует cmd/migrate; инструмент задан deployment/migrations.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	action := flag.String("action", "up", "migration action: up | down | force")
	version := flag.Int("version", -1, "target version for -action force")
	flag.Parse()

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		exitf("POSTGRES_DSN is required")
	}

	path := os.Getenv("MIGRATIONS_PATH")
	if path == "" {
		path = "./migrations"
	}

	m, err := migrate.New("file://"+path, dsn)
	if err != nil {
		exitf("open migrations: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil {
			fmt.Fprintf(os.Stderr, "close source: %v\n", srcErr)
		}
		if dbErr != nil {
			fmt.Fprintf(os.Stderr, "close database: %v\n", dbErr)
		}
	}()

	switch *action {
	case "up":
		err = m.Up()
	case "down":
		err = m.Steps(-1)
	case "force":
		if *version < 0 {
			exitf("-action force requires -version N")
		}
		err = m.Force(*version)
	default:
		exitf("unknown -action %q (want up|down|force)", *action)
	}

	if errors.Is(err, migrate.ErrNoChange) {
		fmt.Println("no change")
		return
	}
	if err != nil {
		exitf("migrate %s: %v", *action, err)
	}

	current, dirty, verr := m.Version()
	if verr != nil {
		fmt.Printf("migrate %s: done\n", *action)
		return
	}
	fmt.Printf("migrate %s: done, version=%d dirty=%t\n", *action, current, dirty)
}

func exitf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
