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

// The instance actor's name (migration 0166) is held in the one handle
// namespace, so NO writer — registration, admin creation, a channel, an
// importer — can claim it, whatever its case. Proved at the trigger, like 0142.
func TestNoAccountOrChannelCanTakeTheInstanceActorName(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()

	reserved, err := q.InstanceActorNameReserved(ctx, "PeerTube")
	if err != nil || !reserved {
		t.Fatalf("InstanceActorNameReserved = %v, %v; want true", reserved, err)
	}
	for _, name := range []string{"peertube", "PeerTube"} {
		_, err := q.CreateUser(ctx, sqlcgen.CreateUserParams{
			Username: name, Email: uuid.NewString() + "@example.test", PasswordHash: "x", Role: "user",
		})
		if !pgconv.IsHandleReserved(err) {
			t.Errorf("account %q: err = %v (constraint %q), want the reservation", name, err, pgconv.ConstraintName(err))
		}
	}
	userID, _, cleanup := seedUserAndChannel(t, st)
	defer cleanup()
	_, err = st.Pool.Exec(ctx,
		`INSERT INTO channels (owner_id, handle, display_name) VALUES ($1, 'PEERTUBE', 'x')`, userID)
	if !pgconv.IsHandleReserved(err) {
		t.Errorf("channel: err = %v (constraint %q), want the reservation", err, pgconv.ConstraintName(err))
	}
	// OAuth-derived usernames probe the same namespace and step around it.
	if taken, err := q.UsernameExists(ctx, "peertube"); err != nil || !taken {
		t.Errorf("UsernameExists(peertube) = %v, %v; want taken", taken, err)
	}
}

// The key is a singleton: a second mint is a no-op and the first one stands.
func TestInstanceActorKeyIsASingleton(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	if _, err := st.Pool.Exec(ctx, `DELETE FROM instance_actor_keys`); err != nil {
		t.Fatal(err)
	}
	defer func() { _, _ = st.Pool.Exec(context.Background(), `DELETE FROM instance_actor_keys`) }()

	for i, pub := range []string{"first", "second"} {
		n, err := q.InsertInstanceActorKeyIfAbsent(ctx, sqlcgen.InsertInstanceActorKeyIfAbsentParams{PublicKeyPem: pub, PrivateKeyPem: "p"})
		if err != nil || n != int64(1-i) {
			t.Fatalf("insert %d = %d, %v; want %d", i, n, err, 1-i)
		}
	}
	row, err := q.GetInstanceActorKey(ctx)
	if err != nil || row.PublicKeyPem != "first" {
		t.Errorf("key = %q, %v; want the first", row.PublicKeyPem, err)
	}
}
