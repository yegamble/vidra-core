//go:build integration

// Integration test: CountForeignLayoutMediaRefs against a REAL PostgreSQL with
// migrations applied. The whole media-GC bucket-adoption gate rests on this one
// query being able to tell "media this install laid out" from "media somebody
// else laid out", and that is a SQL rule (two LIKE shape tests against the row's
// own video_id), so it is proven here rather than against a fake. Run via:
//
//	docker compose --profile core up -d postgres redis migrate
//	DATABASE_URL=postgres://vidra:vidra@localhost:5432/vidra?sslmode=disable \
//	go test -tags=integration -race ./internal/store/...
package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestCountForeignLayoutMediaRefsCountsOnlyOtherSystemsKeys. The counts are
// compared as DELTAS, not absolutes: the query is instance-wide by design, so
// rows another suite leaves behind must not be able to decide this one.
func TestCountForeignLayoutMediaRefsCountsOnlyOtherSystemsKeys(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	before, err := q.CountForeignLayoutMediaRefs(ctx)
	if err != nil {
		t.Fatalf("baseline count: %v", err)
	}

	_, channelID, cleanup := seedUserChannel(t, st)
	defer cleanup()
	newVideo := func(title string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title) VALUES ($1, $2) RETURNING id`,
			channelID, title,
		).Scan(&id); err != nil {
			t.Fatalf("seed video: %v", err)
		}
		return id
	}
	addFile := func(video uuid.UUID, key string) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO video_files (video_id, kind, storage_key, size_bytes) VALUES ($1, 'original', $2, 1)`,
			video, key,
		); err != nil {
			t.Fatalf("seed video_file %q: %v", key, err)
		}
	}
	addPlaylist := func(video uuid.UUID, master string) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx,
			`INSERT INTO streaming_playlists (video_id, master_key) VALUES ($1, $2)`,
			video, master,
		); err != nil {
			t.Fatalf("seed streaming_playlist %q: %v", master, err)
		}
	}
	count := func(what string) int64 {
		t.Helper()
		n, err := q.CountForeignLayoutMediaRefs(ctx)
		if err != nil {
			t.Fatalf("count after %s: %v", what, err)
		}
		return n - before
	}

	// Vidra's own layout, including a replacement generation and a
	// dead-lettered transcode's empty master: none of it is foreign.
	native := newVideo("native")
	addFile(native, "web-videos/"+native.String()+".mp4")
	addFile(native, "web-videos/"+native.String()+".r1.mp4")
	addPlaylist(native, "streaming-playlists/"+native.String()+"/r1/master.m3u8")
	deadLettered := newVideo("dead-lettered")
	addPlaylist(deadLettered, "")
	if got := count("native rows"); got != 0 {
		t.Fatalf("foreign refs after seeding only native media = +%d, want +0", got)
	}

	// A reference-mode import: the source instance's own keys, recorded
	// verbatim against a bucket that instance may still be serving.
	ptUUID := uuid.New()
	imported := newVideo("imported")
	addFile(imported, "web-videos/"+ptUUID.String()+"-720.mp4")
	addPlaylist(imported, "streaming-playlists/hls/"+ptUUID.String()+"/master.m3u8")
	if got := count("imported rows"); got != 2 {
		t.Fatalf("foreign refs after a reference-mode import = +%d, want +2", got)
	}
}
