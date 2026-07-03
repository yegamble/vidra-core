// Package cache provides the Redis client used for sessions, rate limiting,
// idempotency keys, and hot status lookups. Redis is never the durable source
// of truth for data that must survive a restart.
package cache

import (
	"context"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"go.opentelemetry.io/otel"
	oteltrace "go.opentelemetry.io/otel/trace"
)

// Cache wraps a Redis client.
type Cache struct {
	Client *redis.Client
}

// Option customises the client at construction.
type Option func(*options)

type options struct {
	trace bool
}

// WithTracing enables OpenTelemetry spans around every Redis command. Install it
// only when OpenTelemetry is on (otherwise it is pure overhead): each command
// becomes a short client span ("redis.<CMD>"). Command arguments (which can carry
// tokens/session keys/idempotency values) are never recorded — the span name is
// the fixed command verb only — so no denylisted data reaches a span attribute.
func WithTracing() Option {
	return func(o *options) { o.trace = true }
}

// New parses a Redis URL, builds a client, and verifies connectivity with a
// ping bounded by ctx.
func New(ctx context.Context, redisURL string, opts ...Option) (*Cache, error) {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	opt, err := redis.ParseURL(redisURL)
	if err != nil {
		return nil, fmt.Errorf("cache: parse redis url: %w", err)
	}
	client := redis.NewClient(opt)
	if o.trace {
		client.AddHook(tracingHook{tracer: otel.Tracer("github.com/vidra/vidra-core/internal/cache")})
	}

	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := client.Ping(pingCtx).Err(); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("cache: ping: %w", err)
	}
	return &Cache{Client: client}, nil
}

// Ping checks Redis connectivity, bounded by ctx. Used by readiness probes.
func (c *Cache) Ping(ctx context.Context) error {
	pingCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return c.Client.Ping(pingCtx).Err()
}

// Close closes the Redis client.
func (c *Cache) Close() error {
	if c.Client != nil {
		return c.Client.Close()
	}
	return nil
}

// tracingHook wraps every Redis command in a short client span. It records only
// the command verb (bounded cardinality); arguments are never recorded.
type tracingHook struct {
	tracer oteltrace.Tracer
}

func (h tracingHook) DialHook(next redis.DialHook) redis.DialHook { return next }

func (h tracingHook) ProcessHook(next redis.ProcessHook) redis.ProcessHook {
	return func(ctx context.Context, cmd redis.Cmder) error {
		ctx, span := h.tracer.Start(ctx, "redis."+cmd.Name(), oteltrace.WithSpanKind(oteltrace.SpanKindClient))
		defer span.End()
		err := next(ctx, cmd)
		if err != nil && err != redis.Nil {
			span.RecordError(err)
		}
		return err
	}
}

func (h tracingHook) ProcessPipelineHook(next redis.ProcessPipelineHook) redis.ProcessPipelineHook {
	return func(ctx context.Context, cmds []redis.Cmder) error {
		ctx, span := h.tracer.Start(ctx, "redis.pipeline", oteltrace.WithSpanKind(oteltrace.SpanKindClient))
		defer span.End()
		return next(ctx, cmds)
	}
}
