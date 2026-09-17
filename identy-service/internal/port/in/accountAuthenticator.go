package in

import (
	context "context"
)

// AccountAuthenticator  in-port для AuthService
// Покрывает юз-кейс POST /iam/auth/login
//
// Контракт:
//   - email и fingerprint передаются как plain string
//   - passwordHash  bcrypt-хеш входного пароля (хеширование на стороне клиента/handler)
//   - clientIP используется для IP-first rate check (шаг [1] до account lookup)
//   - возвращает LoginResult VO при успехе
//   - при ошибке: ErrRateLimitIP, ErrAccountLocked, ErrRateLimitAccount,
//     ErrInvalidCredentials (никогда не раскрывает причину  timing safety)
type AccountAuthenticator interface {
	Login(
		ctx context.Context,
		email string,
		passwordHash string,
		fingerprint string,
		clientIP string,
	) (LoginResult, error)
	InitiateGoogleOAuth(
		ctx context.Context,
		clientIP string,
	) (redirectURL string, state OAuthState, err error)
	HandleGoogleCallback(
		ctx context.Context,
		code string,
		receivedCSRF string,
		storedState OAuthState,
		fingerprint string,
		clientIP string,
	) (LoginResult, error)
}

type LoginResult struct {
	AccessToken string
	AccountID   string
	Role        string
	SessionID   string
}

type OAuthState struct {
	CsrfToken     string
	CodeVerifier  string
	CodeChallenge string
	ExpiresAt     int64
}
