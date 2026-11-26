package service

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
)

type fakeJWKS struct {
	key *rsa.PublicKey
	err error
}

func (f fakeJWKS) PublicKey(context.Context, string) (*rsa.PublicKey, error) {
	return f.key, f.err
}

type fakeRevocation struct {
	blacklisted map[string]bool
	rev         map[string]int64
	err         error
}

func (f fakeRevocation) IsJTIBlacklisted(_ context.Context, jti string) (bool, error) {
	return f.blacklisted[jti], f.err
}
func (f fakeRevocation) AccountRev(_ context.Context, accountID string) (int64, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
	v, ok := f.rev[accountID]
	return v, ok, nil
}
func (f fakeRevocation) Ping(context.Context) error { return f.err }

func newKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	return key
}

func sign(t *testing.T, key *rsa.PrivateKey, mutate func(jwt.MapClaims) (bool, string)) string {
	t.Helper()
	claims := jwt.MapClaims{
		"sub": "11111111-1111-1111-1111-111111111111", "role": "patient", "jti": "jti-1",
		"session_id": "s-1", "rev": 1, "iss": "saymo-iam", "aud": "saymo",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	}
	kid := "key-v1"
	includeKid := true
	if mutate != nil {
		includeKid, kid = mutate(claims)
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if includeKid {
		tok.Header["kid"] = kid
	}
	s, err := tok.SignedString(key)
	require.NoError(t, err)
	return s
}

func TestValidateTokenHappyPath(t *testing.T) {
	key := newKey(t)
	svc := NewTokenService(fakeJWKS{key: &key.PublicKey}, fakeRevocation{rev: map[string]int64{}}, "saymo-iam", "saymo")
	auth, err := svc.ValidateToken(context.Background(), sign(t, key, nil))
	require.NoError(t, err)
	assert.Equal(t, "11111111-1111-1111-1111-111111111111", auth.AccountID)
	assert.Equal(t, "patient", auth.Role)
}

func TestValidateTokenRejectsExpiredBeyondSkew(t *testing.T) {
	key := newKey(t)
	svc := NewTokenService(fakeJWKS{key: &key.PublicKey}, fakeRevocation{}, "saymo-iam", "saymo")
	token := sign(t, key, func(c jwt.MapClaims) (bool, string) {
		c["exp"] = time.Now().Add(-2 * time.Minute).Unix()
		c["iat"] = time.Now().Add(-3 * time.Minute).Unix()
		return true, "key-v1"
	})
	_, err := svc.ValidateToken(context.Background(), token)
	assert.ErrorIs(t, err, corerr.ErrInvalidToken)
}

func TestValidateTokenRejectsWrongAudience(t *testing.T) {
	key := newKey(t)
	svc := NewTokenService(fakeJWKS{key: &key.PublicKey}, fakeRevocation{}, "saymo-iam", "saymo")
	token := sign(t, key, func(c jwt.MapClaims) (bool, string) { c["aud"] = "other"; return true, "key-v1" })
	_, err := svc.ValidateToken(context.Background(), token)
	assert.ErrorIs(t, err, corerr.ErrInvalidToken)
}

func TestValidateTokenRejectsMissingKid(t *testing.T) {
	key := newKey(t)
	svc := NewTokenService(fakeJWKS{key: &key.PublicKey}, fakeRevocation{}, "saymo-iam", "saymo")
	token := sign(t, key, func(c jwt.MapClaims) (bool, string) { return false, "" })
	_, err := svc.ValidateToken(context.Background(), token)
	assert.ErrorIs(t, err, corerr.ErrInvalidToken)
}

func TestValidateTokenRejectsBlacklistedJTI(t *testing.T) {
	key := newKey(t)
	svc := NewTokenService(fakeJWKS{key: &key.PublicKey}, fakeRevocation{blacklisted: map[string]bool{"jti-1": true}}, "saymo-iam", "saymo")
	_, err := svc.ValidateToken(context.Background(), sign(t, key, nil))
	assert.ErrorIs(t, err, corerr.ErrTokenRevoked)
}

func TestValidateTokenRejectsRevMismatch(t *testing.T) {
	key := newKey(t)
	svc := NewTokenService(fakeJWKS{key: &key.PublicKey},
		fakeRevocation{rev: map[string]int64{"11111111-1111-1111-1111-111111111111": 2}}, "saymo-iam", "saymo")
	_, err := svc.ValidateToken(context.Background(), sign(t, key, nil))
	assert.ErrorIs(t, err, corerr.ErrTokenRevoked)
}

func TestValidateTokenJWKSAvailableFailure(t *testing.T) {
	key := newKey(t)
	svc := NewTokenService(fakeJWKS{err: corerr.ErrJWKSUnavailable}, fakeRevocation{}, "saymo-iam", "saymo")
	_, err := svc.ValidateToken(context.Background(), sign(t, key, nil))
	assert.ErrorIs(t, err, corerr.ErrJWKSUnavailable)
}

func TestValidateTokenRevocationStoreFailure(t *testing.T) {
	key := newKey(t)
	svc := NewTokenService(fakeJWKS{key: &key.PublicKey}, fakeRevocation{err: errors.New("redis down")}, "saymo-iam", "saymo")
	_, err := svc.ValidateToken(context.Background(), sign(t, key, nil))
	assert.ErrorIs(t, err, corerr.ErrAuthUnavailable)
}
