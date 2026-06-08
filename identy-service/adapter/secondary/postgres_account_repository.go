// Package secondary contains outbound adapter implementations.
package secondary

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/trace"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	"github.com/Reddetk/SayMoDev/identy-service/core/entity"
	out "github.com/Reddetk/SayMoDev/identy-service/port/out"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

// ---------------------------------------------------------------------------
// Constants
// ---------------------------------------------------------------------------

const (
	repoTracerName     = "identy-service/adapter/postgres-account-repo"
	pgUniqueViolation  = "23505"
)

// outbox event_type constants -- kept local; OutboxEventsProducer owns the
// canonical list. Duplicated here because the repository writes directly into
// the outbox table inside its own transactions (no interface indirection).
const (
	repoEvtAccountRegistered    = "account.registered"
	repoEvtEmailVerified        = "account.email_verified"
	repoEvtTokenRevoked         = "token.revoked"
	repoEvtAccountDeleted       = "account.deleted"
)

// ---------------------------------------------------------------------------
// Struct
// ---------------------------------------------------------------------------

// PostgresAccountRepository implements port/out.AccountRepository.
// All write methods open their own pgx transaction.
// The outbox INSERT is executed inside the same transaction as the business
// operation -- the outbox worker replicates to Kafka asynchronously.
type PostgresAccountRepository struct {
	pool   *pgxpool.Pool
	logger *slog.Logger
	tracer trace.Tracer
}

func NewPostgresAccountRepository(pool *pgxpool.Pool, logger *slog.Logger) (*PostgresAccountRepository, error) {
	if pool == nil {
		return nil, fmt.Errorf("postgres account repo: pool is required")
	}
	if logger == nil {
		logger = slog.Default()
	}
	return &PostgresAccountRepository{
		pool:   pool,
		logger: logger,
		tracer: otel.Tracer(repoTracerName),
	}, nil
}

// compile-time interface check
var _ out.AccountRepository = (*PostgresAccountRepository)(nil)

// ---------------------------------------------------------------------------
// Internal helpers
// ---------------------------------------------------------------------------

// pgxQuerier is the minimal interface satisfied by pgx.Tx and *pgxpool.Pool.
type pgxQuerier interface {
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
}

// rollbackOnError should be deferred immediately after tx.Begin.
// Rolls back if the transaction was not yet committed (pgx.ErrTxClosed means
// Commit already ran). Does not overwrite the originating error.
func rollbackOnError(ctx context.Context, tx pgx.Tx, logger *slog.Logger) {
	if err := tx.Rollback(ctx); err != nil && !errors.Is(err, pgx.ErrTxClosed) {
		logger.WarnContext(ctx, "repo: rollback failed", slog.String("error", err.Error()))
	}
}

// repoOutboxInsert writes one row to the outbox table inside tx.
// Kept package-private; not related to OutboxEventsProducer (different concern).
func repoOutboxInsert(
	ctx context.Context,
	tx pgx.Tx,
	eventType string,
	partitionKey string,
	payload map[string]any,
) error {
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("repoOutboxInsert marshal %s: %w", eventType, err)
	}
	const sql = `
		INSERT INTO outbox (id, event_type, payload, partition_key, created_at)
		VALUES ($1, $2, $3, $4, NOW())`
	_, err = tx.Exec(ctx, sql, uuid.New().String(), eventType, data, partitionKey)
	return err
}

// emailDomain returns the domain part of an email for use in trace attributes.
// Avoids logging PII (full email address).
func emailDomain(email string) string {
	for i := len(email) - 1; i >= 0; i-- {
		if email[i] == '@' {
			return email[i+1:]
		}
	}
	return "unknown"
}

// ---------------------------------------------------------------------------
// Row scanner
// ---------------------------------------------------------------------------

