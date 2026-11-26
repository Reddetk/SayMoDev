package auth

// goca:manual — HTTP-клиент JWKS с таймаутом.

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// HTTPClient реализует httpClient для JWKS.
type HTTPClient struct {
	client *http.Client
}

// NewHTTPClient создаёт клиент с таймаутом 2с (fail-closed).
func NewHTTPClient() *HTTPClient {
	return &HTTPClient{client: &http.Client{Timeout: 2 * time.Second}}
}

// Get скачивает URL.
func (c *HTTPClient) Get(ctx context.Context, url string) ([]byte, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	response, err := c.client.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("jwks status %d", response.StatusCode)
	}
	return io.ReadAll(io.LimitReader(response.Body, 1<<20))
}
