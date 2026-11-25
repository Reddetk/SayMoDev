// goca:manual — JWT/JWKS/RBAC BC#3; генератор goca auth не покрывает RS256+revocation.
package auth

import "errors"

// Sentinel-ошибки аутентификации. Тексты содержат короткие токены,
// по которым строятся 401-ответы и фикстуры.
var (
	ErrMissingToken          = errors.New("token missing")
	ErrMalformed             = errors.New("token malformed")
	ErrExpired               = errors.New("token expired")
	ErrIssuer                = errors.New("token issuer invalid")
	ErrAudience              = errors.New("token audience invalid")
	ErrSignature             = errors.New("token signature invalid")
	ErrKID                   = errors.New("token kid unknown")
	ErrAlg                   = errors.New("token alg not allowed")
	ErrClaims                = errors.New("token claims invalid")
	ErrJWKS                  = errors.New("jwks unavailable")
	ErrRevoked               = errors.New("token revoked")
	ErrRev                   = errors.New("token rev check failed")
	ErrRevocationUnavailable = errors.New("revocation store unavailable (redis)")
)
