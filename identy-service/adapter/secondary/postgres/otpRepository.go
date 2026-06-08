// Package postgres stands for PGX
package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

// ---------------------------------------------------------------------------
// PostgresOtpRepository
// ---------------------------------------------------------------------------

// PostgresOtpRepository реализует порт out.OtpRepository.
//
// Таблица: verification_codes
//   email      TEXT        NOT NULL
//   purpose    TEXT        NOT NULL
//   code_hash  TEXT        NOT NULL  -- SHA256 hex, хеширование выполняет доменный слой
//   expires_at TIMESTAMPTZ NOT NULL
//   created_at TIMESTAMPTZ NOT NULL
//   PRIMARY KEY (email, purpose)  -- или UNIQUE(email, purpose)
//
// Преобразование времени:
//   VO хранит expiresAt как Unix milliseconds (int64).
//   Для PostgreSQL: time.UnixMilli(ms).UTC() <-> t.UnixMilli().
type PostgresOtpRepository struct {
	pool   *pgxpool.Pool
	logger *zap.Logger
}

// NewPostgresOtpRepository создаёт репозиторий с указанным pgx-пулом.
func NewPostgresOtpRepository(pool *pgxpool.Pool, logger *zap.Logger) *PostgresOtpRepository {
	return &PostgresOtpRepository{
		pool:   pool,
		logger: logger,
	}
}

// ---------------------------------------------------------------------------
// Upsert
// ---------------------------------------------------------------------------

// Upsert создаёт или заменяет запись OTP-кода в таблице.
//
// ON CONFLICT (email, purpose) обновляет code_hash, expires_at, created_at.
// Адаптер не хеширует code_hash -- получает уже хешированный хеш из VO.
// OTP plaintext никогда не попадает в логи и в параметры запроса.
func (r *PostgresOtpRepository) Upsert(ctx context.Context, verCode *valobj.VerificationCode) error {
	const query = `
		INSERT INTO verification_codes (email, purpose, code_hash, expires_at, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (email, purpose) DO UPDATE
			SET code_hash  = EXCLUDED.code_hash,
			    expires_at = EXCLUDED.expires_at,
			    created_at = EXCLUDED.created_at`

	expiresAt := time.UnixMilli(verCode.ExpiresAt()).UTC()
	createdAt := time.Now().UTC()

	_, err := r.pool.Exec(ctx, query,
		verCode.Email(),
		verCode.Purpose().String(),
		verCode.Hash(),
		expiresAt,
		createdAt,
	)
	if err != nil {
		return fmt.Errorf("otpRepository.Upsert: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Find
// ---------------------------------------------------------------------------

// Find ищет актуальную (не истекшую) запись OTP-кода.
//
// Отсутствие записи или истекший TTL: возвращает (nil, nil), не ошибку.
// OTP plaintext никогда не логируется.
func (r *PostgresOtpRepository) Find(ctx context.Context, email string, purpose valobj.OTPPurpose) (*valobj.VerificationCode, error) {
	const query = `
		SELECT email, purpose, code_hash, expires_at
		FROM verification_codes
		WHERE email   = $1
		  AND purpose = $2
		  AND expires_at > NOW()`

	var (
		dbEmail     string
		dbPurpose   string
		dbCodeHash  string
		dbExpiresAt time.Time
	)

	err := r.pool.QueryRow(ctx, query, email, purpose.String()).
		Scan(&dbEmail, &dbPurpose, &dbCodeHash, &dbExpiresAt)

	if errors.Is(err, pgx.ErrNoRows) {
		// Запись отсутствует или истекла -- нормальное состояние
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("otpRepository.Find: %w", err)
	}

	parsedPurpose, err := valobj.ParseOTPPurpose(dbPurpose)
	if err != nil {
		return nil, fmt.Errorf("otpRepository.Find: invalid purpose %q in DB: %w", dbPurpose, err)
	}

	// Синтетический id для валидации RestoreVerificationCode.
	// Таблица использует (email, purpose) как натуральный PK;
	// отдельного UUID id нет.
	syntheticID := dbEmail + ":" + dbPurpose

	vc, err := valobj.RestoreVerificationCode(
		syntheticID,
		dbEmail,
		dbCodeHash,
		parsedPurpose,
		dbExpiresAt.UnixMilli(),
	)
	if err != nil {
		return nil, fmt.Errorf("otpRepository.Find: restore failed: %w", err)
	}
	return vc, nil
}

// ---------------------------------------------------------------------------
// CleanUp
// ---------------------------------------------------------------------------

// CleanUp удаляет конкретную запись OTP-кода.
//
// Идемпотентно: если запись уже удалена или не существует -- возвращает nil.
func (r *PostgresOtpRepository) CleanUp(ctx context.Context, verCode *valobj.VerificationCode) error {
	const query = `
		DELETE FROM verification_codes
		WHERE email   = $1
		  AND purpose = $2`

	_, err := r.pool.Exec(ctx, query,
		verCode.Email(),
		verCode.Purpose().String(),
	)
	if err != nil {
		return fmt.Errorf("otpRepository.CleanUp: %w", err)
	}
	// RowsAffected() не проверяем -- DELETE 0 является успехом (идемпотентность)
	return nil
}

// ---------------------------------------------------------------------------
// Immulate TODO
// ---------------------------------------------------------------------------

// Immulate удаляет все истекшие записи OTP-кодов.
//
// Вызывается по расписанию (pg_cron: */30 * * * *), не в request-path.
// Ошибки логируются, не возвращаются клиенту (порт возвращает error, но вызывающий
// scheduler его проглатывает и логирует).
func (r *PostgresOtpRepository) Immulate(ctx context.Context) error {
	const query = `DELETE FROM verification_codes WHERE expires_at < NOW()`

	tag, err := r.pool.Exec(ctx, query)
	if err != nil {
		r.logger.Error("otpRepository.Immulate: failed to delete expired codes")
		return fmt.Errorf("otpRepository.Immulate: %w", err)
	}

	if tag.RowsAffected() > 0 {
		r.logger.Info("otpRepository.Immulate: expired codes deleted rows_deleted :TODO")
	}
	return nil
}

// ---------------------------------------------------------------------------
// Compile-time interface assertion
// ---------------------------------------------------------------------------

var _ interface {
	Upsert(ctx context.Context, verCode *valobj.VerificationCode) error
	Find(ctx context.Context, email string, purpose valobj.OTPPurpose) (*valobj.VerificationCode, error)
	CleanUp(ctx context.Context, verCode *valobj.VerificationCode) error
	Immulate(ctx context.Context) error
} = (*PostgresOtpRepository)(nil)
