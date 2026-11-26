package service

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/in"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

const (
	clockSkew   = 30 * time.Second
	l1ValidTTL  = 60 * time.Second
	l1MaxTokens = 10_000
)

// TokenService implements BC#1 Token Validation Flow: signature → claims → revocation L1→L2.
type TokenService struct {
	jwks     out.JWKSProvider
	revoked  out.RevocationStore
	issuer   string
	audience string
	now      func() time.Time

	mu    sync.RWMutex
	valid map[string]time.Time
}

// NewTokenService wires JWT validation.
func NewTokenService(jwks out.JWKSProvider, revoked out.RevocationStore, issuer, audience string) *TokenService {
	return &TokenService{
		jwks: jwks, revoked: revoked, issuer: issuer, audience: audience,
		now:   func() time.Time { return time.Now() },
		valid: make(map[string]time.Time),
	}
}

type claims struct {
	Role      string `json:"role"`
	SessionID string `json:"session_id"`
	Rev       int64  `json:"rev"`
	jwt.RegisteredClaims
}

// ValidateToken verifies a raw bearer token.
func (s *TokenService) ValidateToken(ctx context.Context, rawToken string) (in.AuthContext, error) {
	parser := jwt.NewParser(
		jwt.WithValidMethods([]string{jwt.SigningMethodRS256.Alg()}),
		jwt.WithLeeway(clockSkew),
		jwt.WithIssuer(s.issuer),
		jwt.WithAudience(s.audience),
		jwt.WithExpirationRequired(),
	)

	parsed, err := parser.ParseWithClaims(rawToken, &claims{}, func(token *jwt.Token) (any, error) {
		kid, _ := token.Header["kid"].(string)
		if kid == "" {
			return nil, corerr.ErrInvalidToken
		}
		return s.jwks.PublicKey(ctx, kid)
	})
	if err != nil {
		if errors.Is(err, corerr.ErrJWKSUnavailable) {
			return in.AuthContext{}, err
		}
		return in.AuthContext{}, corerr.ErrInvalidToken
	}
	cl, ok := parsed.Claims.(*claims)
	if !ok || !parsed.Valid {
		return in.AuthContext{}, corerr.ErrInvalidToken
	}
	if cl.Subject == "" || cl.ID == "" {
		return in.AuthContext{}, corerr.ErrInvalidToken
	}

	// Revocation: jti may be served from the 60s L1 window, account rev is always read fresh from L2.
	if !s.isL1Valid(cl.ID) {
		blacklisted, err := s.revoked.IsJTIBlacklisted(ctx, cl.ID)
		if err != nil {
			return in.AuthContext{}, corerr.ErrAuthUnavailable
		}
		if blacklisted {
			return in.AuthContext{}, corerr.ErrTokenRevoked
		}
	}
	accountRev, found, err := s.revoked.AccountRev(ctx, cl.Subject)
	if err != nil {
		return in.AuthContext{}, corerr.ErrAuthUnavailable
	}
	if found && accountRev > cl.Rev {
		return in.AuthContext{}, corerr.ErrTokenRevoked
	}

	s.markL1Valid(cl.ID)

	return in.AuthContext{
		AccountID: cl.Subject,
		Role:      normalizeRole(cl.Role),
		SessionID: cl.SessionID,
		Rev:       cl.Rev,
	}, nil
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "administrator", "admin":
		return "administrator"
	case "patient":
		return "patient"
	case "relative":
		return "relative"
	default:
		return role
	}
}

func (s *TokenService) isL1Valid(jti string) bool {
	s.mu.RLock()
	exp, ok := s.valid[jti]
	s.mu.RUnlock()
	return ok && s.now().Before(exp)
}

func (s *TokenService) markL1Valid(jti string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(s.valid) >= l1MaxTokens {
		s.valid = make(map[string]time.Time, l1MaxTokens)
	}
	s.valid[jti] = s.now().Add(l1ValidTTL)
}

var _ in.TokenValidator = (*TokenService)(nil)
