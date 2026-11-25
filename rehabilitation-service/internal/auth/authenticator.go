// goca:manual — валидация RS256 + L1/L2 revocation; сгенерированный auth не пригоден.
package auth

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// RevocationStore — L2-хранилище отзыва токенов.
type RevocationStore interface {
	JTIBlacklisted(ctx context.Context, jti string) (bool, error)
	AccountRev(ctx context.Context, sub string) (rev int, found bool, err error)
}

// Identity — проверенный субъект запроса.
type Identity struct {
	AccountID string
	Role      string
	JTI       string
	SessionID string
	Rev       int
}

// Options — настройки аутентификатора.
type Options struct {
	JWKSURL  string
	CacheTTL time.Duration
	Now      func() time.Time
	Logger   *zap.Logger
}

type cachedIdentity struct {
	identity Identity
	expires  time.Time
}

// Authenticator валидирует RS256 JWT и проверяет отзыв L1 -> L2.
// L1 хранит только разрешающие решения TTL 60s (ADR-003: окно stale valid ≤60s).
type Authenticator struct {
	keys     *jwksClient
	store    RevocationStore
	cacheTTL time.Duration
	now      func() time.Time
	logger   *zap.Logger

	mu    sync.Mutex
	cache map[string]cachedIdentity
}

func NewAuthenticator(opts Options, store RevocationStore) *Authenticator {
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	cacheTTL := opts.CacheTTL
	if cacheTTL <= 0 {
		cacheTTL = 60 * time.Second
	}
	logger := opts.Logger
	if logger == nil {
		logger = zap.NewNop()
	}
	return &Authenticator{
		keys:     newJWKSClient(opts.JWKSURL, now),
		store:    store,
		cacheTTL: cacheTTL,
		now:      now,
		logger:   logger,
		cache:    map[string]cachedIdentity{},
	}
}

// Authenticate проверяет подпись, claims и отзыв. Возвращает Identity либо одну из sentinel-ошибок.
func (a *Authenticator) Authenticate(ctx context.Context, raw string) (Identity, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Identity{}, ErrMissingToken
	}

	unverified, _, err := jwt.NewParser().ParseUnverified(raw, jwt.MapClaims{})
	if err != nil {
		return Identity{}, ErrMalformed
	}

	alg, _ := unverified.Header["alg"].(string)
	if alg != "RS256" {
		return Identity{}, ErrAlg
	}
	kid, _ := unverified.Header["kid"].(string)
	if kid == "" {
		return Identity{}, ErrKID
	}

	publicKey, err := a.keys.key(ctx, kid)
	if err != nil {
		return Identity{}, err
	}

	claims := &Claims{}
	_, err = jwt.ParseWithClaims(raw, claims,
		func(*jwt.Token) (any, error) { return publicKey, nil },
		jwt.WithValidMethods([]string{"RS256"}),
		jwt.WithLeeway(ClockSkew*time.Second),
		jwt.WithIssuer(Issuer),
		jwt.WithAudience(Audience),
		jwt.WithExpirationRequired(),
		jwt.WithIssuedAt(),
		jwt.WithTimeFunc(a.now),
	)
	if err != nil {
		return Identity{}, mapJWTError(err)
	}

	if err := validateClaims(claims); err != nil {
		return Identity{}, err
	}

	identity := Identity{
		AccountID: claims.Subject,
		Role:      claims.Role,
		JTI:       claims.ID,
		SessionID: claims.SessionID,
		Rev:       claims.Rev,
	}

	if cached, ok := a.cached(identity); ok {
		return cached, nil
	}

	if a.store == nil {
		return Identity{}, fmt.Errorf("%w: no revocation store", ErrRevocationUnavailable)
	}

	blacklisted, err := a.store.JTIBlacklisted(ctx, identity.JTI)
	if err != nil {
		a.logger.Warn("revocation store unavailable", zap.String("jti", identity.JTI), zap.Error(err))
		return Identity{}, fmt.Errorf("%w: %v", ErrRevocationUnavailable, err)
	}
	if blacklisted {
		return Identity{}, ErrRevoked
	}

	accountRev, found, err := a.store.AccountRev(ctx, identity.AccountID)
	if err != nil {
		a.logger.Warn("revocation store unavailable", zap.String("sub", identity.AccountID), zap.Error(err))
		return Identity{}, fmt.Errorf("%w: %v", ErrRevocationUnavailable, err)
	}
	if !found {
		return Identity{}, fmt.Errorf("%w: account rev is missing", ErrRev)
	}
	if identity.Rev < accountRev {
		return Identity{}, fmt.Errorf("%w: token rev %d < account rev %d", ErrRev, identity.Rev, accountRev)
	}

	a.remember(identity, claims.ExpiresAt.Time)
	return identity, nil
}

func validateClaims(claims *Claims) error {
	if claims.IssuedAt == nil {
		return fmt.Errorf("%w: iat is missing", ErrClaims)
	}
	if _, err := uuid.Parse(claims.Subject); err != nil {
		return fmt.Errorf("%w: sub is missing or invalid", ErrClaims)
	}
	if _, err := uuid.Parse(claims.ID); err != nil {
		return fmt.Errorf("%w: jti is missing or invalid", ErrClaims)
	}
	if _, err := uuid.Parse(claims.SessionID); err != nil {
		return fmt.Errorf("%w: session_id is missing or invalid", ErrClaims)
	}
	switch claims.Role {
	case "patient", "relative", "administrator":
	default:
		return fmt.Errorf("%w: role is missing or unknown", ErrClaims)
	}
	if claims.Rev < 0 {
		return fmt.Errorf("%w: rev is negative", ErrRev)
	}
	return nil
}

func mapJWTError(err error) error {
	switch {
	case errors.Is(err, jwt.ErrTokenExpired):
		return ErrExpired
	case errors.Is(err, jwt.ErrTokenInvalidIssuer):
		return ErrIssuer
	case errors.Is(err, jwt.ErrTokenInvalidAudience):
		return ErrAudience
	case errors.Is(err, jwt.ErrTokenSignatureInvalid):
		return ErrSignature
	case errors.Is(err, jwt.ErrTokenUsedBeforeIssued):
		return fmt.Errorf("%w: iat is in the future", ErrClaims)
	case errors.Is(err, jwt.ErrTokenMalformed):
		return ErrMalformed
	case strings.Contains(strings.ToLower(err.Error()), "aud"):
		return ErrAudience
	default:
		return fmt.Errorf("%w: %v", ErrClaims, err)
	}
}

func (a *Authenticator) cached(identity Identity) (Identity, bool) {
	a.mu.Lock()
	defer a.mu.Unlock()

	entry, ok := a.cache[identity.JTI]
	if !ok || a.now().After(entry.expires) {
		delete(a.cache, identity.JTI)
		return Identity{}, false
	}
	return entry.identity, true
}

func (a *Authenticator) remember(identity Identity, tokenExp time.Time) {
	expires := a.now().Add(a.cacheTTL)
	if !tokenExp.IsZero() && tokenExp.Before(expires) {
		expires = tokenExp
	}

	a.mu.Lock()
	defer a.mu.Unlock()
	a.cache[identity.JTI] = cachedIdentity{identity: identity, expires: expires}
}
