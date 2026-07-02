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

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
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

	for _, table := range []string{"users", "sessions", "channels", "channel_follows", "videos"} {
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

// TestReorderPlaylistItemsPersists proves the ReorderPlaylistItems query rewrites
// item positions atomically against a real PostgreSQL: the unnest(...) WITH
// ORDINALITY UPDATE assigns each video its 1-based index, and the reordered
// items read back in the new order. Seeds the full FK chain and cleans up via
// the users→… ON DELETE CASCADE.
func TestReorderPlaylistItemsPersists(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var userID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"reorder-"+suffix, "reorder-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	// ON DELETE CASCADE from users tears down channels/videos/playlists/items.
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }()

	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Reorder') RETURNING id`,
		userID, "reorder_"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	vids := make([]uuid.UUID, 3)
	for i := range vids {
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, $2, 'public', 'published') RETURNING id`,
			channelID, "v",
		).Scan(&vids[i]); err != nil {
			t.Fatalf("seed video %d: %v", i, err)
		}
	}

	var playlistID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO playlists (owner_id, title) VALUES ($1, 'Mix') RETURNING id`, userID,
	).Scan(&playlistID); err != nil {
		t.Fatalf("seed playlist: %v", err)
	}
	for i, v := range vids {
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO playlist_items (playlist_id, video_id, position) VALUES ($1, $2, $3)`,
			playlistID, v, i+1,
		); err != nil {
			t.Fatalf("seed item %d: %v", i, err)
		}
	}

	// Reverse the order via the query under test.
	want := []uuid.UUID{vids[2], vids[1], vids[0]}
	if err := q.ReorderPlaylistItems(ctx, sqlcgen.ReorderPlaylistItemsParams{
		PlaylistID: playlistID,
		VideoIds:   want,
	}); err != nil {
		t.Fatalf("ReorderPlaylistItems: %v", err)
	}

	got, err := q.ListPlaylistItemVideoIDs(ctx, playlistID)
	if err != nil {
		t.Fatalf("ListPlaylistItemVideoIDs: %v", err)
	}
	if len(got) != 3 || got[0] != want[0] || got[1] != want[1] || got[2] != want[2] {
		t.Fatalf("order after reorder = %v, want %v", got, want)
	}
}
