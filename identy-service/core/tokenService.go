package core

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/logger"
	"github.com/Reddetk/SayMoDev/identy-service/port/in"
	"github.com/Reddetk/SayMoDev/identy-service/port/out"
)

var tokenTracer = otel.Tracer("iam.tokenService")

type TokenService struct {
	tokenIssuer    out.TokenIssuer
	tokenBlacklist out.TokenBlacklist
	eventsProducer out.AccountEventsProducer
	logger         logger.Logger
}

func NewTokenService(
	tokenIssuer out.TokenIssuer,
	tokenBlacklist out.TokenBlacklist,
	eventsProducer out.AccountEventsProducer,
	log logger.Logger,
) *TokenService {
	return &TokenService{
		tokenIssuer:    tokenIssuer,
		tokenBlacklist: tokenBlacklist,
		eventsProducer: eventsProducer,
		logger:         log,
	}
}

func (s *TokenService) IssueToken(
	ctx context.Context,
	accountID string,
	role string,
	sessionID string,
	rev int64,
) (accessToken string, jti string, err error) {
	ctx, span := tokenTracer.Start(ctx, "TokenService.IssueToken")
	defer span.End()

	log := s.logger.With(
		logger.String("op", "issueToken"),
		logger.String("account_id", accountID),
		logger.String("session_id", sessionID),
	)

	span.SetAttributes(
		attribute.String("token.account_id", accountID),
		attribute.String("token.session_id", sessionID),
		attribute.Int64("token.rev", rev),
	)

	accessToken, jti, err = s.tokenIssuer.Issue(ctx, accountID, role, sessionID, rev)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "token issue failed")
		log.Error("token.issue: TokenIssuer.Issue failed", logger.Error(err))
		return "", "", err
	}

	span.SetAttributes(attribute.String("token.jti", jti))
	log.Debug("token.issue: token issued", logger.String("jti", jti))
	return accessToken, jti, nil
}

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

	log := s.logger.With(
		logger.String("op", "revokeToken"),
		logger.String("account_id", accountID),
		logger.String("jti", jti),
		logger.String("reason", reason),
	)

	span.SetAttributes(
		attribute.String("token.account_id", accountID),
		attribute.String("token.jti", jti),
		attribute.String("token.revoke_reason", reason),
	)

	if err := s.tokenBlacklist.Add(ctx, jti, expiresAtUnix); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "blacklist write failed")
		log.Error("token.revoke: blacklist L2 Add failed", logger.Error(err))
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
		log.Warn("token.revoke: AccessTokenRevoked event failed (non-fatal)", logger.Error(evErr))
	}

	log.Info("token.revoke: token revoked", logger.String("reason", reason))
	return nil
}

func (s *TokenService) GetJWKS(ctx context.Context) ([]string, error) {
	ctx, span := tokenTracer.Start(ctx, "TokenService.GetJWKS")
	defer span.End()

	keys, err := s.tokenIssuer.GetPublicKeys(ctx)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "get public keys failed")
		s.logger.Error("token.jwks: GetPublicKeys failed", logger.Error(err))
		return nil, corerr.ErrJWKSKeysEmpty
	}

	span.SetAttributes(attribute.Int("token.jwks_key_count", len(keys)))
	s.logger.Debug("token.jwks: keys fetched", logger.Int("count", len(keys)))
	return keys, nil
}

func (s *TokenService) ValidateToken(
	ctx context.Context,
	rawToken string,
) (in.AuthContext, error) {
	ctx, span := tokenTracer.Start(ctx, "TokenService.ValidateToken")
	defer span.End()

	log := s.logger.With(logger.String("op", "validateToken"))

	accountID, role, sessionID, jti, rev, _, err := s.tokenIssuer.Verify(ctx, rawToken)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "token verification failed")
		log.Debug("token.validate: Verify failed", logger.Error(err))
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

	blacklisted, err := s.tokenBlacklist.Contains(ctx, jti)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "blacklist check failed")
		log.Error("token.validate: blacklist Contains failed (fail-closed)",
			logger.String("jti", jti),
			logger.Error(err),
		)
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}
	if blacklisted {
		span.SetStatus(codes.Error, "token is blacklisted")
		log.Debug("token.validate: token is blacklisted", logger.String("jti", jti))
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	accountRev, err := s.tokenBlacklist.GetAccountRev(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "account rev check failed")
		log.Error("token.validate: GetAccountRev failed (fail-closed)",
			logger.String("account_id", accountID),
			logger.Error(err),
		)
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}
	if accountRev > rev {
		span.SetAttributes(attribute.Int64("token.account_rev", accountRev))
		span.SetStatus(codes.Error, "token rev outdated (mass-revoked)")
		log.Debug("token.validate: rev outdated",
			logger.String("account_id", accountID),
			logger.Int64("account_rev", accountRev),
			logger.Int64("token_rev", rev),
		)
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	parsedRole, err := valobj.ParseRole(role)
	if err != nil {
		span.RecordError(err)
		log.Warn("token.validate: ParseRole failed", logger.String("role", role), logger.Error(err))
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	authCtx, err := valobj.NewAuthContext(accountID, parsedRole, sessionID, rev)
	if err != nil {
		span.RecordError(err)
		log.Warn("token.validate: NewAuthContext failed", logger.Error(err))
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	log.Debug("token.validate: token valid",
		logger.String("account_id", accountID),
		logger.String("session_id", sessionID),
	)
	return valobj.MapAuthContext(authCtx), nil
}
