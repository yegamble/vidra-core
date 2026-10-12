//go:build integration

package store

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// An inbound Move (0167) is claimed at most once per origin per cooldown, the
// claim is atomic, and the edges it migrates are the live ones — bounded, oldest
// first, never a rejected follow and never another actor's.
func TestRemoteActorMoveClaimAndEdges(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	suffix := uuid.NewString()[:8]
	origin := "https://old.test/users/mover-" + suffix
	other := "https://old.test/users/stays-" + suffix
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actor_moves WHERE origin_actor_url = $1`, origin)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE username LIKE $1`, "%-"+suffix)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url IN ($1, $2)`, origin, other)
	})

	claim := func() int64 {
		t.Helper()
		n, err := q.ClaimRemoteActorMove(ctx, sqlcgen.ClaimRemoteActorMoveParams{OriginActorUrl: origin, TargetActorUrl: "https://new.test/users/x"})
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		return n
	}
	if claim() != 1 {
		t.Fatal("the first Move from an origin was not claimed")
	}
	if claim() != 0 {
		t.Fatal("a second Move inside the cooldown was claimed")
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE remote_actor_moves SET claimed_at = now() - interval '25 hours' WHERE origin_actor_url = $1`, origin); err != nil {
		t.Fatal(err)
	}
	if claim() != 1 {
		t.Fatal("a Move after the cooldown was not claimed")
	}

	for _, a := range []string{origin, other} {
		if err := q.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{ActorUrl: a, ActorType: "Group", Domain: "old.test", PublicKeyPem: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	follow := func(name, actor, state string, at time.Time) uuid.UUID {
		t.Helper()
		var user, id uuid.UUID
		if err := st.Pool.QueryRow(ctx, `INSERT INTO users (username, email, password_hash) VALUES ($1, $1 || '@example.test', 'x') RETURNING id`,
			name+"-"+suffix).Scan(&user); err != nil {
			t.Fatal(err)
		}
		if err := st.Pool.QueryRow(ctx, `INSERT INTO remote_channel_follows (user_id, remote_actor_url, state, created_at) VALUES ($1, $2, $3, $4) RETURNING id`,
			user, actor, state, at).Scan(&id); err != nil {
			t.Fatal(err)
		}
		return id
	}
	now := time.Now()
	first := follow("first", origin, "accepted", now.Add(-2*time.Hour))
	second := follow("second", origin, "pending", now.Add(-time.Hour))
	follow("refused", origin, "rejected", now.Add(-3*time.Hour))
	follow("elsewhere", other, "accepted", now.Add(-4*time.Hour))

	edges := func(limit int32) []uuid.UUID {
		t.Helper()
		rows, err := q.ListRemoteChannelFollowsOfActor(ctx, sqlcgen.ListRemoteChannelFollowsOfActorParams{RemoteActorUrl: origin, ResultLimit: limit})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids
	}
	if got := edges(10); len(got) != 2 || got[0] != first || got[1] != second {
		t.Errorf("edges = %v, want [%s %s] (live, oldest first)", got, first, second)
	}
	if got := edges(1); len(got) != 1 || got[0] != first {
		t.Errorf("bounded edges = %v, want only the oldest", got)
	}
}
