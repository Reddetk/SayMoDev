// goca:manual — BC#4 customer/supplier HTTP client с circuit breaker и fail-closed.
package bc4

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"
)

const (
	breakerThreshold = 5
	breakerOpenFor   = 30 * time.Second
)

type Client struct {
	baseURL string
	token   string
	http    *http.Client

	mu        sync.Mutex
	failures  int
	openUntil time.Time
	now       func() time.Time
}

func New(baseURL, serviceToken string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		token:   serviceToken,
		http:    &http.Client{Timeout: 2 * time.Second},
		now:     time.Now,
	}
}

// ActivePatients вызывает BC#4 GET /admin/progress/program/:id/version/:v/active-patients.
func (c *Client) ActivePatients(ctx context.Context, programID string, version int) (int, error) {
	if c.baseURL == "" {
		return 0, fmt.Errorf("bc4 url is not configured")
	}
	if !c.allow() {
		return 0, fmt.Errorf("bc4 circuit breaker is open")
	}

	url := fmt.Sprintf("%s/admin/progress/program/%s/version/%d/active-patients", c.baseURL, programID, version)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, err
	}
	if c.token != "" {
		req.Header.Set("X-Service-Token", c.token)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		c.failure()
		return 0, fmt.Errorf("bc4 request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		c.failure()
		return 0, fmt.Errorf("bc4 status %d", resp.StatusCode)
	}

	var payload struct {
		ActivePatientCount int `json:"activePatientCount"`
		ActiveCount        int `json:"activeCount"`
		Count              int `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&payload); err != nil {
		c.failure()
		return 0, fmt.Errorf("bc4 decode: %w", err)
	}
	c.success()

	switch {
	case payload.ActivePatientCount != 0:
		return payload.ActivePatientCount, nil
	case payload.ActiveCount != 0:
		return payload.ActiveCount, nil
	default:
		return payload.Count, nil
	}
}

func (c *Client) allow() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now().After(c.openUntil)
}

func (c *Client) success() {
	c.mu.Lock()
	c.failures = 0
	c.openUntil = time.Time{}
	c.mu.Unlock()
}

func (c *Client) failure() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.failures++
	if c.failures >= breakerThreshold {
		c.openUntil = c.now().Add(breakerOpenFor)
		c.failures = 0
	}
}
