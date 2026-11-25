// Package redis — адаптер Redis L2-кэша и его readiness-проба.
// goca:manual — кэш BC#3 (L1/L2 + presigned URL) генератор не создаёт.
package redis

import (
	"context"

	goredis "github.com/redis/go-redis/v9"
)

type Probe struct {
	client *goredis.Client
}

func New(addr, password string) *Probe {
	return &Probe{client: goredis.NewClient(&goredis.Options{
		Addr:     addr,
		Password: password,
	})}
}

func (p *Probe) Name() string { return "redis" }

func (p *Probe) Ping(ctx context.Context) error { return p.client.Ping(ctx).Err() }

func (p *Probe) Client() *goredis.Client { return p.client }

func (p *Probe) Close() error { return p.client.Close() }
