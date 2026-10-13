//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The new-follower backfill lists only what public discovery shows: public,
// published, not moderator-blocked, owner active and not account-unlisted. The
// channel outbox's own query checks neither owner clause, so this is the query
// a push to a stranger's inbox must use.
func TestChannelBackfillVideoIDsFollowTheDiscoveryPredicate(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	suffix := uuid.NewString()[:8]

	owner := func(name string) (uuid.UUID, uuid.UUID) {
		var uid, cid uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO users (username, email, password_hash) VALUES ($1, $2, 'x') RETURNING id`,
			name+"-"+suffix, name+"-"+suffix+"@example.test").Scan(&uid); err != nil {
			t.Fatalf("user: %v", err)
		}
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, $2, 'C') RETURNING id`,
			uid, "ch-"+name+"-"+suffix).Scan(&cid); err != nil {
			t.Fatalf("channel: %v", err)
		}
		t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id=$1`, uid) })
		return uid, cid
	}
	video := func(cid uuid.UUID, privacy, state string) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx,
			`INSERT INTO videos (channel_id, title, description, privacy, state) VALUES ($1,'t','',$2,$3) RETURNING id`,
			cid, privacy, state).Scan(&id); err != nil {
			t.Fatalf("video: %v", err)
		}
		return id
	}
	list := func(cid uuid.UUID, limit int32) []uuid.UUID {
		ids, err := q.ListChannelBackfillVideoIDs(ctx, sqlcgen.ListChannelBackfillVideoIDsParams{ChannelID: cid, ResultLimit: limit})
		if err != nil {
			t.Fatalf("ListChannelBackfillVideoIDs: %v", err)
		}
		return ids
	}

	_, cid := owner("bf-ok")
	pub1 := video(cid, "public", "published")
	pub2 := video(cid, "public", "published")
	video(cid, "unlisted", "published")
	video(cid, "private", "published")
	video(cid, "public", "processing")
	blocked := video(cid, "public", "published")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO video_blocks (video_id, reason) VALUES ($1,'x')`, blocked); err != nil {
		t.Fatalf("block: %v", err)
	}
	got := list(cid, 20)
	if len(got) != 2 || got[0] != pub2 || got[1] != pub1 {
		t.Fatalf("ids = %v, want [%v %v] (public+published+unblocked, newest first)", got, pub2, pub1)
	}
	if lim := list(cid, 1); len(lim) != 1 {
		t.Errorf("limit 1 returned %d", len(lim))
	}

	uid, ucid := owner("bf-unlisted")
	video(ucid, "public", "published")
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET unlisted = TRUE WHERE id=$1`, uid); err != nil {
		t.Fatal(err)
	}
	if got := list(ucid, 20); len(got) != 0 {
		t.Errorf("unlisted owner's videos = %v, want none", got)
	}

	bid, bcid := owner("bf-banned")
	video(bcid, "public", "published")
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET is_active = FALSE WHERE id=$1`, bid); err != nil {
		t.Fatal(err)
	}
	if got := list(bcid, 20); len(got) != 0 {
		t.Errorf("deactivated owner's videos = %v, want none", got)
	}
}

func TestChannelFollowerBackfillClaimCooldownAndCap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	host := "bf-" + uuid.NewString()[:8] + ".example"
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM channel_follower_backfills WHERE host=$1`, host)
	})
	ch := uuid.New()
	claim := func(h string, c uuid.UUID, hostCap, cap int32) int64 {
		n, err := q.ClaimChannelFollowerBackfill(ctx, sqlcgen.ClaimChannelFollowerBackfillParams{
			Host: h, ChannelID: c, CooldownSeconds: 86400, HostChannelCap: hostCap, DailyCap: cap})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	const big = 1_000_000
	if claim(host, ch, 10, big) != 1 {
		t.Fatal("first claim refused")
	}
	if claim(host, ch, 10, big) != 0 {
		t.Error("second claim for the same channel inside the cooldown was granted")
	}
	if claim(host, uuid.New(), 10, big) != 1 {
		t.Error("a second channel from the same host was refused")
	}
	for i := 0; i < 8; i++ { // channels 3..10
		if claim(host, uuid.New(), 10, big) != 1 {
			t.Fatalf("channel %d refused under the host cap", i+3)
		}
	}
	if claim(host, uuid.New(), 10, big) != 0 {
		t.Error("an 11th channel from one host inside the day was granted")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE channel_follower_backfills SET last_backfill_at = now() - interval '25 hours' WHERE host=$1 AND channel_id=$2`, host, ch); err != nil {
		t.Fatal(err)
	}
	if claim(host, ch, 10, big) != 1 {
		t.Error("claim after the cooldown elapsed was refused")
	}
	// The instance-wide cap counts distinct hosts: a new host is refused at the
	// cap, a host already counted is not.
	if claim("new-"+host, uuid.New(), 10, 0) != 0 {
		t.Error("a new host over the daily host cap was granted")
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM channel_follower_backfills WHERE host=$1`, "new-"+host)
	})
	if claim(host, uuid.New(), 100, 1) != 1 {
		t.Error("a host already counted today was refused by the distinct-host cap")
	}
}
