package out

import (
	"context"

	valobj "github.com/Reddetk/SayMoDev/identy-service/internal/core/valObj"
)

// GoogleOAuthProvider — out-порт для взаимодействия с Google OAuth2.
//
// Адаптер выполняет:
//   - генерацию PKCE-параметров (code_verifier, code_challenge, CSRF state)
//   - формирование redirect URL к Google Consent Screen
//   - server-to-server code exchange (POST https://oauth2.googleapis.com/token)
//   - верификацию Google ID token через JWKS (кэш по Cache-Control)
//   - извлечение и валидацию claims (iss, aud, exp, email_verified)
//
// Core не знает о HTTP, JWKS-кэше, client_secret.
// Fail-closed: JWKS недоступен → адаптер возвращает ErrOAuthJWKSUnavailable.
type GoogleOAuthProvider interface {
	// BuildAuthURL генерирует OAuthState (PKCE + CSRF) и формирует redirect URL
	// к Google Consent Screen.
	//
	// state сохраняется адаптером в signed cookie (httpOnly, Secure, Max-Age=600s)
	// или server-side store с привязкой к fingerprint.
	//
	// Spec: IAM OAuth2 flow, шаги [1]–[2].
	BuildAuthURL(ctx context.Context) (redirectURL string, state valobj.OAuthState, err error)

	// ExchangeCode выполняет server-to-server code exchange и верификацию Google ID token.
	//
	// Цепочка (атомарна внутри адаптера):
	//   [1] POST https://oauth2.googleapis.com/token с code + code_verifier (PKCE)
	//   [2] Загрузка/проверка Google JWKS (кэш по Cache-Control, обычно 6h)
	//   [3] RS256 верификация подписи ID token
	//   [4] Проверка claims: iss, aud=client_id, exp, email_verified=true
	//   [5] Возврат GoogleClaims VO
	//
	// Возвращает ErrOAuthJWKSUnavailable если JWKS недоступен (fail-closed).
	// Возвращает ErrOAuthEmailNotVerified если email_verified != true.
	//
	// Spec: IAM OAuth2 flow, шаги [5]–[6].
	ExchangeCode(
		ctx context.Context,
		code string,
		state valobj.OAuthState,
	) (valobj.GoogleClaims, error)

	// ValidateState верифицирует CSRF token из callback против сохранённого state.
	//
	// Возвращает ErrOAuthStateMismatch если токены не совпадают.
	// Возвращает ErrOAuthStateExpired если state истёк (TTL 10 мин).
	//
	// Spec: IAM OAuth2 flow, шаг [5a].
	ValidateState(ctx context.Context, receivedCSRF string, storedState valobj.OAuthState) error
}
