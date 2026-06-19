// Package store provides the PostgreSQL connection pool used by the
// vidra-core service. PostgreSQL is the durable system of record; all schema
// changes flow through numbered migrations in /migrations.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Store wraps a pgx connection pool.
type Store struct {
	Pool *pgxpool.Pool
}

// New opens a pooled connection to PostgreSQL using the given DSN and verifies
// connectivity with a ping bounded by ctx.
func New(ctx context.Context, databaseURL string) (*Store, error) {
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, fmt.Errorf("store: create pool: %w", err)
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("store: ping: %w", err)
	}
	return &Store{Pool: pool}, nil
}

// Queries returns a typed sqlc query set bound to the pool. pgxpool.Pool
// satisfies sqlcgen.DBTX, so callers get connection-per-query pooling for free.
func (s *Store) Queries() *sqlcgen.Queries {
	return sqlcgen.New(s.Pool)
}

// Ping checks database connectivity, bounded by ctx. Used by readiness probes.
func (s *Store) Ping(ctx context.Context) error {
	return s.Pool.Ping(ctx)
}

// Close releases all pooled connections.
func (s *Store) Close() {
	if s.Pool != nil {
		s.Pool.Close()
	}
}
