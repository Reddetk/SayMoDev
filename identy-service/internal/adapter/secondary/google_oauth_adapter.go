// Package secondary contains outbound adapter implementations.
package secondary

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"go.uber.org/zap"

	corerr "github.com/Reddetk/SayMoDev/identy-service/internal/core/coreErrors"
	valobj "github.com/Reddetk/SayMoDev/identy-service/internal/core/valObj"
	"github.com/Reddetk/SayMoDev/identy-service/internal/logger"
)

//
// Config
//

const (
	googleTokenEndpoint = "https://oauth2.googleapis.com/token"
	googleJWKSEndpoint  = "https://www.googleapis.com/oauth2/v3/certs"
	googleAuthEndpoint  = "https://accounts.google.com/o/oauth2/v2/auth"

	googleIssuer1 = "https://accounts.google.com"
	googleIssuer2 = "accounts.google.com"

	oauthStateTTL = 10 * time.Minute

	// codeVerifierLen is 64 URL-safe random bytes -> 86-char base64url (within RFC 7636 [43,128]).
	codeVerifierLen = 64
)

// GoogleOAuthConfig holds constructor parameters for GoogleOAuthAdapter.
type GoogleOAuthConfig struct {
	ClientID     string
	ClientSecret string
	RedirectURI  string

	// HTTPClient is optional; defaults to a 10-second timeout client.
	HTTPClient *http.Client

	// Logger is optional; defaults to zap.NewNop().
	Logger logger.Logger

	// NowFunc is optional; defaults to time.Now. Used for TTL checks in tests.
	NowFunc func() time.Time
}

//
// JWKS cache types
//

type jwksKey struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type jwksResponse struct {
	Keys []jwksKey `json:"keys"`
}

type jwksCache struct {
	mu        sync.RWMutex
	keys      map[string]*rsa.PublicKey // kid -> public key
	expiresAt time.Time
}

//
// Adapter
//

// GoogleOAuthAdapter implements port/out.GoogleOAuthProvider.
type GoogleOAuthAdapter struct {
	clientID     string
	clientSecret string
	redirectURI  string
	httpClient   *http.Client
	logger       logger.Logger
	now          func() time.Time
	jwks         jwksCache
}

// NewGoogleOAuthAdapter constructs the adapter and validates required config.
func NewGoogleOAuthAdapter(cfg GoogleOAuthConfig) (*GoogleOAuthAdapter, error) {
	if cfg.ClientID == "" {
		return nil, fmt.Errorf("google oauth adapter: ClientID is required")
	}
	if cfg.ClientSecret == "" {
		return nil, fmt.Errorf("google oauth adapter: ClientSecret is required")
	}
	if cfg.RedirectURI == "" {
		return nil, fmt.Errorf("google oauth adapter: RedirectURI is required")
	}

	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	logger := cfg.Logger

	nowFunc := cfg.NowFunc
	if nowFunc == nil {
		nowFunc = time.Now
	}

	return &GoogleOAuthAdapter{
		clientID:     cfg.ClientID,
		clientSecret: cfg.ClientSecret,
		redirectURI:  cfg.RedirectURI,
		httpClient:   client,
		logger:       logger,
		now:          nowFunc,
	}, nil
}

//
// BuildAuthURL
//

