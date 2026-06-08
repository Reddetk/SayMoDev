package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// CORSConfig -- CORS policy parameters for the primary adapter.
//
// Injected from cmd at RouterDeps construction.
// Zero value (CORSConfig{}) -- CORS disabled (all preflight -> 403).
//
// AllowedOrigins: list of allowed origins.
//   Wildcard "*" is allowed only if AllowCredentials = false.
//   Empty slice -- blocks all cross-origin requests.
//
// AllowCredentials: true required for Bearer tokens from the browser.
//   With true, wildcard "*" in AllowedOrigins is not allowed -- browser blocks.
//
// MaxAge: preflight response cache time in seconds.
//   0 -- browser does not cache (each request sends OPTIONS).
//   Recommended: 600 (10 min).
//
// Metrics: cors_requests_total, cors_rejected_total, cors_preflight_requests_total
// записываются в ObservabilityMiddleware.handle (до c.Next()).
// NewCORSMiddleware не регистрирует счётчики -- это зона ответственности
// ObservabilityMiddleware, чтобы избежать duplicate registration паники.
type CORSConfig struct {
	AllowedOrigins   []string
	AllowedMethods   []string
	AllowedHeaders   []string
	ExposedHeaders   []string
	AllowCredentials bool
	MaxAge           int
}

// NewCORSMiddleware builds gin.HandlerFunc implementing the CORS policy
// from the provided CORSConfig.
//
// Order in router.go: first, before ObservabilityMiddleware and JWTMiddleware.
// Reason: preflight OPTIONS must not pass through JWT validation.
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

		// Not a cross-origin request -- pass without CORS headers.
		if origin == "" {
			c.Next()
			return
		}

		_, allowed := allowedOriginSet[origin]
		if !allowed {
			// Origin not in whitelist.
			// Metrics: corsRejectedTotal инкрементируется в ObservabilityMiddleware.
			if c.Request.Method == http.MethodOptions {
				c.AbortWithStatus(http.StatusForbidden)
				return
			}
			c.Next()
			return
		}

		// Origin allowed -- set CORS headers.
		c.Header("Access-Control-Allow-Origin", origin)
		c.Header("Vary", "Origin")

		if cfg.AllowCredentials {
			c.Header("Access-Control-Allow-Credentials", "true")
		}
		if exposeHeaders != "" {
			c.Header("Access-Control-Expose-Headers", exposeHeaders)
		}

		// Preflight OPTIONS -- respond and abort chain.
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
