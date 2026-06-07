package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig -- параметры политики CORS для первичного адаптера.
//
// Инжектируется из cmd при построении RouterDeps.
// Нулевое значение (CORSConfig{}) -- CORS отключён (все preflight -> 403).
//
// AllowedOrigins: список разрешённых origin.
//   Wildcard "*" допустим только если AllowCredentials = false.
//   Пустой срез -- запрещает все cross-origin запросы.
//
// AllowCredentials: true обязателен для Bearer-токенов из браузера.
//   При true wildcard "*" в AllowedOrigins недопустим -- браузер блокирует.
//
// MaxAge: время кеширования preflight-ответа в секундах.
//   0 -- браузер не кеширует (каждый запрос делает OPTIONS).
//   Рекомендуемое значение: 600 (10 мин).
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

// NewCORSMiddleware строит gin.HandlerFunc реализующий политику CORS
// по переданному CORSConfig.
//
// Порядок применения в router.go: первым, до gin.Recovery() и JWTMiddleware.
// Причина: preflight OPTIONS не должен проходить через JWT-валидацию.
//
// Реализация не использует внешних зависимостей -- только net/http и strings.
func NewCORSMiddleware(cfg CORSConfig) gin.HandlerFunc {
	allowedOriginSet := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allowedOriginSet[o] = struct{}{}
	}

	allowMethods := strings.Join(cfg.AllowedMethods, ", ")
	allowHeaders := strings.Join(cfg.AllowedHeaders, ", ")
	exposeHeaders := strings.Join(cfg.ExposedHeaders, ", ")
	maxAge := strconv.Itoa(cfg.MaxAge)

	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")

		// Не cross-origin запрос -- пропускаем без CORS-заголовков.
		if origin == "" {
			c.Next()
			return
		}

		// Проверяем разрешён ли origin.
		_, allowed := allowedOriginSet[origin]
		if !allowed {
			// Origin не в whitelist -- отвечаем без Access-Control-Allow-Origin.
			// Браузер заблокирует запрос самостоятельно.
			// Preflight завершаем явно чтобы не пропустить в цепочку.
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		// Origin разрешён -- выставляем CORS-заголовки.
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Vary", "Origin")

		if cfg.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		if exposeHeaders != "" {
			c.Header("Access-Control-Expose-Headers", exposeHeaders)
		}

		// Preflight OPTIONS -- отвечаем и прерываем цепочку.
		if c.Request.Method == http.MethodOptions {
			if allowMethods != "" {
				c.Header("Access-Control-Allow-Methods", allowMethods)
			}
			if allowHeaders != "" {
				c.Header("Access-Control-Allow-Headers", allowHeaders)
			}
			if cfg.MaxAge > 0 {
				c.Header("Access-Control-Max-Age", maxAge)
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}

		c.Next()
	}
}
