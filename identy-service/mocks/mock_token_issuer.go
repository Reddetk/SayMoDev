package mocks

import (
	"context"

	corerr "github.com/Reddetk/SayMoDev/identy-service/core/coreErrors"
	"github.com/stretchr/testify/mock"
)

// MockTokenIssuer is a testify mock for out.TokenIssuer.
//
// Three scenarios for Verify must be configured via separate On() calls
// with different rawToken matchers:
//
//	// Scenario 1: valid token
//	issuer.On("Verify", mock.Anything, "valid-token").
//	    Return("acc-id", "patient", "sess-id", "jti-id", int64(1), expAt, nil)
//
//	// Scenario 2: expired / revoked token
//	issuer.On("Verify", mock.Anything, "expired-token").
//	    Return("", "", "", "", int64(0), int64(0), mocks.ErrTokenRevoked)
//
//	// Scenario 3: JWKS unavailable
//	issuer.On("Verify", mock.Anything, "unknown-kid-token").
//	    Return("", "", "", "", int64(0), int64(0), mocks.ErrJWKSKeysEmpty)
type MockTokenIssuer struct {
	mock.Mock
}

// Issue signs a JWT and returns (accessToken, jti, error).
//
// Happy path: Return("eyJhbGci...", "jti-uuid-123", nil)
// Error path:  Return("", "", mocks.ErrJTIGenerationFailed)
func (m *MockTokenIssuer) Issue(
	ctx context.Context,
	accountID string,
	role string,
	sessionID string,
	rev int64,
) (string, string, error) {
	args := m.Called(ctx, accountID, role, sessionID, rev)
	return args.String(0), args.String(1), args.Error(2)
}

// Verify validates RS256 signature and returns flat claims.
//
// Happy path:  Return("acc-uuid", "patient", "sess-uuid", "jti-uuid", int64(1), expAt, nil)
// Error path 1 -- expired/revoked: Return("", "", "", "", int64(0), int64(0), mocks.ErrTokenRevoked)
// Error path 2 -- JWKS empty:      Return("", "", "", "", int64(0), int64(0), mocks.ErrJWKSKeysEmpty)
func (m *MockTokenIssuer) Verify(
	ctx context.Context,
	rawToken string,
) (accountID, role, sessionID, jti string, rev int64, expiresAt int64, err error) {
	args := m.Called(ctx, rawToken)
	return args.String(0),
		args.String(1),
		args.String(2),
		args.String(3),
		args.Get(4).(int64),
		args.Get(5).(int64),
		args.Error(6)
}

// GetPublicKeys returns active RSA public keys as JWKS JSON strings.
//
// Happy path: Return([]string{`{"kty":"RSA",...}`}, nil)
// Error path:  Return(nil, mocks.ErrJWKSKeysEmpty)
func (m *MockTokenIssuer) GetPublicKeys(
	ctx context.Context,
) ([]string, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]string), args.Error(1)
}

// Compile-time interface satisfaction check.
var _ interface {
	Issue(context.Context, string, string, string, int64) (string, string, error)
	Verify(context.Context, string) (string, string, string, string, int64, int64, error)
	GetPublicKeys(context.Context) ([]string, error)
} = (*MockTokenIssuer)(nil)

// Re-exported token sentinels for use in test On().Return() calls.
var (
	ErrJTIGenerationFailed = corerr.ErrJTIGenerationFailed
	ErrTokenRevoked        = corerr.ErrTokenRevoked
	ErrJWKSKeysEmpty       = corerr.ErrJWKSKeysEmpty
)
