// Package auth — валидация JWT BC#1 (RS256 + JWKS) для BC#2.
package auth

// goca:manual — JWT/JWKS middleware BC#2 по ADR-003/004, goca auth не используется.

import (
	"context"
	"crypto/rsa"
	"crypto/x509"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// Claims — доменная часть проверенного токена.
type Claims struct {
	Subject   string
	Role      string
	SessionID string
	JTI       string
	Rev       int
}

type rawClaims struct {
	Role      string `json:"role"`
	SessionID string `json:"session_id"`
	Rev       int    `json:"rev"`
	jwt.RegisteredClaims
}

// Verifier проверяет подпись и claims.
type Verifier struct {
	issuer   string
	audience string
	jwksURL  string

	localKey *rsa.PublicKey

	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey
	fetchedAt time.Time
	client    httpClient
}

type httpClient interface {
	Get(ctx context.Context, url string) ([]byte, error)
}

// NewVerifier создаёт верификатор. Если publicKeyPath задан — используется локальный
// ключ (local/test); иначе ключи тянутся из JWKS с TTL 1ч.
func NewVerifier(issuer, audience, jwksURL, publicKeyPath string, client httpClient) (*Verifier, error) {
	verifier := &Verifier{
		issuer:   issuer,
		audience: audience,
		jwksURL:  jwksURL,
		keys:     map[string]*rsa.PublicKey{},
		client:   client,
	}
	if publicKeyPath != "" {
		key, err := loadPublicKey(publicKeyPath)
		if err != nil {
			return nil, err
		}
		verifier.localKey = key
	}
	if verifier.localKey == nil && jwksURL == "" {
		return nil, fmt.Errorf("auth: neither JWT_PUBLIC_KEY_PATH nor JWKS_URL is configured")
	}
	return verifier, nil
}

// Verify проверяет токен и возвращает claims.
func (v *Verifier) Verify(ctx context.Context, tokenString string) (Claims, error) {
	claims := &rawClaims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (any, error) {
		if token.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("auth: unexpected alg %q", token.Method.Alg())
		}
		kid, _ := token.Header["kid"].(string)
		return v.key(ctx, kid)
	},
		jwt.WithIssuer(v.issuer),
		jwt.WithAudience(v.audience),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(30*time.Second),
	)
	if err != nil {
		return Claims{}, fmt.Errorf("auth: %w", err)
	}
	if !token.Valid {
		return Claims{}, errors.New("auth: token is not valid")
	}
	if claims.Subject == "" || claims.ID == "" {
		return Claims{}, errors.New("auth: sub/jti claims are required")
	}
	return Claims{
		Subject:   claims.Subject,
		Role:      claims.Role,
		SessionID: claims.SessionID,
		JTI:       claims.ID,
		Rev:       claims.Rev,
	}, nil
}

func (v *Verifier) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if v.localKey != nil {
		return v.localKey, nil
	}

	v.mu.RLock()
	key := v.keys[kid]
	fresh := time.Since(v.fetchedAt) < time.Hour
	v.mu.RUnlock()
	if key != nil && fresh {
		return key, nil
	}
	if err := v.refresh(ctx); err != nil {
		return nil, err
	}
	v.mu.RLock()
	key = v.keys[kid]
	v.mu.RUnlock()
	if key == nil {
		return nil, fmt.Errorf("auth: unknown kid %q", kid)
	}
	return key, nil
}

func (v *Verifier) refresh(ctx context.Context) error {
	if v.client == nil {
		return errors.New("auth: no http client for JWKS")
	}
	body, err := v.client.Get(ctx, v.jwksURL)
	if err != nil {
		return fmt.Errorf("auth: fetch jwks: %w", err)
	}
	var jwks struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.Unmarshal(body, &jwks); err != nil {
		return fmt.Errorf("auth: parse jwks: %w", err)
	}
	keys := make(map[string]*rsa.PublicKey, len(jwks.Keys))
	for _, key := range jwks.Keys {
		if key.Kty != "RSA" {
			continue
		}
		publicKey, err := decodeJWK(key.N, key.E)
		if err != nil {
			continue
		}
		keys[key.Kid] = publicKey
	}
	v.mu.Lock()
	v.keys = keys
	v.fetchedAt = time.Now()
	v.mu.Unlock()
	return nil
}

func loadPublicKey(path string) (*rsa.PublicKey, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("auth: read public key: %w", err)
	}
	block, _ := pem.Decode(body)
	if block == nil {
		return nil, errors.New("auth: public key is not PEM")
	}
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		if key, ok := parsed.(*rsa.PublicKey); ok {
			return key, nil
		}
	}
	if key, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("auth: unsupported public key format")
}
