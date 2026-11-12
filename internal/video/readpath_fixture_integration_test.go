//go:build integration

package video_test

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store"
)

// readPathShape sizes the fixture. The benchmark uses the shape measured on a
// real imported instance (about 5,500 owners with one channel each, 15,000
// videos, 78% of them sensitive, none carrying a flag); the equality test uses
// a small one WITH flags so the per-flag policy function is exercised.
type readPathShape struct {
	Owners, Videos int
	Flags          bool
}

func openStore(tb testing.TB) *store.Store {
	tb.Helper()
	dsn := os.Getenv("DATABASE_URL")
	if dsn == "" {
		tb.Skip("DATABASE_URL not set; skipping integration test")
	}
	st, err := store.New(context.Background(), dsn)
	if err != nil {
		tb.Fatalf("connect: %v", err)
	}
	tb.Cleanup(st.Close)
	return st
}

// fixtureVideos numbers the fixture's own videos; every dependent insert keys
// off rn so the shape is the same on every run.
const fixtureVideos = `(SELECT v.id, row_number() OVER (ORDER BY v.created_at, v.id) AS rn
     FROM videos v JOIN channels c ON c.id = v.channel_id
     WHERE c.handle LIKE 'rpc' || $1 || '\_%') v`

// seedReadPath inserts the fixture with set-based statements and removes it
// again through the users ON DELETE CASCADE. It returns the fixture's suffix.
// Keys repeat on purpose (created_at in threes, 200 distinct view counts, 400
// distinct titles) so every sort has to fall through to its tiebreak.
func seedReadPath(tb testing.TB, st *store.Store, shape readPathShape) string {
	tb.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	sfx := strings.ReplaceAll(uuid.NewString(), "-", "")[:8]
	tb.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE username LIKE 'rpu' || $1 || '\_%'`, sfx)
	})
	steps := []struct {
		name string
		sql  string
		args []any
	}{
		{"users", `INSERT INTO users (username, email, password_hash, unlisted)
			SELECT 'rpu' || $1 || '_' || i, 'rpu' || $1 || '_' || i || '@example.test', 'x', i % 2500 = 7
			FROM generate_series(1, $2::int) AS i`, []any{sfx, shape.Owners}},
		{"channels", `INSERT INTO channels (owner_id, handle, display_name)
			SELECT u.id, 'rpc' || substr(u.username, 4), 'Channel ' || substr(u.username, 13)
			FROM users u WHERE u.username LIKE 'rpu' || $1 || '\_%'`, []any{sfx}},
		{"videos", `INSERT INTO videos (channel_id, title, description, privacy, state, is_sensitive,
			                    sensitive_flags, category, language, created_at, originally_published_at)
			SELECT ch.id,
			       (ARRAY['Morning coffee routine','gopher deep dive','Sunset timelapse','kitchen tips','ambient synth mix'])[1 + i % 5] || ' ' || (i % 400),
			       repeat('A paragraph of description text for the card. ', 12),
			       CASE WHEN i % 13 = 0 THEN 'private' WHEN i % 211 = 0 THEN 'internal' ELSE 'public' END,
			       CASE WHEN i % 97 = 0 THEN 'draft' ELSE 'published' END,
			       i % 9 < 7,
			       CASE WHEN $3::bool AND i % 9 < 7 AND i % 4 = 0 THEN ARRAY['violent'] ELSE '{}'::text[] END,
			       CASE WHEN i % 4 = 0 THEN (ARRAY['music','gaming','education'])[1 + i % 3] END,
			       CASE WHEN i % 3 = 0 THEN (ARRAY['en','fr'])[1 + i % 2] END,
			       date_trunc('second', now()) - make_interval(mins => (i / 3) * 97),
			       CASE WHEN i % 10 = 0 THEN date_trunc('second', now()) - make_interval(days => i % 900) END
			FROM generate_series(1, $2::int) AS i
			JOIN (SELECT id, row_number() OVER (ORDER BY handle) AS rn, count(*) OVER () AS total
			      FROM channels WHERE handle LIKE 'rpc' || $1 || '\_%') ch
			  ON ch.rn = 1 + (i * 7919) % ch.total`, []any{sfx, shape.Videos, shape.Flags}},
		{"view counts", `INSERT INTO video_view_counts (video_id, views)
			SELECT v.id, ((v.rn * 7919) % 200) * 25 FROM ` + fixtureVideos + ` WHERE v.rn % 5 <> 0`, []any{sfx}},
		{"metadata", `INSERT INTO video_metadata (video_id, duration_seconds)
			SELECT v.id, 30 + (v.rn * 31) % 3600 FROM ` + fixtureVideos, []any{sfx}},
		{"files", `INSERT INTO video_files (video_id, kind, storage_key, size_bytes)
			SELECT v.id, k.kind, 'rp/' || $1 || '/' || v.id || '/' || k.kind, 1000
			FROM ` + fixtureVideos + `
			CROSS JOIN (VALUES ('thumbnail', 20), ('original', 0), ('rendition', 2)) AS k(kind, skip)
			WHERE k.skip = 0 OR v.rn % k.skip <> 0`, []any{sfx}},
		{"blocks", `INSERT INTO video_blocks (video_id)
			SELECT v.id FROM ` + fixtureVideos + ` WHERE v.rn % 37 = 0`, []any{sfx}},
		{"tags", `INSERT INTO video_tags (video_id, tag)
			SELECT v.id, 'rptag' || (v.rn % 40) FROM ` + fixtureVideos + `
			UNION ALL
			SELECT v.id, 'rptopic' || (v.rn % 7) FROM ` + fixtureVideos + ` WHERE v.rn % 2 = 0`, []any{sfx}},
		// Statistics and a visibility map for the seeded rows, as a database
		// that has been running for a while has them; without these the
		// benchmark measures plans it would never choose.
		{"vacuum", `VACUUM (ANALYZE) users, channels, videos, video_view_counts, video_metadata, video_files, video_blocks, video_tags`, nil},
	}
	for _, s := range steps {
		if _, err := st.Pool.Exec(ctx, s.sql, s.args...); err != nil {
			tb.Fatalf("seed %s: %v", s.name, err)
		}
	}
	return sfx
}
