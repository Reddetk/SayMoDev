package core

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/port/in"
	"github.com/Reddetk/SayMoDev/identy-service/port/out"
)

var tokenTracer = otel.Tracer("iam.tokenService")

// TokenService реализует in-порты TokenOperator и TokenValidator.
//
// Изолированная ответственность: выпуск, отзыв и валидация токенов.
// Не знает о AuthService, SessionService или AccountService.
//
// Зависимости (out-порты):
//   - TokenIssuer           -- Issue + Verify (RS256) + GetPublicKeys (JWKS)
//   - TokenBlacklist        -- Add (L2 запись) + Contains (L1->L2 чтение) + GetAccountRev (L2)
//   - AccountEventsProducer -- AccessTokenRevoked
//
// G9 Write Order (blacklist):
//
//	[1] tokenBlacklist.Add   -- Redis L2
//	[2] AccessTokenRevoked   -- outbox → PostgreSQL L3 (асинхронно)
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
// Вызывается только из AuthService после успешного SaveSessionWithTx.
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
//	[1] tokenBlacklist.Add (L2 Redis)  -- блокирующее действие; ошибка == откат
//	[2] AccessTokenRevoked event       -- fire-and-forget; outbox worker → L3
//
// reason: "password_changed" | "account_locked" | "admin_revoke"
// expiresAtUnix: exp claim; адаптер вычислит TTL как (exp - now).
// TTL <= 0 -- Add является no-op (токен уже истёк).
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

	if err := s.tokenBlacklist.Add(ctx, jti, expiresAtUnix); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "blacklist write failed")
		return corerr.ErrTokenRevoked
	}

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
//	[2] valobj.NewJWKSResponse    -- строит VO, валидирует непустоту среза
//
// Fail-closed: пустой срез или ошибка адаптера → ErrJWKSKeysEmpty → HTTP 503.
func (s *TokenService) GetJWKS(ctx context.Context) ([]string, error) {
	ctx, span := tokenTracer.Start(ctx, "TokenService.GetJWKS")
	defer span.End()

	keys, err := s.tokenIssuer.GetPublicKeys(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get public keys failed")
		return nil, corerr.ErrJWKSKeysEmpty
	}

	span.SetAttributes(attribute.Int("token.jwks_key_count", len(keys)))
	return keys, nil
}

// ValidateToken верифицирует rawToken и возвращает AuthContext.
//
// Реализует in-порт TokenValidator. Вызывается исключительно из JWTMiddleware.
//
// Цепочка (BC#1 Token Validation Flow):
//
//	[1] tokenIssuer.Verify          -- RS256 подпись, exp, iss, aud, kid
//	[2] tokenBlacklist.Contains     -- jti blacklist L1->L2 (fail-closed)
//	[3] tokenBlacklist.GetAccountRev -- account:rev L2 mass-revoke check (fail-closed)
//	[4] valobj.NewAuthContext        -- UUID-валидация accountID/sessionID, rev >= 1
//
// Fail-closed semantics:
//
//	Redis недоступен на шаге 2 или 3 → ErrTokenRevoked (401).
//	ErrJWKSKeysEmpty пробрасывается as-is → HTTP 503 в middleware.
func (s *TokenService) ValidateToken(
	ctx context.Context,
	rawToken string,
) (in.AuthContext, error) {
	ctx, span := tokenTracer.Start(ctx, "TokenService.ValidateToken")
	defer span.End()

	// [1] RS256 верификация + распарсинг claims
	accountID, role, sessionID, jti, rev, _, err := s.tokenIssuer.Verify(ctx, rawToken)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "token verification failed")
		if err == corerr.ErrJWKSKeysEmpty {
			return in.AuthContext{}, corerr.ErrJWKSKeysEmpty
		}
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	span.SetAttributes(
		attribute.String("token.account_id", accountID),
		attribute.String("token.jti", jti),
		attribute.Int64("token.rev", rev),
	)

	// [2] jti blacklist check -- fail-closed
	blacklisted, err := s.tokenBlacklist.Contains(ctx, jti)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "blacklist check failed")
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}
	if blacklisted {
		span.SetStatus(codes.Error, "token is blacklisted")
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	// [3] account rev check -- mass-revoke (смена пароля, блокировка, удаление аккаунта)
	// Spec BC#1 Step 3 L2: account:rev > token.rev → 401
	// Fail-closed: Redis недоступен → ErrTokenRevoked
	accountRev, err := s.tokenBlacklist.GetAccountRev(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "account rev check failed")
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}
	if accountRev > rev {
		span.SetAttributes(attribute.Int64("token.account_rev", accountRev))
		span.SetStatus(codes.Error, "token rev outdated (mass-revoked)")
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	// [4] Сборка AuthContext -- UUID и rev валидируются внутри конструктора
	parsedRole, err := valobj.ParseRole(role)
	if err != nil {
		span.RecordError(err)
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	authCtx, err := valobj.NewAuthContext(accountID, parsedRole, sessionID, rev)
	if err != nil {
		span.RecordError(err)
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	return valobj.MapAuthContext(authCtx), nil
}
