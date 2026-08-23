//go:build integration

// Integration coverage for the parts of RealProber that talk to a real
// PostgreSQL. Requires DATABASE_URL; self-skips without it. Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration ./internal/doctor/...
package doctor

import (
	"context"
	"os"
	"testing"
	"time"
)

// The fakes cannot check this one: `SHOW max_connections` returns TEXT, not an
// integer, and a Scan into an int fails at runtime against a real server while
// compiling perfectly. That is exactly the class of bug a fake prober hides.
func TestRealProberReadsMaxConnections(t *testing.T) {
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	got, err := RealProber{}.ServerMaxConnections(ctx, dsn)
	if err != nil {
		t.Fatalf("ServerMaxConnections: %v", err)
	}
	// No exact assertion: the value is the server's, and a deployment is free to
	// set it to anything. What is being proven is that a number comes back at
	// all, which is what the pool arithmetic needs.
	if got < 1 {
		t.Errorf("max_connections = %d, want a positive number", got)
	}
	t.Logf("max_connections = %d", got)
}
