package mocks

import (
	"context"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	"github.com/stretchr/testify/mock"
)

// MockRateLimiter is a testify mock for out.RateLimiter.
//
// RecordFailure note: per spec the caller (AuthService) logs the error
// and does NOT return it to the client. Configure accordingly:
//
//	rl.On("RecordFailure", mock.Anything, "1.2.3.4", "").
//	    Return(mocks.ErrRedisUnavailable)
//	// AuthService must log WARN and continue  assert no error propagation.
type MockRateLimiter struct {
	mock.Mock
}

// CheckIP checks per-IP hourly attempt count.
// Called first in Login, before any DB access.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrRateLimitIP)
func (m *MockRateLimiter) CheckIP(
	ctx context.Context,
	clientIP string,
) error {
	args := m.Called(ctx, clientIP)
	return args.Error(0)
}

// CheckAccount checks per-account daily failed attempt count.
// Called after FindByEmail, before bcrypt.Compare.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrRateLimitAccount)
func (m *MockRateLimiter) CheckAccount(
	ctx context.Context,
	accountID string,
) error {
	args := m.Called(ctx, accountID)
	return args.Error(0)
}

// RecordFailure increments IP and account failure counters.
// accountID may be empty string when account was not found (IP counter only).
// Error is logged by caller, not returned to client.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrRedisUnavailable)
func (m *MockRateLimiter) RecordFailure(
	ctx context.Context,
	clientIP string,
	accountID string,
) error {
	args := m.Called(ctx, clientIP, accountID)
	return args.Error(0)
}

// Compile-time interface satisfaction check.
var _ interface {
	CheckIP(context.Context, string) error
	CheckAccount(context.Context, string) error
	RecordFailure(context.Context, string, string) error
} = (*MockRateLimiter)(nil)

// Re-exported rate limit sentinels.
var (
	ErrRateLimitIP      = corerr.ErrRateLimitIP
	ErrRateLimitAccount = corerr.ErrRateLimitAccount
)
