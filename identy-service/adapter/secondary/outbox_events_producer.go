// Package secondary contains outbound adapter implementations.
package secondary

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/zap"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	outboxInsertSQL = `
		INSERT INTO outbox (id, event_type, payload, partition_key, created_at)
		VALUES ($1, $2, $3, $4, NOW())`

	tracerName = "identy-service/adapter/outbox"
)

// event_type constants -- matched to spec table.
const (
	evtAccountRegistered             = "account.registered"
	evtAccountEmailVerified          = "account.email_verified"
	evtAccountLockedByFailedAttempts = "account.locked.failed_attempts"
	evtAccountLockedByAdmin          = "account.locked.admin"
	evtAccountUnlocked               = "account.unlocked"
	evtAccessTokenRevoked            = "token.revoked"
	evtSessionCreated                = "session.created"
	evtSessionTerminated             = "session.terminated"
	evtSessionTerminatedByAdmin      = "session.terminated.admin"
	evtAccountPasswordChanged        = "account.password_changed"
	evtAccountPasswordResetCompleted = "account.password_reset"
	evtAccountPersonalDataUpdated    = "account.data_updated"
	evtAccountDeleted                = "account.deleted"
)

// ---------------------------------------------------------------------------
// OutboxEventsProducer
// ---------------------------------------------------------------------------

// OutboxEventsProducer implements port/out.AccountEventsProducer.
//
// All methods INSERT a single row into the outbox table inside the caller's
// transaction (pgx.Tx must be stored on ctx via TxFromContext). If no
// transaction is found on ctx, the adapter falls back to the pool for
// non-transactional contexts (audit-only events).
//
// The outbox worker reads and publishes to Kafka asynchronously.
// This adapter never touches Kafka directly.
type OutboxEventsProducer struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
	tracer trace.Tracer
}

// NewOutboxEventsProducer constructs the adapter.
func NewOutboxEventsProducer(pool *pgxpool.Pool, logger *zap.Logger) (*OutboxEventsProducer, error) {
	if pool == nil {
		return nil, fmt.Errorf("outbox events producer: pool is required")
	}
	if logger == nil {
		logger = zap.NewNop()
	}
	return &OutboxEventsProducer{
		pool:   pool,
		logger: logger,
		tracer: otel.Tracer(tracerName),
	}, nil
}

// ---------------------------------------------------------------------------
// Registration events
// ---------------------------------------------------------------------------

// AccountRegistered inserts account.registered into outbox.
//
// Invariant: if role == patient, classifier must be non-zero.
// Fails with ErrOutboxUnavailable on INSERT error.
func (p *OutboxEventsProducer) AccountRegistered(
	ctx context.Context,
	accountID string,
	role valobj.Role,
	classifier valobj.Classifier,
	registrationMethod string,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccountRegistered",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.String("account.role", role.String()),
			attribute.String("registration.method", registrationMethod),
		),
	)
	defer span.End()

	payload := map[string]any{
		"role":               role.String(),
		"registrationMethod": registrationMethod,
	}
	// Classifier is mandatory for patient role (spec invariant).
	// Always include it in payload regardless of role to keep consumer logic simple.
	payload["classifier"] = map[string]any{
		"difficulty":   classifier.Difficulty(),
		"aphasia_type": classifier.AphasiaType(),
	}

	if err := p.insert(ctx, evtAccountRegistered, accountID, payload); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		p.logger.Error("outbox: AccountRegistered insert failed",
			zap.String("account_id", accountID),
			zap.String("event_type", evtAccountRegistered),
			zap.Error(err),
		)
		return corerr.ErrOutboxUnavailable
	}

	p.logger.Info("outbox: event enqueued",
		zap.String("event_type", evtAccountRegistered),
		zap.String("account_id", accountID),
	)
	return nil
}

// AccountEmailVerified inserts account.email_verified into outbox.
func (p *OutboxEventsProducer) AccountEmailVerified(
	ctx context.Context,
	accountID string,
	verifiedAt int64,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccountEmailVerified",
		trace.WithAttributes(attribute.String("account.id", accountID)),
	)
	defer span.End()

	payload := map[string]any{
		"verifiedAt": verifiedAt,
	}
	return p.insertWithLogging(ctx, span, evtAccountEmailVerified, accountID, payload)
}

// ---------------------------------------------------------------------------
// Account locking events
// ---------------------------------------------------------------------------

// AccountLockedByFailedAttempts inserts account.locked.failed_attempts into outbox.
// lockedUntil is nil for permanent locks.
func (p *OutboxEventsProducer) AccountLockedByFailedAttempts(
	ctx context.Context,
	accountID string,
	lockedUntil *int64,
	attemptsCount int,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccountLockedByFailedAttempts",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.Int("attempts.count", attemptsCount),
		),
	)
	defer span.End()

	payload := map[string]any{
		"lockedUntil":   lockedUntil,
		"attemptsCount": attemptsCount,
	}
	return p.insertWithLogging(ctx, span, evtAccountLockedByFailedAttempts, accountID, payload)
}

// AccountLockedByAdmin inserts account.locked.admin into outbox.
func (p *OutboxEventsProducer) AccountLockedByAdmin(
	ctx context.Context,
	accountID string,
	lockedUntil *int64,
	adminID string,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccountLockedByAdmin",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.String("admin.id", adminID),
		),
	)
	defer span.End()

	payload := map[string]any{
		"lockedUntil": lockedUntil,
		"adminID":     adminID,
	}
	return p.insertWithLogging(ctx, span, evtAccountLockedByAdmin, accountID, payload)
}

// AccountUnlocked inserts account.unlocked into outbox.
func (p *OutboxEventsProducer) AccountUnlocked(
	ctx context.Context,
	accountID string,
	unlockedAt int64,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccountUnlocked",
		trace.WithAttributes(attribute.String("account.id", accountID)),
	)
	defer span.End()

	payload := map[string]any{
		"unlockedAt": unlockedAt,
	}
	return p.insertWithLogging(ctx, span, evtAccountUnlocked, accountID, payload)
}

// ---------------------------------------------------------------------------
// Token / session events
// ---------------------------------------------------------------------------

// AccessTokenRevoked inserts token.revoked into outbox.
// reason examples: "password_changed", "account_locked", "admin_revoke".
func (p *OutboxEventsProducer) AccessTokenRevoked(
	ctx context.Context,
	accountID string,
	revision int64,
	revokedAt int64,
	reason string,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccessTokenRevoked",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.String("revoke.reason", reason),
			attribute.Int64("account.revision", revision),
		),
	)
	defer span.End()

	payload := map[string]any{
		"revision":  revision,
		"revokedAt": revokedAt,
		"reason":    reason,
	}
	return p.insertWithLogging(ctx, span, evtAccessTokenRevoked, accountID, payload)
}

// SessionCreated inserts session.created into outbox.
// Security: fingerprint is logged at INFO level for audit; it is not PII.
func (p *OutboxEventsProducer) SessionCreated(
	ctx context.Context,
	accountID string,
	sessionID string,
	fingerprint string,
	createdAt int64,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.SessionCreated",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.String("session.id", sessionID),
		),
	)
	defer span.End()

	payload := map[string]any{
		"sessionID":   sessionID,
		"fingerprint": fingerprint,
		"createdAt":   createdAt,
	}
	return p.insertWithLogging(ctx, span, evtSessionCreated, accountID, payload)
}

// SessionTerminated inserts session.terminated into outbox.
// Initiator: user (logout) or system (LRU eviction on 6th login).
func (p *OutboxEventsProducer) SessionTerminated(
	ctx context.Context,
	accountID string,
	sessionID string,
	terminatedAt int64,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.SessionTerminated",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.String("session.id", sessionID),
		),
	)
	defer span.End()

	payload := map[string]any{
		"sessionID":    sessionID,
		"terminatedAt": terminatedAt,
	}
	return p.insertWithLogging(ctx, span, evtSessionTerminated, accountID, payload)
}

// SessionTerminatedByAdmin inserts session.terminated.admin into outbox.
// adminID is required for compliance audit trail (ADR-001).
func (p *OutboxEventsProducer) SessionTerminatedByAdmin(
	ctx context.Context,
	accountID string,
	sessionID string,
	adminID string,
	terminatedAt int64,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.SessionTerminatedByAdmin",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.String("session.id", sessionID),
			attribute.String("admin.id", adminID),
		),
	)
	defer span.End()

	payload := map[string]any{
		"sessionID":    sessionID,
		"adminID":      adminID,
		"terminatedAt": terminatedAt,
	}
	return p.insertWithLogging(ctx, span, evtSessionTerminatedByAdmin, accountID, payload)
}

// ---------------------------------------------------------------------------
// Password events
// ---------------------------------------------------------------------------

// AccountPasswordChanged inserts account.password_changed into outbox.
func (p *OutboxEventsProducer) AccountPasswordChanged(
	ctx context.Context,
	accountID string,
	changedAt int64,
	historyCount int,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccountPasswordChanged",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.Int("password.history_count", historyCount),
		),
	)
	defer span.End()

	payload := map[string]any{
		"changedAt":    changedAt,
		"historyCount": historyCount,
	}
	return p.insertWithLogging(ctx, span, evtAccountPasswordChanged, accountID, payload)
}

// AccountPasswordResetCompleted inserts account.password_reset into outbox.
func (p *OutboxEventsProducer) AccountPasswordResetCompleted(
	ctx context.Context,
	accountID string,
	resetCompletedAt int64,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccountPasswordResetCompleted",
		trace.WithAttributes(attribute.String("account.id", accountID)),
	)
	defer span.End()

	payload := map[string]any{
		"resetCompletedAt": resetCompletedAt,
	}
	return p.insertWithLogging(ctx, span, evtAccountPasswordResetCompleted, accountID, payload)
}

// ---------------------------------------------------------------------------
// Profile events
// ---------------------------------------------------------------------------

// AccountPersonalDataUpdated inserts account.data_updated into outbox.
// changedFields is a slice of field names; actorID is accountID or adminID of initiator.
func (p *OutboxEventsProducer) AccountPersonalDataUpdated(
	ctx context.Context,
	accountID string,
	changedFields []string,
	actorID string,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccountPersonalDataUpdated",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.String("actor.id", actorID),
			attribute.Int("changed_fields.count", len(changedFields)),
		),
	)
	defer span.End()

	payload := map[string]any{
		"changedFields": changedFields,
		"actorID":       actorID,
	}
	return p.insertWithLogging(ctx, span, evtAccountPersonalDataUpdated, accountID, payload)
}

// ---------------------------------------------------------------------------
// Account deletion
// ---------------------------------------------------------------------------

// AccountDeleted inserts account.deleted into outbox.
// Mandatory for financial and medical data compliance (GDPR cascade).
func (p *OutboxEventsProducer) AccountDeleted(
	ctx context.Context,
	accountID string,
	deletedAt int64,
	adminID string,
) error {
	ctx, span := p.tracer.Start(ctx, "outbox.AccountDeleted",
		trace.WithAttributes(
			attribute.String("account.id", accountID),
			attribute.String("admin.id", adminID),
		),
	)
	defer span.End()

	payload := map[string]any{
		"deletedAt": deletedAt,
		"adminID":   adminID,
	}
	return p.insertWithLogging(ctx, span, evtAccountDeleted, accountID, payload)
}

// ---------------------------------------------------------------------------
// Core insert logic
// ---------------------------------------------------------------------------

// insert executes the outbox INSERT using a transaction from ctx (if present)
// or the pool directly.
//
// Tracing rule: span and error logging are the caller's responsibility.
// insert only returns a raw error; wrapping to ErrOutboxUnavailable is done
// by the public method (insertWithLogging wraps both).
func (p *OutboxEventsProducer) insert(
	ctx context.Context,
	eventType string,
	partitionKey string,
	payload map[string]any,
) error {
	rawPayload, err := json.Marshal(payload)
	if err != nil {
		// json.Marshal on map[string]any fails only on non-serialisable types
		// (channels, functions). That is a programmer error, not a runtime fault.
		return fmt.Errorf("outbox: marshal payload for %s: %w", eventType, err)
	}

	eventID := uuid.New().String()

	querier := p.querier(ctx)
	_, err = querier.Exec(ctx, outboxInsertSQL, eventID, eventType, rawPayload, partitionKey)
	return err
}

// insertWithLogging is the standard public-method wrapper:
//  1. calls insert
//  2. on error: records span error, logs ERROR, returns ErrOutboxUnavailable
//  3. on success: logs INFO
func (p *OutboxEventsProducer) insertWithLogging(
	ctx context.Context,
	span trace.Span,
	eventType string,
	partitionKey string,
	payload map[string]any,
) error {
	if err := p.insert(ctx, eventType, partitionKey, payload); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		p.logger.Error("outbox: insert failed",
			zap.String("event_type", eventType),
			zap.String("partition_key", partitionKey),
			zap.Error(err),
		)
		return corerr.ErrOutboxUnavailable
	}

	p.logger.Info("outbox: event enqueued",
		zap.String("event_type", eventType),
		zap.String("partition_key", partitionKey),
	)
	return nil
}

// ---------------------------------------------------------------------------
// Transaction extraction
// ---------------------------------------------------------------------------

// outboxQuerier is the minimal interface satisfied by both pgx.Tx and *pgxpool.Pool.
type outboxQuerier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// txContextKey is the unexported key type used to store pgx.Tx on context.
type txContextKey struct{}

// TxToContext stores tx on ctx for retrieval by OutboxEventsProducer.
// Call this in the repository layer before invoking any event producer method.
func TxToContext(ctx context.Context, tx pgx.Tx) context.Context {
	return context.WithValue(ctx, txContextKey{}, tx)
}

// querier returns the transaction from ctx if present, otherwise falls back
// to the pool. This allows the outbox INSERT to participate in the caller's
// transaction without the adapter requiring an explicit tx parameter.
func (p *OutboxEventsProducer) querier(ctx context.Context) outboxQuerier {
	if tx, ok := ctx.Value(txContextKey{}).(pgx.Tx); ok && tx != nil {
		return tx
	}
	return p.pool
}

// ---------------------------------------------------------------------------
// Compile-time interface check
// ---------------------------------------------------------------------------

var _ interface {
	AccountRegistered(ctx context.Context, accountID string, role valobj.Role, classifier valobj.Classifier, registrationMethod string) error
	AccountEmailVerified(ctx context.Context, accountID string, verifiedAt int64) error
	AccountLockedByFailedAttempts(ctx context.Context, accountID string, lockedUntil *int64, attemptsCount int) error
	AccountLockedByAdmin(ctx context.Context, accountID string, lockedUntil *int64, adminID string) error
	AccountUnlocked(ctx context.Context, accountID string, unlockedAt int64) error
	AccessTokenRevoked(ctx context.Context, accountID string, revision int64, revokedAt int64, reason string) error
	SessionCreated(ctx context.Context, accountID string, sessionID string, fingerprint string, createdAt int64) error
	SessionTerminated(ctx context.Context, accountID string, sessionID string, terminatedAt int64) error
	SessionTerminatedByAdmin(ctx context.Context, accountID string, sessionID string, adminID string, terminatedAt int64) error
	AccountPasswordChanged(ctx context.Context, accountID string, changedAt int64, historyCount int) error
	AccountPasswordResetCompleted(ctx context.Context, accountID string, resetCompletedAt int64) error
	AccountPersonalDataUpdated(ctx context.Context, accountID string, changedFields []string, actorID string) error
	AccountDeleted(ctx context.Context, accountID string, deletedAt int64, adminID string) error
} = (*OutboxEventsProducer)(nil)
