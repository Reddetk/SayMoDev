// goca:manual — JWKS-клиент с TTL 1h и fail-closed; шаблон enterprise не генерирует.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"
)

const (
	jwksTTL         = time.Hour
	jwksRetryWindow = time.Minute
)

type jwksDocument struct {
	Keys []struct {
		Kty string `json:"kty"`
		Use string `json:"use"`
		Alg string `json:"alg"`
		Kid string `json:"kid"`
		N   string `json:"n"`
		E   string `json:"e"`
	} `json:"keys"`
}

// jwksClient кэширует публичные ключи по kid (TTL 1h) и делает один fetch
// при неизвестном kid, но не чаще раза в минуту (ADR-004).
type jwksClient struct {
	url    string
	client *http.Client
	now    func() time.Time

	mu          sync.Mutex
	keys        map[string]*rsa.PublicKey
	fetchedAt   time.Time
	lastAttempt time.Time
}

func newJWKSClient(url string, now func() time.Time) *jwksClient {
	return &jwksClient{
		url:    url,
		client: &http.Client{Timeout: 2 * time.Second},
		now:    now,
		keys:   map[string]*rsa.PublicKey{},
	}
}

func (c *jwksClient) key(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	c.mu.Lock()
	if key, ok := c.keys[kid]; ok && c.now().Sub(c.fetchedAt) < jwksTTL {
		c.mu.Unlock()
		return key, nil
	}
	lastAttempt := c.lastAttempt
	c.mu.Unlock()

	// Не чаще одного fetch в минуту. Если TTL истёк, а обновить ключ нельзя —
	// fail-closed (ErrJWKS), а не бесконечное доверие старому ключу.
	if !lastAttempt.IsZero() && c.now().Sub(lastAttempt) < jwksRetryWindow {
		return nil, ErrJWKS
	}

	c.mu.Lock()
	c.lastAttempt = c.now()
	c.mu.Unlock()

	keys, err := c.fetch(ctx)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrJWKS, err)
	}

	c.mu.Lock()
	c.keys = keys
	c.fetchedAt = c.now()
	c.mu.Unlock()

	if key, ok := keys[kid]; ok {
		return key, nil
	}
	return nil, ErrKID
}

func (c *jwksClient) fetch(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	if c.url == "" {
		return nil, fmt.Errorf("empty JWKS URL")
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}

	var doc jwksDocument
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return nil, err
	}

	keys := make(map[string]*rsa.PublicKey, len(doc.Keys))
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || (k.Use != "" && k.Use != "sig") {
			continue
		}
		pub, err := parseRSAPublicKey(k.N, k.E)
		if err != nil {
			continue
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return nil, fmt.Errorf("no usable RSA keys")
	}
	return keys, nil
}

func parseRSAPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	if len(nBytes) == 0 || len(eBytes) == 0 {
		return nil, fmt.Errorf("empty RSA parameters")
	}

	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() <= 1 {
		return nil, fmt.Errorf("invalid RSA exponent")
	}
	return &rsa.PublicKey{N: new(big.Int).SetBytes(nBytes), E: int(e.Int64())}, nil
}
