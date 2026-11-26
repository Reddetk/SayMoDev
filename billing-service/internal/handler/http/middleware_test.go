package httpapi

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/Reddetk/SayMoDev/billing-service/internal/adapter/auth"
	"github.com/Reddetk/SayMoDev/billing-service/internal/platform/health"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/billing-service/internal/port/out"
)

type authStore struct {
	out.Store
	status    string
	statusErr error
}

func (s *authStore) GetAccountStatus(context.Context, string) (string, error) {
	return s.status, s.statusErr
}

type authCache struct {
	out.Cache
	revoked bool
	err     error
}

func (c *authCache) IsRevoked(context.Context, string, string, int) (bool, error) {
	return c.revoked, c.err
}

func TestAuthMiddleware(t *testing.T) {
	key, path := writeTestKey(t)
	verifier, err := auth.NewVerifier("urn:saymo:identity-service", "api.saymo", "", path, nil)
	require.NoError(t, err)
	store := &authStore{status: "active"}
	cache := &authCache{}
	middleware := AuthMiddleware(verifier, store, cache)

	engine := gin.New()
	engine.GET("/protected", middleware, func(c *gin.Context) {
		authCtx := authFrom(c)
		c.JSON(http.StatusOK, gin.H{"account": authCtx.AccountID})
	})

	do := func(token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodGet, "/protected", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	assert.Equal(t, http.StatusUnauthorized, do("").Code)
	assert.Equal(t, http.StatusUnauthorized, do("bad-token").Code)

	valid := signTestToken(t, key, uuid.NewString(), time.Now().Add(time.Hour))
	assert.Equal(t, http.StatusOK, do(valid).Code)

	// invalid sub
	badSub := signTestToken(t, key, "not-a-uuid", time.Now().Add(time.Hour))
	assert.Equal(t, http.StatusUnauthorized, do(badSub).Code)

	// locked / deleted
	store.status = "locked"
	assert.Equal(t, http.StatusForbidden, do(valid).Code)
	store.status = "deleted"
	assert.Equal(t, http.StatusForbidden, do(valid).Code)
	store.status = "active"

	// store error
	store.statusErr = errors.New("db down")
	assert.Equal(t, http.StatusServiceUnavailable, do(valid).Code)
	store.statusErr = nil

	// revoked
	cache.revoked = true
	assert.Equal(t, http.StatusUnauthorized, do(valid).Code)
	cache.revoked = false

	// cache error
	cache.err = errors.New("redis down")
	assert.Equal(t, http.StatusServiceUnavailable, do(valid).Code)
	cache.err = nil

	// expired token
	expired := signTestToken(t, key, uuid.NewString(), time.Now().Add(-time.Hour))
	assert.Equal(t, http.StatusUnauthorized, do(expired).Code)
}

type idemStore struct {
	out.Store
	record    *out.IdempotencyRecord
	getErr    error
	createOK  bool
	createErr error
	completed *out.IdempotencyRecord
	deleted   string
}

func (s *idemStore) GetIdempotency(context.Context, string, string) (*out.IdempotencyRecord, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if s.record == nil {
		return nil, out.ErrNotFound
	}
	return s.record, nil
}
func (s *idemStore) CreateIdempotency(context.Context, out.IdempotencyRecord) (bool, error) {
	return s.createOK, s.createErr
}
func (s *idemStore) CompleteIdempotency(_ context.Context, rec out.IdempotencyRecord) error {
	s.completed = &rec
	return nil
}
func (s *idemStore) DeleteIdempotency(_ context.Context, _, key string) error {
	s.deleted = key
	return nil
}

type idemCache struct {
	out.Cache
	value   []byte
	err     error
	set     []byte
	lastKey string
}

func (c *idemCache) Get(context.Context, string) ([]byte, error) {
	if c.err != nil {
		return nil, c.err
	}
	if c.value == nil {
		return nil, out.ErrNotFound
	}
	return c.value, nil
}
func (c *idemCache) Set(_ context.Context, key string, value []byte, _ time.Duration) error {
	c.set = value
	c.lastKey = key
	return nil
}

func TestIdempotencyMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	key := uuid.NewString()

	newEngine := func(store *idemStore, cache *idemCache, required bool, handler gin.HandlerFunc) *gin.Engine {
		engine := gin.New()
		engine.POST("/x", func(c *gin.Context) {
			c.Set(authContextKey, inAuth("acc"))
		}, IdempotencyMiddleware(store, cache, required), handler)
		return engine
	}
	post := func(engine *gin.Engine, idem string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, "/x", strings.NewReader("{}"))
		if idem != "" {
			req.Header.Set("Idempotency-Key", idem)
		}
		rec := httptest.NewRecorder()
		engine.ServeHTTP(rec, req)
		return rec
	}

	// required missing
	engine := newEngine(&idemStore{}, &idemCache{}, true, func(c *gin.Context) { c.Status(200) })
	assert.Equal(t, http.StatusBadRequest, post(engine, "").Code)

	// invalid UUID
	assert.Equal(t, http.StatusBadRequest, post(engine, "not-uuid").Code)

	// optional missing → handler executes
	engine = newEngine(&idemStore{}, &idemCache{}, false, func(c *gin.Context) { c.Status(204) })
	assert.Equal(t, http.StatusNoContent, post(engine, "").Code)

	// cache hit replay
	payload, _ := json.Marshal(cachedResponse{Status: 201, Body: json.RawMessage(`{"ok":true}`)})
	engine = newEngine(&idemStore{}, &idemCache{value: payload}, true, func(c *gin.Context) { c.Status(500) })
	rec := post(engine, key)
	assert.Equal(t, http.StatusCreated, rec.Code)
	assert.Contains(t, rec.Body.String(), "ok")

	// PG completed replay
	body := []byte(`{"replayed":true}`)
	store := &idemStore{record: &out.IdempotencyRecord{Endpoint: "/x", Status: "completed", ResponseCode: 200, ResponseBody: body}}
	cache := &idemCache{}
	engine = newEngine(store, cache, true, func(c *gin.Context) { c.Status(500) })
	rec = post(engine, key)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Body.String(), "replayed")

	// endpoint mismatch
	store = &idemStore{record: &out.IdempotencyRecord{Endpoint: "/other", Status: "completed"}}
	engine = newEngine(store, &idemCache{}, true, func(c *gin.Context) { c.Status(200) })
	assert.Equal(t, http.StatusConflict, post(engine, key).Code)

	// in progress
	store = &idemStore{record: &out.IdempotencyRecord{Endpoint: "/x", Status: "in_progress"}}
	engine = newEngine(store, &idemCache{}, true, func(c *gin.Context) { c.Status(200) })
	assert.Equal(t, http.StatusConflict, post(engine, key).Code)

	// create error
	store = &idemStore{createErr: errors.New("db down")}
	engine = newEngine(store, &idemCache{}, true, func(c *gin.Context) { c.Status(200) })
	assert.Equal(t, http.StatusInternalServerError, post(engine, key).Code)

	// create false (race)
	store = &idemStore{createOK: false}
	engine = newEngine(store, &idemCache{}, true, func(c *gin.Context) { c.Status(200) })
	assert.Equal(t, http.StatusConflict, post(engine, key).Code)

	// success stores completed + cache
	store = &idemStore{createOK: true}
	cache = &idemCache{}
	engine = newEngine(store, cache, true, func(c *gin.Context) { c.JSON(201, gin.H{"id": "1"}) })
	assert.Equal(t, http.StatusCreated, post(engine, key).Code)
	require.NotNil(t, store.completed)
	assert.Equal(t, 201, store.completed.ResponseCode)
	assert.NotEmpty(t, cache.set)
	assert.Contains(t, cache.lastKey, "/x", "cache key is endpoint-scoped")

	// 4xx deletes key
	store = &idemStore{createOK: true}
	engine = newEngine(store, &idemCache{}, true, func(c *gin.Context) { c.Status(400) })
	assert.Equal(t, http.StatusBadRequest, post(engine, key).Code)
	assert.Equal(t, key, store.deleted)

	// 500 deletes key
	store = &idemStore{createOK: true}
	engine = newEngine(store, &idemCache{}, true, func(c *gin.Context) { c.Status(500) })
	assert.Equal(t, http.StatusInternalServerError, post(engine, key).Code)
	assert.Equal(t, key, store.deleted)
}

