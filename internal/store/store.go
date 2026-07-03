// Package store provides the PostgreSQL connection pool used by the
// vidra-core service. PostgreSQL is the durable system of record; all schema
// changes flow through numbered migrations in /migrations.
package store

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Store wraps a pgx connection pool.
type Store struct {
	Pool *pgxpool.Pool
}

// Option customises the pool at construction.
type Option func(*options)

type options struct {
	tracer pgx.QueryTracer
}

// WithTracing enables OpenTelemetry spans around every pgx query. Install it only
// when OpenTelemetry is on (otherwise it is pure overhead): each query becomes a
// short client span ("postgres.query"). Query ARGUMENTS are never recorded — they
// can carry secrets/PII — and the span name is a fixed low-cardinality string, so
// no denylisted data reaches a span attribute.
func WithTracing() Option {
	return func(o *options) {
		o.tracer = &queryTracer{tracer: otel.Tracer("github.com/vidra/vidra-core/internal/store")}
	}
}

// New opens a pooled connection to PostgreSQL using the given DSN and verifies
// connectivity with a ping bounded by ctx.
func New(ctx context.Context, databaseURL string, opts ...Option) (*Store, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	cfg.MaxConns = 10
	cfg.MinConns = 1
	cfg.MaxConnLifetime = time.Hour
	cfg.MaxConnIdleTime = 30 * time.Minute
	if o.tracer != nil {
		cfg.ConnConfig.Tracer = o.tracer
	}

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

// queryTracer implements pgx.QueryTracer, opening a client span per query and
// closing it when the query completes. It records nothing but the fixed span
// name and (on failure) the error — never the SQL arguments.
type queryTracer struct {
	tracer oteltrace.Tracer
}

// pgxSpanKey stashes the in-flight span on the query context.
type pgxSpanKey struct{}

func (t *queryTracer) TraceQueryStart(ctx context.Context, _ *pgx.Conn, _ pgx.TraceQueryStartData) context.Context {
	ctx, span := t.tracer.Start(ctx, "postgres.query", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
	return context.WithValue(ctx, pgxSpanKey{}, span)
}

func (t *queryTracer) TraceQueryEnd(ctx context.Context, _ *pgx.Conn, data pgx.TraceQueryEndData) {
	span, ok := ctx.Value(pgxSpanKey{}).(oteltrace.Span)
	if !ok {
		return
	}
	if data.Err != nil {
		span.RecordError(data.Err)
	}
	span.End()
}
