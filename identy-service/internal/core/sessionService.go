package core

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/internal/logger"
	"github.com/Reddetk/SayMoDev/identy-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/identy-service/internal/port/out"
)

var sessTracer = otel.Tracer("iam.sessionService")

type SessionService struct {
	accRep         out.AccountRepository
	tokenBlacklist out.TokenBlacklist
	eventsProducer out.AccountEventsProducer
	logger         logger.Logger
}

func NewSessionService(
	accRep out.AccountRepository,
	tokenBlacklist out.TokenBlacklist,
	eventsProducer out.AccountEventsProducer,
	log logger.Logger,
) *SessionService {
	return &SessionService{
		accRep:         accRep,
		tokenBlacklist: tokenBlacklist,
		eventsProducer: eventsProducer,
		logger:         log,
	}
}

func (s *SessionService) Logout(
	ctx context.Context,
	accountID string,
	sessionID string,
) error {
	ctx, span := sessTracer.Start(ctx, "SessionService.Logout")
	defer span.End()

	log := s.logger.With(
		logger.String("op", "logout"),
		logger.String("account_id", accountID),
		logger.String("session_id", sessionID),
	)

	span.SetAttributes(
		attribute.String("session.account_id", accountID),
		attribute.String("session.session_id", sessionID),
	)

	account, err := s.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "account not found")
		log.Warn("session.logout: account not found", logger.Error(err))
		return corerr.ErrAccountNotFound
	}

	revokedJTI, err := account.RevokeSession(sessionID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "session not found")
		log.Warn("session.logout: RevokeSession failed (session already gone?)", logger.Error(err))
		return err
	}

	if err := s.accRep.DeleteSessionWithTx(ctx, account, revokedJTI); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete session failed")
		log.Error("session.logout: DeleteSessionWithTx failed", logger.Error(err))
		return err
	}

	if blErr := s.tokenBlacklist.Add(ctx, revokedJTI, 0); blErr != nil {
		span.RecordError(blErr)
		log.Warn("session.logout: blacklist L2 Add failed (outbox will retry)",
			logger.String("revoked_jti", revokedJTI),
			logger.Error(blErr),
		)
	}

	span.SetAttributes(attribute.String("session.revoked_jti", revokedJTI))

	if evErr := s.eventsProducer.SessionTerminated(
		ctx,
		accountID,
		sessionID,
		time.Now().UnixMilli(),
	); evErr != nil {
		span.RecordError(evErr)
		log.Warn("session.logout: SessionTerminated event failed (non-fatal)", logger.Error(evErr))
	}

	log.Info("session.logout: session terminated", logger.String("revoked_jti", revokedJTI))
	return nil
}

func (s *SessionService) AdminTerminateSession(
	ctx context.Context,
	targetAccountID string,
	sessionID string,
	adminID string,
) error {
	ctx, span := sessTracer.Start(ctx, "SessionService.AdminTerminateSession")
	defer span.End()

	log := s.logger.With(
		logger.String("op", "adminTerminateSession"),
		logger.String("target_account_id", targetAccountID),
		logger.String("session_id", sessionID),
		logger.String("admin_id", adminID),
	)

	span.SetAttributes(
		attribute.String("session.target_account_id", targetAccountID),
		attribute.String("session.session_id", sessionID),
		attribute.String("session.admin_id", adminID),
	)

	account, err := s.accRep.FindByAccountID(ctx, targetAccountID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "account not found")
		log.Warn("session.adminTerminate: account not found", logger.Error(err))
		return corerr.ErrAccountNotFound
	}

	revokedJTI, err := account.RevokeSession(sessionID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "session not found")
		log.Warn("session.adminTerminate: RevokeSession failed", logger.Error(err))
		return err
	}

	if err := s.accRep.DeleteSessionWithTx(ctx, account, revokedJTI); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete session failed")
		log.Error("session.adminTerminate: DeleteSessionWithTx failed", logger.Error(err))
		return err
	}

	if blErr := s.tokenBlacklist.Add(ctx, revokedJTI, 0); blErr != nil {
		span.RecordError(blErr)
		log.Warn("session.adminTerminate: blacklist L2 Add failed (outbox will retry)",
			logger.String("revoked_jti", revokedJTI),
			logger.Error(blErr),
		)
	}

	span.SetAttributes(attribute.String("session.revoked_jti", revokedJTI))

	if evErr := s.eventsProducer.SessionTerminatedByAdmin(
		ctx,
		targetAccountID,
		sessionID,
		adminID,
		time.Now().UnixMilli(),
	); evErr != nil {
		span.RecordError(evErr)
		log.Warn("session.adminTerminate: SessionTerminatedByAdmin event failed (non-fatal)", logger.Error(evErr))
	}

	log.Info("session.adminTerminate: session terminated by admin",
		logger.String("revoked_jti", revokedJTI),
	)
	return nil
}

func (s *SessionService) AdminGetSessions(ctx context.Context, AccID string) ([]in.SessionDTO, error) {
	ctx, span := sessTracer.Start(ctx, "SessionService.AdminGetSessions")
	defer span.End()

	acc, err := s.accRep.FindByAccountID(ctx, AccID)
	if err != nil {
		s.logger.Warn("session.adminGetSessions: account not found",
			logger.String("account_id", AccID),
			logger.Error(err),
		)
		return nil, err
	}
	return acc.SessionsDTO(), nil
}
