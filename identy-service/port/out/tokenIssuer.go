package out

import (
	"context"

	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

// TokenIssuer -- out-port для выпуска JWT Access Token, верификации
// входящих токенов и публикации публичных ключей.
//
// Три ответственности объединены в одном порту намеренно:
// все три операции работают с одним и тем же набором RSA-ключей.
// Разделение в отдельные порты создало бы лишнюю абстракцию (YAGNI).
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
// Инварианты Verify:
//   - проверяет RS256 подпись через ключ из локального JWKS-кеша (kid lookup)
//   - при неизвестном kid -- ONE re-fetch (timeout=consts.JWKSFetchTimeoutSeconds), fail-closed
//   - проверяет exp, iss, aud
//   - rev возвращается as-is из claim;
//     сравнение token.rev с account.rev (массовый отзыв) -- в TokenService
//   - возвращает плоские примитивы; доменные VO собирает TokenService
//
// Инварианты GetPublicKeys:
//   - возвращает только активные (не отозванные) ключи
//   - результат кешируется адаптером (TTL = consts.JWKSCacheTTLSeconds)
//   - пустой срез недопустим -- адаптер возвращает ErrJWKSKeysEmpty
//
// Доменный слой не знает об алгоритме подписи, ключах и формате токена.
// Все детали JWT -- инфраструктурный артефакт адаптера.
type TokenIssuer interface {
	// Issue подписывает JWT и возвращает (accessToken, jti, error).
	//
	// accountID, role, sessionID -- доменные идентификаторы из Account агрегата.
	// rev -- текущая revision аккаунта для mass-revoke проверки.
	//
	// Ошибки:
	//   - ErrJTIGenerationFailed -- исчерпаны consts.JTIMaxRetries попыток генерации уникального jti
	Issue(
		ctx context.Context,
		accountID string,
		role string,
		sessionID string,
		rev int64,
	) (accessToken string, jti string, err error)

	// Verify верифицирует RS256 подпись rawToken и возвращает распарсенные claims.
	//
	// Адаптер проверяет: RS256 подпись, exp, iss, aud.
	// kid извлекается из заголовка токена и используется для поиска ключа в JWKS-кеше.
	// При неизвестном kid -- ONE re-fetch (timeout=consts.JWKSFetchTimeoutSeconds), fail-closed.
	//
	// Возвращает плоские примитивы -- доменные VO (сборка AuthContext) остается за TokenService.
	//
	// Ошибки:
	//   - ErrTokenRevoked  -- подпись невалидна или exp истёк
	//   - ErrJWKSKeysEmpty -- kid не найден после re-fetch (fail-closed, HTTP 503)
	Verify(
		ctx context.Context,
		rawToken string,
	) (accountID, role, sessionID, jti string, rev int64, expiresAt int64, err error)

	// GetPublicKeys возвращает срез активных RSA публичных ключей в виде JWKSKey VO.
	//
	// Используется TokenService.GetJWKS для формирования ответа
	// GET /iam/.well-known/jwks.json.
	//
	// Ошибки:
	//   - ErrJWKSKeysEmpty -- активных ключей нет (ошибка конфигурации)
	GetPublicKeys(ctx context.Context) ([]valobj.JWKSKey, error)
}
