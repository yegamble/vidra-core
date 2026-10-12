//go:build integration

package store

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedRemoteInstanceActor caches a remote instance actor (the FK target of both
// instance-follow tables) and removes it — and its follow rows — afterwards.
func seedRemoteInstanceActor(t *testing.T, st *Store) string {
	t.Helper()
	host := "pt-" + uuid.NewString()[:8] + ".example"
	url := "https://" + host + "/accounts/peertube"
	if err := st.Queries().UpsertRemoteActor(context.Background(), sqlcgen.UpsertRemoteActorParams{
		ActorUrl: url, ActorType: "Application", PreferredUsername: "peertube", PublicKeyPem: "k", Domain: host,
		InboxUrl: url + "/inbox",
	}); err != nil {
		t.Fatalf("seed remote actor: %v", err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url = $1`, url)
	})
	return url
}

func TestInstanceFollowerLifecycleOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	actor := seedRemoteInstanceActor(t, st)

	insert := func(state, follow string) int64 {
		n, err := q.InsertInstanceFollowerIfAbsent(ctx, sqlcgen.InsertInstanceFollowerIfAbsentParams{
			RemoteActorUrl: actor, State: state, FollowActivityUrl: follow,
		})
		if err != nil {
			t.Fatalf("insert: %v", err)
		}
		return n
	}
	if insert("pending", "f1") != 1 || insert("accepted", "f2") != 0 {
		t.Fatal("a known follower must be left in the state the admin left it")
	}
	row, err := q.GetInstanceFollowerByActor(ctx, actor)
	if err != nil || row.State != "pending" || row.FollowActivityUrl != "f1" {
		t.Fatalf("row = %+v, %v", row, err)
	}
	// A rejected follower survives its own Undo.
	if _, err := st.Pool.Exec(ctx, `UPDATE instance_followers SET state = 'rejected' WHERE remote_actor_url = $1`, actor); err != nil {
		t.Fatal(err)
	}
	if n, err := q.DeleteInstanceFollowerByActor(ctx, actor); err != nil || n != 0 {
		t.Errorf("Undo of a rejected follower deleted %d, %v", n, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE instance_followers SET state = 'accepted' WHERE remote_actor_url = $1`, actor); err != nil {
		t.Fatal(err)
	}
	if n, err := q.DeleteInstanceFollowerByActor(ctx, actor); err != nil || n != 1 {
		t.Errorf("Undo of an accepted follower deleted %d, %v", n, err)
	}
	// The follower goes with its cached actor (a remote Delete{Actor}).
	insert("accepted", "f3")
	if _, err := q.DeleteRemoteActor(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := q.GetInstanceFollowerByActor(ctx, actor); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("follower outlived its actor: %v", err)
	}
}

// At most one UNREAD instance-follower notice per admin, however many requests.
func TestInstanceFollowerStaffNoticeIsOneUnreadPerAdmin(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	var admin uuid.UUID
	suffix := uuid.NewString()[:8]
	if err := st.Pool.QueryRow(ctx,
		`INSERT INTO users (username, email, password_hash, role) VALUES ($1, $2, 'x', 'admin') RETURNING id`,
		"if-admin-"+suffix, "if-admin-"+suffix+"@example.test").Scan(&admin); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE id = $1`, admin) })
	count := func() (n int) {
		t.Helper()
		if err := st.Pool.QueryRow(ctx, `SELECT count(*) FROM notifications
			WHERE user_id = $1 AND type = 'federation_follower_request' AND remote_follow_id IS NULL AND channel_id IS NULL`,
			admin).Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	for i := 0; i < 2; i++ {
		if _, err := q.NotifyStaffOfInstanceFollowerRequest(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if count() != 1 {
		t.Fatalf("notices = %d, want 1 unread", count())
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE notifications SET read_at = now() WHERE user_id = $1`, admin); err != nil {
		t.Fatal(err)
	}
	if _, err := q.NotifyStaffOfInstanceFollowerRequest(ctx); err != nil {
		t.Fatal(err)
	}
	if count() != 2 {
		t.Errorf("notices = %d, want a fresh one once the last was read", count())
	}
}

func TestDeliveryQueueCarriesTheInstanceSigner(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	inbox := "https://pt-" + uuid.NewString()[:8] + ".example/inbox"
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM federation_deliveries WHERE inbox_url = $1`, inbox)
	})
	if err := q.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{InboxUrl: inbox, Payload: []byte(`{}`), SigningInstanceActor: true}); err != nil {
		t.Fatal(err)
	}
	rows, err := q.ClaimDueDeliveries(ctx, sqlcgen.ClaimDueDeliveriesParams{BatchSize: 500, LeaseSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if r.InboxUrl == inbox {
			if !r.SigningInstanceActor || r.SigningChannelID.Valid || r.SigningUserID.Valid {
				t.Errorf("claimed row = %+v, want the instance signer alone", r)
			}
			return
		}
	}
	t.Fatal("the instance-signed delivery was not claimed")
}

// The instance outbox federates exactly what AnnounceVideo would: public,
// published, not blocked, on a channel that did not opt out of ActivityPub.
// Membership only — sibling packages share this database.
func TestInstanceOutboxPredicateOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	_, channelID, cleanup := seedUserAndChannel(t, st)
	defer cleanup()
	video := func(privacy, state string) uuid.UUID {
		var id uuid.UUID
		if err := st.Pool.QueryRow(ctx, `INSERT INTO videos (channel_id, title, privacy, state) VALUES ($1, 'v', $2, $3) RETURNING id`,
			channelID, privacy, state).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	public, blocked := video("public", "published"), video("public", "published")
	hidden := []uuid.UUID{blocked, video("private", "published"), video("unlisted", "published"),
		video("internal", "published"), video("public", "processing")}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO video_blocks (video_id, reason) VALUES ($1, 'tos')`, blocked); err != nil {
		t.Fatal(err)
	}
	listed := func(t *testing.T) map[uuid.UUID]bool {
		ids, err := q.ListInstanceOutboxVideoIDs(ctx, sqlcgen.ListInstanceOutboxVideoIDsParams{ResultLimit: 100000})
		if err != nil {
			t.Fatal(err)
		}
		out := map[uuid.UUID]bool{}
		for _, id := range ids {
			out[id] = true
		}
		return out
	}
	got := listed(t)
	if !got[public] {
		t.Error("a public published video is missing from the instance outbox")
	}
	for _, id := range hidden {
		if got[id] {
			t.Errorf("video %s must never be federated", id)
		}
	}
	if n, err := q.CountInstanceOutboxVideos(ctx); err != nil || n < 1 {
		t.Errorf("count = %d, %v", n, err)
	}
	if listed, err := q.IsVideoInInstanceOutbox(ctx, public); err != nil || !listed {
		t.Errorf("IsVideoInInstanceOutbox(public) = %v, %v", listed, err)
	}
	// The owner opts out of discovery: the video leaves the outbox (and so the
	// backfill and the live Announce, which read the same view).
	userID := uuid.Nil
	if err := st.Pool.QueryRow(ctx, `SELECT owner_id FROM channels WHERE id = $1`, channelID).Scan(&userID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET unlisted = TRUE WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	if listed(t)[public] {
		t.Error("an opted-out owner's video is still in the instance outbox")
	}
	if in, _ := q.IsVideoInInstanceOutbox(ctx, public); in {
		t.Error("IsVideoInInstanceOutbox must follow the opt-out")
	}
	if u, err := q.IsChannelOwnerUnlisted(ctx, channelID); err != nil || !u {
		t.Errorf("IsChannelOwnerUnlisted = %v, %v", u, err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE users SET unlisted = FALSE WHERE id = $1`, userID); err != nil {
		t.Fatal(err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE channels SET activitypub_enabled = FALSE WHERE id = $1`, channelID); err != nil {
		t.Fatal(err)
	}
	if listed(t)[public] {
		t.Error("a channel that opted out of ActivityPub is still in the instance outbox")
	}
}

func TestInstanceFollowerInboxesOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	accepted, pending := seedRemoteInstanceActor(t, st), seedRemoteInstanceActor(t, st)
	shared := "https://shared-" + uuid.NewString()[:8] + ".example/inbox"
	if _, err := st.Pool.Exec(ctx, `UPDATE remote_actors SET shared_inbox_url = $2 WHERE actor_url = $1`, accepted, shared); err != nil {
		t.Fatal(err)
	}
	for actor, state := range map[string]string{accepted: "accepted", pending: "pending"} {
		if _, err := q.InsertInstanceFollowerIfAbsent(ctx, sqlcgen.InsertInstanceFollowerIfAbsentParams{
			RemoteActorUrl: actor, State: state, FollowActivityUrl: "f",
		}); err != nil {
			t.Fatal(err)
		}
	}
	inboxes, err := q.ListAcceptedInstanceFollowerInboxes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]bool{}
	for _, in := range inboxes {
		got[in] = true
	}
	if !got[shared] || got[accepted+"/inbox"] || got[pending+"/inbox"] {
		t.Errorf("inboxes = %v, want the accepted follower's shared inbox only", inboxes)
	}
	// Never to a blocked actor, nor to an instance on the blocklist.
	has := func() bool {
		t.Helper()
		inboxes, err := q.ListAcceptedInstanceFollowerInboxes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, in := range inboxes {
			if in == shared {
				return true
			}
		}
		return false
	}
	if _, err := st.Pool.Exec(ctx, `INSERT INTO blocked_remote_actors (remote_actor_url, reason) VALUES ($1, '')`, accepted); err != nil {
		t.Fatal(err)
	}
	if has() {
		t.Error("an admin-blocked actor still receives Announces")
	}
	if _, err := st.Pool.Exec(ctx, `DELETE FROM blocked_remote_actors WHERE remote_actor_url = $1`, accepted); err != nil {
		t.Fatal(err)
	}
	domain := strings.TrimPrefix(strings.SplitN(accepted, "/accounts", 2)[0], "https://")
	if _, err := st.Pool.Exec(ctx, `INSERT INTO blocked_instances (domain) VALUES ($1)`, domain); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM blocked_instances WHERE domain = $1`, domain)
	})
	if has() {
		t.Error("an instance on the blocklist still receives Announces")
	}
}

// The backfill cooldown is a database fact: one claim per HOST per window,
// outliving the follower row (an Undo deletes that), whatever the case of the
// host a request spells.
func TestInstanceFollowerBackfillCooldownOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	host := "pt-" + uuid.NewString()[:8] + ".example"
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM instance_follower_backfills WHERE host = $1`, host)
	})
	claim := func(h string) int64 {
		t.Helper()
		n, err := q.ClaimInstanceFollowerBackfill(ctx, sqlcgen.ClaimInstanceFollowerBackfillParams{Host: h, CooldownSeconds: 86400})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if claim(host) != 1 || claim(strings.ToUpper(host)) != 0 {
		t.Fatal("want the first claim granted and a second one for the same host inside the window refused")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE instance_follower_backfills SET last_backfill_at = now() - interval '25 hours' WHERE host = $1`, host); err != nil {
		t.Fatal(err)
	}
	if claim(host) != 1 || claim(host) != 0 {
		t.Error("want a claim granted again once the window has passed, and only one")
	}
}
