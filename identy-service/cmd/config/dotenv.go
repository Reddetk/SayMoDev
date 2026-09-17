// Package config  минимальный парсер .env-файлов.
//
// Не имеет никаких зависимостей кроме стандартной библиотеки.
// Импортируется как migrate, так и cmd/config.
//
// Правила:
//   - Пустые строки и строки с #  пропускаются.
//   - Значение может быть обёрнуто в одинарные или двойные кавычки.
//   - Уже установленные переменные окружения имеют приоритет (не перезаписываются).
//   - Поддерживает префикс export (совместимость с bash).
package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// ENVLoad читает файл path и проставляет переменные через os.Setenv.
// Если файл не существует  возвращает os.ErrNotExist, вызывающий код решает игнорировать.
func ENVLoad(path string) error {
	f, err := os.Open(path) //nolint:gosec
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		line = strings.TrimPrefix(line, "export ")

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("dotenv %s:%d: missing '=' in %q", path, lineNum, line)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = unquote(value)

		if key == "" {
			return fmt.Errorf("dotenv %s:%d: empty key", path, lineNum)
		}

		if os.Getenv(key) != "" {
			continue
		}

		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("dotenv %s:%d: setenv %s: %w", path, lineNum, key, err)
		}
	}

	return scanner.Err()
}

func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
