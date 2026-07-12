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

// TestLiveStreamQueriesPersist exercises the live_streams queries against real
// PostgreSQL, including the 0061 replay_enabled column and the UpdateLiveStream
// edit query: create (replay off by default), key-hash lookup, state flip, edit
// (replay on + metadata), and the channel→ ON DELETE CASCADE.
func TestLiveStreamQueriesPersist(t *testing.T) {
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
		"live-"+suffix, "live-"+suffix+"@example.test",
	).Scan(&userID); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, userID) }()

	var channelID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Live') RETURNING id`,
		userID, "live_"+suffix,
	).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}

	// Create: replay defaults off; only the hash is stored.
	created, err := q.CreateLiveStream(ctx, sqlcgen.CreateLiveStreamParams{
		ChannelID: channelID, Title: "My Show", Description: "d", Privacy: "unlisted",
		Permanent: false, ReplayEnabled: false, StreamKeyHash: "hash-" + suffix,
	})
	if err != nil {
		t.Fatalf("CreateLiveStream: %v", err)
	}
	if created.State != "offline" || created.ReplayEnabled {
		t.Fatalf("created = %+v, want offline + replay off", created)
	}

	// Key-hash lookup resolves the id (the ingest boundary path) and, since
	// W11, the owning user (the per-user simultaneous-lives cap).
	byHash, err := q.GetLiveStreamByKeyHash(ctx, "hash-"+suffix)
	if err != nil || byHash.ID != created.ID {
		t.Fatalf("GetLiveStreamByKeyHash = (%+v, %v), want the created stream", byHash, err)
	}
	if byHash.OwnerID != userID {
		t.Fatalf("GetLiveStreamByKeyHash owner = %s, want the seed user %s", byHash.OwnerID, userID)
	}

	// Nothing of this user's is live yet (the W11 cap-count queries).
	if n, err := q.CountLiveStreamsLiveByOwner(ctx, userID); err != nil || n != 0 {
		t.Fatalf("CountLiveStreamsLiveByOwner (offline) = (%d, %v), want (0, nil)", n, err)
	}

	// State flip (ingest start).
	if err := q.SetLiveStreamState(ctx, sqlcgen.SetLiveStreamStateParams{ID: created.ID, State: "live"}); err != nil {
		t.Fatalf("SetLiveStreamState: %v", err)
	}

	// The cap counts see the live session: exactly one for this owner, at
	// least one instance-wide (the DB is shared with other suites).
	if n, err := q.CountLiveStreamsLiveByOwner(ctx, userID); err != nil || n != 1 {
		t.Fatalf("CountLiveStreamsLiveByOwner (live) = (%d, %v), want (1, nil)", n, err)
	}
	if n, err := q.CountLiveStreamsLive(ctx); err != nil || n < 1 {
		t.Fatalf("CountLiveStreamsLive = (%d, %v), want >= 1", n, err)
	}

	// The duration-watchdog scan: a future cutoff sees the session as overdue,
	// a past cutoff does not (started_at was stamped by the state flip).
	overdueAt := func(cutoff time.Time) bool {
		rows, lerr := q.ListOverdueLiveStreams(ctx, pgtype.Timestamptz{Time: cutoff, Valid: true})
		if lerr != nil {
			t.Fatalf("ListOverdueLiveStreams: %v", lerr)
		}
		for _, r := range rows {
			if r.ID == created.ID {
				return true
			}
		}
		return false
	}
	if !overdueAt(time.Now().Add(time.Hour)) {
		t.Error("session missing from ListOverdueLiveStreams with a future cutoff")
	}
	if overdueAt(time.Now().Add(-time.Hour)) {
		t.Error("session wrongly overdue with a cutoff before its started_at")
	}

	// Edit: replay on + metadata, state untouched.
	edited, err := q.UpdateLiveStream(ctx, sqlcgen.UpdateLiveStreamParams{
		ID: created.ID, Title: "After", Description: "d2", Privacy: "public",
		Permanent: true, ReplayEnabled: true,
	})
	if err != nil {
		t.Fatalf("UpdateLiveStream: %v", err)
	}
	if edited.Title != "After" || !edited.Permanent || !edited.ReplayEnabled || edited.State != "live" {
		t.Fatalf("edited = %+v, want After/permanent/replay-on/live", edited)
	}
	got, err := q.GetLiveStreamByID(ctx, created.ID)
	if err != nil || !got.ReplayEnabled || got.OwnerID != userID {
		t.Fatalf("GetLiveStreamByID = (%+v, %v), want replay-on owned by the seed user", got, err)
	}

	// Deleting the channel cascades the live stream away (0034 FK).
	if _, err := st.Pool.Exec(ctx, `DELETE FROM channels WHERE id = $1`, channelID); err != nil {
		t.Fatalf("delete channel: %v", err)
	}
	var count int
	if err := st.Pool.QueryRow(ctx,
		`SELECT count(*) FROM live_streams WHERE id = $1`, created.ID,
	).Scan(&count); err != nil || count != 0 {
		t.Fatalf("live_streams after channel delete = %d/%v, want 0 (ON DELETE CASCADE)", count, err)
	}
}