// BuildAuthURL implements GoogleOAuthProvider.
// Generates PKCE parameters and a CSRF token, then assembles the Google
// Consent Screen redirect URL.
func (a *GoogleOAuthAdapter) BuildAuthURL(ctx context.Context) (string, valobj.OAuthState, error) {
	// Generate code_verifier: 64 random bytes encoded as base64url = 86 chars.
	verifierBytes := make([]byte, codeVerifierLen)
	if _, err := rand.Read(verifierBytes); err != nil {
		a.logger.Error("google oauth: failed to generate code_verifier", zap.Error(err))
		return "", valobj.OAuthState{}, fmt.Errorf("google oauth BuildAuthURL: %w", err)
	}
	codeVerifier := base64.RawURLEncoding.EncodeToString(verifierBytes)

	// code_challenge = BASE64URL(SHA256(code_verifier))
	sum := sha256.Sum256([]byte(codeVerifier))
	codeChallenge := base64.RawURLEncoding.EncodeToString(sum[:])

	// CSRF state token: UUID v4.
	csrfToken := uuid.New().String()

	expiresAt := a.now().Add(oauthStateTTL).UnixMilli()
	state, err := valobj.NewOAuthState(csrfToken, codeVerifier, codeChallenge, expiresAt)
	if err != nil {
		a.logger.Error("google oauth: failed to construct OAuthState", zap.Error(err))
		return "", valobj.OAuthState{}, fmt.Errorf("google oauth BuildAuthURL: %w", err)
	}

	params := url.Values{}
	params.Set("client_id", a.clientID)
	params.Set("redirect_uri", a.redirectURI)
	params.Set("response_type", "code")
	params.Set("scope", "openid email profile")
	params.Set("code_challenge", codeChallenge)
	params.Set("code_challenge_method", "S256")
	params.Set("state", csrfToken)
	params.Set("prompt", "select_account")

	redirectURL := googleAuthEndpoint + "?" + params.Encode()
	return redirectURL, state, nil
}

//
// ExchangeCode
//

// googleTokenResponse mirrors the fields we care about from Google token endpoint.
type googleTokenResponse struct {
	IDToken string `json:"id_token"`
	Error   string `json:"error"`
}

// rawIDTokenClaims is used for JSON extraction before RS256 verification.
type rawIDTokenClaims struct {
	Iss           string `json:"iss"`
	Aud           string `json:"aud"`
	Sub           string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	Picture       string `json:"picture"`
	Exp           int64  `json:"exp"`
	Kid           string `json:"-"` // extracted from header, not payload
}

// ExchangeCode implements GoogleOAuthProvider.
func (a *GoogleOAuthAdapter) ExchangeCode(
	ctx context.Context,
	code string,
	state valobj.OAuthState,
) (valobj.GoogleClaims, error) {
	idToken, err := a.fetchIDToken(ctx, code, state.CodeVerifier())
	if err != nil {
		return valobj.GoogleClaims{}, err
	}

	claims, err := a.verifyIDToken(ctx, idToken)
	if err != nil {
		return valobj.GoogleClaims{}, err
	}

	return valobj.NewGoogleClaims(
		claims.Sub,
		claims.Email,
		claims.Name,
		claims.Picture,
		claims.EmailVerified,
	)
}

// fetchIDToken POSTs to Google token endpoint and returns the raw id_token JWT.
func (a *GoogleOAuthAdapter) fetchIDToken(ctx context.Context, code, codeVerifier string) (string, error) {
	form := url.Values{}
	form.Set("grant_type", "authorization_code")
	form.Set("code", code)
	form.Set("redirect_uri", a.redirectURI)
	form.Set("client_id", a.clientID)
	form.Set("client_secret", a.clientSecret)
	form.Set("code_verifier", codeVerifier)

	req, err := http.NewRequestWithContext(
		ctx, http.MethodPost, googleTokenEndpoint,
		strings.NewReader(form.Encode()),
	)
	if err != nil {
		return "", fmt.Errorf("google oauth: build token request: %w", corerr.ErrOAuthTokenExchangeFailed)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logger.Error("google oauth: token exchange transport error", zap.Error(err))
		return "", corerr.ErrOAuthTokenExchangeFailed
	}
	defer func() { _ = resp.Body.Close() }()

	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode != http.StatusOK {
		a.logger.Error("google oauth: token endpoint non-200", zap.Int("status", resp.StatusCode))
		return "", corerr.ErrOAuthTokenExchangeFailed
	}

	var tokenResp googleTokenResponse
	if err := json.Unmarshal(body, &tokenResp); err != nil {
		a.logger.Error("google oauth: failed to parse token response", zap.Error(err))
		return "", corerr.ErrOAuthTokenExchangeFailed
	}
	if tokenResp.Error != "" {
		a.logger.Error("google oauth: token response contains error", zap.String("google_error", tokenResp.Error))
		return "", corerr.ErrOAuthTokenExchangeFailed
	}
	if tokenResp.IDToken == "" {
		a.logger.Error("google oauth: id_token missing from token response")
		return "", corerr.ErrOAuthIDTokenInvalid
	}

	return tokenResp.IDToken, nil
}

