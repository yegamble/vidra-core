//go:build integration

// Proves the pgx query-span instrumentation (WithTracing) actually emits a
// client span per query against a live database. Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/store/ -run TestQuerySpansEmitted
package store

import (
	"context"
	"os"
	"testing"
	"time"

	"go.opentelemetry.io/otel"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestQuerySpansEmitted(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	rec := tracetest.NewSpanRecorder()
	tp := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(rec))
	prev := otel.GetTracerProvider()
	otel.SetTracerProvider(tp)
	defer func() {
		otel.SetTracerProvider(prev)
		_ = tp.Shutdown(context.Background())
	}()

	st, err := New(ctx, dsn, WithTracing())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	if _, err := st.Pool.Exec(ctx, "SELECT 1"); err != nil {
		t.Fatalf("exec: %v", err)
	}

	var found bool
	for _, s := range rec.Ended() {
		if s.Name() == "postgres.query" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a postgres.query span; got %d spans", len(rec.Ended()))
	}
}
