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

// TestLivePublicRailHonoursTheOwnerAccountGate: the public "Live now" rail must
// drop a stream whose owner opted out of discovery (unlisted) or is deactivated,
// exactly as the video feed and channel search do, and bring it back when the
// account is restored. LIST and COUNT are asserted together, with an anonymous
// control read at each step.
func TestLivePublicRailHonoursTheOwnerAccountGate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	suffix := uuid.NewString()[:8]
	var owner uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
		"livegate-"+suffix, "livegate-"+suffix+"@example.test").Scan(&owner); err != nil {
		t.Fatalf("seed user: %v", err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, owner) })
	var channelID, streamID uuid.UUID
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'Gate') RETURNING id`,
		owner, "gatechan-"+suffix).Scan(&channelID); err != nil {
		t.Fatalf("seed channel: %v", err)
	}
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO live_streams (channel_id, title, description, privacy, state, stream_key_hash, started_at)
		 VALUES ($1, 'Gate Test', '', 'public', 'live', $2, now()) RETURNING id`,
		channelID, "hash-"+suffix).Scan(&streamID); err != nil {
		t.Fatalf("seed stream: %v", err)
	}

	onRail := func() (bool, int64) {
		t.Helper()
		rows, err := q.ListLivePublicStreams(ctx, sqlcgen.ListLivePublicStreamsParams{ViewerID: pgtype.UUID{}, ResultLimit: 500})
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		total, err := q.CountLivePublicStreams(ctx, pgtype.UUID{})
		if err != nil {
			t.Fatalf("count: %v", err)
		}
		for _, r := range rows {
			if r.ID == streamID {
				return true, total
			}
		}
		return false, total
	}

	if ok, _ := onRail(); !ok {
		t.Fatal("control: the active, listed owner's live stream is missing from the rail")
	}
	for _, col := range []string{"unlisted = TRUE", "is_active = FALSE"} {
		_, before := onRail()
		if _, err := st.Pool.Exec(ctx, `UPDATE users SET `+col+` WHERE id = $1`, owner); err != nil {
			t.Fatalf("set %s: %v", col, err)
		}
		ok, after := onRail()
		if ok || after != before-1 {
			t.Errorf("with %s: on rail=%v total %d -> %d, want hidden and the total down by one", col, ok, before, after)
		}
		if _, err := st.Pool.Exec(ctx, `UPDATE users SET unlisted = FALSE, is_active = TRUE WHERE id = $1`, owner); err != nil {
			t.Fatalf("restore: %v", err)
		}
		if ok, _ := onRail(); !ok {
			t.Errorf("after restoring from %s the stream did not come back", col)
		}
	}
}