func TestCORSAndMaxBody(t *testing.T) {
	gin.SetMode(gin.TestMode)
	engine := gin.New()
	engine.Use(CORS([]string{"https://app.saymo.com"}), MaxBody(10))
	engine.POST("/echo", func(c *gin.Context) {
		body, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.Status(http.StatusRequestEntityTooLarge)
			return
		}
		c.Data(200, "text/plain", body)
	})

	req := httptest.NewRequest(http.MethodOptions, "/echo", nil)
	req.Header.Set("Origin", "https://app.saymo.com")
	rec := httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusNoContent, rec.Code)
	assert.Equal(t, "https://app.saymo.com", rec.Header().Get("Access-Control-Allow-Origin"))

	// disallowed origin: no CORS header
	req = httptest.NewRequest(http.MethodOptions, "/echo", nil)
	req.Header.Set("Origin", "https://evil.example")
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	// body limit
	req = httptest.NewRequest(http.MethodPost, "/echo", bytes.NewReader([]byte("01234567890123456789")))
	rec = httptest.NewRecorder()
	engine.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusRequestEntityTooLarge, rec.Code)
}

func TestNewFullRouter_Routes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	handler := NewHealthHandler(health.New("test", time.Second))
	router := NewFullRouter(handler, NewBillingHandler(&fakeBilling{}),
		func(c *gin.Context) { c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "unauthorized"}) },
		func(bool) gin.HandlerFunc { return func(c *gin.Context) { c.Next() } },
		[]string{"https://app.saymo.com"}, 1<<20)
	require.NotNil(t, router)

	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health/ready", nil))
	assert.Equal(t, http.StatusOK, rec.Code)

	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/health", nil))
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)

	rec = httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/billing/subscriptions", strings.NewReader("{}"))
	request.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(rec, request)
	assert.Equal(t, http.StatusUnauthorized, rec.Code, "auth middleware protects billing routes")
}

func inAuth(account string) in.AuthContext {
	return in.AuthContext{AccountID: account, Role: "patient"}
}

func writeTestKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	path := filepath.Join(t.TempDir(), "public.pem")
	require.NoError(t, os.WriteFile(path, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0o600))
	return key, path
}

func signTestToken(t *testing.T, key *rsa.PrivateKey, subject string, exp time.Time) string {
	t.Helper()
	now := time.Now()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, jwt.MapClaims{
		"iss": "urn:saymo:identity-service", "aud": "api.saymo", "sub": subject, "role": "patient",
		"exp": exp.Unix(), "iat": now.Unix(), "jti": uuid.NewString(), "session_id": uuid.NewString(), "rev": 1,
	})
	token.Header["kid"] = "key-v1"
	signed, err := token.SignedString(key)
	require.NoError(t, err)
	return signed
}
