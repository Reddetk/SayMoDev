// goca:manual — presigned GET для PDF (Yandex Object Storage / s3mock).
package s3

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Presigner struct {
	client *s3.Client
	bucket string
}

func NewPresigner(client *s3.Client, bucket string) *Presigner {
	return &Presigner{client: client, bucket: bucket}
}

// PresignGet строит presigned GET URL по постоянному content_url.
func (p *Presigner) PresignGet(ctx context.Context, contentURL string, ttl time.Duration) (string, error) {
	key, err := objectKey(contentURL, p.bucket)
	if err != nil {
		return "", err
	}

	presign := s3.NewPresignClient(p.client)
	request, err := presign.PresignGetObject(ctx,
		&s3.GetObjectInput{Bucket: aws.String(p.bucket), Key: aws.String(key)},
		s3.WithPresignExpires(ttl),
	)
	if err != nil {
		return "", fmt.Errorf("presign get object: %w", err)
	}
	return request.URL, nil
}

func objectKey(contentURL, bucket string) (string, error) {
	parsed, err := url.Parse(contentURL)
	if err != nil {
		return "", fmt.Errorf("parse content url: %w", err)
	}
	path := strings.TrimPrefix(parsed.Path, "/")
	prefix := strings.Trim(bucket, "/") + "/"
	if !strings.HasPrefix(path, prefix) {
		return "", fmt.Errorf("content url is outside bucket %q", bucket)
	}
	key := strings.TrimPrefix(path, prefix)
	if key == "" {
		return "", fmt.Errorf("empty object key")
	}
	return key, nil
}
