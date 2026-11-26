// goca:manual — загрузчик .env для локального запуска (goca init его не генерирует).

package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadEnvFiles подгружает env-файлы для локального запуска, не перезаписывая
// уже установленные переменные процесса (Docker/K8s secrets имеют приоритет).
//
// Порядок: .env.${ENV} (если ENV задан), затем .env.local только для ENV=local
// или когда ENV не задан. Для test/prod fallback на .env.local запрещён.
// Отсутствие файлов не является ошибкой.
func LoadEnvFiles() error {
	env := os.Getenv("ENV")
	if env != "" {
		if err := loadFile(".env." + env); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("config: load .env.%s: %w", env, err)
		}
	}
	if env == "" || env == EnvLocal {
		if err := loadFile(".env.local"); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("config: load .env.local: %w", err)
		}
	}
	return nil
}

func loadFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	// Два прохода: сначала собрать пары, затем применить. Внутри одного файла
	// действует last-wins (как в dotenv), но переменные, существовавшие до
	// загрузки, не перезаписываются.
	var pairs [][2]string
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		key, val, ok := parseEnvLine(scanner.Text())
		if !ok {
			continue
		}
		pairs = append(pairs, [2]string{key, val})
	}
	if err := scanner.Err(); err != nil {
		return err
	}

	preset := make(map[string]bool, len(pairs))
	for _, pair := range pairs {
		if _, exists := preset[pair[0]]; exists {
			continue
		}
		_, preset[pair[0]] = os.LookupEnv(pair[0])
	}

	for _, pair := range pairs {
		if preset[pair[0]] {
			continue
		}
		if err := os.Setenv(pair[0], pair[1]); err != nil {
			return err
		}
	}
	return nil
}

func parseEnvLine(line string) (key, val string, ok bool) {
	line = strings.TrimSpace(strings.TrimSuffix(line, "\r"))
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	line = strings.TrimPrefix(line, "export ")
	key, val, found := strings.Cut(line, "=")
	if !found {
		return "", "", false
	}
	key = strings.TrimSpace(key)
	if key == "" {
		return "", "", false
	}
	val = strings.TrimSpace(val)
	if len(val) >= 2 {
		if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
			val = val[1 : len(val)-1]
		}
	}
	return key, val, true
}
