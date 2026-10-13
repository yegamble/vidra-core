//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// TestChannelPlaylistsVisibilityAndSetNull proves, on real PostgreSQL, the
// three promises of migration 0164: the channel list shows other viewers only
// PUBLIC playlists (the owner sees their own unlisted/private ones too), the
// page and its total agree, and deleting the channel nulls the link instead of
// deleting the owner's playlist.
func TestChannelPlaylistsVisibilityAndSetNull(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	mkUser := func(name string) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test").Scan(&id); err != nil {
			t.Fatalf("seed user: %v", err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}
	owner, other := mkUser("plch-owner"), mkUser("plch-other")
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'C') RETURNING id`,
		owner, "plch_"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	var ids = map[string]uuid.UUID{}
	for _, vis := range []string{"public", "unlisted", "private"} {
		p, err := q.CreatePlaylist(ctx, sqlcgen.CreatePlaylistParams{
			OwnerID: owner, Title: vis, Visibility: vis, ChannelID: pgconv.UUIDPtr(&channelID),
		})
		if err != nil {
			t.Fatalf("create %s: %v", vis, err)
		}
		ids[vis] = p.ID
	}
	// A playlist with no channel and one on ANOTHER channel must never appear.
	if _, err := q.CreatePlaylist(ctx, sqlcgen.CreatePlaylistParams{OwnerID: owner, Title: "loose", Visibility: "public"}); err != nil {
		t.Fatal(err)
	}

	list := func(viewer *uuid.UUID, limit, offset int32) ([]sqlcgen.ListPlaylistsByChannelRow, int64) {
		rows, err := q.ListPlaylistsByChannel(ctx, sqlcgen.ListPlaylistsByChannelParams{
			ChannelID: channelID, ViewerID: pgconv.UUIDPtr(viewer), ResultLimit: limit, ResultOffset: offset,
		})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		n, err := q.CountPlaylistsByChannel(ctx, sqlcgen.CountPlaylistsByChannelParams{ChannelID: channelID, ViewerID: pgconv.UUIDPtr(viewer)})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		return rows, n
	}

	for name, viewer := range map[string]*uuid.UUID{"anonymous": nil, "other user": &other} {
		rows, n := list(viewer, 20, 0)
		if len(rows) != 1 || rows[0].ID != ids["public"] || n != 1 {
			t.Errorf("%s sees %d rows (total %d), want only the public playlist", name, len(rows), n)
		}
	}
	rows, n := list(&owner, 20, 0)
	if len(rows) != 3 || n != 3 || rows[0].ID != ids["private"] {
		t.Errorf("owner sees %d rows (total %d) first=%v, want 3 newest-first", len(rows), n, rows[0].Title)
	}
	if page, n := list(&owner, 1, 2); len(page) != 1 || page[0].ID != ids["public"] || n != 3 {
		t.Errorf("last page = %d rows total %d, want the oldest (public) of 3", len(page), n)
	}

	// A viewer who blocked or muted the owner sees none of their playlists.
	blocker, muter := mkUser("plch-blocker"), mkUser("plch-muter")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, blocker, owner); err != nil {
		t.Fatalf("block: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, muter, owner); err != nil {
		t.Fatalf("mute: %v", err)
	}
	for name, v := range map[string]*uuid.UUID{"blocking": &blocker, "muting": &muter} {
		if rows, n := list(v, 20, 0); len(rows) != 0 || n != 0 {
			t.Errorf("%s viewer sees %d rows (total %d), want none", name, len(rows), n)
		}
	}

	// Unlink (set_channel=true, NULL) then relink.
	cleared, err := q.UpdatePlaylist(ctx, sqlcgen.UpdatePlaylistParams{ID: ids["public"], SetChannel: true})
	if err != nil || cleared.ChannelID.Valid {
		t.Fatalf("unlink = %+v, %v", cleared, err)
	}
	if _, n := list(nil, 20, 0); n != 0 {
		t.Errorf("unlinked playlist still listed (total %d)", n)
	}
	kept, err := q.UpdatePlaylist(ctx, sqlcgen.UpdatePlaylistParams{ID: ids["unlisted"], Title: pgconv.TrimPtr(ptrStr("renamed"))})
	if err != nil || !kept.ChannelID.Valid {
		t.Fatalf("a title-only update must leave the link: %+v, %v", kept, err)
	}

	// Deleting the channel nulls the link; the owner's playlist survives.
	if _, err := st.Pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	var channelAfter *uuid.UUID
	if err := st.Pool.QueryRow(ctx, `SELECT channel_id FROM playlists WHERE id = $1`, ids["unlisted"]).Scan(&channelAfter); err != nil {
		t.Fatalf("playlist gone after channel delete: %v", err)
	}
	if channelAfter != nil {
		t.Errorf("channel_id = %v after the channel was deleted, want NULL", channelAfter)
	}
}

func ptrStr(s string) *string { return &s }
