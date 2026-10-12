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

// TestPublicVideosByOwnerMatchesChannelUnion is the drift alarm for the two
// twin queries: for every viewer class, the owner-scoped list must equal the
// union of the per-channel public lists, across every privacy value, a draft, a
// quarantined, a blocked and a sensitive video on two channels of one owner.
func TestPublicVideosByOwnerMatchesChannelUnion(t *testing.T) {
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
	owner := mkUser("par-owner")
	signedIn, muter, blocker := mkUser("par-in"), mkUser("par-mute"), mkUser("par-block")
	var chans []uuid.UUID
	for _, h := range []string{"par1", "par2"} {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'C') RETURNING id`,
			owner, h+"_"+suffix).Scan(&id); err != nil {
			t.Fatalf("seed channel: %v", err)
		}
		chans = append(chans, id)
	}
	var blockedVideo uuid.UUID
	n := 0
	for ci, ch := range chans {
		for _, privacy := range []string{"public", "unlisted", "private", "password", "internal"} {
			for _, state := range []string{"published", "draft", "quarantined"} {
				for _, sens := range []bool{false, true} {
					n++
					var id uuid.UUID
					if err := st.Pool.QueryRow(ctx,
						`INSERT INTO videos (channel_id, title, privacy, state, is_sensitive) VALUES ($1, $2, $3, $4, $5) RETURNING id`,
						ch, "v"+string(rune('a'+n%26)), privacy, state, sens).Scan(&id); err != nil {
						t.Fatalf("seed video: %v", err)
					}
					if ci == 1 && privacy == "public" && state == "published" && sens {
						blockedVideo = id
					}
				}
			}
		}
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO video_blocks (video_id, reason) VALUES ($1, 'x')`, blockedVideo); err != nil {
		t.Fatalf("block: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`, muter, owner); err != nil {
		t.Fatalf("mute: %v", err)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, blocker, owner); err != nil {
		t.Fatalf("user block: %v", err)
	}

	viewers := map[string]*uuid.UUID{"anonymous": nil, "signed-in": &signedIn, "muting": &muter, "blocking": &blocker}
	for name, v := range viewers {
		for _, hide := range []bool{false, true} {
			want := map[uuid.UUID]bool{}
			for _, ch := range chans {
				rows, err := q.ListPublicVideosByChannel(ctx, sqlcgen.ListPublicVideosByChannelParams{
					ChannelID: ch, ViewerID: pgconv.UUIDPtr(v), HideSensitive: hide, Sort: "-published_at", ResultLimit: 500,
				})
				if err != nil {
					t.Fatalf("channel list: %v", err)
				}
				for _, r := range rows {
					want[r.ID] = true
				}
			}
			rows, err := q.ListPublicVideosByOwner(ctx, sqlcgen.ListPublicVideosByOwnerParams{
				OwnerID: owner, ViewerID: pgconv.UUIDPtr(v), HideSensitive: hide, Sort: "-published_at", ResultLimit: 500,
			})
			if err != nil {
				t.Fatalf("owner list: %v", err)
			}
			got := map[uuid.UUID]bool{}
			for _, r := range rows {
				got[r.ID] = true
			}
			total, err := q.CountPublicVideosByOwnerVisible(ctx, sqlcgen.CountPublicVideosByOwnerVisibleParams{
				OwnerID: owner, ViewerID: pgconv.UUIDPtr(v), HideSensitive: hide,
			})
			if err != nil {
				t.Fatalf("owner count: %v", err)
			}
			if len(got) != len(want) || int64(len(got)) != total {
				t.Errorf("%s hide=%v: owner list %d / count %d, channel union %d", name, hide, len(got), total, len(want))
			}
			for id := range want {
				if !got[id] {
					t.Errorf("%s hide=%v: %v in the channel lists but not the owner list", name, hide, id)
				}
			}
			if (name == "muting" || name == "blocking") && len(got) != 0 {
				t.Errorf("%s viewer must see nothing, got %d", name, len(got))
			}
			if name == "signed-in" && !hide && len(got) == 0 {
				t.Errorf("signed-in viewer saw nothing: the parity check would be vacuous")
			}
		}
	}
}