// scanAccount reads account columns (no sessions, no password_history).
// Used by findForUpdate, findAndHydrate.
func (r *PostgresAccountRepository) scanAccount(row pgx.Row) (*entity.Account, error) {
	var (
		id           string
		email        string
		personalInfo string
		roleStr      string
		statusStr    string
		googleUID    *string
		passwordHash *string
		rev          int64
		lockedUntil  *int64
		createdAt    time.Time
		updatedAt    time.Time
	)
	if err := row.Scan(
		&id, &email, &personalInfo, &roleStr, &statusStr,
		&googleUID, &passwordHash, &rev, &lockedUntil,
		&createdAt, &updatedAt,
	); err != nil {
		return nil, err
	}

	role, err := valobj.ParseRole(roleStr)
	if err != nil {
		return nil, fmt.Errorf("scanAccount: invalid role %q: %w", roleStr, err)
	}
	status, err := valobj.ParseAccountStatus(statusStr)
	if err != nil {
		return nil, fmt.Errorf("scanAccount: invalid status %q: %w", statusStr, err)
	}
	meta := valobj.RestoreMetadata(createdAt, updatedAt)

	return entity.RestoreAccount(
		id, email, personalInfo, role, status,
		googleUID, passwordHash, rev, lockedUntil, meta,
		nil, nil,
	)
}

// ---------------------------------------------------------------------------
// Subsidiary loaders
// ---------------------------------------------------------------------------

const loadSessionsSQL = `
	SELECT session_id, jti, fingerprint, last_activity, created_at
	FROM sessions
	WHERE account_id = $1`

func (r *PostgresAccountRepository) loadSessions(
	ctx context.Context,
	q pgxQuerier,
	accountID string,
) ([]entity.Session, error) {
	rows, err := q.Query(ctx, loadSessionsSQL, accountID)
	if err != nil {
		return nil, fmt.Errorf("loadSessions: %w", err)
	}
	defer rows.Close()

	var sessions []entity.Session
	for rows.Next() {
		var (
			sessionID    string
			jti          string
			fingerprint  string
			lastActivity int64
			createdAt    time.Time
		)
		if err := rows.Scan(&sessionID, &jti, &fingerprint, &lastActivity, &createdAt); err != nil {
			return nil, fmt.Errorf("loadSessions scan: %w", err)
		}
		meta := valobj.RestoreMetadata(createdAt, createdAt)
		s, err := entity.RestoreSession(sessionID, jti, fingerprint, lastActivity, meta)
		if err != nil {
			return nil, fmt.Errorf("loadSessions RestoreSession: %w", err)
		}
		sessions = append(sessions, *s)
	}
	return sessions, rows.Err()
}

const loadPasswordHistorySQL = `
	SELECT hash, created_at
	FROM password_history
	WHERE account_id = $1
	ORDER BY created_at DESC
	LIMIT 5`

func (r *PostgresAccountRepository) loadPasswordHistory(
	ctx context.Context,
	q pgxQuerier,
	accountID string,
) ([]valobj.PasswordEntry, error) {
	rows, err := q.Query(ctx, loadPasswordHistorySQL, accountID)
	if err != nil {
		return nil, fmt.Errorf("loadPasswordHistory: %w", err)
	}
	defer rows.Close()

	var history []valobj.PasswordEntry
	for rows.Next() {
		var (
			hash      string
			createdAt time.Time
		)
		if err := rows.Scan(&hash, &createdAt); err != nil {
			return nil, fmt.Errorf("loadPasswordHistory scan: %w", err)
		}
		history = append(history, valobj.NewPasswordEntry(hash, createdAt))
	}
	return history, rows.Err()
}

// findAndHydrate executes the provided SELECT, then loads sessions and
// password_history, and returns a fully populated aggregate.
func (r *PostgresAccountRepository) findAndHydrate(
	ctx context.Context,
	sql string,
	arg any,
) (*entity.Account, error) {
	row := r.pool.QueryRow(ctx, sql, arg)
	acc, err := r.scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, corerr.ErrAccountNotFound
	}
	if err != nil {
		return nil, err
	}

	sessions, err := r.loadSessions(ctx, r.pool, acc.UUID())
	if err != nil {
		return nil, err
	}
	history, err := r.loadPasswordHistory(ctx, r.pool, acc.UUID())
	if err != nil {
		return nil, err
	}

	return entity.RestoreAccount(
		acc.UUID(), acc.Email(), acc.PersonalInfo(),
		acc.Role(), acc.Status(),
		acc.GoogleUID(), acc.PasswordHash(),
		acc.Revision(), acc.LockedUntil(), acc.Metadata(),
		sessions, history,
	)
}

