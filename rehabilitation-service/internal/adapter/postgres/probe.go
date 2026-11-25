// Package postgres — адаптер PostgreSQL (pgx) и его readiness-проба.
// goca:manual — enterprise-шаблон даёт GORM; BC#3 использует pgx + golang-migrate.
package postgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
)

type Probe struct {
	pool *pgxpool.Pool
}

func New(ctx context.Context, dsn string) (*Probe, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("postgres: parse dsn: %w", err)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("postgres: create pool: %w", err)
	}
	return &Probe{pool: pool}, nil
}

func (p *Probe) Name() string { return "postgres" }

func (p *Probe) Ping(ctx context.Context) error { return p.pool.Ping(ctx) }

func (p *Probe) Pool() *pgxpool.Pool { return p.pool }

func (p *Probe) Close() { p.pool.Close() }
