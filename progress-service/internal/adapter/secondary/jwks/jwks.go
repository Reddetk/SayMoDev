// Package jwks resolves RS256 public keys from the IAM JWKS endpoint (cache TTL 1h).
package jwks

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/telemetry"
	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// Provider fetches and caches public keys by kid.
type Provider struct {
	url    string
	client *http.Client
	ttl    time.Duration

	mu      sync.RWMutex
	keys    map[string]*rsa.PublicKey
	expires time.Time

	fetchMu sync.Mutex
	now     func() time.Time
}

type jwksDocument struct {
	Keys []jsonWebKey `json:"keys"`
}

type jsonWebKey struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	Alg string `json:"alg"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// New builds a JWKS provider. Unknown kid triggers exactly one forced re-fetch.
func New(url string, ttl time.Duration, timeout time.Duration) *Provider {
	return &Provider{
		url:    url,
		client: &http.Client{Timeout: timeout},
		ttl:    ttl,
		keys:   make(map[string]*rsa.PublicKey),
		now:    func() time.Time { return time.Now() },
	}
}

// PublicKey implements out.JWKSProvider.
func (p *Provider) PublicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	p.mu.RLock()
	key, ok := p.keys[kid]
	fresh := p.now().Before(p.expires)
	p.mu.RUnlock()
	if ok && fresh {
		return key, nil
	}

	// Serialise refreshes: stampede protection, spec allows only one fetch per unknown kid.
	p.fetchMu.Lock()
	defer p.fetchMu.Unlock()

	p.mu.RLock()
	key, ok = p.keys[kid]
	fresh = p.now().Before(p.expires)
	p.mu.RUnlock()
	if ok && fresh {
		return key, nil
	}

	if err := p.refresh(ctx); err != nil {
		return nil, err
	}

	p.mu.RLock()
	defer p.mu.RUnlock()
	if key, ok = p.keys[kid]; ok {
		return key, nil
	}
	return nil, corerr.ErrInvalidToken
}

func (p *Provider) refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, p.url, nil)
	if err != nil {
		return fmt.Errorf("jwks: build request: %w", err)
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", corerr.ErrJWKSUnavailable, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%w: status %d", corerr.ErrJWKSUnavailable, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return fmt.Errorf("%w: read: %v", corerr.ErrJWKSUnavailable, err)
	}
	var doc jwksDocument
	if err := json.Unmarshal(body, &doc); err != nil {
		return fmt.Errorf("%w: decode: %v", corerr.ErrJWKSUnavailable, err)
	}
	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := parseRSA(k)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return fmt.Errorf("%w: no RSA keys", corerr.ErrJWKSUnavailable)
	}

	p.mu.Lock()
	p.keys = keys
	p.expires = p.now().Add(p.ttl)
	p.mu.Unlock()
	telemetry.JWKSActiveKeys.Set(float64(len(keys)))
	return nil
}

func parseRSA(k jsonWebKey) (*rsa.PublicKey, error) {
	nb, err := base64.RawURLEncoding.DecodeString(k.N)
	if err != nil {
		return nil, err
	}
	eb, err := base64.RawURLEncoding.DecodeString(k.E)
	if err != nil {
		return nil, err
	}
	e := 0
	for _, b := range eb {
		e = e<<8 | int(b)
	}
	if e == 0 {
		return nil, fmt.Errorf("jwks: empty exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nb), E: e}, nil
}

var _ out.JWKSProvider = (*Provider)(nil)
