// Package s3 issues presigned PUT/GET URLs for Yandex Object Storage (S3-compatible, no CDN).
package s3

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	corerr "github.com/Reddetk/SayMoDev/progress-service/internal/core/coreErrors"
	"github.com/Reddetk/SayMoDev/progress-service/internal/port/out"
)

// Options configures the Yandex Object Storage client.
type Options struct {
	Endpoint       string
	Region         string
	Bucket         string
	AccessKey      string
	SecretKey      string
	ForcePathStyle bool
}

// Presigner implements out.ObjectStorage.
type Presigner struct {
	client     *awss3.PresignClient
	bucket     string
	configured bool
}

// New builds the presigner; without static keys it stays unconfigured and returns ErrS3NotConfigured.
func New(ctx context.Context, opts Options) (*Presigner, error) {
	p := &Presigner{bucket: opts.Bucket}
	if opts.AccessKey == "" || opts.SecretKey == "" || opts.Bucket == "" {
		return p, nil
	}

	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(opts.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(opts.AccessKey, opts.SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("s3: load config: %w", err)
	}
	client := awss3.NewFromConfig(cfg, func(o *awss3.Options) {
		o.UsePathStyle = opts.ForcePathStyle
		if opts.Endpoint != "" {
			o.BaseEndpoint = aws.String(opts.Endpoint)
		}
	})
	p.client = awss3.NewPresignClient(client)
	p.configured = true
	return p, nil
}

// Configured reports whether static credentials and a bucket are present.
func (p *Presigner) Configured() bool { return p.configured }

// PresignPut returns a presigned PUT URL (TTL 15 minutes by default).
func (p *Presigner) PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error) {
	if !p.configured {
		return "", corerr.ErrS3NotConfigured
	}
	req, err := p.client.PresignPutObject(ctx, &awss3.PutObjectInput{
		Bucket:      aws.String(p.bucket),
		Key:         aws.String(key),
		ContentType: aws.String(contentType),
	}, func(o *awss3.PresignOptions) { o.Expires = ttl })
	if err != nil {
		return "", fmt.Errorf("s3: presign put: %w", err)
	}
	return req.URL, nil
}

// PresignGet returns a presigned GET URL for admin media viewing (TTL 1 hour).
func (p *Presigner) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if !p.configured {
		return "", corerr.ErrS3NotConfigured
	}
	req, err := p.client.PresignGetObject(ctx, &awss3.GetObjectInput{
		Bucket: aws.String(p.bucket),
		Key:    aws.String(key),
	}, func(o *awss3.PresignOptions) { o.Expires = ttl })
	if err != nil {
		return "", fmt.Errorf("s3: presign get: %w", err)
	}
	return req.URL, nil
}

var _ out.ObjectStorage = (*Presigner)(nil)
