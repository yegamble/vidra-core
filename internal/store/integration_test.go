//go:build integration

// Integration tests require a live PostgreSQL and Redis reachable via the
// DATABASE_URL and REDIS_URL environment variables, with migrations already
// applied. Run with:
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	REDIS_URL=redis://localhost:6379/0 \
//	go test -tags=integration ./internal/store/...
package store

import (
	"context"
	"os"
	"testing"
	"time"
)

func dsn(t *testing.T) string {
	t.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		t.Skip("DATABASE_URL not set; skipping integration test")
	}
	return v
}

// TestFreshDatabaseHasFoundationTables proves migrations applied against the
// target database created the users and sessions tables.
func TestFreshDatabaseHasFoundationTables(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	for _, table := range []string{"users", "sessions"} {
		var exists bool
		err := st.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM information_schema.tables WHERE table_name = $1)`,
			table,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query table %q: %v", table, err)
		}
		if !exists {
			t.Errorf("expected table %q to exist after migration", table)
		}
	}
}

// TestRequiredExtensionsInstalled proves the 0001 extensions migration applied.
func TestRequiredExtensionsInstalled(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()

	for _, ext := range []string{"pg_trgm", "uuid-ossp"} {
		var exists bool
		err := st.Pool.QueryRow(ctx,
			`SELECT EXISTS (SELECT 1 FROM pg_extension WHERE extname = $1)`,
			ext,
		).Scan(&exists)
		if err != nil {
			t.Fatalf("query extension %q: %v", ext, err)
		}
		if !exists {
			t.Errorf("expected extension %q to be installed", ext)
		}
	}
}
