package out

import (
	"context"
	"crypto/rsa"
	"time"
)

// Cache is the L2 (Redis) cache used before PostgreSQL in read paths.
type Cache interface {
	Get(ctx context.Context, key string) (string, bool, error)
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	Del(ctx context.Context, keys ...string) error
	Ping(ctx context.Context) error
}

// IdempotencyState is the outcome of an idempotency-key acquisition.
type IdempotencyState int

const (
	IdempotencyNew IdempotencyState = iota
	IdempotencyInProgress
	IdempotencyCompleted
)

// StoredResponse is a previously completed response replayed for a duplicate key.
type StoredResponse struct {
	Status int
	Body   []byte
}

// IdempotencyStore implements T1 idempotency for state-mutating HTTP endpoints.
type IdempotencyStore interface {
	Begin(ctx context.Context, key string, ttl time.Duration) (IdempotencyState, *StoredResponse, error)
	Complete(ctx context.Context, key string, resp StoredResponse, ttl time.Duration) error
	Release(ctx context.Context, key string) error
	Ping(ctx context.Context) error
}

// ContentCatalog is the BC#3 read-only program catalog (sync HTTP, never inside a TX).
type ContentCatalog interface {
	ResolveProgram(ctx context.Context, difficulty, aphasiaType string) (ProgramInfo, error)
	GetProgram(ctx context.Context, programID string, version int) (ProgramInfo, error)
}

// EventPublisher publishes outbox rows to Kafka.
type EventPublisher interface {
	Publish(ctx context.Context, topic, key string, payload []byte) error
	Close() error
}

// ObjectStorage issues presigned URLs for Yandex Object Storage (S3-compatible).
type ObjectStorage interface {
	PresignPut(ctx context.Context, key, contentType string, ttl time.Duration) (string, error)
	PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error)
	Configured() bool
}

// JWKSProvider resolves RS256 public keys by kid from the IAM JWKS endpoint.
type JWKSProvider interface {
	PublicKey(ctx context.Context, kid string) (*rsa.PublicKey, error)
}

// RevocationStore is the shared redis-blacklist projection written by BC#1.
type RevocationStore interface {
	IsJTIBlacklisted(ctx context.Context, jti string) (bool, error)
	AccountRev(ctx context.Context, accountID string) (int64, bool, error)
	Ping(ctx context.Context) error
}
