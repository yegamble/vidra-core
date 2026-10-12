package httpapi

// The outbound instance-follow half of instanceFedRepo (federation.InstanceRepository).

import (
	"context"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

func (r *instanceFedRepo) UpsertInstanceFollowing(_ context.Context, a sqlcgen.UpsertInstanceFollowingParams) (sqlcgen.InstanceFollowing, error) {
	if r.following == nil {
		r.following = map[uuid.UUID]*sqlcgen.InstanceFollowing{}
	}
	f := &sqlcgen.InstanceFollowing{ID: uuid.New(), RemoteActorUrl: a.RemoteActorUrl, Host: a.Host, State: "pending", FollowActivityUrl: a.FollowActivityUrl}
	r.following[f.ID] = f
	return *f, nil
}

func (r *instanceFedRepo) ListInstanceFollowing(context.Context, sqlcgen.ListInstanceFollowingParams) ([]sqlcgen.ListInstanceFollowingRow, error) {
	var out []sqlcgen.ListInstanceFollowingRow
	for _, f := range r.following {
		out = append(out, sqlcgen.ListInstanceFollowingRow{ID: f.ID, RemoteActorUrl: f.RemoteActorUrl, Host: f.Host, State: f.State, PreferredUsername: "peertube"})
	}
	return out, nil
}

func (r *instanceFedRepo) CountInstanceFollowing(context.Context, *string) (int64, error) {
	return int64(len(r.following)), nil
}

func (r *instanceFedRepo) DeleteInstanceFollowingByID(_ context.Context, id uuid.UUID) (sqlcgen.DeleteInstanceFollowingByIDRow, error) {
	f, ok := r.following[id]
	if !ok {
		return sqlcgen.DeleteInstanceFollowingByIDRow{}, pgx.ErrNoRows
	}
	delete(r.following, id)
	return sqlcgen.DeleteInstanceFollowingByIDRow{RemoteActorUrl: f.RemoteActorUrl, FollowActivityUrl: f.FollowActivityUrl, State: f.State}, nil
}

func (r *instanceFedRepo) AcceptInstanceFollowingByActivity(context.Context, sqlcgen.AcceptInstanceFollowingByActivityParams) (int64, error) {
	return 0, nil
}

func (r *instanceFedRepo) RejectInstanceFollowingByActivity(context.Context, sqlcgen.RejectInstanceFollowingByActivityParams) (int64, error) {
	return 0, nil
}

func (r *instanceFedRepo) HasAcceptedInstanceFollowingForHost(context.Context, string) (bool, error) {
	return false, nil
}

// UpsertRemoteActor keeps the inbox the base fake drops: following needs it.
func (r *instanceFedRepo) UpsertRemoteActor(_ context.Context, a sqlcgen.UpsertRemoteActorParams) error {
	r.remoteActors[a.ActorUrl] = sqlcgen.RemoteActor{ActorUrl: a.ActorUrl, ActorType: a.ActorType,
		PreferredUsername: a.PreferredUsername, Domain: a.Domain, InboxUrl: a.InboxUrl, PublicKeyPem: a.PublicKeyPem}
	return nil
}
