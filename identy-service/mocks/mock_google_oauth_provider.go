package mocks

import (
	"context"
	"errors"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/core/valObj"
	"github.com/stretchr/testify/mock"
)

// ErrOAuthStateMismatch is a placeholder sentinel for CSRF state mismatch.
//
// DESIGN GAP: not declared in core/coreErrors/businesErrors.go.
// ErrOAuthStateCSRFMismatch exists and covers the same concept.
// Replace this with:
//
//	ErrOAuthStateMismatch = corerr.ErrOAuthStateCSRFMismatch
//
// The name mismatch (StateMismatch vs StateCSRFMismatch) should be resolved
// by aligning the mock name to the existing sentinel.
var ErrOAuthStateMismatch = corerr.ErrOAuthStateCSRFMismatch

// MockGoogleOAuthProvider is a testify mock for out.GoogleOAuthProvider.
//
// ExchangeCode has two error scenarios:
//
//	// JWKS unavailable (infrastructure)
//	provider.On("ExchangeCode", mock.Anything, "code-jwks-down", mock.Anything).
//	    Return(valobj.GoogleClaims{}, mocks.ErrOAuthJWKSUnavailable)
//
//	// Email not verified (domain rule)
//	provider.On("ExchangeCode", mock.Anything, "code-unverified", mock.Anything).
//	    Return(valobj.GoogleClaims{}, mocks.ErrOAuthEmailNotVerified)
//
// ValidateState has two error scenarios:
//
//	// CSRF token mismatch
//	provider.On("ValidateState", mock.Anything, "bad-csrf", mock.Anything).
//	    Return(mocks.ErrOAuthStateMismatch)
//
//	// State expired (TTL 10 min)
//	provider.On("ValidateState", mock.Anything, "expired-csrf", mock.Anything).
//	    Return(mocks.ErrOAuthStateExpired)
type MockGoogleOAuthProvider struct {
	mock.Mock
}

// BuildAuthURL generates PKCE + CSRF state and returns Google redirect URL.
//
// Happy path: Return("https://accounts.google.com/o/oauth2/auth?...", state, nil)
// Error path:  Return("", valobj.OAuthState{}, mocks.ErrOAuthJWKSUnavailable)
func (m *MockGoogleOAuthProvider) BuildAuthURL(
	ctx context.Context,
) (string, valobj.OAuthState, error) {
	args := m.Called(ctx)
	return args.String(0), args.Get(1).(valobj.OAuthState), args.Error(2)
}

// ExchangeCode performs server-to-server code exchange and ID token verification.
//
// Happy path:  Return(valobj.GoogleClaims{Sub: "google-uid-123", Email: "user@gmail.com", EmailVerified: true}, nil)
// Error path 1 -- JWKS down:        Return(valobj.GoogleClaims{}, mocks.ErrOAuthJWKSUnavailable)
// Error path 2 -- email unverified: Return(valobj.GoogleClaims{}, mocks.ErrOAuthEmailNotVerified)
func (m *MockGoogleOAuthProvider) ExchangeCode(
	ctx context.Context,
	code string,
	state valobj.OAuthState,
) (valobj.GoogleClaims, error) {
	args := m.Called(ctx, code, state)
	return args.Get(0).(valobj.GoogleClaims), args.Error(1)
}

// ValidateState verifies CSRF token from callback against stored state.
//
// Happy path:  Return(nil)
// Error path 1 -- mismatch: Return(mocks.ErrOAuthStateMismatch)
// Error path 2 -- expired:  Return(mocks.ErrOAuthStateExpired)
func (m *MockGoogleOAuthProvider) ValidateState(
	ctx context.Context,
	receivedCSRF string,
	storedState valobj.OAuthState,
) error {
	args := m.Called(ctx, receivedCSRF, storedState)
	return args.Error(0)
}

// Compile-time interface satisfaction check.
var _ interface {
	BuildAuthURL(context.Context) (string, valobj.OAuthState, error)
	ExchangeCode(context.Context, string, valobj.OAuthState) (valobj.GoogleClaims, error)
	ValidateState(context.Context, string, valobj.OAuthState) error
} = (*MockGoogleOAuthProvider)(nil)

// Re-exported OAuth sentinels.
var (
	ErrOAuthJWKSUnavailable  = corerr.ErrOAuthJWKSUnavailable
	ErrOAuthEmailNotVerified = corerr.ErrOAuthEmailNotVerified
	ErrOAuthStateExpired     = errors.New("oauth state has expired")
)
