package auth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const (
	testKID      = "saymo-rsa-2026-01"
	testIssuer   = "urn:saymo:identity-service"
	testAudience = "api.saymo"
)

type fakeStore struct {
	blacklisted bool
	accountRev  *int
	err         error
}

func (f *fakeStore) JTIBlacklisted(context.Context, string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.blacklisted, nil
}

func (f *fakeStore) AccountRev(context.Context, string) (int, bool, error) {
	if f.err != nil {
		return 0, false, f.err
	}
	if f.accountRev == nil {
		return 0, false, nil
	}
	return *f.accountRev, true, nil
}

func readAuthCSV(t *testing.T, name string) []map[string]string {
	t.Helper()
	path := filepath.Join("..", "..", "testdata", "fixtures", name)
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v", name, err)
	}
	defer f.Close()

	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	header := map[string]int{}
	for i, h := range rows[0] {
		header[h] = i
	}
	out := make([]map[string]string, 0, len(rows)-1)
	for _, row := range rows[1:] {
		rec := map[string]string{}
		for k, i := range header {
			rec[k] = row[i]
		}
		out = append(out, rec)
	}
	return out
}

type fixtureClaims struct {
	Sub       string `json:"sub"`
	Role      string `json:"role"`
	Issuer    string `json:"iss"`
	Audience  string `json:"aud"`
	ExpOffset int    `json:"exp_offset"`
	IATOffset int    `json:"iat_offset"`
	JTI       string `json:"jti"`
	SessionID string `json:"session_id"`
	Rev       int    `json:"rev"`
}

func newRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	return key
}

func jwksHandler(t *testing.T, key *rsa.PublicKey) http.Handler {
	t.Helper()
	e := big.NewInt(int64(key.E)).Bytes()
	doc := map[string]any{
		"keys": []map[string]string{{
			"kty": "RSA",
			"use": "sig",
			"alg": "RS256",
			"kid": testKID,
			"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
			"e":   base64.RawURLEncoding.EncodeToString(e),
		}},
	}
	body, err := json.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal jwks: %v", err)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	})
}

func jwksServer(t *testing.T, key *rsa.PublicKey) *httptest.Server {
	t.Helper()
	return httptest.NewServer(jwksHandler(t, key))
}

func signForFixture(t *testing.T, key *rsa.PrivateKey, mode, alg, kid string, claims Claims) string {
	t.Helper()

	method := jwt.SigningMethod(jwt.SigningMethodRS256)
	var signingKey any = key

	switch mode {
	case "valid", "unknown_kid", "bad_signature":
	case "none_alg":
		method = jwt.SigningMethodNone
		signingKey = jwt.UnsafeAllowNoneSignatureType
	case "hs256":
		method = jwt.SigningMethodHS256
		signingKey = []byte("not-a-rsa-key")
	default:
		t.Fatalf("unknown signing mode %q", mode)
	}

	token := jwt.NewWithClaims(method, claims)
	token.Header["kid"] = kid
	if alg != "" {
		token.Header["alg"] = alg
	}

	signed, err := token.SignedString(signingKey)
	if err != nil {
		t.Fatalf("sign token: %v", err)
	}

	if mode == "bad_signature" {
		parts := strings.Split(signed, ".")
		sig := []byte(parts[2])
		idx := len(sig) / 2
		if sig[idx] == 'A' {
			sig[idx] = 'B'
		} else {
			sig[idx] = 'A'
		}
		parts[2] = string(sig)
		signed = strings.Join(parts, ".")
	}

	return signed
}

func buildClaims(f fixtureClaims, now time.Time) Claims {
	return Claims{
		Role:      f.Role,
		SessionID: f.SessionID,
		Rev:       f.Rev,
		RegisteredClaims: jwt.RegisteredClaims{
			Issuer:    f.Issuer,
			Subject:   f.Sub,
			Audience:  jwt.ClaimStrings{f.Audience},
			ExpiresAt: jwt.NewNumericDate(now.Add(time.Duration(f.ExpOffset) * time.Second)),
			IssuedAt:  jwt.NewNumericDate(now.Add(time.Duration(f.IATOffset) * time.Second)),
			ID:        f.JTI,
		},
	}
}

func registered(sub, jti string, now time.Time) jwt.RegisteredClaims {
	return jwt.RegisteredClaims{
		Issuer:    testIssuer,
		Subject:   sub,
		Audience:  jwt.ClaimStrings{testAudience},
		ExpiresAt: jwt.NewNumericDate(now.Add(time.Hour)),
		IssuedAt:  jwt.NewNumericDate(now),
		ID:        jti,
	}
}
