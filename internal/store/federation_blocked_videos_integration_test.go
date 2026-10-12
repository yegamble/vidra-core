//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// T13: the federation read paths must exclude a moderator-blocked video. Proven
// on real Postgres because the predicate is SQL; block and unblock are applied in
// turn so a broken fixture cannot pass as a working filter.
func TestFederationQueriesExcludeBlockedVideosOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var owner, channelID, videoID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"fedblock-"+suffix, "fedblock-"+suffix+"@example.test").Scan(&owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) })
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Fed Block') RETURNING id`,
		owner, "fedchan-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'federated', 'public', 'published') RETURNING id`,
		channelID).Scan(&videoID); err != nil {
		t.Fatalf("seed video: %v", err)
	}

	state := func() (listed int, counted int64, blocked bool) {
		t.Helper()
		rows, err := q.ListChannelOutboxVideos(ctx, sqlcgen.ListChannelOutboxVideosParams{ChannelID: channelID, Limit: 50})
		if err != nil {
			t.Fatalf("ListChannelOutboxVideos: %v", err)
		}
		counted, err = q.CountPublicVideosByChannel(ctx, channelID)
		if err != nil {
			t.Fatalf("CountPublicVideosByChannel: %v", err)
		}
		v, err := q.GetVideoByID(ctx, videoID)
		if err != nil {
			t.Fatalf("GetVideoByID: %v", err)
		}
		return len(rows), counted, v.Blocked
	}

	if n, c, b := state(); n != 1 || c != 1 || b {
		t.Fatalf("unblocked: list=%d count=%d blocked=%v, want 1/1/false", n, c, b)
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO video_blocks (video_id, reason) VALUES ($1, 'tos')`, videoID); err != nil {
		t.Fatalf("block: %v", err)
	}
	if n, c, b := state(); n != 0 || c != 0 || !b {
		t.Fatalf("blocked: list=%d count=%d blocked=%v, want 0/0/true", n, c, b)
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM video_blocks WHERE video_id = $1`, videoID); err != nil {
		t.Fatalf("unblock: %v", err)
	}
	if n, c, b := state(); n != 1 || c != 1 || b {
		t.Fatalf("re-unblocked: list=%d count=%d blocked=%v, want 1/1/false", n, c, b)
	}
}
