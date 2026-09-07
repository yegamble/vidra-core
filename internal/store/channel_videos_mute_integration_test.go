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

// TestChannelVideosRespectMutesAndBlocksOnRealPG holds the A16 ruling in the
// only place that can hold it: the SQL. ListPublicVideosByChannel and its
// Count carried the moderator `video_blocks` predicate but no per-viewer
// mute/block clause, so a muted account's OWN channel page kept listing
// everything the mute hid from the feed, from search, from the subscriptions
// feed and from the recommendation rails — and an autosuggest channel hit
// links straight to that page.
//
// The handler test in internal/httpapi proves the wiring against a fake that
// MIRRORS this clause; only this one proves the clause exists. Each exclusion
// is applied and then lifted so a broken fixture cannot masquerade as a
// working filter, and the LIST and the COUNT are asserted together: a page
// that returned no rows while still reporting total=1 would promise the muter
// a second page it cannot serve.
func TestChannelVideosRespectMutesAndBlocksOnRealPG(t *testing.T) {
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
		t.Helper()
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test",
		).Scan(&id); err != nil {
			t.Fatalf("seed user %s: %v", name, err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, id) })
		return id
	}

	owner := mkUser("chanmute-owner")
	viewer := mkUser("chanmute-viewer")
	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Chan Mute') RETURNING id`,
		owner, "chanmute-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'on the channel page', 'public', 'published')`,
		channelID,
	); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	// visible reports what one viewer sees on the channel page: the row count
	// from the LIST and the number the COUNT promises. They must agree.
	visible := func(who pgtype.UUID) (int, int64) {
		t.Helper()
		rows, err := q.ListPublicVideosByChannel(ctx, sqlcgen.ListPublicVideosByChannelParams{
			ChannelID: channelID, ViewerID: who, Sort: "-published_at", ResultLimit: 50,
		})
		if err != nil {
			t.Fatalf("ListPublicVideosByChannel: %v", err)
		}
		total, err := q.CountPublicVideosByChannelVisible(ctx, sqlcgen.CountPublicVideosByChannelVisibleParams{
			ChannelID: channelID, ViewerID: who,
		})
		if err != nil {
			t.Fatalf("CountPublicVideosByChannelVisible: %v", err)
		}
		if int64(len(rows)) != total {
			t.Errorf("list returned %d rows while the count promised %d", len(rows), total)
		}
		return len(rows), total
	}

	anon := pgtype.UUID{}
	asViewer := pgtype.UUID{Bytes: viewer, Valid: true}

	if n, total := visible(asViewer); n != 1 || total != 1 {
		t.Fatalf("before any relationship the viewer sees %d/%d, want 1/1", n, total)
	}

	for _, tc := range []struct {
		why     string
		apply   string
		unapply string
	}{
		{
			why:     "the viewer muted the channel owner",
			apply:   `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`,
			unapply: `DELETE FROM muted_accounts WHERE muter_id = $1 AND muted_id = $2`,
		},
		{
			why:     "the viewer blocked the channel owner",
			apply:   `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`,
			unapply: `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`,
		},
	} {
		if _, err := st.Pool.Exec(ctx, tc.apply, viewer, owner); err != nil {
			t.Fatalf("apply %q: %v", tc.why, err)
		}
		if n, total := visible(asViewer); n != 0 || total != 0 {
			t.Errorf("with %s the viewer still sees %d/%d, want 0/0", tc.why, n, total)
		}
		// The live control: the exclusion is per-viewer, so an anonymous
		// caller's page is untouched at the same instant.
		if n, total := visible(anon); n != 1 || total != 1 {
			t.Errorf("with %s an anonymous caller sees %d/%d, want 1/1 (per-viewer)", tc.why, n, total)
		}
		if _, err := st.Pool.Exec(ctx, tc.unapply, viewer, owner); err != nil {
			t.Fatalf("unapply %q: %v", tc.why, err)
		}
		if n, total := visible(asViewer); n != 1 || total != 1 {
			t.Errorf("after lifting %q the viewer sees %d/%d, want 1/1", tc.why, n, total)
		}
	}

	// The reverse direction is NOT an exclusion: being blocked BY the owner
	// does not hide the owner's public channel page, exactly as it does not
	// hide their videos from the feed. Asserting it pins the clause's shape
	// (one-directional, keyed on the VIEWER) rather than its mere presence.
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, owner, viewer,
	); err != nil {
		t.Fatalf("seed reverse block: %v", err)
	}
	if n, _ := visible(asViewer); n != 1 {
		t.Errorf("blocked BY the owner the viewer sees %d, want 1 (the clause is one-directional)", n)
	}
}
