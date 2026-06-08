package config

import (
	"bufio"
	"fmt"
	"os"
	"strings"
)

// LoadDotEnv читает файл path в формате KEY=VALUE и проставляет переменные
// через os.Setenv ТОЛЬКО если переменная ещё не установлена в окружении.
//
// Правила формата:
//   - Пустые строки и строки начинающиеся с # -- комментарии, пропускаются.
//   - Значение может быть обёрнуто в одинарные или двойные кавычки; кавычки обрезаются.
//   - Переменные окружения, уже установленные в процессе, имеют приоритет (не перезаписываются).
//   - Порядок: реальное окружение > .env файл.
//
// Предназначен исключительно для локальной разработки.
// В production/staging не вызывать: все переменные приходят из Kubernetes Secrets.
//
// Пример вызова в main.go:
//
//	// Только для локального запуска; в prod файл не существует, ошибка игнорируется.
//	if err := config.LoadDotEnv("identy-service/.env"); err != nil && !os.IsNotExist(err) {
//	    log.Fatalf("config: %v", err)
//	}
func LoadDotEnv(path string) error {
	f, err := os.Open(path) //nolint:gosec // path задаётся вызывающим кодом, не пользователем
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Пропускаем пустые строки и комментарии.
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		// Убираем опциональный префикс export (совместимость с bash-скриптами).
		line = strings.TrimPrefix(line, "export ")

		key, value, found := strings.Cut(line, "=")
		if !found {
			return fmt.Errorf("dotenv %s:%d: missing '=' in line %q", path, lineNum, line)
		}

		key = strings.TrimSpace(key)
		value = strings.TrimSpace(value)
		value = unquote(value)

		if key == "" {
			return fmt.Errorf("dotenv %s:%d: empty key", path, lineNum)
		}

		// Уже установленная переменная окружения имеет приоритет.
		if os.Getenv(key) != "" {
			continue
		}

		if err := os.Setenv(key, value); err != nil {
			return fmt.Errorf("dotenv %s:%d: setenv %s: %w", path, lineNum, key, err)
		}
	}

	return scanner.Err()
}

// unquote убирает обрамляющие одинарные или двойные кавычки если они совпадают.
func unquote(s string) string {
	if len(s) >= 2 {
		if (s[0] == '"' && s[len(s)-1] == '"') ||
			(s[0] == '\'' && s[len(s)-1] == '\'') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
