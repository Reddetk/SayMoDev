// Command migrate applies golang-migrate migrations for progress-service.
package main

import (
	"errors"
	"flag"
	"fmt"
	"log"
	"os"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"
)

func main() {
	var (
		action  string
		version int
	)
	flag.StringVar(&action, "action", "up", "up | down | force")
	flag.IntVar(&version, "version", 0, "target version for -action force")
	flag.Parse()

	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		log.Fatal("POSTGRES_DSN is required")
	}
	path := os.Getenv("MIGRATIONS_PATH")
	if path == "" {
		path = "./migrations"
	}

	m, err := migrate.New("file://"+path, dsn)
	if err != nil {
		log.Fatalf("migrate: init: %v", err)
	}
	defer func() {
		srcErr, dbErr := m.Close()
		if srcErr != nil || dbErr != nil {
			log.Printf("migrate: close: src=%v db=%v", srcErr, dbErr)
		}
	}()

	switch action {
	case "up":
		err = m.Up()
	case "down":
		err = m.Steps(-1)
	case "force":
		if version < 0 {
			log.Fatal("migrate: -version is required for -action force")
		}
		err = m.Force(version)
	default:
		log.Fatalf("migrate: unknown action %q", action)
	}
	if errors.Is(err, migrate.ErrNoChange) {
		fmt.Println("migrate: no change")
		return
	}
	if err != nil {
		log.Fatalf("migrate: %s: %v", action, err)
	}
	fmt.Printf("migrate: %s ok\n", action)
}