// verifyIDToken validates the JWT signature (RS256) and claims.
func (a *GoogleOAuthAdapter) verifyIDToken(ctx context.Context, idToken string) (*rawIDTokenClaims, error) {
	parts := strings.Split(idToken, ".")
	if len(parts) != 3 {
		return nil, corerr.ErrOAuthIDTokenInvalid
	}

	// Decode header to get kid.
	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, corerr.ErrOAuthIDTokenInvalid
	}
	var header struct {
		Kid string `json:"kid"`
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil {
		return nil, corerr.ErrOAuthIDTokenInvalid
	}
	if header.Alg != "RS256" {
		a.logger.Error("google oauth: unexpected token algorithm", zap.String("alg", header.Alg))
		return nil, corerr.ErrOAuthIDTokenInvalid
	}

	// Fetch (or hit cache) JWKS.
	pubKey, err := a.getPublicKey(ctx, header.Kid)
	if err != nil {
		return nil, err // ErrOAuthJWKSUnavailable or ErrOAuthIDTokenInvalid
	}

	// Verify RS256 signature.
	signingInput := parts[0] + "." + parts[1]
	sigBytes, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil {
		return nil, corerr.ErrOAuthIDTokenInvalid
	}
	digest := sha256.Sum256([]byte(signingInput))
	if err := rsa.VerifyPKCS1v15(pubKey, crypto.SHA256, digest[:], sigBytes); err != nil {
		a.logger.Error("google oauth: RS256 signature verification failed", zap.Error(err))
		return nil, corerr.ErrOAuthIDTokenInvalid
	}

	// Decode payload.
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, corerr.ErrOAuthIDTokenInvalid
	}
	var claims rawIDTokenClaims
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return nil, corerr.ErrOAuthIDTokenInvalid
	}

	// Validate claims.
	now := a.now().UnixMilli()
	if claims.Exp < now {
		a.logger.Error("google oauth: id_token expired")
		return nil, corerr.ErrOAuthIDTokenInvalid
	}
	if claims.Iss != googleIssuer1 && claims.Iss != googleIssuer2 {
		a.logger.Error("google oauth: invalid iss", zap.String("iss", claims.Iss))
		return nil, corerr.ErrOAuthIDTokenInvalid
	}
	if claims.Aud != a.clientID {
		a.logger.Error("google oauth: invalid aud")
		return nil, corerr.ErrOAuthIDTokenInvalid
	}
	if !claims.EmailVerified {
		return nil, corerr.ErrOAuthEmailNotVerified
	}

	return &claims, nil
}

//
// JWKS cache
//

// getPublicKey returns the RSA public key for the given kid, using the cache.
// Fail-closed: returns ErrOAuthJWKSUnavailable if JWKS cannot be fetched.
func (a *GoogleOAuthAdapter) getPublicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	// Fast path: read from cache.
	a.jwks.mu.RLock()
	if a.now().Before(a.jwks.expiresAt) {
		key, ok := a.jwks.keys[kid]
		a.jwks.mu.RUnlock()
		if ok {
			return key, nil
		}
		// kid not in cache  fall through to refresh.
	} else {
		a.jwks.mu.RUnlock()
	}

	// Slow path: refresh cache.
	return a.refreshJWKS(ctx, kid)
}

