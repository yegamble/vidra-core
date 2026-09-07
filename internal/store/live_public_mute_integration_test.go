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

// TestLivePublicRailRespectsMutesAndBlocksOnRealPG closes the last public list
// that took no viewer at all.
//
// The A16 mute-scope slice read it out of the code and could not measure it: the
// lab had no live stream, so `GET /live` — the home "Live now" rail — was
// recorded as a finding rather than a fix. ListLivePublicStreams took only
// limit/offset, so a muted or blocked account's live stream stayed on the
// muter's rail while the feed, search, the subscriptions list and the channel
// page had all dropped them.
//
// A live stream cannot be created through the product without the RTMP publish
// path, so the row is inserted directly at the state the ingest boundary would
// leave it in (state='live', privacy='public', started_at set). Every surface
// under test here is a list PREDICATE over live_streams + channels + the mute
// tables and never touches media, so the missing ingest cannot flatter it.
//
// Each relationship is applied and then LIFTED, with an anonymous control read
// at the same instant, so a broken fixture cannot masquerade as a working
// filter; the LIST and the COUNT are asserted together because a rail returning
// no rows while the total still promised one would promise a page it cannot
// serve.
func TestLivePublicRailRespectsMutesAndBlocksOnRealPG(t *testing.T) {
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

	streamer := mkUser("liverail-streamer")
	viewer := mkUser("liverail-viewer")

	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Live Rail') RETURNING id`,
		streamer, "liverail-"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	var streamID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO live_streams (channel_id, title, description, privacy, state, stream_key_hash, started_at)
		 VALUES ($1, 'Live Rail Test', '', 'public', 'live', $2, now()) RETURNING id`,
		channelID, "hash-"+suffix,
	).Scan(&streamID); err != nil {
		t.Fatalf("seed live stream: %v", err)
	}

	// visible counts the fixture's stream on one viewer's rail, from the LIST,
	// and reports what the COUNT promises for the same viewer.
	visible := func(who pgtype.UUID) (int, int64) {
		t.Helper()
		rows, err := q.ListLivePublicStreams(ctx, sqlcgen.ListLivePublicStreamsParams{
			ViewerID: who, ResultLimit: 100,
		})
		if err != nil {
			t.Fatalf("ListLivePublicStreams: %v", err)
		}
		total, err := q.CountLivePublicStreams(ctx, who)
		if err != nil {
			t.Fatalf("CountLivePublicStreams: %v", err)
		}
		n := 0
		for _, r := range rows {
			if r.ID == streamID {
				n++
			}
		}
		return n, total
	}

	anon := pgtype.UUID{}
	asViewer := pgtype.UUID{Bytes: viewer, Valid: true}

	baseline, baseTotal := visible(asViewer)
	if baseline != 1 {
		t.Fatalf("before any relationship the viewer sees %d live streams of the fixture, want 1", baseline)
	}
	if n, _ := visible(anon); n != 1 {
		t.Fatalf("an anonymous caller sees %d, want 1", n)
	}

	for _, tc := range []struct {
		why     string
		apply   string
		unapply string
	}{
		{
			why:     "the viewer muted the streamer",
			apply:   `INSERT INTO muted_accounts (muter_id, muted_id) VALUES ($1, $2)`,
			unapply: `DELETE FROM muted_accounts WHERE muter_id = $1 AND muted_id = $2`,
		},
		{
			why:     "the viewer blocked the streamer",
			apply:   `INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`,
			unapply: `DELETE FROM user_blocks WHERE blocker_id = $1 AND blocked_id = $2`,
		},
	} {
		if _, err := st.Pool.Exec(ctx, tc.apply, viewer, streamer); err != nil {
			t.Fatalf("apply %q: %v", tc.why, err)
		}
		n, total := visible(asViewer)
		if n != 0 {
			t.Errorf("with %s the stream is still on the viewer's rail (%d)", tc.why, n)
		}
		if total != baseTotal-1 {
			t.Errorf("with %s the count is %d, want %d — the list and the total must move together", tc.why, total, baseTotal-1)
		}
		// The live control: the exclusion is per-viewer.
		if n, _ := visible(anon); n != 1 {
			t.Errorf("with %s an anonymous caller sees %d, want 1 (per-viewer)", tc.why, n)
		}
		if _, err := st.Pool.Exec(ctx, tc.unapply, viewer, streamer); err != nil {
			t.Fatalf("unapply %q: %v", tc.why, err)
		}
		if n, total := visible(asViewer); n != 1 || total != baseTotal {
			t.Errorf("after lifting %q the viewer sees %d (total %d), want 1 (total %d)", tc.why, n, total, baseTotal)
		}
	}

	// The clause is keyed on the VIEWER and one-directional: being blocked BY
	// the streamer does not hide their public rail entry, exactly as it does not
	// hide their videos. This pins the clause's shape, not its mere presence.
	if _, err := st.Pool.Exec(ctx,
		`INSERT INTO user_blocks (blocker_id, blocked_id) VALUES ($1, $2)`, streamer, viewer,
	); err != nil {
		t.Fatalf("seed reverse block: %v", err)
	}
	if n, _ := visible(asViewer); n != 1 {
		t.Errorf("blocked BY the streamer the viewer sees %d, want 1 (the clause is one-directional)", n)
	}
}
