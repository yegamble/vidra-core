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

// An inbound Move (0167) is claimed at most once per origin per cooldown and
// released if it does not verify; a verified move is a queue row the worker
// takes round-robin; blocks of the old actor carry to the new one; and the
// edges it re-points are the live ones — bounded, oldest first, never a rejected
// follow, never another actor's, never a user who blocks the target.
func TestRemoteActorMoveClaimQueueAndEdges(t *testing.T) {
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
	target := "https://new.test/users/x-" + suffix
	t.Cleanup(func() {
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actor_moves WHERE origin_actor_url IN ($1, $2)`, origin, other)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM users WHERE username LIKE $1`, "%-"+suffix)
		_, _ = st.Pool.Exec(context.Background(), `DELETE FROM remote_actors WHERE actor_url IN ($1, $2)`, origin, other)
	})

	claim := func(o string) int64 {
		t.Helper()
		n, err := q.ClaimRemoteActorMove(ctx, sqlcgen.ClaimRemoteActorMoveParams{OriginActorUrl: o, TargetActorUrl: target})
		if err != nil {
			t.Fatalf("claim: %v", err)
		}
		return n
	}
	if claim(origin) != 1 || claim(origin) != 0 {
		t.Fatal("want the first Move claimed and a second inside the cooldown refused")
	}
	// An unverified claim is released, and can be claimed again at once.
	if err := q.ReleaseRemoteActorMove(ctx, origin); err != nil || claim(origin) != 1 {
		t.Fatalf("released claim not re-claimable (%v)", err)
	}
	// A move under way is not released, and is the queue's next item.
	if err := q.SetRemoteActorMoveState(ctx, sqlcgen.SetRemoteActorMoveStateParams{OriginActorUrl: origin, State: "moving"}); err != nil {
		t.Fatal(err)
	}
	if err := q.ReleaseRemoteActorMove(ctx, origin); err != nil || claim(origin) != 0 {
		t.Fatalf("a moving claim was released (%v)", err)
	}
	if _, err := st.Pool.Exec(ctx, `UPDATE remote_actor_moves SET claimed_at = now() - interval '25 hours' WHERE origin_actor_url = $1`, origin); err != nil {
		t.Fatal(err)
	}
	if claim(origin) != 1 {
		t.Fatal("a Move after the cooldown was not claimed")
	}
	for _, o := range []string{origin, other} {
		claim(o)
		if err := q.SetRemoteActorMoveState(ctx, sqlcgen.SetRemoteActorMoveStateParams{OriginActorUrl: o, State: "moving"}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := q.NextMovingRemoteActorMove(ctx)
	second, err2 := q.NextMovingRemoteActorMove(ctx)
	if err != nil || err2 != nil || first.OriginActorUrl == second.OriginActorUrl || first.TargetActorUrl != target {
		t.Errorf("queue = %+v then %+v (%v, %v), want both moves in turn", first, second, err, err2)
	}
	for _, o := range []string{origin, other} {
		if err := q.SetRemoteActorMoveState(ctx, sqlcgen.SetRemoteActorMoveStateParams{OriginActorUrl: o, State: "done"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := q.NextMovingRemoteActorMove(ctx); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("a done move is still queued (%v)", err)
	}

	for _, a := range []string{origin, other} {
		if err := q.UpsertRemoteActor(ctx, sqlcgen.UpsertRemoteActorParams{ActorUrl: a, ActorType: "Group", Domain: "old.test", PublicKeyPem: "k"}); err != nil {
			t.Fatal(err)
		}
	}
	follow := func(name, actor, state string, at time.Time) (uuid.UUID, uuid.UUID) {
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
		return user, id
	}
	now := time.Now()
	_, firstEdge := follow("first", origin, "accepted", now.Add(-2*time.Hour))
	_, secondEdge := follow("second", origin, "pending", now.Add(-time.Hour))
	follow("refused", origin, "rejected", now.Add(-3*time.Hour))
	follow("elsewhere", other, "accepted", now.Add(-4*time.Hour))
	wary, _ := follow("wary", origin, "accepted", now.Add(-5*time.Hour))

	// wary blocked the origin: the block carries to the target, so wary keeps
	// the old follow and is not on the list.
	if _, err := st.Pool.Exec(ctx, `INSERT INTO remote_actor_blocks (blocker_id, remote_actor_url) VALUES ($1, $2)`, wary, origin); err != nil {
		t.Fatal(err)
	}
	if n, err := q.CarryRemoteActorBlocks(ctx, sqlcgen.CarryRemoteActorBlocksParams{OriginActorUrl: origin, TargetActorUrl: target}); err != nil || n != 1 {
		t.Fatalf("carried %d blocks (%v), want 1", n, err)
	}
	edges := func(limit int32) []uuid.UUID {
		t.Helper()
		rows, err := q.ListRemoteChannelFollowsOfActor(ctx, sqlcgen.ListRemoteChannelFollowsOfActorParams{
			RemoteActorUrl: origin, TargetActorUrl: target, ResultLimit: limit})
		if err != nil {
			t.Fatal(err)
		}
		ids := make([]uuid.UUID, 0, len(rows))
		for _, r := range rows {
			ids = append(ids, r.ID)
		}
		return ids
	}
	if got := edges(10); len(got) != 2 || got[0] != firstEdge || got[1] != secondEdge {
		t.Errorf("edges = %v, want [%s %s] (live, oldest first, not the target's blocker)", got, firstEdge, secondEdge)
	}
	if got := edges(1); len(got) != 1 || got[0] != firstEdge {
		t.Errorf("bounded edges = %v, want only the oldest", got)
	}
}
