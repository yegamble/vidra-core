//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestPlaylistItemsHonourBlocksAndSensitiveHide runs the REAL ListPlaylistItems
// / CountPlaylistItems SQL (the handler tests use a fake repo that bypasses it):
// a moderator-blocked video is never listed or counted, the sensitive "hide"
// policy and a viewer's per-flag override are honoured exactly as
// vidra_sensitive_hidden() resolves them for every other browse query, and
// LIMIT/OFFSET page through the filtered set with the total matching it.
func TestPlaylistItemsHonourBlocksAndSensitiveHide(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	exec := func(sql string, args ...any) {
		t.Helper()
		if _, err := st.Pool.Exec(ctx, sql, args...); err != nil {
			t.Fatalf("%s: %v", sql, err)
		}
	}
	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	owner, viewerFlag, viewerPlain := mkUser("pfo"), mkUser("pfa"), mkUser("pfb")
	var channelID, playlistID uuid.UUID
	if err := st.Pool.QueryRow(ctx, `INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'PF') RETURNING id`,
		owner, "pf_"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx, `INSERT INTO playlists (owner_id, title, visibility) VALUES ($1, 'Mix', 'public') RETURNING id`, owner).Scan(&playlistID); err != nil {
		t.Fatalf("seed playlist: %v", err)
	}
	// Playlist order: plain, blocked, sensitive (unflagged), flagged (sex).
	type seed struct {
		title     string
		sensitive bool
		flags     []string
	}
	seeds := []seed{{"plain", false, nil}, {"blocked", false, nil}, {"sensitive", true, nil}, {"flagged", true, []string{"sex"}}}
	ids := map[string]uuid.UUID{}
	for i, s := range seeds {
		flags := s.flags
		if flags == nil {
			flags = []string{}
		}
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state, is_sensitive, sensitive_flags)
			 VALUES ($1, $2, 'public', 'published', $3, $4) RETURNING id`,
			channelID, s.title, s.sensitive, flags).Scan(&id); err != nil {
			t.Fatalf("seed video %s: %v", s.title, err)
		}
		ids[s.title] = id
		exec(`INSERT INTO playlist_items (playlist_id, video_id, position) VALUES ($1, $2, $3)`, playlistID, id, i+1)
	}
	exec(`INSERT INTO video_blocks (video_id, reason) VALUES ($1, 'tos')`, ids["blocked"])
	// viewerFlag overrides ONLY the "sex" flag to display; viewerPlain hides it.
	exec(`INSERT INTO user_content_prefs (user_id, sensitive_flag_policies) VALUES ($1, '{"sex":"display"}'), ($2, '{"sex":"hide"}')`, viewerFlag, viewerPlain)

	titles := func(rows []sqlcgen.ListPlaylistItemsRow) []string {
		out := make([]string, 0, len(rows))
		for _, r := range rows {
			out = append(out, r.Title)
		}
		return out
	}
	anon := pgtype.UUID{}
	as := func(id uuid.UUID) pgtype.UUID { return pgtype.UUID{Bytes: id, Valid: true} }
	for _, c := range []struct {
		name   string
		hide   bool
		viewer pgtype.UUID
		want   []string
	}{
		{"anonymous, hide off: blocked is gone, sensitive stays", false, anon, []string{"plain", "sensitive", "flagged"}},
		{"anonymous, hide on", true, anon, []string{"plain"}},
		{"hide on, per-flag override display shows flagged only", true, as(viewerFlag), []string{"plain", "flagged"}},
		{"hide off, per-flag override hide drops flagged only", false, as(viewerPlain), []string{"plain", "sensitive"}},
		{"the owner gets the same filtered list", true, as(owner), []string{"plain"}},
	} {
		rows, err := q.ListPlaylistItems(ctx, sqlcgen.ListPlaylistItemsParams{
			PlaylistID: playlistID, HideSensitive: c.hide, ViewerID: c.viewer, ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("%s: list: %v", c.name, err)
		}
		got := titles(rows)
		if len(got) != len(c.want) {
			t.Fatalf("%s: listed %v, want %v", c.name, got, c.want)
		}
		for i := range got {
			if got[i] != c.want[i] {
				t.Fatalf("%s: listed %v, want %v", c.name, got, c.want)
			}
		}
		total, err := q.CountPlaylistItems(ctx, sqlcgen.CountPlaylistItemsParams{PlaylistID: playlistID, HideSensitive: c.hide, ViewerID: c.viewer})
		if err != nil {
			t.Fatalf("%s: count: %v", c.name, err)
		}
		if total != int64(len(c.want)) {
			t.Errorf("%s: count = %d, want %d", c.name, total, len(c.want))
		}
	}

	// Paging walks the FILTERED set: no page is short because a hidden row sat
	// inside it, and the pages concatenate to the full filtered list.
	var paged []string
	for offset := int32(0); offset < 3; offset += 2 {
		rows, err := q.ListPlaylistItems(ctx, sqlcgen.ListPlaylistItemsParams{
			PlaylistID: playlistID, HideSensitive: false, ViewerID: anon, ResultLimit: 2, ResultOffset: offset,
		})
		if err != nil {
			t.Fatalf("page at %d: %v", offset, err)
		}
		if want := min(2, 3-int(offset)); len(rows) != want {
			t.Errorf("page at %d has %d rows, want %d", offset, len(rows), want)
		}
		paged = append(paged, titles(rows)...)
	}
	if len(paged) != 3 || paged[0] != "plain" || paged[1] != "sensitive" || paged[2] != "flagged" {
		t.Errorf("paged = %v, want [plain sensitive flagged]", paged)
	}

	// The playlist's own video_count drops the blocked video (viewer-independent
	// part; the sensitive policy is per viewer and lives in CountPlaylistItems).
	p, err := q.GetPlaylistByID(ctx, playlistID)
	if err != nil {
		t.Fatalf("GetPlaylistByID: %v", err)
	}
	if p.VideoCount != 3 {
		t.Errorf("video_count = %d, want 3 (blocked excluded)", p.VideoCount)
	}
	// Management still sees every item, blocked or not, so remove/move/reorder
	// by position keep working for the owner.
	all, err := q.ListPlaylistItemVideoIDs(ctx, playlistID)
	if err != nil || len(all) != 4 {
		t.Errorf("ListPlaylistItemVideoIDs = %v, %v; want all 4 items", all, err)
	}
}
