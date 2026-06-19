package core

import (
	"context"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/logger"
	"github.com/Reddetk/SayMoDev/identy-service/port/in"
	"github.com/Reddetk/SayMoDev/identy-service/port/out"
)

var sessTracer = otel.Tracer("iam.sessionService")

// SessionService реализует управление жизненным циклом сессии после её создания.
//
// Не выпускает токены — это ответственность AuthService.
// Не знает о AuthService, AccountService или OtpService.
//
// Инварианты:
//   - Максимум 5 concurrent сессий на аккаунт — соблюдается в entity.Account.OpenSession();
//     этот сервис не проверяет и не дублирует логику.
//   - Pessimistic lock при eviction — ответственность адаптера (SELECT ... FOR UPDATE в Postgres).
//   - JTI blacklist: G9 Write Order — outbox (L3) через DeleteSessionWithTx,
//     Redis L2 — через tokenBlacklist.Add после успешного коммита.
//
// Юз-кейсы:
//   - POST /iam/auth/logout               → Logout
//   - DELETE /iam/admin/sessions/:id      → AdminTerminateSession
//
// События:
//   - SessionTerminated       — logout
//   - SessionTerminatedByAdmin — admin-initiated termination
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

// Logout реализует POST /iam/auth/logout.
//
// Цепочка (ADR-001, G9 Write Order):
//
//	[1] FindByAccountID — загрузить агрегат со всеми сессиями
//	[2] account.RevokeSession — удалить сессию с агрегата, получить jti
//	[3] DeleteSessionWithTx — ACID: DELETE session + INSERT blacklist outbox (L3)
//	[4] TokenBlacklist.Add — Redis L2 после успешного коммита (G9)
//	[5] SessionTerminated event — fire-and-forget
//
// accountID и sessionID поступают из AuthContext JWT, валидированного middleware.
func (s *SessionService) Logout(
	ctx context.Context,
	accountID string,
	sessionID string,
) error {
	ctx, span := sessTracer.Start(ctx, "SessionService.Logout")
	defer span.End()

	span.SetAttributes(
		attribute.String("session.account_id", accountID),
		attribute.String("session.session_id", sessionID),
	)

	account, err := s.accRep.FindByAccountID(ctx, accountID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "account not found")
		return corerr.ErrAccountNotFound
	}

	// [2] Удаляем сессию с агрегата — получаем jti для blacklist
	revokedJTI, err := account.RevokeSession(sessionID)
	if err != nil {
		// ErrSessionNotFound: сессия уже завершена или не принадлежит аккаунту — idempotent logout
		span.RecordError(err)
		span.SetStatus(codes.Error, "session not found")
		return err
	}

	// [3] ACID: DELETE session + outbox blacklist (L3)
	if err := s.accRep.DeleteSessionWithTx(ctx, account, revokedJTI); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete session failed")
		return err
	}

	// [4] G9 Write Order: L2 Redis после успешного коммита L3
	if blErr := s.tokenBlacklist.Add(ctx, revokedJTI, 0); blErr != nil {
		// WARN: outbox (шаг 3) уже гарантирует L3; L2 недоступен — не блокируем ответ
		span.RecordError(blErr)
	}

	span.SetAttributes(attribute.String("session.revoked_jti", revokedJTI))

	// [5] fire-and-forget: ошибка публикации не блокирует ответ клиенту
	if evErr := s.eventsProducer.SessionTerminated(
		ctx,
		accountID,
		sessionID,
		time.Now().UnixMilli(),
	); evErr != nil {
		span.RecordError(evErr)
	}

	return nil
}

// AdminTerminateSession реализует DELETE /iam/admin/sessions/:id.
//
// Цепочка идентична Logout, отличается:
//   - accountID целевого аккаунта (ане adminID из AuthContext) передаётся из path-параметра хандлера
//   - событие SessionTerminatedByAdmin (ADR-001) содержит adminID
//
// adminID — UUID администратора из AuthContext JWT запроса; хандлер вложил его в вызов.
// targetAccountID — владелец сессии; хандлер резолвит его из path-параметра sessionID через sessions-lookup.
func (s *SessionService) AdminTerminateSession(
	ctx context.Context,
	targetAccountID string,
	sessionID string,
	adminID string,
) error {
	ctx, span := sessTracer.Start(ctx, "SessionService.AdminTerminateSession")
	defer span.End()

	span.SetAttributes(
		attribute.String("session.target_account_id", targetAccountID),
		attribute.String("session.session_id", sessionID),
		attribute.String("session.admin_id", adminID),
	)

	// [1] Загрузить целевой агрегат, не агрегат администратора
	account, err := s.accRep.FindByAccountID(ctx, targetAccountID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "account not found")
		return corerr.ErrAccountNotFound
	}

	// [2] Удалить сессию с агрегата
	revokedJTI, err := account.RevokeSession(sessionID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "session not found")
		return err
	}

	// [3] ACID: DELETE session + outbox blacklist (L3)
	if err := s.accRep.DeleteSessionWithTx(ctx, account, revokedJTI); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "delete session failed")
		return err
	}

	// [4] G9 Write Order: Redis L2 после L3
	if blErr := s.tokenBlacklist.Add(ctx, revokedJTI, 0); blErr != nil {
		span.RecordError(blErr)
	}

	span.SetAttributes(attribute.String("session.revoked_jti", revokedJTI))

	// [5] fire-and-forget: SessionTerminatedByAdmin (ADR-001) — несёт adminID
	if evErr := s.eventsProducer.SessionTerminatedByAdmin(
		ctx,
		targetAccountID,
		sessionID,
		adminID,
		time.Now().UnixMilli(),
	); evErr != nil {
		span.RecordError(evErr)
	}

	return nil
}

func (s *SessionService) AdminGetSessions(ctx context.Context, AccID string) ([]in.SessionDTO, error) {
	ctx, span := sessTracer.Start(ctx, "SessionService.AdminGetSessions")
	defer span.End()

	acc, err := s.accRep.FindByAccountID(ctx, AccID)
	if err != nil {
		return nil, err
	}
	return acc.SessionsDTO(), nil
}
