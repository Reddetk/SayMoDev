package in

import (
	context "context"

	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

// TokenOperator -- in-port для TokenService.
//
// Покрывает два юз-кейса:
//
//	1. GET /iam/.well-known/jwks.json
//	   Публикация активных RSA-публичных ключей для всех downstream BC.
//	   Ответ кешируется адаптером TTL=consts.JWKSCacheTTLSeconds.
//	   Fail-closed: ErrJWKSKeysEmpty → HTTP 503.
//
//	2. IssueToken / RevokeToken
//	   Вызываются только из AuthService через прямой зависимость,
//	   не через HTTP-хандлер. HTTP-адаптер вызывает только GetJWKS.
//
// Инварианты:
//   - JWT подписывается RS256; kid, jti, exp — ответственность адаптера
//   - jti уникален: retry до consts.JTIMaxRetries — затем ErrJTIGenerationFailed
//   - Blacklist write: Redis L2 → outbox → PostgreSQL L3 (G9 Write Order)
//   - AccessTokenRevoked публикуется атомарно с записью в blacklist
type TokenOperator interface {
	// IssueToken выпускает JWT Access Token и записывает jti через
	// tokenBlacklist.Добавляет запись о вышедших jti в Redis (L2).
	//
	// Возвращает (accessToken, jti, error).
	// Ошибки: ErrJTIGenerationFailed → HTTP 500.
	IssueToken(
		ctx context.Context,
		accountID string,
		role string,
		sessionID string,
		rev int64,
	) (accessToken string, jti string, err error)

	// RevokeToken заносит jti в blacklist (L2 + outbox L3) и
	// публикует событие AccessTokenRevoked.
	//
	// expiresAtUnix — exp claim токена; используется для TTL в Redis.
	// revision — текущая revision аккаунта — включается в тело события.
	// reason — причина отзыва ("password_changed", "account_locked", "admin_revoke").
	RevokeToken(
		ctx context.Context,
		accountID string,
		jti string,
		expiresAtUnix int64,
		revision int64,
		reason string,
	) error

	// GetJWKS возвращает JWKSResponse с активными RSA-публичными ключами.
	// Используется HTTP-адаптером для GET /iam/.well-known/jwks.json.
	// Fail-closed: ErrJWKSKeysEmpty → HTTP 503.
	GetJWKS(ctx context.Context) (valobj.JWKSResponse, error)
}
