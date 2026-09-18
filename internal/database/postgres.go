// Package database wires the Postgres connection pool.
package database

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/pytsekas/experiment-go/internal/config"
)

// NewPool opens a pgx connection pool and verifies connectivity.
// The caller owns the pool and must Close it.
func NewPool(ctx context.Context, cfg config.DB) (*pgxpool.Pool, error) {
	poolCfg, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		return nil, fmt.Errorf("parse database dsn: %w", err)
	}

	poolCfg.MaxConns = int32(cfg.MaxConns) //nolint:gosec // bounded by config validation
	poolCfg.MinConns = int32(cfg.MinConns) //nolint:gosec // bounded by config validation
	poolCfg.MaxConnLifetime = cfg.MaxConnLifetime
	poolCfg.ConnConfig.ConnectTimeout = cfg.ConnectTimeout

	pool, err := pgxpool.NewWithConfig(ctx, poolCfg)
	if err != nil {
		return nil, fmt.Errorf("create connection pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, cfg.ConnectTimeout+time.Second)
	defer cancel()

	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()

		return nil, fmt.Errorf("ping database: %w", err)
	}

	return pool, nil
}
