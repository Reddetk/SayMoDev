package in

import (
	context "context"
)

// TokenValidator -- in-port для JWTMiddleware.
//
// Единственная точка входа для валидации токена в primary-адаптере.
// Мидлвейр не знает о JWKS, RS256, Redis или парсинге JWT.
//
// Реализует TokenService.
//
// Цепочка внутри TokenService.ValidateToken:
//
//	[1] tokenIssuer.Verify      -- RS256, exp, iss, aud, kid (re-fetch при неизвестном kid)
//	[2] tokenBlacklist.Contains -- jti blacklist (L1->L2), fail-closed
//	[3] valobj.NewAuthContext    -- сборка и валидация AuthContext VO
//
// Ошибки (middleware маппирует в HTTP статус):
//   - ErrTokenRevoked  -- подпись невалидна, exp истёк, jti в blacklist --> HTTP 401
//   - ErrJWKSKeysEmpty -- kid не найден после re-fetch                  --> HTTP 503
type TokenValidator interface {
	// ValidateToken верифицирует rawToken и возвращает AuthContext.
	//
	// rawToken -- Bearer значение без префикса "Bearer ".
	// AuthContext записывается middleware в c.Keys["аутхCонтехт"] для handler'a.
	ValidateToken(ctx context.Context, rawToken string) (AuthContext, error)
}

type AuthContext struct {
	AccountID string
	Role      string
	SessionID string
	Rev       int64
}
