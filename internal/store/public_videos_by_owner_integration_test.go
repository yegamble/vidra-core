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

// TestListPublicVideosByOwner proves the account-wide public list against real
// PostgreSQL: it spans every channel the owner has, and it carries the public
// channel list's whole visibility filter — no private, unlisted, draft,
// quarantined or moderator-blocked video, the per-viewer mute clause, and the
// sensitive hide — with the count agreeing with the page.
func TestListPublicVideosByOwner(t *testing.T) {
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
	mkChannel := func(owner uuid.UUID, handle string) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'C') RETURNING id`,
			owner, handle+"_"+suffix).Scan(&id); err != nil {
			t.Fatalf("seed channel: %v", err)
		}
		return id
	}
	mkVideo := func(ch uuid.UUID, title, privacy, state string, sensitive bool) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, privacy, state, is_sensitive) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
			ch, title, privacy, state, sensitive).Scan(&id); err != nil {
			t.Fatalf("seed video %s: %v", title, err)
		}
		return id
	}
	owner, viewer, strangerOwner := mkUser("pvo-owner"), mkUser("pvo-viewer"), mkUser("pvo-other")
	c1, c2 := mkChannel(owner, "pvo1"), mkChannel(owner, "pvo2")
	cOther := mkChannel(strangerOwner, "pvo3")

	pub1 := mkVideo(c1, "pub1", "public", "published", false)
	pub2 := mkVideo(c2, "pub2", "public", "published", false)
	sens := mkVideo(c2, "sensitive", "public", "published", true)
	mkVideo(c1, "private", "private", "published", false)
	mkVideo(c2, "unlisted", "unlisted", "published", false)
	mkVideo(c1, "draft", "public", "draft", false)
	mkVideo(c1, "quarantined", "public", "quarantined", false)
	blocked := mkVideo(c2, "blocked", "public", "published", false)
	mkVideo(cOther, "someone-else", "public", "published", false)
	if _, err := st.Pool.Exec(ctx, `INSERT INTO video_blocks (video_id, reason) VALUES ($1, 'x')`, blocked); err != nil {
		t.Fatalf("block: %v", err)
	}

	ids := func(viewerID *uuid.UUID, hideSensitive bool) (map[uuid.UUID]bool, int64) {
		t.Helper()
		rows, err := q.ListPublicVideosByOwner(ctx, sqlcgen.ListPublicVideosByOwnerParams{
			OwnerID: owner, ViewerID: pgconv.UUIDPtr(viewerID), HideSensitive: hideSensitive, Sort: "-published_at", ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		n, err := q.CountPublicVideosByOwnerVisible(ctx, sqlcgen.CountPublicVideosByOwnerVisibleParams{
			OwnerID: owner, ViewerID: pgconv.UUIDPtr(viewerID), HideSensitive: hideSensitive,
		})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		out := map[uuid.UUID]bool{}
		for _, r := range rows {
			out[r.ID] = true
		}
		return out, n
	}

	got, n := ids(nil, false)
	if len(got) != 3 || n != 3 || !got[pub1] || !got[pub2] || !got[sens] {
		t.Errorf("anonymous sees %v (total %d), want pub1+pub2+sensitive only", got, n)
	}
	if got, n = ids(nil, true); len(got) != 2 || n != 2 || got[sens] {
		t.Errorf("hide_sensitive sees %v (total %d), want sensitive removed", got, n)
	}
	// A viewer who muted the owner sees nothing, and the total agrees.
	if _, err := st.Pool.Exec(ctx, `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, viewer, owner); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if got, n = ids(&viewer, false); len(got) != 0 || n != 0 {
		t.Errorf("muting viewer sees %v (total %d), want none", got, n)
	}
}
