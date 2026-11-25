// Package s3 — адаптер Yandex Object Storage (S3 API) и его readiness-проба.
// goca:manual — внешнее хранилище PDF BC#3 генератор не создаёт.
package s3

import (
	"context"
	"fmt"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	awsconfig "github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
)

type Probe struct {
	client *s3.Client
	bucket string
}

type Options struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

func New(ctx context.Context, opts Options) (*Probe, error) {
	cfg, err := awsconfig.LoadDefaultConfig(ctx,
		awsconfig.WithRegion(opts.Region),
		awsconfig.WithCredentialsProvider(credentials.NewStaticCredentialsProvider(opts.AccessKey, opts.SecretKey, "")),
	)
	if err != nil {
		return nil, fmt.Errorf("s3: load aws config: %w", err)
	}

	client := s3.NewFromConfig(cfg, func(o *s3.Options) {
		o.BaseEndpoint = aws.String(strings.TrimRight(opts.Endpoint, "/"))
		o.UsePathStyle = true
	})

	return &Probe{client: client, bucket: opts.Bucket}, nil
}

func (p *Probe) Name() string { return "s3" }

func (p *Probe) Ping(ctx context.Context) error {
	_, err := p.client.HeadBucket(ctx, &s3.HeadBucketInput{Bucket: aws.String(p.bucket)})
	return err
}

func (p *Probe) Client() *s3.Client { return p.client }
