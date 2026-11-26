// goca:manual — загрузка PDF в S3 с дедупликацией и circuit breaker (docs §PDF).
package s3

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/Reddetk/SayMoDev/rehabilitation-service/internal/port/out"
)

const (
	breakerThreshold = 5
	breakerOpenFor   = 30 * time.Second
)

var ErrBreakerOpen = fmt.Errorf("s3 circuit breaker is open: %w", out.ErrUnavailable)

type breaker struct {
	mu        sync.Mutex
	failures  int
	openUntil time.Time
	now       func() time.Time
}

func (b *breaker) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.now().After(b.openUntil)
}

func (b *breaker) success() {
	b.mu.Lock()
	b.failures = 0
	b.openUntil = time.Time{}
	b.mu.Unlock()
}

func (b *breaker) failure() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures++
	if b.failures >= breakerThreshold {
		b.openUntil = b.now().Add(breakerOpenFor)
		b.failures = 0
	}
}

type Uploader struct {
	client   *s3.Client
	bucket   string
	endpoint string
	breaker  breaker
}

func NewUploader(client *s3.Client, bucket, endpoint string) *Uploader {
	return &Uploader{client: client, bucket: bucket, endpoint: strings.TrimRight(endpoint, "/"), breaker: breaker{now: time.Now}}
}

// UploadPDF возвращает постоянный URL, SHA-256 и размер загруженного объекта.
func (u *Uploader) UploadPDF(ctx context.Context, programID, lessonID string, content []byte) (string, string, int64, error) {
	if !u.breaker.allow() {
		return "", "", 0, ErrBreakerOpen
	}

	hash := fmt.Sprintf("%x", sha256.Sum256(content))
	key := fmt.Sprintf("programs/%s/lessons/%s/%s.pdf", programID, lessonID, hash)

	_, err := u.client.PutObject(ctx, &s3.PutObjectInput{
		Bucket:      aws.String(u.bucket),
		Key:         aws.String(key),
		Body:        bytes.NewReader(content),
		ContentType: aws.String("application/pdf"),
	})
	if err != nil {
		u.breaker.failure()
		return "", "", 0, fmt.Errorf("s3 put object: %w: %v", out.ErrUnavailable, err)
	}
	u.breaker.success()

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
