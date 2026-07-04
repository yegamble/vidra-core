//go:build integration

// Benchmarks for the read-hot query paths against REAL PostgreSQL. They seed a
// realistic corpus, then measure the query the discovery surfaces run on every
// page load. Self-skip when DATABASE_URL is absent (same contract as the other
// integration tests). Run with:
//
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -run=^$ -bench=. ./internal/store/...
package store

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// benchDSN is the *testing.B counterpart of dsn: it self-skips the benchmark
// when DATABASE_URL is unset, so `go test -bench` is safe on a bare host.
func benchDSN(b *testing.B) string {
	b.Helper()
	v := os.Getenv("DATABASE_URL")
	if v == "" {
		b.Skip("DATABASE_URL not set; skipping integration benchmark")
	}
	return v
}

// benchSeedVideos seeds n published public videos under one channel with varied
// titles (so trigram search has something to rank) and returns the owner id +
// a cleanup that cascades the whole chain via the users ON DELETE CASCADE.
func benchSeedVideos(b *testing.B, st *Store, n int) func() {
	b.Helper()
	ctx := context.Background()
	suffix := uuid.NewString()[:8]
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"bench-"+suffix, "bench-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		b.Fatalf("seed user: %v", err)
	}
	cleanup := func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Bench') RETURNING id`,
		userID, "bench_"+suffix,
	).Scan(&channelID); err != nil {
		cleanup()
		b.Fatalf("seed channel: %v", err)
	}
	// A single multi-row insert keeps setup fast even for a large corpus.
	titles := []string{"morning coffee routine", "gopher deep dive", "sunset timelapse", "kitchen tips", "ambient synth mix"}
	for i := 0; i < n; i++ {
		title := fmt.Sprintf("%s %d", titles[i%len(titles)], i)
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, 'public', 'published')`,
			channelID, title,
		); err != nil {
			cleanup()
			b.Fatalf("seed video %d: %v", i, err)
		}
	}
	return cleanup
}

// BenchmarkListPublicVideosSorted measures the public feed query (recent sort) —
// the query the home/browse feed runs on every page load. Joins view counts,
// thumbnail presence, and channel identity across the seeded corpus.
func BenchmarkListPublicVideosSorted(b *testing.B) {
	ctx := context.Background()
	st, err := New(ctx, benchDSN(b))
	if err != nil {
		b.Fatalf("connect: %v", err)
	}
	defer st.Close()
	b.Cleanup(benchSeedVideos(b, st, 500))
	q := st.Queries()
	arg := sqlcgen.ListPublicVideosSortedParams{
		ViewerID:    pgtype.UUID{}, // anonymous
		Sort:        "recent",
		ResultLimit: 24,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := q.ListPublicVideosSorted(ctx, arg); err != nil {
			b.Fatalf("ListPublicVideosSorted: %v", err)
		}
	}
}

// BenchmarkSearchPublicVideos measures the pg_trgm trigram title search — the
// query the search box runs on every keystroke-completed query.
func BenchmarkSearchPublicVideos(b *testing.B) {
	ctx := context.Background()
	st, err := New(ctx, benchDSN(b))
	if err != nil {
		b.Fatalf("connect: %v", err)
	}
	defer st.Close()
	b.Cleanup(benchSeedVideos(b, st, 500))
	q := st.Queries()
	arg := sqlcgen.SearchPublicVideosParams{
		Query:       "gopher",
		ViewerID:    pgtype.UUID{}, // anonymous
		ResultLimit: 24,
	}
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := q.SearchPublicVideos(ctx, arg); err != nil {
			b.Fatalf("SearchPublicVideos: %v", err)
		}
	}
}
