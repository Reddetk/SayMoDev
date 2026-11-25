// goca:manual — claims BC#1 (iss/aud/role/rev); шаблон генерирует свои claims.
package auth

import "github.com/golang-jwt/jwt/v5"

const (
	// Issuer — iss BC#1 (ADR-003, Token Validation Flow).
	Issuer = "urn:saymo:identity-service"
	// Audience — aud платформенного API.
	Audience = "api.saymo"
	// ClockSkew — допустимое расхождение часов.
	ClockSkew = 30 // seconds
)

// Claims — payload JWT BC#1. Регистрационные claims встроены.
type Claims struct {
	Role      string `json:"role"`
	SessionID string `json:"session_id"`
	Rev       int    `json:"rev"`
	jwt.RegisteredClaims
}
