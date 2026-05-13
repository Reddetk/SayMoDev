package out

import (
	"context"

	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
)

// AccountEventsProducer defines outbox events for Account aggregate
// All events are published to outbox (transactional event sourcing pattern)
type AccountEventsProducer interface {
	// Registration events
	// AccountRegistered — Завершение регистрации (Step 2: POST /iam/auth/register или OAuth callback)
	// Triggers BC#4 to create program progress and initialize program by classifier
	// registrationMethod: "email" (from browser localStorage) or "oauth2" (from localStorage or fallback)
	AccountRegistered(
		ctx context.Context,
		accountID string,
		role valobj.Role,
		classifier valobj.Classifier,
		registrationMethod string, // "email" or "oauth2"
	) error

	// AccountEmailVerified — Регистрация завершена, email верифицирован
	// Optional audit event — for analytics/audit only
	AccountEmailVerified(ctx context.Context, accountID string, verifiedAt int64) error

	// Account locking events
	// AccountLockedByFailedAttempts — Превышение лимита логинов
	// BC#2 transitions subscription to suspended
	AccountLockedByFailedAttempts(
		ctx context.Context,
		accountID string,
		lockedUntil *int64,
		attemptsCount int,
	) error

	// AccountLockedByAdmin — Административная блокировка
	// BC#2 transitions subscription to suspended
	AccountLockedByAdmin(
		ctx context.Context,
		accountID string,
		lockedUntil *int64,
		adminID string,
	) error

	// AccountUnlocked — Административная разблокировка
	// BC#2 transitions subscription to active/resumed
	AccountUnlocked(ctx context.Context, accountID string, unlockedAt int64) error

	// Token/Session events
	// AccessTokenRevoked — Смена пароля, блокировка, инкремент rev
	// Updates Redis rev:{accountId}. Tokens with rev < current are rejected
	// Reason: "password_changed", "account_locked", "admin_revoke", etc.
	AccessTokenRevoked(
		ctx context.Context,
		accountID string,
		revision int64,
		revokedAt int64,
		reason string,
	) error

	// SessionCreated — Успешный логин / создание сессии
	// Audit/Analytics event — security logging
	SessionCreated(
		ctx context.Context,
		accountID string,
		sessionID string,
		fingerprint string,
		createdAt int64,
	) error

	// SessionTerminated — Logout, eviction, админ-отзыв
	// Adds JTI to Redis blacklist with TTL = remaining token lifetime
	SessionTerminated(
		ctx context.Context,
		accountID string,
		sessionID string,
		terminatedAt int64,
	) error

	// Password events
	// AccountPasswordChanged — Успешная смена пароля
	// Triggers AccessTokenRevoked. Stores last 5 password hashes
	AccountPasswordChanged(
		ctx context.Context,
		accountID string,
		changedAt int64,
		historyCount int,
	) error

	// AccountPasswordResetCompleted — Подтверждение OTP сброса
	// Audit event — records access recovery
	AccountPasswordResetCompleted(
		ctx context.Context,
		accountID string,
		resetCompletedAt int64,
	) error

	// Profile events
	// AccountPersonalDataUpdated — PATCH /account/profile
	// Audit event — records profile data changes
	// changedFields: comma-separated list of changed field names
	AccountPersonalDataUpdated(
		ctx context.Context,
		accountID string,
		changedFields []string,
		actorID string,
	) error

	// AccountDeleted — DELETE /iam/accounts/{accountId} (admin)
	// Mandatory event for financial and medical data compliance
	// BC#2: (1) recurring_unbind, (2) billing_archive snapshot, (3) physical DELETE subscription + payment_method
	// BC#4: PII anonymisation in lesson_answers, physical DELETE active_programs, accessible_lessons
	// Invariant: unbind → archive → delete
	AccountDeleted(
		ctx context.Context,
		accountID string,
		deletedAt int64,
		adminID string,
	) error


	SessionOpened(ctx context.Context, accountID string, sessionID string) error
}
