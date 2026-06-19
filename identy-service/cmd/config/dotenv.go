package config

import "github.com/Reddetk/SayMoDev/identy-service/internal/dotenv"

// LoadDotEnv -- обёртка для обратной совместимости.
// Весь новый код использует internal/dotenv.Load напрямую.
func LoadDotEnv(path string) error {
	return dotenv.Load(path)
}
