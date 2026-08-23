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

// The pool defaults. They are what the pool was hardcoded to before the sizing
// became configurable, so a caller that passes no sizing Option — `vidra
// doctor`'s prober, a one-shot command, a test — opens exactly the pool it
// always did.
//
// internal/config restates these as DefaultDBMaxConns and friends, because that
// package is the configuration surface and must not depend on the database
// driver; config's TestPoolDefaultsMatchStore asserts the two agree.
const (
	DefaultMaxConns        = 10
	DefaultMinConns        = 1
	DefaultConnMaxLifetime = time.Hour
	DefaultConnMaxIdleTime = 30 * time.Minute
)

// Option customises the pool at construction.
type Option func(*options)

type options struct {
	tracer          pgx.QueryTracer
	maxConns        int32
	minConns        int32
	maxConnLifetime time.Duration
	maxConnIdleTime time.Duration
}

// WithMaxConns caps the pool. It is a PER-PROCESS budget against a SERVER-WIDE
// max_connections, so the number that matters to PostgreSQL is this one times
// the number of api and worker processes pointed at it.
//
// Two is the floor the api enforces in config validation (the leader elector
// pins one connection), but this package does not re-litigate it: a
// non-positive value is ignored and leaves the default in place, so a caller
// that simply did not set the field cannot accidentally open a pool of zero.
func WithMaxConns(n int) Option {
	return func(o *options) {
		if n > 0 {
			o.maxConns = int32(n)
		}
	}
}

// WithMinConns sets the number of connections the pool keeps warm. Zero is
// meaningful here (open nothing until asked), so unlike the other three this
// takes any non-negative value.
func WithMinConns(n int) Option {
	return func(o *options) {
		if n >= 0 {
			o.minConns = int32(n)
		}
	}
}

// WithConnMaxLifetime bounds how long any one connection is reused before the
// pool retires it. It is what lets a connection-pooling proxy, a failover or a
// rotated credential take effect without a restart.
func WithConnMaxLifetime(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.maxConnLifetime = d
		}
	}
}

// WithConnMaxIdleTime retires connections that have gone unused for this long,
// returning the server-side slots an idle replica is holding.
func WithConnMaxIdleTime(d time.Duration) Option {
	return func(o *options) {
		if d > 0 {
			o.maxConnIdleTime = d
		}
	}
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
	o := options{
		maxConns:        DefaultMaxConns,
		minConns:        DefaultMinConns,
		maxConnLifetime: DefaultConnMaxLifetime,
		maxConnIdleTime: DefaultConnMaxIdleTime,
	}
	for _, opt := range opts {
		opt(&o)
	}
	cfg, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("store: parse database url: %w", err)
	}
	cfg.MaxConns = o.maxConns
	cfg.MinConns = o.minConns
	cfg.MaxConnLifetime = o.maxConnLifetime
	cfg.MaxConnIdleTime = o.maxConnIdleTime
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
