// goca:manual — идемпотентный сид для E2E read-контрактов BC#3.
package main

import (
	"context"
	"crypto/sha256"
	"fmt"
	"os"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	programID = "3f2a9c11-7b4d-4e21-9c8a-1d2e3f4a5b6c"
	lesson1ID = "a1b2c3d4-e5f6-4a7b-8c9d-0e1f2a3b4c5d"
	lesson2ID = "b2c3d4e5-f6a7-4b8c-9d0e-1f2a3b4c5d6e"
)

func main() {
	dsn := os.Getenv("POSTGRES_DSN")
	if dsn == "" {
		fmt.Fprintln(os.Stderr, "POSTGRES_DSN is required")
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect: %v\n", err)
		os.Exit(1)
	}
	defer pool.Close()

	if _, err := pool.Exec(ctx, `
INSERT INTO programs (id, title, difficulty, aphasia_type, version)
VALUES ($1, $2, 'easy', 'motor', 1)
ON CONFLICT (id) DO NOTHING`, programID, "Базовый курс восстановления речи (seed)"); err != nil {
		fmt.Fprintf(os.Stderr, "seed program: %v\n", err)
		os.Exit(1)
	}

	lessons := []struct {
		id    string
		title string
		desc  string
	}{
		{lesson1ID, "Знакомство со звуками", "Первый урок программы"},
		{lesson2ID, "Дыхательная гимнастика", "Второй урок программы"},
	}

	for i, lesson := range lessons {
		hash := fmt.Sprintf("%x", sha256.Sum256([]byte(lesson.id)))
		url := fmt.Sprintf("http://localhost:9005/saymo-programs/programs/%s/lessons/%s/v1.pdf", programID, lesson.id)
		if _, err := pool.Exec(ctx, `
INSERT INTO program_lessons
    (id, program_id, program_version, order_position, title, description,
     content_url, content_hash, file_size_bytes, mime_type)
VALUES ($1, $2, 1, $3, $4, $5, $6, $7, 1024, 'application/pdf')
ON CONFLICT (program_id, program_version, order_position) DO NOTHING`,
			lesson.id, programID, i+1, lesson.title, lesson.desc, url, hash,
		); err != nil {
			fmt.Fprintf(os.Stderr, "seed lesson %d: %v\n", i+1, err)
			os.Exit(1)
		}
	}

	fmt.Println("seed: ok")
}
