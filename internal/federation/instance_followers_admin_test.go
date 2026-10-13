package federation

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// The fake must provide the whole instance surface: NewService picks it up by
// type assertion, so a missing method would silently switch the instance actor
// off in every test instead of failing to compile.
var _ InstanceRepository = fakeRepo{}

// followerByID finds a fake instance follower by row id.
func (f fakeRepo) followerByID(id uuid.UUID) (string, *sqlcgen.GetInstanceFollowerByActorRow) {
	for actor, r := range f.inst.followers {
		if r.ID == id {
			return actor, r
		}
	}
	return "", nil
}

func (f fakeRepo) ListInstanceFollowers(_ context.Context, a sqlcgen.ListInstanceFollowersParams) ([]sqlcgen.ListInstanceFollowersRow, error) {
	var out []sqlcgen.ListInstanceFollowersRow
	for actor, r := range f.inst.followers {
		if a.State == nil || *a.State == r.State {
			ra := f.remoteActors[actor]
			out = append(out, sqlcgen.ListInstanceFollowersRow{ID: r.ID, RemoteActorUrl: actor, State: r.State,
				PreferredUsername: ra.PreferredUsername, Domain: ra.Domain, CreatedAt: time.Now()})
		}
	}
	return out, nil
}

func (f fakeRepo) CountInstanceFollowers(ctx context.Context, state *string) (int64, error) {
	rows, err := f.ListInstanceFollowers(ctx, sqlcgen.ListInstanceFollowersParams{State: state})
	return int64(len(rows)), err
}

// transition mirrors the decision queries' WHERE state IN (from...).
func (f fakeRepo) transition(id uuid.UUID, to string, from ...string) (string, *sqlcgen.GetInstanceFollowerByActorRow, error) {
	actor, r := f.followerByID(id)
	if r != nil {
		for _, s := range from {
			if r.State == s {
				r.State = to
				return actor, r, nil
			}
		}
	}
	return "", nil, pgx.ErrNoRows
}

func (f fakeRepo) AcceptPendingInstanceFollowerByID(_ context.Context, id uuid.UUID) (sqlcgen.AcceptPendingInstanceFollowerByIDRow, error) {
	actor, r, err := f.transition(id, "accepted", "pending")
	if err != nil {
		return sqlcgen.AcceptPendingInstanceFollowerByIDRow{}, err
	}
	return sqlcgen.AcceptPendingInstanceFollowerByIDRow{RemoteActorUrl: actor, FollowActivityUrl: r.FollowActivityUrl}, nil
}

func (f fakeRepo) RejectInstanceFollowerByID(_ context.Context, id uuid.UUID) (sqlcgen.RejectInstanceFollowerByIDRow, error) {
	actor, r, err := f.transition(id, "rejected", "pending", "accepted")
	if err != nil {
		return sqlcgen.RejectInstanceFollowerByIDRow{}, err
	}
	return sqlcgen.RejectInstanceFollowerByIDRow{RemoteActorUrl: actor, FollowActivityUrl: r.FollowActivityUrl}, nil
}

func (f fakeRepo) DeleteInstanceFollowerByID(_ context.Context, id uuid.UUID) (sqlcgen.DeleteInstanceFollowerByIDRow, error) {
	actor, r := f.followerByID(id)
	if r == nil {
		return sqlcgen.DeleteInstanceFollowerByIDRow{}, pgx.ErrNoRows
	}
	delete(f.inst.followers, actor)
	return sqlcgen.DeleteInstanceFollowerByIDRow{RemoteActorUrl: actor, FollowActivityUrl: r.FollowActivityUrl, State: r.State}, nil
}

func TestAdminDecisionsOnAnInstanceFollower(t *testing.T) {
	repo := newInstanceInboxRepo()
	id := uuid.New()
	repo.inst.followers[ptInstance] = &sqlcgen.GetInstanceFollowerByActorRow{ID: id, State: "pending", FollowActivityUrl: instanceFollowID}
	repo.inst.outbox = []uuid.UUID{uuid.New()}
	svc := NewService(repo, WithBaseURL("https://videos.example"))
	ctx := context.Background()

	if list, total, err := svc.ListInstanceFollowers(ctx, "pending", 20, 0); err != nil || total != 1 || list[0].ID != id {
		t.Fatalf("pending list = %+v %d %v", list, total, err)
	}
	if err := svc.AcceptInstanceFollower(ctx, id); err != nil {
		t.Fatal(err)
	}
	got := sent(t, repo)
	if len(got["Accept"]) != 1 || len(got["Announce"]) != 1 {
		t.Errorf("approval sent Accept %d, Announce %d; want the Accept and the backfill", len(got["Accept"]), len(got["Announce"]))
	}
	if err := svc.AcceptInstanceFollower(ctx, id); !errors.Is(err, ErrInstanceFollowNotFound) {
		t.Errorf("accepting twice: %v, want ErrInstanceFollowNotFound", err)
	}
	if err := svc.RejectInstanceFollower(ctx, id); err != nil || repo.inst.followers[ptInstance].State != "rejected" {
		t.Fatalf("reject: %v", err)
	}
	before := len(repo.deliveries)
	if err := svc.RemoveInstanceFollower(ctx, id); err != nil {
		t.Fatal(err)
	}
	if len(repo.deliveries) != before {
		t.Error("removing an already-rejected follower must not send a second Reject")
	}
	if err := svc.RemoveInstanceFollower(ctx, id); !errors.Is(err, ErrInstanceFollowNotFound) {
		t.Errorf("removing twice: %v", err)
	}
}
