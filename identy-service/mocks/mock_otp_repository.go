package mocks

import (
	"context"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/internal/core/valObj"
	"github.com/stretchr/testify/mock"
)

// Re-exported sentinels for use in test files without importing corerr directly.
var (
	// ErrOTPNotFound  OTP record absent; Find returns nothing for this (email, purpose).
	ErrOTPNotFound = corerr.ErrOTPNotFound

	// ErrDBUnavailable  DB infrastructure unreachable (Upsert/CleanUp/Immulate).
	ErrDBUnavailable = corerr.ErrDBUnavailable
)

// MockOtpRepository is a testify mock for out.OtpRepository.
//
// Find has two happy-path scenarios reflecting domain lifecycle:
//
//	// Scenario 1: fresh OTP  IsExpired() == false; domain proceeds
//	repo.On("Find", mock.Anything, "user@example.com", valobj.OTPPurposeRegistration).
//	    Return(testdata.NewFreshVerificationCode(), nil)
//
//	// Scenario 2: expired OTP  IsExpired() == true; domain rejects it
//	repo.On("Find", mock.Anything, "user@example.com", valobj.OTPPurposeRegistration).
//	    Return(testdata.NewExpiredVerificationCode(), nil)
//
//	// Scenario 3: not found
//	repo.On("Find", mock.Anything, "missing@example.com", valobj.OTPPurposeRegistration).
//	    Return(nil, mocks.ErrOTPNotFound)
type MockOtpRepository struct {
	mock.Mock
}

// Upsert inserts or updates a VerificationCode record.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrDBUnavailable)
func (m *MockOtpRepository) Upsert(
	ctx context.Context,
	verCode *valobj.VerificationCode,
) error {
	args := m.Called(ctx, verCode)
	return args.Error(0)
}

// Find loads a VerificationCode by email and purpose.
// Returns a record even if expired  domain calls IsExpired() to decide.
//
// Happy path (fresh):   Return(testdata.NewFreshVerificationCode(), nil)
// Happy path (expired): Return(testdata.NewExpiredVerificationCode(), nil)
// Error path:           Return(nil, mocks.ErrOTPNotFound)
func (m *MockOtpRepository) Find(
	ctx context.Context,
	email string,
	purpose valobj.OTPPurpose,
) (*valobj.VerificationCode, error) {
	args := m.Called(ctx, email, purpose)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*valobj.VerificationCode), args.Error(1)
}

// CleanUp deletes a VerificationCode record after successful verification.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrDBUnavailable)
func (m *MockOtpRepository) CleanUp(
	ctx context.Context,
	verCode *valobj.VerificationCode,
) error {
	args := m.Called(ctx, verCode)
	return args.Error(0)
}

// Immulate simulates/schedules OTP cleanup for expired records (background job).
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrDBUnavailable)
func (m *MockOtpRepository) Immulate(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}

// Compile-time interface satisfaction check.
var _ interface {
	Upsert(context.Context, *valobj.VerificationCode) error
	Find(context.Context, string, valobj.OTPPurpose) (*valobj.VerificationCode, error)
	CleanUp(context.Context, *valobj.VerificationCode) error
	Immulate(context.Context) error
} = (*MockOtpRepository)(nil)
