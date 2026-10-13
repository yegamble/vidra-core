//go:build integration

package store

import (
	"context"
	"errors"
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
	url := "https://pt-" + uuid.NewString()[:8] + ".example/accounts/peertube"
	if err := st.Queries().UpsertRemoteActor(context.Background(), sqlcgen.UpsertRemoteActorParams{
		ActorUrl: url, ActorType: "Application", PreferredUsername: "peertube", PublicKeyPem: "k",
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
