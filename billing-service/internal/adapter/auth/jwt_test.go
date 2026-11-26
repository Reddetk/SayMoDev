package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVerifier_LocalKey(t *testing.T) {
	key, path := writeKey(t)
	verifier, err := NewVerifier("urn:saymo:identity-service", "api.saymo", "", path, nil)
	require.NoError(t, err)

	token := sign(t, key, "account-1", "patient", time.Now().Add(time.Hour))
	claims, err := verifier.Verify(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, "account-1", claims.Subject)
	assert.Equal(t, "patient", claims.Role)
	assert.Equal(t, 1, claims.Rev)
	assert.NotEmpty(t, claims.JTI)
}

func TestVerifier_RejectsBadClaims(t *testing.T) {
	key, path := writeKey(t)
	verifier, err := NewVerifier("urn:saymo:identity-service", "api.saymo", "", path, nil)
	require.NoError(t, err)

	expired := sign(t, key, "account-1", "patient", time.Now().Add(-time.Hour))
	_, err = verifier.Verify(context.Background(), expired)
	require.Error(t, err)

	wrongAudience := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "urn:saymo:identity-service", "aud": "other", "sub": "a", "exp": time.Now().Add(time.Hour).Unix(),
		"jti": "j1", "rev": 1,
	})
	signed, err := wrongAudience.SignedString(key)
	require.NoError(t, err)
	_, err = verifier.Verify(context.Background(), signed)
	require.Error(t, err)
}

func TestVerifier_JWKS(t *testing.T) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	jwks := []byte(`{"keys":[{"kty":"RSA","kid":"key-v1","n":"` +
		base64.RawURLEncoding.EncodeToString(key.N.Bytes()) + `","e":"` +
		base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()) + `"}]}`)

	verifier, err := NewVerifier("urn:saymo:identity-service", "api.saymo", "http://identity/jwks", "", staticClient{jwks})
	require.NoError(t, err)

	token := sign(t, key, "account-2", "administrator", time.Now().Add(time.Hour))
	claims, err := verifier.Verify(context.Background(), token)
	require.NoError(t, err)
	assert.Equal(t, "administrator", claims.Role)
}

func TestNewVerifier_RequiresKeySource(t *testing.T) {
	_, err := NewVerifier("iss", "aud", "", "", nil)
	require.Error(t, err)
}

type staticClient struct{ body []byte }

func (c staticClient) Get(context.Context, string) ([]byte, error) { return c.body, nil }

func writeKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "public.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))
	return key, path
}

func sign(t *testing.T, key *rsa.PrivateKey, subject, role string, exp time.Time) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "urn:saymo:identity-service", "aud": "api.saymo", "sub": subject, "role": role,
		"exp": exp.Unix(), "iat": now.Unix(), "jti": "jti-1", "session_id": "s-1", "rev": 1,
	})
	token.Header["kid"] = "key-v1"
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}
