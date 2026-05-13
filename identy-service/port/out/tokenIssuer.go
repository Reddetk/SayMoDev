package out

import "context"

// TokenIssuer -- out-port для выпуска JWT Access Token
//
// Инварианты:
//   - алгоритм подписи: RS256
//   - kid выбирается адаптером из текущего активного ключа
//   - jti генерируется адаптером как UUID v4 с retry до consts.JTIMaxRetries раз
//   - exp = iat + consts.JWTExpirySeconds (30 дней, фиксировано)
//   - iss = consts.JWTIssuer, aud = consts.JWTAudience
//
// Доменный слой не знает об алгоритме подписи, ключах и формате токена.
// Все детали JWT -- инфраструктурный артефакт адаптера.
type TokenIssuer interface {
	// Issue подписывает и возвращает (accessToken, jti, error)
	// accountID, role, sessionID -- доменные идентификаторы из Account агрегата
	// rev -- текущая revision аккаунта для mass-revoke проверки
	Issue(
		ctx context.Context,
		accountID string,
		role string,
		sessionID string,
		rev int64,
	) (accessToken string, jti string, err error)
}
