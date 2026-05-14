package out

import (
	"context"

	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

// TokenIssuer — out-port для выпуска JWT Access Token и публикации публичных ключей.
//
// Две ответственности объединены в одном порту намеренно:
// обе операции работают с одним и тем же набором RSA-ключей.
// Разделение в отдельный KeyStore-порт создало бы лишнюю абстракцию
// без доменной ценности (YAGNI).
//
// Инварианты Issue:
//   - алгоритм подписи: RS256 (фиксировано, адаптер не может переопределить)
//   - kid выбирается адаптером из текущего активного ключа
//   - jti генерируется адаптером как UUID v4;
//     при коллизии повторяется до consts.JTIMaxRetries раз,
//     после чего возвращает ErrJTIGenerationFailed
//   - exp = iat + consts.JWTExpirySeconds (30 дней, фиксировано)
//   - iss = consts.JWTIssuer, aud = consts.JWTAudience
//   - claims в токене: sub, role, jti, session_id, rev, exp, iat, iss, aud, kid
//
// Инварианты GetPublicKeys:
//   - возвращает только активные (не отозванные) ключи
//   - результат кешируется адаптером (TTL = consts.JWKSCacheTTLSeconds)
//   - пустой срез недопустим — адаптер возвращает ErrJWKSKeysEmpty
//
// Доменный слой не знает об алгоритме подписи, ключах и формате токена.
// Все детали JWT — инфраструктурный артефакт адаптера.
type TokenIssuer interface {
	// Issue подписывает JWT и возвращает (accessToken, jti, error).
	//
	// accountID, role, sessionID — доменные идентификаторы из Account агрегата.
	// rev — текущая revision аккаунта для mass-revoke проверки.
	//
	// Ошибки:
	//   - ErrJTIGenerationFailed — исчерпаны consts.JTIMaxRetries попыток генерации уникального jti
	Issue(
		ctx context.Context,
		accountID string,
		role string,
		sessionID string,
		rev int64,
	) (accessToken string, jti string, err error)

	// GetPublicKeys возвращает срез активных RSA публичных ключей в виде JWKSKey VO.
	//
	// Используется TokenService.GetJWKS для формирования ответа
	// GET /iam/.well-known/jwks.json.
	//
	// Ошибки:
	//   - ErrJWKSKeysEmpty — активных ключей нет (ошибка конфигурации)
	GetPublicKeys(ctx context.Context) ([]valobj.JWKSKey, error)
}
