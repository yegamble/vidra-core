//go:build integration

package store

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func TestInstanceFollowingLifecycleOnRealPG(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	st, err := New(ctx, dsn(t))
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer st.Close()
	q := st.Queries()
	actor := seedRemoteInstanceActor(t, st)
	u, _ := url.Parse(actor)
	host := u.Host

	upsert := func(follow string) sqlcgen.InstanceFollowing {
		t.Helper()
		row, err := q.UpsertInstanceFollowing(ctx, sqlcgen.UpsertInstanceFollowingParams{RemoteActorUrl: actor, Host: host, FollowActivityUrl: follow})
		if err != nil {
			t.Fatal(err)
		}
		return row
	}
	if row := upsert("f1"); row.State != "pending" || row.FollowActivityUrl != "f1" {
		t.Fatalf("first follow = %+v", row)
	}
	if row := upsert("f2"); row.FollowActivityUrl != "f1" {
		t.Errorf("a re-follow must keep the original Follow: %+v", row)
	}
	accept := func(follow, signer string) int64 {
		n, err := q.AcceptInstanceFollowingByActivity(ctx, sqlcgen.AcceptInstanceFollowingByActivityParams{FollowActivityUrl: follow, RemoteActorUrl: signer})
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	if accept("f1", "https://evil.example/accounts/peertube") != 0 {
		t.Error("an Accept signed by another actor matched our follow")
	}
	if following, _ := q.HasAcceptedInstanceFollowingForHost(ctx, strings.ToUpper(host)); following {
		t.Error("a pending follow must not open the ingestion gate")
	}
	if accept("f1", actor) != 1 {
		t.Fatal("the followed actor's Accept did not match")
	}
	if following, err := q.HasAcceptedInstanceFollowingForHost(ctx, strings.ToUpper(host)); err != nil || !following {
		t.Errorf("gate = %v, %v; want open, case-insensitively", following, err)
	}
	if n, err := q.RejectInstanceFollowingByActivity(ctx, sqlcgen.RejectInstanceFollowingByActivityParams{FollowActivityUrl: "f1", RemoteActorUrl: actor}); err != nil || n != 1 {
		t.Fatalf("reject = %d, %v", n, err)
	}
	row := upsert("f3")
	if row.State != "pending" || row.FollowActivityUrl != "f3" {
		t.Errorf("a re-follow after a Reject must re-arm: %+v", row)
	}
	state := "pending"
	if n, err := q.CountInstanceFollowing(ctx, &state); err != nil || n < 1 {
		t.Errorf("count = %d, %v", n, err)
	}
	if d, err := q.DeleteInstanceFollowingByID(ctx, row.ID); err != nil || d.FollowActivityUrl != "f3" {
		t.Errorf("unfollow = %+v, %v", d, err)
	}
	again := upsert("f4")
	if _, err := q.DeleteRemoteActor(ctx, actor); err != nil {
		t.Fatal(err)
	}
	if _, err := q.DeleteInstanceFollowingByID(ctx, again.ID); !errors.Is(err, pgx.ErrNoRows) {
		t.Errorf("the follow outlived its actor: %v", err)
	}
}