func (a *GoogleOAuthAdapter) refreshJWKS(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	a.jwks.mu.Lock()
	defer a.jwks.mu.Unlock()

	// Double-check after acquiring write lock.
	if a.now().Before(a.jwks.expiresAt) {
		if key, ok := a.jwks.keys[kid]; ok {
			return key, nil
		}
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, googleJWKSEndpoint, nil)
	if err != nil {
		a.logger.Error("google oauth: build JWKS request failed", zap.Error(err))
		return nil, corerr.ErrOAuthJWKSUnavailable
	}

	resp, err := a.httpClient.Do(req)
	if err != nil {
		a.logger.Error("google oauth: JWKS fetch transport error", zap.Error(err))
		return nil, corerr.ErrOAuthJWKSUnavailable
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		a.logger.Error("google oauth: JWKS endpoint non-200", zap.Int("status", resp.StatusCode))
		return nil, corerr.ErrOAuthJWKSUnavailable
	}

	body, _ := io.ReadAll(resp.Body)
	var jwksResp jwksResponse
	if err := json.Unmarshal(body, &jwksResp); err != nil {
		a.logger.Error("google oauth: JWKS parse failed", zap.Error(err))
		return nil, corerr.ErrOAuthJWKSUnavailable
	}

	// Parse Cache-Control max-age; default to 6h if absent.
	ttl := parseCacheControlMaxAge(resp.Header.Get("Cache-Control"), 6*time.Hour)

	newKeys := make(map[string]*rsa.PublicKey, len(jwksResp.Keys))
	for _, k := range jwksResp.Keys {
		if k.Kty != "RSA" || k.Use != "sig" {
			continue
		}
		pub, err := jwkToRSAPublicKey(k)
		if err != nil {
			a.logger.Warn("google oauth: skip malformed JWK",
				zap.String("kid", k.Kid),
				zap.Error(err),
			)
			continue
		}
		newKeys[k.Kid] = pub
	}

	if len(newKeys) == 0 {
		a.logger.Error("google oauth: JWKS contains no usable RSA keys")
		return nil, corerr.ErrOAuthJWKSUnavailable
	}

	a.jwks.keys = newKeys
	a.jwks.expiresAt = a.now().Add(ttl)

	key, ok := newKeys[kid]
	if !ok {
		a.logger.Error("google oauth: kid not found in JWKS after refresh", zap.String("kid", kid))
		return nil, corerr.ErrOAuthIDTokenInvalid
	}
	return key, nil
}

//
// ValidateState
//

// ValidateState implements GoogleOAuthProvider.
func (a *GoogleOAuthAdapter) ValidateState(
	ctx context.Context,
	receivedCSRF string,
	storedState valobj.OAuthState,
) error {
	if storedState.IsExpired(a.now().UnixMilli()) {
		return corerr.ErrOAuthStateExpired
	}
	// Constant-time comparison to prevent timing attacks.
	if !hmacEqual([]byte(receivedCSRF), []byte(storedState.CSRFToken())) {
		return corerr.ErrOAuthStateCSRFMismatch
	}
	return nil
}

//
// Helpers
//

// hmacEqual is a constant-time bytes comparison (same semantics as hmac.Equal).
func hmacEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// jwkToRSAPublicKey parses base64url-encoded n and e into an *rsa.PublicKey.
func jwkToRSAPublicKey(k jwksKey) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, fmt.Errorf("decode n: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, fmt.Errorf("decode e: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	eInt := new(big.Int).SetBytes(eBytes)
	if !eInt.IsInt64() {
		return nil, fmt.Errorf("exponent too large")
	}

	return &rsa.PublicKey{N: n, E: int(eInt.Int64())}, nil
}

// parseCacheControlMaxAge extracts max-age from a Cache-Control header value.
// Falls back to defaultTTL on any parse error or missing directive.
func parseCacheControlMaxAge(header string, defaultTTL time.Duration) time.Duration {
	for _, part := range strings.Split(header, ",") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "max-age=") {
			var secs int64
			if _, err := fmt.Sscanf(part, "max-age=%d", &secs); err == nil && secs > 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	return defaultTTL
}
