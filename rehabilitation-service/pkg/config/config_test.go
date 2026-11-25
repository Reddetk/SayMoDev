package config

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadFromFixtures — таблица из testdata/fixtures/config.csv.
// CSV написан отдельным агентом без доступа к коду; здесь только исполнение.
func TestLoadFromFixtures(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "fixtures", "config.csv")
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open fixtures: %v", err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read csv: %v", err)
	}
	if len(rows) < 2 {
		t.Fatal("fixtures must contain at least one case")
	}

	header := map[string]int{}
	for i, name := range rows[0] {
		header[name] = i
	}

	for _, row := range rows[1:] {
		row := row
		t.Run(row[header["case_id"]], func(t *testing.T) {
			var env map[string]string
			if err := json.Unmarshal([]byte(row[header["env_json"]]), &env); err != nil {
				t.Fatalf("env_json: %v", err)
			}

			lookup := func(key string) (string, bool) {
				v, ok := env[key]
				return v, ok
			}

			cfg, err := LoadFrom(lookup)
			wantValid := row[header["expected_valid"]] == "true"

			if wantValid {
				if err != nil {
					t.Fatalf("want valid config, got error: %v", err)
				}
				if cfg == nil {
					t.Fatal("want valid config, got nil")
				}
				return
			}

			if err == nil {
				t.Fatalf("want error containing %q, got valid config", row[header["expected_error_contains"]])
			}
			if field := row[header["expected_error_contains"]]; !strings.Contains(err.Error(), field) {
				t.Fatalf("want error containing %q, got %q", field, err.Error())
			}
		})
	}
}
