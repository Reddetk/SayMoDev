package auth

import (
	"context"
	"crypto/rsa"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
)

func TestMiddlewareRejectsMissingAndMalformedHeaders(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	auth := NewAuthenticator(Options{JWKSURL: "http://127.0.0.1:1/jwks.json"}, &fakeStore{})

	router := gin.New()
	router.GET("/private", auth.Middleware(), func(c *gin.Context) { c.Status(http.StatusOK) })

	cases := []struct {
		name   string
		header string
	}{
		{name: "missing", header: ""},
		{name: "not bearer", header: "Basic abc"},
		{name: "empty token", header: "Bearer "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/private", nil)
			if tc.header != "" {
				req.Header.Set("Authorization", tc.header)
			}
			rec := httptest.NewRecorder()
			router.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("want 401, got %d", rec.Code)
			}
			if rec.Header().Get("WWW-Authenticate") == "" {
				t.Fatal("WWW-Authenticate header is required")
			}
			body := rec.Body.String()
			if !strings.Contains(body, `"code"`) {
				t.Fatalf("401 body must contain stable code: %s", body)
			}
			if strings.Contains(body, `"reason"`) || strings.Contains(body, "dial tcp") {
				t.Fatalf("401 body must not leak internals: %s", body)
			}
		})
	}
}

func TestRequireAdminWithoutIdentityReturns401(t *testing.T) {
	gin.SetMode(gin.ReleaseMode)
	auth := NewAuthenticator(Options{}, nil)

	router := gin.New()
	router.POST("/admin", auth.RequireAdmin(), func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/admin", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("want 401, got %d", rec.Code)
	}
}

func TestAuthenticateWithoutRevocationStoreFailsClosed(t *testing.T) {
	key := newRSAKey(t)
	srv := jwksServer(t, &key.PublicKey)
	defer srv.Close()

	now := time.Now().UTC()
	auth := NewAuthenticator(Options{JWKSURL: srv.URL, Now: func() time.Time { return now }}, nil)

	raw := signForFixture(t, key, "valid", "RS256", testKID, Claims{
		Role:             "patient",
		SessionID:        "5c8e1f37-2a4b-4d96-8e03-7f1a6b2c9d85",
		Rev:              1,
		RegisteredClaims: registered("3f2b9c4e-71a5-4d2e-9b8f-0c6a1d5e7f21", "b7c1d2e3-4f5a-4b7c-8d9e-0f1a2b3c4d5e", now),
	})

	if _, err := auth.Authenticate(context.Background(), raw); err == nil {
		t.Fatal("missing revocation store must fail closed")
	}
}

func TestJWKSKeyIsFetchedOncePerKid(t *testing.T) {
	key := newRSAKey(t)

	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		jwksHandler(t, &key.PublicKey).ServeHTTP(w, r)
	}))
	defer srv.Close()

	now := time.Now().UTC()
	auth := NewAuthenticator(Options{JWKSURL: srv.URL, Now: func() time.Time { return now }}, &fakeStore{accountRev: intPtr(0)})

	raw := signForFixture(t, key, "valid", "RS256", testKID, Claims{
		Role:             "patient",
		SessionID:        "5c8e1f37-2a4b-4d96-8e03-7f1a6b2c9d85",
		Rev:              1,
		RegisteredClaims: registered("3f2b9c4e-71a5-4d2e-9b8f-0c6a1d5e7f21", "b7c1d2e3-4f5a-4b7c-8d9e-0f1a2b3c4d5e", now),
	})

	for i := 0; i < 3; i++ {
		if _, err := auth.Authenticate(context.Background(), raw); err != nil {
			t.Fatalf("authenticate #%d: %v", i, err)
		}
	}
	if got := calls.Load(); got != 1 {
		t.Fatalf("JWKS fetches: want 1, got %d", got)
	}
}

// Ключ, убранный из JWKS, перестаёт приниматься после TTL 1h (ADR-004).
func TestJWKSRefetchesAfterTTLAndRejectsRetiredKey(t *testing.T) {
	key1 := newRSAKey(t)
	key2 := newRSAKey(t)

	var current atomic.Pointer[rsa.PrivateKey]
	current.Store(key1)

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		pub := current.Load().PublicKey
		jwksHandler(t, &pub).ServeHTTP(w, r)
	}))
	defer srv.Close()

	now := time.Now().UTC()
	clock := func() time.Time { return now }
	auth := NewAuthenticator(Options{JWKSURL: srv.URL, Now: clock}, &fakeStore{accountRev: intPtr(0)})

	claims := Claims{
		Role:             "patient",
		SessionID:        "5c8e1f37-2a4b-4d96-8e03-7f1a6b2c9d85",
		Rev:              1,
		RegisteredClaims: registered("3f2b9c4e-71a5-4d2e-9b8f-0c6a1d5e7f21", "b7c1d2e3-4f5a-4b7c-8d9e-0f1a2b3c4d5e", now),
	}
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(2 * time.Hour))
	tokenOld := signForFixture(t, key1, "valid", "RS256", testKID, claims)
	tokenNew := signForFixture(t, key2, "valid", "RS256", testKID, claims)

	if _, err := auth.Authenticate(context.Background(), tokenOld); err != nil {
		t.Fatalf("initial auth: %v", err)
	}

	current.Store(key2)
	now = now.Add(61 * time.Minute)

	if _, err := auth.Authenticate(context.Background(), tokenOld); !errors.Is(err, ErrSignature) {
		t.Fatalf("retired key must be rejected with signature error, got %v", err)
	}
	if _, err := auth.Authenticate(context.Background(), tokenNew); err != nil {
		t.Fatalf("new key must be accepted after refetch: %v", err)
	}
}

// TTL истёк, JWKS недоступен: ключ больше не принимается (fail-closed).
func TestJWKSTTLExpiredWithUnavailableJWKSFailsClosed(t *testing.T) {
	key := newRSAKey(t)

	srv := jwksServer(t, &key.PublicKey)
	now := time.Now().UTC()
	clock := func() time.Time { return now }
	auth := NewAuthenticator(Options{JWKSURL: srv.URL, Now: clock}, &fakeStore{accountRev: intPtr(0)})

	claims := Claims{
		Role:             "patient",
		SessionID:        "5c8e1f37-2a4b-4d96-8e03-7f1a6b2c9d85",
		Rev:              1,
		RegisteredClaims: registered("3f2b9c4e-71a5-4d2e-9b8f-0c6a1d5e7f21", "b7c1d2e3-4f5a-4b7c-8d9e-0f1a2b3c4d5e", now),
	}
	claims.ExpiresAt = jwt.NewNumericDate(now.Add(3 * time.Hour))
	token := signForFixture(t, key, "valid", "RS256", testKID, claims)

	if _, err := auth.Authenticate(context.Background(), token); err != nil {
		t.Fatalf("initial auth: %v", err)
	}

	srv.Close()
	now = now.Add(61 * time.Minute)

	if _, err := auth.Authenticate(context.Background(), token); !errors.Is(err, ErrJWKS) {
		t.Fatalf("expired key with unavailable JWKS must fail closed, got %v", err)
	}
}
