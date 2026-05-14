package core

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/port/out"
)

var tokenTracer = otel.Tracer("iam.tokenService")

// TokenService реализует in-port TokenOperator.
//
// Изолированная ответственность: выпуск и отзыв токенов.
// Не знает о AuthService, SessionService или AccountService.
//
// Зависимости (out-порты):
//   - TokenIssuer     -- Issue (RS256 подпись, jti retry) + GetPublicKeys (JWKS)
//   - TokenBlacklist  -- Add (L2 Redis)
//   - AccountEventsProducer -- AccessTokenRevoked
//
// G9 Write Order (blacklist):
//
//	[1] tokenBlacklist.Add   -- Redis L2
//	[2] AccessTokenRevoked   -- outbox → PostgreSQL L3 (асинхронно через отдельный out-порт)
//
// Замечание по G9: в этом сервисе L2 пишется первым
// (отличие от SessionService/AccountService, где L3 атомарно через DeleteSessionWithTx).
// RevokeToken может вызываться автономно (mass-revoke через AccountService)
// без удаления сессии -- поэтому L3 запись происходит как fire-and-forget
// через событие с outbox-доставкой.
type TokenService struct {
	tokenIssuer    out.TokenIssuer
	tokenBlacklist out.TokenBlacklist
	eventsProducer out.AccountEventsProducer
}

func NewTokenService(
	tokenIssuer out.TokenIssuer,
	tokenBlacklist out.TokenBlacklist,
	eventsProducer out.AccountEventsProducer,
) *TokenService {
	return &TokenService{
		tokenIssuer:    tokenIssuer,
		tokenBlacklist: tokenBlacklist,
		eventsProducer: eventsProducer,
	}
}

// IssueToken выпускает JWT Access Token.
//
// Цепочка:
//
//	[1] tokenIssuer.Issue -- RS256, kid из активного ключа, jti retry до JTIMaxRetries
//
// TokenService не знает о сессии. AuthService вызывает IssueToken
// после успешного SaveSessionWithTx -- порядок гарантирует handler.
func (s *TokenService) IssueToken(
	ctx context.Context,
	accountID string,
	role string,
	sessionID string,
	rev int64,
) (accessToken string, jti string, err error) {
	ctx, span := tokenTracer.Start(ctx, "TokenService.IssueToken")
	defer span.End()

	span.SetAttributes(
		attribute.String("token.account_id", accountID),
		attribute.String("token.session_id", sessionID),
		attribute.Int64("token.rev", rev),
	)

	accessToken, jti, err = s.tokenIssuer.Issue(ctx, accountID, role, sessionID, rev)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "token issue failed")
		return "", "", err
	}

	span.SetAttributes(attribute.String("token.jti", jti))
	return accessToken, jti, nil
}

// RevokeToken заносит jti в blacklist и публикует AccessTokenRevoked.
//
// G9 Write Order:
//
//	[1] tokenBlacklist.Add (L2 Redis)   -- блокирующее действие; ошибка == откат операции
//	[2] AccessTokenRevoked event        -- fire-and-forget; outbox worker доставит в L3
//
// reason: "password_changed" | "account_locked" | "admin_revoke" | ...
// expiresAtUnix: exp claim токена -- адаптер вычислит TTL как (exp - now).
// если TTL <= 0 — tokenBlacklist.Add является no-op (токен уже истёк).
func (s *TokenService) RevokeToken(
	ctx context.Context,
	accountID string,
	jti string,
	expiresAtUnix int64,
	revision int64,
	reason string,
) error {
	ctx, span := tokenTracer.Start(ctx, "TokenService.RevokeToken")
	defer span.End()

	span.SetAttributes(
		attribute.String("token.account_id", accountID),
		attribute.String("token.jti", jti),
		attribute.String("token.revoke_reason", reason),
	)

	// [1] L2 Redis -- блокирующее действие.
	// Ошибка L2 -- жёсткий отказ: токен останется валидным до истечения, если L2 недоступен.
	// Отличие от SessionService: здесь нет синхронной L3-записи через DeleteSessionWithTx,
	// поэтому L2-ошибка -- основание для отката.
	if err := s.tokenBlacklist.Add(ctx, jti, expiresAtUnix); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "blacklist write failed")
		return corerr.ErrTokenRevoked
	}

	// [2] fire-and-forget: outbox worker доставит в L3.
	// Ошибка публикации не отменяет запись в L2 -- токен уже заблокирован.
	if evErr := s.eventsProducer.AccessTokenRevoked(
		ctx,
		accountID,
		revision,
		time.Now().UnixMilli(),
		reason,
	); evErr != nil {
		span.RecordError(evErr)
	}

	return nil
}

// GetJWKS возвращает JWKSResponse с активными RSA-публичными ключами.
//
// Цепочка:
//
//	[1] tokenIssuer.GetPublicKeys -- адаптер возвращает кешированный срез []JWKSKey
//	[2] valobj.NewJWKSResponse   -- строит VO, валидирует непустоту среза
//
// Fail-closed: пустой срез или ошибка адаптера → ErrJWKSKeysEmpty → HTTP 503.
// Кеширование (TTL=consts.JWKSCacheTTLSeconds) — ответственность адаптера.
func (s *TokenService) GetJWKS(ctx context.Context) (valobj.JWKSResponse, error) {
	ctx, span := tokenTracer.Start(ctx, "TokenService.GetJWKS")
	defer span.End()

	keys, err := s.tokenIssuer.GetPublicKeys(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get public keys failed")
		return valobj.JWKSResponse{}, corerr.ErrJWKSKeysEmpty
	}

	resp, err := valobj.NewJWKSResponse(keys)
	if err != nil {
		// GetPublicKeys вернул пустой срез без ошибки -- нарушение контракта адаптера
		span.RecordError(err)
		span.SetStatus(codes.Error, "empty keys slice from adapter")
		return valobj.JWKSResponse{}, corerr.ErrJWKSKeysEmpty
	}

	span.SetAttributes(attribute.Int("token.jwks_key_count", len(keys)))
	return resp, nil
}
