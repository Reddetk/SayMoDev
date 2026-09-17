package mocks

import (
	"context"
	"errors"

	"github.com/stretchr/testify/mock"
)

// ErrRedisUnavailable is a placeholder sentinel for the Redis infrastructure
// error path in TokenBlacklist tests.
//
// DESIGN GAP: this error is not yet declared in corerr (businesErrors.go).
// It must be added to core/coreErrors/businesErrors.go as:
//
//	ErrRedisUnavailable = errors.New("redis is unavailable")
//
// Once added, replace this local declaration with:
//
//	ErrRedisUnavailable = corerr.ErrRedisUnavailable
var ErrRedisUnavailable = errors.New("redis is unavailable")

// MockTokenBlacklist is a testify mock for out.TokenBlacklist.
//
// GetAccountRev has two distinct error-adjacent scenarios that produce
// different behaviour in TokenService (spec BC#1 Step 3):
//
//	// L2 miss  key absent in Redis; TokenService continues (not a failure)
//	bl.On("GetAccountRev", mock.Anything, "acc-id").Return(int64(0), nil)
//
//	// Redis down  TokenService must return ErrTokenRevoked (fail-closed)
//	bl.On("GetAccountRev", mock.Anything, "acc-id").Return(int64(0), mocks.ErrRedisUnavailable)
//
// These two cases MUST be covered by separate test functions.
type MockTokenBlacklist struct {
	mock.Mock
}

// Add writes a jti into Redis with TTL = expiresAtUnix - now.
// If TTL <= 0 the adapter is expected to no-op; the mock does not enforce that.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrRedisUnavailable)
func (m *MockTokenBlacklist) Add(
	ctx context.Context,
	jti string,
	expiresAtUnix int64,
) error {
	args := m.Called(ctx, jti, expiresAtUnix)
	return args.Error(0)
}

// Contains checks L1 -> L2 for jti presence.
//
// Happy path (not blacklisted): Return(false, nil)
// Happy path (blacklisted):     Return(true,  nil)
// Error path:                   Return(false, mocks.ErrRedisUnavailable)
func (m *MockTokenBlacklist) Contains(
	ctx context.Context,
	jti string,
) (bool, error) {
	args := m.Called(ctx, jti)
	return args.Bool(0), args.Error(1)
}

// GetAccountRev returns the rev stored in Redis L2 for the given accountID.
//
// L2 miss (key absent):  Return(int64(0), nil)     not a failure
// Rev set:               Return(int64(5), nil)
// Redis down:            Return(int64(0), mocks.ErrRedisUnavailable)  fail-closed
func (m *MockTokenBlacklist) GetAccountRev(
	ctx context.Context,
	accountID string,
) (int64, error) {
	args := m.Called(ctx, accountID)
	return args.Get(0).(int64), args.Error(1)
}

// SetAccountRev writes the current rev into Redis after a mass-revoke Tx.
//
// Happy path: Return(nil)
// Error path:  Return(mocks.ErrRedisUnavailable)
func (m *MockTokenBlacklist) SetAccountRev(
	ctx context.Context,
	accountID string,
	rev int64,
) error {
	args := m.Called(ctx, accountID, rev)
	return args.Error(0)
}

// Compile-time interface satisfaction check.
var _ interface {
	Add(context.Context, string, int64) error
	Contains(context.Context, string) (bool, error)
	GetAccountRev(context.Context, string) (int64, error)
	SetAccountRev(context.Context, string, int64) error
} = (*MockTokenBlacklist)(nil)
