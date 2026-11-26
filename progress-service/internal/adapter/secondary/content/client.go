// Package content implements the BC#3 read-only catalog client (sync HTTP, 2s timeout, 1 retry, circuit breaker).
package content

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/Reddetk/SayMoDev/progress-service/internal/adapter/telemetry"
	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// Client calls BC#3 GET /content/programs* endpoints.
type Client struct {
	baseURL    string
	http       *http.Client
	retryDelay time.Duration
	breaker    *breaker
}

// Options configures timeouts and the circuit breaker.
type Options struct {
	Timeout          time.Duration
	RetryDelay       time.Duration
	FailureThreshold int
	OpenDuration     time.Duration
	HalfOpenMax      int
}

// New builds the BC#3 client.
func New(baseURL string, opts Options) *Client {
	if opts.FailureThreshold <= 0 {
		opts.FailureThreshold = 5
	}
	if opts.OpenDuration <= 0 {
		opts.OpenDuration = 30 * time.Second
	}
	if opts.Timeout <= 0 {
		opts.Timeout = 2 * time.Second
	}
	return &Client{
		baseURL:    baseURL,
		http:       &http.Client{Timeout: opts.Timeout},
		retryDelay: opts.RetryDelay,
		breaker:    newBreaker(opts.FailureThreshold, opts.OpenDuration, opts.HalfOpenMax),
	}
}

// ResolveProgram finds the program for a classifier tuple.
func (c *Client) ResolveProgram(ctx context.Context, difficulty, aphasiaType string) (out.ProgramInfo, error) {
	q := url.Values{}
	q.Set("difficulty", difficulty)
	if aphasiaType != "" {
		q.Set("aphasia_type", aphasiaType)
	}
	body, err := c.get(ctx, "/content/programs?"+q.Encode(), "programs")
	if err != nil {
		if errors.Is(err, corerr.ErrProgramNotFound) {
			return out.ProgramInfo{}, corerr.ErrNoProgramForClassifier
		}
		return out.ProgramInfo{}, err
	}
	items, err := decodeProgramList(body)
	if err != nil || len(items) == 0 {
		return out.ProgramInfo{}, corerr.ErrNoProgramForClassifier
	}
	first := items[0]
	return c.GetProgram(ctx, first.programID(), first.Version)
}

// GetProgram loads one program (version 0 means latest).
func (c *Client) GetProgram(ctx context.Context, programID string, version int) (out.ProgramInfo, error) {
	if _, err := uuid.Parse(programID); err != nil {
		return out.ProgramInfo{}, corerr.ErrProgramNotFound
	}
	path := "/content/programs/" + programID
	if version > 0 {
		path += "?version=" + strconv.Itoa(version)
	}
	body, err := c.get(ctx, path, "program")
	if err != nil {
		return out.ProgramInfo{}, err
	}
	info, err := decodeProgram(body)
	if err != nil {
		return out.ProgramInfo{}, fmt.Errorf("%w: %v", corerr.ErrProgramCatalogUnavailable, err)
	}
	return info, nil
}

func (c *Client) get(ctx context.Context, path, endpoint string) ([]byte, error) {
	start := time.Now()
	defer func() {
		telemetry.BC3APILatency.WithLabelValues(endpoint).Observe(time.Since(start).Seconds())
	}()

	if !c.breaker.allow() {
		telemetry.BC3APICallsTotal.WithLabelValues(endpoint, "circuit_open").Inc()
		return nil, corerr.ErrProgramCatalogUnavailable
	}

	body, err := c.doOnce(ctx, path)
	if err == nil {
		c.breaker.success()
		telemetry.BC3APICallsTotal.WithLabelValues(endpoint, "success").Inc()
		return body, nil
	}
	if errors.Is(err, corerr.ErrProgramNotFound) {
		c.breaker.success()
		telemetry.BC3APICallsTotal.WithLabelValues(endpoint, "success").Inc()
		return nil, err
	}
	// One retry with the configured delay (spec: Retry 1×500ms).
	if c.retryDelay > 0 {
		select {
		case <-ctx.Done():
			c.breaker.failure()
			telemetry.BC3APICallsTotal.WithLabelValues(endpoint, "timeout").Inc()
			return nil, corerr.ErrProgramCatalogUnavailable
		case <-time.After(c.retryDelay):
		}
		if body, retryErr := c.doOnce(ctx, path); retryErr == nil {
			c.breaker.success()
			telemetry.BC3APICallsTotal.WithLabelValues(endpoint, "success").Inc()
			return body, nil
		}
	}
	c.breaker.failure()
	telemetry.BC3APICallsTotal.WithLabelValues(endpoint, "failure").Inc()
	return nil, corerr.ErrProgramCatalogUnavailable
}

func (c *Client) doOnce(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, corerr.ErrProgramNotFound
	case resp.StatusCode >= 500:
		return nil, fmt.Errorf("bc3: status %d", resp.StatusCode)
	case resp.StatusCode != http.StatusOK:
		return nil, corerr.ErrProgramCatalogUnavailable
	}
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

