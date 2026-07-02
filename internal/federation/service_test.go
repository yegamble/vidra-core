package federation

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo is an in-memory Repository for the federation tests. Count fields feed
// NodeInfo; the maps feed actor resolution + key minting. Maps mutate in place
// (map headers survive the value-receiver copy), so a single fakeRepo value both
// serves and stores minted keys.
type fakeRepo struct {
	users, videos, comments int64
	err                     error

	usersByName  map[string]sqlcgen.GetUserActorByUsernameRow
	channels     map[string]sqlcgen.Channel
	acctKeys     map[uuid.UUID]sqlcgen.GetAccountActorKeyRow
	chanKeys     map[uuid.UUID]sqlcgen.GetChannelActorKeyRow
	remoteActors map[string]sqlcgen.RemoteActor
}

func (f fakeRepo) CountUsers(context.Context) (int64, error)        { return f.users, f.err }
func (f fakeRepo) CountPublicVideos(context.Context) (int64, error) { return f.videos, f.err }
func (f fakeRepo) CountComments(context.Context) (int64, error)     { return f.comments, f.err }

func (f fakeRepo) GetUserActorByUsername(_ context.Context, name string) (sqlcgen.GetUserActorByUsernameRow, error) {
	if u, ok := f.usersByName[strings.ToLower(name)]; ok {
		return u, nil
	}
	return sqlcgen.GetUserActorByUsernameRow{}, pgx.ErrNoRows
}

func (f fakeRepo) GetChannelByHandle(_ context.Context, handle string) (sqlcgen.Channel, error) {
	if c, ok := f.channels[strings.ToLower(handle)]; ok {
		return c, nil
	}
	return sqlcgen.Channel{}, pgx.ErrNoRows
}

func (f fakeRepo) GetAccountActorKey(_ context.Context, id uuid.UUID) (sqlcgen.GetAccountActorKeyRow, error) {
	if k, ok := f.acctKeys[id]; ok {
		return k, nil
	}
	return sqlcgen.GetAccountActorKeyRow{}, pgx.ErrNoRows
}

func (f fakeRepo) InsertAccountActorKeyIfAbsent(_ context.Context, arg sqlcgen.InsertAccountActorKeyIfAbsentParams) (int64, error) {
	if _, ok := f.acctKeys[arg.UserID]; ok {
		return 0, nil
	}
	f.acctKeys[arg.UserID] = sqlcgen.GetAccountActorKeyRow{PublicKeyPem: arg.PublicKeyPem, PrivateKeyPem: arg.PrivateKeyPem}
	return 1, nil
}

func (f fakeRepo) GetChannelActorKey(_ context.Context, id uuid.UUID) (sqlcgen.GetChannelActorKeyRow, error) {
	if k, ok := f.chanKeys[id]; ok {
		return k, nil
	}
	return sqlcgen.GetChannelActorKeyRow{}, pgx.ErrNoRows
}

func (f fakeRepo) InsertChannelActorKeyIfAbsent(_ context.Context, arg sqlcgen.InsertChannelActorKeyIfAbsentParams) (int64, error) {
	if _, ok := f.chanKeys[arg.ChannelID]; ok {
		return 0, nil
	}
	f.chanKeys[arg.ChannelID] = sqlcgen.GetChannelActorKeyRow{PublicKeyPem: arg.PublicKeyPem, PrivateKeyPem: arg.PrivateKeyPem}
	return 1, nil
}

func (f fakeRepo) GetRemoteActor(_ context.Context, actorURL string) (sqlcgen.RemoteActor, error) {
	if r, ok := f.remoteActors[actorURL]; ok {
		return r, nil
	}
	return sqlcgen.RemoteActor{}, pgx.ErrNoRows
}

func (f fakeRepo) UpsertRemoteActor(_ context.Context, arg sqlcgen.UpsertRemoteActorParams) error {
	f.remoteActors[arg.ActorUrl] = sqlcgen.RemoteActor{
		ActorUrl:          arg.ActorUrl,
		ActorType:         arg.ActorType,
		PreferredUsername: arg.PreferredUsername,
		Domain:            arg.Domain,
		InboxUrl:          arg.InboxUrl,
		SharedInboxUrl:    arg.SharedInboxUrl,
		PublicKeyPem:      arg.PublicKeyPem,
		FollowersUrl:      arg.FollowersUrl,
	}
	return nil
}

func TestNodeInfoUsage(t *testing.T) {
	svc := NewService(fakeRepo{users: 7, videos: 3, comments: 11})
	got, err := svc.NodeInfoUsage(context.Background())
	if err != nil {
		t.Fatalf("NodeInfoUsage: %v", err)
	}
	want := NodeInfoUsage{Users: 7, LocalPosts: 3, LocalComments: 11}
	if got != want {
		t.Errorf("usage = %+v, want %+v", got, want)
	}
}

func TestNodeInfoUsageErrorPropagates(t *testing.T) {
	sentinel := errors.New("db down")
	_, err := NewService(fakeRepo{err: sentinel}).NodeInfoUsage(context.Background())
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want %v", err, sentinel)
	}
}
