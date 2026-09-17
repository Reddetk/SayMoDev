package mocks

import (
	"context"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/internal/core/valObj"
	"github.com/stretchr/testify/mock"
)

// MockAccountEventsProducer is a testify mock for out.AccountEventsProducer.
// Kafka/outbox is never started in unit tests  this mock replaces it entirely.
//
// Two scenarios per method:
//
//	Happy path: Return(nil)
//	Error path:  Return(mocks.ErrOutboxUnavailable)
type MockAccountEventsProducer struct {
	mock.Mock
}

// AccountRegistered fires after successful registration (email or OAuth).
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccountRegistered(
	ctx context.Context,
	accountID string,
	role valobj.Role,
	classifier valobj.Classifier,
	registrationMethod string,
) error {
	args := m.Called(ctx, accountID, role, classifier, registrationMethod)
	return args.Error(0)
}

// AccountEmailVerified fires after email verification is confirmed.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccountEmailVerified(
	ctx context.Context,
	accountID string,
	verifiedAt int64,
) error {
	args := m.Called(ctx, accountID, verifiedAt)
	return args.Error(0)
}

// AccountLockedByFailedAttempts fires when login attempt limit is exceeded.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccountLockedByFailedAttempts(
	ctx context.Context,
	accountID string,
	lockedUntil *int64,
	attemptsCount int,
) error {
	args := m.Called(ctx, accountID, lockedUntil, attemptsCount)
	return args.Error(0)
}

// AccountLockedByAdmin fires on administrative account lock.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccountLockedByAdmin(
	ctx context.Context,
	accountID string,
	lockedUntil *int64,
	adminID string,
) error {
	args := m.Called(ctx, accountID, lockedUntil, adminID)
	return args.Error(0)
}

// AccountUnlocked fires on administrative account unlock.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccountUnlocked(
	ctx context.Context,
	accountID string,
	unlockedAt int64,
) error {
	args := m.Called(ctx, accountID, unlockedAt)
	return args.Error(0)
}

// AccessTokenRevoked fires when all tokens for an account are invalidated
// (password change, lock, admin revoke).
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccessTokenRevoked(
	ctx context.Context,
	accountID string,
	revision int64,
	revokedAt int64,
	reason string,
) error {
	args := m.Called(ctx, accountID, revision, revokedAt, reason)
	return args.Error(0)
}

// SessionCreated fires after a successful login or session open.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) SessionCreated(
	ctx context.Context,
	accountID string,
	sessionID string,
	fingerprint string,
	createdAt int64,
) error {
	args := m.Called(ctx, accountID, sessionID, fingerprint, createdAt)
	return args.Error(0)
}

// SessionTerminated fires on logout or LRU eviction (system-initiated).
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) SessionTerminated(
	ctx context.Context,
	accountID string,
	sessionID string,
	terminatedAt int64,
) error {
	args := m.Called(ctx, accountID, sessionID, terminatedAt)
	return args.Error(0)
}

// SessionTerminatedByAdmin fires on admin-initiated session termination.
// Carries adminID for audit trail  separate from SessionTerminated per ADR-001.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) SessionTerminatedByAdmin(
	ctx context.Context,
	accountID string,
	sessionID string,
	adminID string,
	terminatedAt int64,
) error {
	args := m.Called(ctx, accountID, sessionID, adminID, terminatedAt)
	return args.Error(0)
}

// AccountPasswordChanged fires after a successful self-service password change.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccountPasswordChanged(
	ctx context.Context,
	accountID string,
	changedAt int64,
	historyCount int,
) error {
	args := m.Called(ctx, accountID, changedAt, historyCount)
	return args.Error(0)
}

// AccountPasswordResetCompleted fires after OTP-based password reset is confirmed.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccountPasswordResetCompleted(
	ctx context.Context,
	accountID string,
	resetCompletedAt int64,
) error {
	args := m.Called(ctx, accountID, resetCompletedAt)
	return args.Error(0)
}

// AccountPersonalDataUpdated fires after PATCH /account/profile.
// changedFields contains the list of modified field names.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccountPersonalDataUpdated(
	ctx context.Context,
	accountID string,
	changedFields []string,
	actorID string,
) error {
	args := m.Called(ctx, accountID, changedFields, actorID)
	return args.Error(0)
}

// AccountDeleted fires on admin-initiated account deletion.
// Mandatory for compliance: triggers BC#2 billing cleanup and BC#4 PII anonymisation.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrOutboxUnavailable)
func (m *MockAccountEventsProducer) AccountDeleted(
	ctx context.Context,
	accountID string,
	deletedAt int64,
	adminID string,
) error {
	args := m.Called(ctx, accountID, deletedAt, adminID)
	return args.Error(0)
}

// Compile-time interface satisfaction check.
// Fails at build time if MockAccountEventsProducer drifts from out.AccountEventsProducer.
var _ interface {
	AccountRegistered(context.Context, string, valobj.Role, valobj.Classifier, string) error
	AccountEmailVerified(context.Context, string, int64) error
	AccountLockedByFailedAttempts(context.Context, string, *int64, int) error
	AccountLockedByAdmin(context.Context, string, *int64, string) error
	AccountUnlocked(context.Context, string, int64) error
	AccessTokenRevoked(context.Context, string, int64, int64, string) error
	SessionCreated(context.Context, string, string, string, int64) error
	SessionTerminated(context.Context, string, string, int64) error
	SessionTerminatedByAdmin(context.Context, string, string, string, int64) error
	AccountPasswordChanged(context.Context, string, int64, int) error
	AccountPasswordResetCompleted(context.Context, string, int64) error
	AccountPersonalDataUpdated(context.Context, string, []string, string) error
	AccountDeleted(context.Context, string, int64, string) error
} = (*MockAccountEventsProducer)(nil)

// ErrOutboxUnavailable is re-exported so test files need not import corerr directly.
var ErrOutboxUnavailable = corerr.ErrOutboxUnavailable