// ---------------------------------------------------------------------------
// findForUpdate -- internal pessimistic lock
// ---------------------------------------------------------------------------

// findForUpdate executes SELECT ... FOR UPDATE inside tx.
// Not part of the AccountRepository port.
// Sessions and password_history are NOT loaded: WithTx methods only need
// structural fields for rev-check and status assertions.
func (r *PostgresAccountRepository) findForUpdate(
	ctx context.Context,
	accountID string,
	tx pgx.Tx,
) (*entity.Account, error) {
	const sql = `
		SELECT uuid, email, personal_info, role, status,
		       google_uid, password_hash, rev, locked_until,
		       created_at, updated_at
		FROM accounts
		WHERE uuid = $1
		FOR UPDATE`

	row := tx.QueryRow(ctx, sql, accountID)
	acc, err := r.scanAccount(row)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, corerr.ErrAccountNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("findForUpdate: %w", err)
	}
	return acc, nil
}

// ---------------------------------------------------------------------------
// EmailExist
// ---------------------------------------------------------------------------

func (r *PostgresAccountRepository) EmailExist(ctx context.Context, email string) (bool, error) {
	ctx, span := r.tracer.Start(ctx, "repo.EmailExist",
		trace.WithAttributes(attribute.String("email.domain", emailDomain(email))),
	)
	defer span.End()

	const sql = `SELECT 1 FROM accounts WHERE email = $1 LIMIT 1`
	var exists int
	err := r.pool.QueryRow(ctx, sql, email).Scan(&exists)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		r.logger.ErrorContext(ctx, "repo: EmailExist failed", slog.String("error", err.Error()))
		return false, fmt.Errorf("EmailExist: %w", err)
	}
	return true, nil
}

// ---------------------------------------------------------------------------
// FindByEmail / FindByAccountID / FindByGoogleUID
// ---------------------------------------------------------------------------

const findByEmailSQL = `
	SELECT uuid, email, personal_info, role, status,
	       google_uid, password_hash, rev, locked_until,
	       created_at, updated_at
	FROM accounts WHERE email = $1`

func (r *PostgresAccountRepository) FindByEmail(
	ctx context.Context, email string,
) (*entity.Account, error) {
	ctx, span := r.tracer.Start(ctx, "repo.FindByEmail",
		trace.WithAttributes(attribute.String("email.domain", emailDomain(email))),
	)
	defer span.End()

	acc, err := r.findAndHydrate(ctx, findByEmailSQL, email)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		r.logger.ErrorContext(ctx, "repo: FindByEmail failed", slog.String("error", err.Error()))
		return nil, err
	}
	return acc, nil
}

func (r *PostgresAccountRepository) FindByAccountID(
	ctx context.Context, accountID string,
) (*entity.Account, error) {
	ctx, span := r.tracer.Start(ctx, "repo.FindByAccountID",
		trace.WithAttributes(attribute.String("account.id", accountID)),
	)
	defer span.End()

	const sql = `
		SELECT uuid, email, personal_info, role, status,
		       google_uid, password_hash, rev, locked_until,
		       created_at, updated_at
		FROM accounts WHERE uuid = $1`

	acc, err := r.findAndHydrate(ctx, sql, accountID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		r.logger.ErrorContext(ctx, "repo: FindByAccountID failed",
			slog.String("account_id", accountID),
			slog.String("error", err.Error()),
		)
		return nil, err
	}
	return acc, nil
}