type programListJSON struct {
	Programs []programJSON `json:"programs"`
}

type programJSON struct {
	ProgramID     string `json:"programId"`
	ID            string `json:"id"`
	Version       int    `json:"version"`
	TotalLessons  int    `json:"totalLessons"`
	TotalLessons2 int    `json:"total_lessons"`
	Difficulty    string `json:"difficulty"`
	AphasiaType   string `json:"aphasiaType"`
	AphasiaType2  string `json:"aphasia_type"`
	Classifier    *struct {
		Difficulty   string `json:"difficulty"`
		AphasiaType  string `json:"aphasiaType"`
		AphasiaType2 string `json:"aphasia_type"`
	} `json:"classifier"`
	Lessons []lessonJSON `json:"lessons"`
}

type lessonJSON struct {
	LessonID       string `json:"lessonId"`
	ID             string `json:"id"`
	OrderPosition  int    `json:"orderPosition"`
	OrderPosition2 int    `json:"order_position"`
	LessonType     string `json:"lessonType"`
	LessonType2    string `json:"lesson_type"`
	Title          string `json:"title"`
}

func (p programJSON) programID() string {
	if p.ProgramID != "" {
		return p.ProgramID
	}
	return p.ID
}

func (p programJSON) total() int {
	if p.TotalLessons > 0 {
		return p.TotalLessons
	}
	return p.TotalLessons2
}

func (l lessonJSON) lessonID() string {
	if l.LessonID != "" {
		return l.LessonID
	}
	return l.ID
}

func (l lessonJSON) position() int {
	if l.OrderPosition > 0 {
		return l.OrderPosition
	}
	return l.OrderPosition2
}

func (l lessonJSON) lessonType() string {
	if l.LessonType != "" {
		return l.LessonType
	}
	if l.LessonType2 != "" {
		return l.LessonType2
	}
	return "regular"
}

// decodeProgramList tolerates both a bare array and {"programs":[...]}.
func decodeProgramList(body []byte) ([]programJSON, error) {
	var arr []programJSON
	if err := json.Unmarshal(body, &arr); err == nil {
		return arr, nil
	}
	var wrapper programListJSON
	if err := json.Unmarshal(body, &wrapper); err != nil {
		return nil, err
	}
	return wrapper.Programs, nil
}

func decodeProgram(body []byte) (out.ProgramInfo, error) {
	var p programJSON
	if err := json.Unmarshal(body, &p); err != nil {
		return out.ProgramInfo{}, err
	}
	id, err := uuid.Parse(p.programID())
	if err != nil {
		return out.ProgramInfo{}, fmt.Errorf("invalid programId: %w", err)
	}
	info := out.ProgramInfo{ProgramID: id, Version: p.Version, TotalLessons: p.total()}
	info.Difficulty = p.Difficulty
	info.AphasiaType = p.AphasiaType
	if p.Classifier != nil {
		if info.Difficulty == "" {
			info.Difficulty = p.Classifier.Difficulty
		}
		if info.AphasiaType == "" {
			info.AphasiaType = p.Classifier.AphasiaType
		}
		if info.AphasiaType == "" {
			info.AphasiaType = p.Classifier.AphasiaType2
		}
	}
	if info.AphasiaType == "" {
		info.AphasiaType = p.AphasiaType2
	}
	for _, l := range p.Lessons {
		lid, err := uuid.Parse(l.lessonID())
		if err != nil {
			return out.ProgramInfo{}, fmt.Errorf("invalid lessonId: %w", err)
		}
		info.Lessons = append(info.Lessons, out.LessonInfo{
			LessonID: lid, OrderPosition: l.position(), LessonType: l.lessonType(), Title: l.Title,
		})
	}
	if info.Version == 0 {
		info.Version = 1
	}
	if info.TotalLessons == 0 {
		info.TotalLessons = len(info.Lessons)
	}
	return info, nil
}

// breaker is a minimal circuit breaker: N failures open it for the configured duration.
type breaker struct {
	mu        sync.Mutex
	threshold int
	openFor   time.Duration
	halfMax   int

	failures  int
	openUntil time.Time
	halfOpen  int
}

func newBreaker(threshold int, openFor time.Duration, halfMax int) *breaker {
	if halfMax <= 0 {
		halfMax = 2
	}
	return &breaker{threshold: threshold, openFor: openFor, halfMax: halfMax}
}

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return true
	}
	if time.Now().Before(b.openUntil) {
		return false
	}
	// half-open
	if b.halfOpen < b.halfMax {
		b.halfOpen++
		return true
	}
	return false
}

func (b *breaker) success() {
	b.mu.Lock()
	b.failures = 0
	b.openUntil = time.Time{}
	b.halfOpen = 0
	b.mu.Unlock()
}

func (b *breaker) failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= b.threshold {
		b.openUntil = time.Now().Add(b.openFor)
		b.halfOpen = 0
	}
}

var _ out.ContentCatalog = (*Client)(nil)
