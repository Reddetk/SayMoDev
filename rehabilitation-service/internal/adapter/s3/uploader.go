// goca:manual — загрузка PDF в S3 с дедупликацией и circuit breaker (docs §PDF).
package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/platform/metrics"
	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
)

const (
	breakerThreshold = 5
	breakerOpenFor   = 30 * time.Second

	stateClosed   = "closed"
	stateOpen     = "open"
	stateHalfOpen = "half_open"

	breakerService = "cdn"
)

var ErrBreakerOpen = fmt.Errorf("s3 circuit breaker is open: %w", out.ErrUnavailable)

type breaker struct {
	mu        sync.Mutex
	failures  int
	state     string
	openUntil time.Time
	now       func() time.Time
}

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.state == stateOpen {
		if b.now().Before(b.openUntil) {
			return false
		}
		b.state = stateHalfOpen
	}
	return true
}

func (b *breaker) success() {
	b.mu.Lock()
	b.failures = 0
	b.state = stateClosed
	b.openUntil = time.Time{}
	b.mu.Unlock()
}

func (b *breaker) failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.state == stateHalfOpen || b.failures >= breakerThreshold {
		b.state = stateOpen
		b.openUntil = b.now().Add(breakerOpenFor)
		b.failures = 0
	}
}

func (b *breaker) currentState() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

type Uploader struct {
	client    *s3.Client
	bucket    string
	endpoint  string
	breaker   breaker
	met       *metrics.Metrics
	lastState string
	stateMu   sync.Mutex
}

func NewUploader(client *s3.Client, bucket, endpoint string) *Uploader {
	return &Uploader{
		client:    client,
		bucket:    bucket,
		endpoint:  strings.TrimRight(endpoint, "/"),
		breaker:   breaker{now: time.Now, state: stateClosed},
		lastState: stateClosed,
	}
}

// SetMetrics включает экспорт метрик загрузки и circuit breaker.
func (u *Uploader) SetMetrics(m *metrics.Metrics) { u.met = m }

func breakerValue(state string) float64 {
	switch state {
	case stateOpen:
		return 1
	case stateHalfOpen:
		return 2
	default:
		return 0
	}
}

// observeBreaker публикует состояние breaker и переходы (docs: service=cdn).
func (u *Uploader) observeBreaker() {
	if u.met == nil {
		return
	}
	state := u.breaker.currentState()
	u.stateMu.Lock()
	previous := u.lastState
	u.lastState = state
	u.stateMu.Unlock()

	u.met.CircuitBreakerState.WithLabelValues(breakerService).Set(breakerValue(state))
	if previous != state {
		u.met.CircuitBreakerTransitions.WithLabelValues(breakerService, previous, state).Inc()
	}
}

// UploadPDF возвращает постоянный URL, SHA-256 и размер загруженного объекта.
func (u *Uploader) UploadPDF(ctx context.Context, programID, lessonID string, content []byte) (string, string, int64, error) {
	if !u.breaker.allow() {
		u.observeBreaker()
		if u.met != nil {
			u.met.CDNUploadTotal.WithLabelValues("circuit_open").Inc()
		}
		return "", "", 0, ErrBreakerOpen
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	key := fmt.Sprintf("programs/%s/lessons/%s/%s.pdf", programID, lessonID, hash)

	started := time.Now()
	_, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(u.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("application/pdf"),
	})
	latency := time.Since(started).Seconds()
	if u.met != nil {
		u.met.CDNUploadLatency.Observe(latency)
	}
	if err != nil {
		u.breaker.failure()
		u.observeBreaker()
		if u.met != nil {
			status := "failed"
			if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
				status = "timeout"
			}
			u.met.CDNUploadTotal.WithLabelValues(status).Inc()
		}
		return "", "", 0, fmt.Errorf("s3 put object: %w: %v", out.ErrUnavailable, err)
	}
	u.breaker.success()
	u.observeBreaker()
	if u.met != nil {
		u.met.CDNUploadTotal.WithLabelValues("success").Inc()
	}

	return fmt.Sprintf("%s/%s/%s", u.endpoint, u.bucket, key), hash, int64(len(content)), nil
}

// ObjectURL — постоянный URL объекта (для дедупликации из БД).
func (u *Uploader) ObjectURL(key string) (string, error) {
	parsed, err := url.Parse(u.endpoint)
	if err != nil {
		return "", err
	}
	return parsed.JoinPath(u.bucket, key).String(), nil
}