func (r *PostgresAccountRepository) FindByGoogleUID(
	ctx context.Context, googleUID string,
) (*entity.Account, error) {
	prefix := googleUID
	if len(prefix) > 8 {
		prefix = prefix[:8]
	}
	ctx, span := r.tracer.Start(ctx, "repo.FindByGoogleUID",
		trace.WithAttributes(attribute.String("google_uid.prefix", prefix)),
	)
	defer span.End()

	const sql = `
		SELECT uuid, email, personal_info, role, status,
		       google_uid, password_hash, rev, locked_until,
		       created_at, updated_at
		FROM accounts WHERE google_uid = $1`

	acc, err := r.findAndHydrate(ctx, sql, googleUID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		r.logger.ErrorContext(ctx, "repo: FindByGoogleUID failed", slog.String("error", err.Error()))
		return nil, err
	}
	return acc, nil
}

// ---------------------------------------------------------------------------
// CreateAccountWithTx
// ---------------------------------------------------------------------------

func (r *PostgresAccountRepository) CreateAccountWithTx(
	ctx context.Context, account *entity.Account,
) (string, error) {
	ctx, span := r.tracer.Start(ctx, "repo.CreateAccountWithTx",
		trace.WithAttributes(
			attribute.String("account.role", account.Role().String()),
			attribute.String("email.domain", emailDomain(account.Email())),
		),
	)
	defer span.End()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("CreateAccountWithTx begin: %w", err)
	}
	defer rollbackOnError(ctx, tx, r.logger)

	// 1. INSERT accounts
	const insertAccountSQL = `
		INSERT INTO accounts
			(uuid, email, personal_info, role, google_uid, password_hash, rev, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NULL, $5, 1, 'active', NOW(), NOW())`

	_, err = tx.Exec(ctx, insertAccountSQL,
		account.UUID(), account.Email(), account.PersonalInfo(),
		account.Role().String(), account.PasswordHash(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			span.SetStatus(codes.Error, "email already exists")
			return "", corerr.ErrEmailAlreadyExists
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return "", fmt.Errorf("CreateAccountWithTx insert account: %w", err)
	}

	// 2. INSERT password_history (only for email/password accounts)
	if account.PasswordHash() != nil {
		const insertHistorySQL = `
			INSERT INTO password_history (account_id, hash, created_at)
			VALUES ($1, $2, NOW())`
		if _, err = tx.Exec(ctx, insertHistorySQL, account.UUID(), *account.PasswordHash()); err != nil {
			span.RecordError(err)
			return "", fmt.Errorf("CreateAccountWithTx insert history: %w", err)
		}
	}

	// 3. Outbox: account.registered
	if err = repoOutboxInsert(ctx, tx, repoEvtAccountRegistered, account.UUID(), map[string]any{
		"role":               account.Role().String(),
		"registrationMethod": "email",
		"classifier":         nil,
	}); err != nil {
		span.RecordError(err)
		return "", fmt.Errorf("CreateAccountWithTx outbox registered: %w", err)
	}

	// 4. Outbox: account.email_verified
	if err = repoOutboxInsert(ctx, tx, repoEvtEmailVerified, account.UUID(), map[string]any{
		"verifiedAt": time.Now().UnixMilli(),
	}); err != nil {
		span.RecordError(err)
		return "", fmt.Errorf("CreateAccountWithTx outbox email_verified: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("CreateAccountWithTx commit: %w", err)
	}

	r.logger.InfoContext(ctx, "repo: account created",
		slog.String("account_id", account.UUID()),
		slog.String("role", account.Role().String()),
	)
	return account.UUID(), nil
}

// ---------------------------------------------------------------------------
// SaveSessionWithTx
// ---------------------------------------------------------------------------

func (r *PostgresAccountRepository) SaveSessionWithTx(
	ctx context.Context, account *entity.Account,
) error {
	ctx, span := r.tracer.Start(ctx, "repo.SaveSessionWithTx",
		trace.WithAttributes(attribute.String("account.id", account.UUID())),
	)
	defer span.End()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("SaveSessionWithTx begin: %w", err)
	}
	defer rollbackOnError(ctx, tx, r.logger)

	// 1. Pessimistic lock
	if _, err = r.findForUpdate(ctx, account.UUID(), tx); err != nil {
		span.RecordError(err)
		return fmt.Errorf("SaveSessionWithTx lock: %w", err)
	}

	// 2. Replace sessions: delete all, re-insert current set (max 5 rows).
	// Simpler than per-row upsert given the small cardinality bound.
	const deleteSessions = `DELETE FROM sessions WHERE account_id = $1`
	if _, err = tx.Exec(ctx, deleteSessions, account.UUID()); err != nil {
		span.RecordError(err)
		return fmt.Errorf("SaveSessionWithTx delete sessions: %w", err)
	}

	const insertSession = `
		INSERT INTO sessions (session_id, account_id, jti, fingerprint, last_activity, created_at)
		VALUES ($1, $2, $3, $4, $5, $6)`
	for _, s := range account.Sessions() {
		if _, err = tx.Exec(ctx, insertSession,
			s.SessionID(), account.UUID(), s.JTI(),
			s.Fingerprint(), s.LastActivity(), s.Metadata().CreatedAt(),
		); err != nil {
			span.RecordError(err)
			return fmt.Errorf("SaveSessionWithTx insert session %s: %w", s.SessionID(), err)
		}
	}

	// 3. G9: evicted JTI -> outbox L3 blacklist
	if evicted := account.EvictedJTI(); evicted != "" {
		if err = repoOutboxInsert(ctx, tx, repoEvtTokenRevoked, account.UUID(), map[string]any{
			"revision":  account.Revision(),
			"revokedAt": time.Now().UnixMilli(),
			"reason":    "session_evicted",
			"jti":       evicted,
		}); err != nil {
			span.RecordError(err)
			return fmt.Errorf("SaveSessionWithTx outbox eviction: %w", err)
		}
	}

	// 4. Touch account metadata
	const touchAccount = `UPDATE accounts SET updated_at = NOW() WHERE uuid = $1`
	if _, err = tx.Exec(ctx, touchAccount, account.UUID()); err != nil {
		span.RecordError(err)
		return fmt.Errorf("SaveSessionWithTx touch account: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("SaveSessionWithTx commit: %w", err)
	}

	// Clear read-once field after successful commit.
	account.ClearEvictedJTI()

	r.logger.InfoContext(ctx, "repo: session saved",
		slog.String("account_id", account.UUID()),
		slog.Int("sessions_count", len(account.Sessions())),
	)
	return nil
}

// ---------------------------------------------------------------------------
// DeleteSessionWithTx
// ---------------------------------------------------------------------------

func (r *PostgresAccountRepository) DeleteSessionWithTx(
	ctx context.Context,
	account *entity.Account,
	revokedJTI string,
) error {
	ctx, span := r.tracer.Start(ctx, "repo.DeleteSessionWithTx",
		trace.WithAttributes(attribute.String("account.id", account.UUID())),
	)
	defer span.End()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("DeleteSessionWithTx begin: %w", err)
	}
	defer rollbackOnError(ctx, tx, r.logger)

	// 1. DELETE session by jti (jti is unique; account_id for safety)
	const deleteSQL = `DELETE FROM sessions WHERE jti = $1 AND account_id = $2`
	if _, err = tx.Exec(ctx, deleteSQL, revokedJTI, account.UUID()); err != nil {
		span.RecordError(err)
		return fmt.Errorf("DeleteSessionWithTx delete: %w", err)
	}

	// 2. Outbox: token.revoked for L3 blacklist
	if err = repoOutboxInsert(ctx, tx, repoEvtTokenRevoked, account.UUID(), map[string]any{
		"revision":  account.Revision(),
		"revokedAt": time.Now().UnixMilli(),
		"reason":    "logout",
		"jti":       revokedJTI,
	}); err != nil {
		span.RecordError(err)
		return fmt.Errorf("DeleteSessionWithTx outbox: %w", err)
	}

	// 3. Touch account metadata
	const touchSQL = `UPDATE accounts SET updated_at = NOW() WHERE uuid = $1`
	if _, err = tx.Exec(ctx, touchSQL, account.UUID()); err != nil {
		span.RecordError(err)
		return fmt.Errorf("DeleteSessionWithTx touch: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("DeleteSessionWithTx commit: %w", err)
	}

	r.logger.InfoContext(ctx, "repo: session deleted",
		slog.String("account_id", account.UUID()),
	)
	return nil
}

// ---------------------------------------------------------------------------
// UpdateAccountStatusTx
// ---------------------------------------------------------------------------

// UpdateAccountStatusTx atomically updates account status, revokes all
// sessions in the blacklist, and -- when status=deleted -- enqueues
// account.deleted into the outbox.
//
// actorID is the UUID of the initiating actor (adminID or the account itself).
// Required for account.deleted payload compliance; empty string accepted for
// lock/unlock operations.
func (r *PostgresAccountRepository) UpdateAccountStatusTx(
	ctx context.Context,
	account *entity.Account,
	revokedJTIs []string,
	actorID string,
) error {
	ctx, span := r.tracer.Start(ctx, "repo.UpdateAccountStatusTx",
		trace.WithAttributes(
			attribute.String("account.id", account.UUID()),
			attribute.String("account.status", account.Status().String()),
			attribute.Int("revoked_jtis.count", len(revokedJTIs)),
		),
	)
	defer span.End()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("UpdateAccountStatusTx begin: %w", err)
	}
	defer rollbackOnError(ctx, tx, r.logger)

	// 1. UPDATE accounts status + rev
	const updateSQL = `
		UPDATE accounts
		SET status = $1, locked_until = $2, rev = $3, updated_at = NOW()
		WHERE uuid = $4`
	if _, err = tx.Exec(ctx, updateSQL,
		account.Status().String(), account.LockedUntil(),
		account.Revision(), account.UUID(),
	); err != nil {
		span.RecordError(err)
		return fmt.Errorf("UpdateAccountStatusTx update account: %w", err)
	}

	// 2. DELETE sessions (entity already cleared them; DB must match)
	const deleteSessionsSQL = `DELETE FROM sessions WHERE account_id = $1`
	if _, err = tx.Exec(ctx, deleteSessionsSQL, account.UUID()); err != nil {
		span.RecordError(err)
		return fmt.Errorf("UpdateAccountStatusTx delete sessions: %w", err)
	}

	// 3. Bulk outbox: token.revoked per JTI
	now := time.Now().UnixMilli()
	for _, jti := range revokedJTIs {
		if err = repoOutboxInsert(ctx, tx, repoEvtTokenRevoked, account.UUID(), map[string]any{
			"revision":  account.Revision(),
			"revokedAt": now,
			"reason":    "account_status_changed",
			"jti":       jti,
		}); err != nil {
			span.RecordError(err)
			return fmt.Errorf("UpdateAccountStatusTx outbox revoke jti=%s: %w", jti, err)
		}
	}

	// 4. Additional outbox event for soft-delete
	if account.Status() == valobj.StatusDeleted {
		if err = repoOutboxInsert(ctx, tx, repoEvtAccountDeleted, account.UUID(), map[string]any{
			"deletedAt": now,
			"adminID":   actorID,
		}); err != nil {
			span.RecordError(err)
			return fmt.Errorf("UpdateAccountStatusTx outbox account.deleted: %w", err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("UpdateAccountStatusTx commit: %w", err)
	}

	r.logger.InfoContext(ctx, "repo: account status updated",
		slog.String("account_id", account.UUID()),
		slog.String("status", account.Status().String()),
		slog.Int("revoked_sessions", len(revokedJTIs)),
	)
	return nil
}

// ---------------------------------------------------------------------------
// ResetPassword
// ---------------------------------------------------------------------------

func (r *PostgresAccountRepository) ResetPassword(
	ctx context.Context,
	account *entity.Account,
	newPasswordHash string,
) error {
	ctx, span := r.tracer.Start(ctx, "repo.ResetPassword",
		trace.WithAttributes(attribute.String("account.id", account.UUID())),
	)
	defer span.End()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("ResetPassword begin: %w", err)
	}
	defer rollbackOnError(ctx, tx, r.logger)

	// 1. UPDATE password_hash + rev++
	const updateSQL = `
		UPDATE accounts
		SET password_hash = $1, rev = rev + 1, updated_at = NOW()
		WHERE uuid = $2`
	if _, err = tx.Exec(ctx, updateSQL, newPasswordHash, account.UUID()); err != nil {
		span.RecordError(err)
		return fmt.Errorf("ResetPassword update account: %w", err)
	}

	// 2. Collect JTIs before deleting sessions.
	// Entity already called RevokeAllSessions(); DB sessions still exist.
	const fetchJTIsSQL = `SELECT jti FROM sessions WHERE account_id = $1`
	rows, err := tx.Query(ctx, fetchJTIsSQL, account.UUID())
	if err != nil {
		span.RecordError(err)
		return fmt.Errorf("ResetPassword fetch jtis: %w", err)
	}
	var deletedJTIs []string
	for rows.Next() {
		var jti string
		if scanErr := rows.Scan(&jti); scanErr != nil {
			rows.Close()
			return fmt.Errorf("ResetPassword scan jti: %w", scanErr)
		}
		deletedJTIs = append(deletedJTIs, jti)
	}
	rows.Close()
	if err = rows.Err(); err != nil {
		return fmt.Errorf("ResetPassword rows.Err: %w", err)
	}

	// 3. DELETE sessions
	const deleteSessionsSQL = `DELETE FROM sessions WHERE account_id = $1`
	if _, err = tx.Exec(ctx, deleteSessionsSQL, account.UUID()); err != nil {
		span.RecordError(err)
		return fmt.Errorf("ResetPassword delete sessions: %w", err)
	}

	// 4. INSERT password_history
	const insertHistorySQL = `
		INSERT INTO password_history (account_id, hash, created_at)
		VALUES ($1, $2, NOW())`
	if _, err = tx.Exec(ctx, insertHistorySQL, account.UUID(), newPasswordHash); err != nil {
		span.RecordError(err)
		return fmt.Errorf("ResetPassword insert history: %w", err)
	}

	// 5. Trim: keep only the 5 most recent entries
	const trimHistorySQL = `
		DELETE FROM password_history
		WHERE account_id = $1
		  AND id NOT IN (
		      SELECT id FROM password_history
		      WHERE account_id = $1
		      ORDER BY created_at DESC
		      LIMIT 5
		  )`
	if _, err = tx.Exec(ctx, trimHistorySQL, account.UUID()); err != nil {
		span.RecordError(err)
		return fmt.Errorf("ResetPassword trim history: %w", err)
	}

	// 6. Bulk outbox: token.revoked per deleted JTI
	// rev in payload is account.Revision()+1 because UPDATE ran rev=rev+1 in DB
	// but the in-memory aggregate has not been mutated by this adapter.
	newRev := account.Revision() + 1
	now := time.Now().UnixMilli()
	for _, jti := range deletedJTIs {
		if err = repoOutboxInsert(ctx, tx, repoEvtTokenRevoked, account.UUID(), map[string]any{
			"revision":  newRev,
			"revokedAt": now,
			"reason":    "password_reset",
			"jti":       jti,
		}); err != nil {
			span.RecordError(err)
			return fmt.Errorf("ResetPassword outbox jti=%s: %w", jti, err)
		}
	}

	if err = tx.Commit(ctx); err != nil {
		return fmt.Errorf("ResetPassword commit: %w", err)
	}

	r.logger.InfoContext(ctx, "repo: password reset",
		slog.String("account_id", account.UUID()),
		slog.Int("sessions_revoked", len(deletedJTIs)),
	)
	return nil
}

// ---------------------------------------------------------------------------
// LinkGoogleUID
// ---------------------------------------------------------------------------

func (r *PostgresAccountRepository) LinkGoogleUID(
	ctx context.Context, accountID, googleUID string,
) error {
	ctx, span := r.tracer.Start(ctx, "repo.LinkGoogleUID",
		trace.WithAttributes(attribute.String("account.id", accountID)),
	)
	defer span.End()

	const sql = `
		UPDATE accounts SET google_uid = $1
		WHERE uuid = $2 AND google_uid IS NULL`

	tag, err := r.pool.Exec(ctx, sql, googleUID, accountID)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		r.logger.ErrorContext(ctx, "repo: LinkGoogleUID failed",
			slog.String("account_id", accountID),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("LinkGoogleUID: %w", err)
	}
	// 0 rows affected means google_uid was already set -- no-op per spec.
	if tag.RowsAffected() == 0 {
		r.logger.InfoContext(ctx, "repo: LinkGoogleUID no-op, already linked",
			slog.String("account_id", accountID),
		)
	}
	return nil
}

// ---------------------------------------------------------------------------
// CreateOAuthAccountWithTx
// ---------------------------------------------------------------------------

func (r *PostgresAccountRepository) CreateOAuthAccountWithTx(
	ctx context.Context, account *entity.Account,
) (*entity.Account, error) {
	ctx, span := r.tracer.Start(ctx, "repo.CreateOAuthAccountWithTx",
		trace.WithAttributes(
			attribute.String("account.role", account.Role().String()),
			attribute.String("email.domain", emailDomain(account.Email())),
		),
	)
	defer span.End()

	tx, err := r.pool.Begin(ctx)
	if err != nil {
		return nil, fmt.Errorf("CreateOAuthAccountWithTx begin: %w", err)
	}
	defer rollbackOnError(ctx, tx, r.logger)

	const insertSQL = `
		INSERT INTO accounts
			(uuid, email, personal_info, role, google_uid, password_hash, rev, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, NULL, 1, 'active', NOW(), NOW())`

	_, err = tx.Exec(ctx, insertSQL,
		account.UUID(), account.Email(), account.PersonalInfo(),
		account.Role().String(), account.GoogleUID(),
	)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == pgUniqueViolation {
			span.SetStatus(codes.Error, "email already exists")
			return nil, corerr.ErrEmailAlreadyExists
		}
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		return nil, fmt.Errorf("CreateOAuthAccountWithTx insert: %w", err)
	}

	if err = repoOutboxInsert(ctx, tx, repoEvtAccountRegistered, account.UUID(), map[string]any{
		"role":               account.Role().String(),
		"registrationMethod": "oauth2",
		"classifier":         nil,
	}); err != nil {
		span.RecordError(err)
		return nil, fmt.Errorf("CreateOAuthAccountWithTx outbox: %w", err)
	}

	if err = tx.Commit(ctx); err != nil {
		return nil, fmt.Errorf("CreateOAuthAccountWithTx commit: %w", err)
	}

	r.logger.InfoContext(ctx, "repo: oauth account created",
		slog.String("account_id", account.UUID()),
	)
	return account, nil
}

// ---------------------------------------------------------------------------
// ChangeAccountData
// ---------------------------------------------------------------------------

func (r *PostgresAccountRepository) ChangeAccountData(
	ctx context.Context, account *entity.Account,
) error {
	ctx, span := r.tracer.Start(ctx, "repo.ChangeAccountData",
		trace.WithAttributes(attribute.String("account.id", account.UUID())),
	)
	defer span.End()

	// Updates only personal_info and role.
	// Does not touch: password_hash, rev, sessions, google_uid.
	const sql = `
		UPDATE accounts
		SET personal_info = $1, role = $2, updated_at = NOW()
		WHERE uuid = $3`

	_, err := r.pool.Exec(ctx, sql,
		account.PersonalInfo(), account.Role().String(), account.UUID(),
	)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, err.Error())
		r.logger.ErrorContext(ctx, "repo: ChangeAccountData failed",
			slog.String("account_id", account.UUID()),
			slog.String("error", err.Error()),
		)
		return fmt.Errorf("ChangeAccountData: %w", err)
	}

	r.logger.InfoContext(ctx, "repo: account data changed",
		slog.String("account_id", account.UUID()),
	)
	return nil
}
