package federation

// The instance actor (migration 0166): PeerTube's `Application` actor. PeerTube
// federates instance-to-instance through it — an admin follows another
// instance, the two instance actors exchange Follow/Accept, and the followed
// instance Announces its public videos to the follower. Without one, a PeerTube
// admin cannot federate with vidra the way they federate with each other.
//
// It lives at /accounts/peertube because that is where PeerTube looks: its
// auto-follow-back matches `type = Application AND preferredUsername =
// peertube`, and following an instance by host WebFingers acct:peertube@host
// when NodeInfo does not name the actor. The name is held in the one handle
// namespace (0142) by the instance actor itself, so no account or channel can
// take it — except an account that already held it before 0166, which keeps
// it; on that instance the instance actor is simply not served.

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/httpsig"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// InstanceActorName is the instance actor's preferredUsername and path segment.
const InstanceActorName = "peertube"

// actorTypeApplication is the ActivityStreams type of an instance actor, here
// and on PeerTube.
const actorTypeApplication = "Application"

// InstanceRepository is the persistence the instance actor needs. It is kept
// apart from Repository so the many fakes of that surface are untouched;
// NewService picks it up from a repository that also satisfies it
// (*sqlcgen.Queries does). Without it the instance actor is never served.
type InstanceRepository interface {
	InstanceActorNameReserved(ctx context.Context, lower string) (bool, error)
	GetInstanceActorKey(ctx context.Context) (sqlcgen.GetInstanceActorKeyRow, error)
	InsertInstanceActorKeyIfAbsent(ctx context.Context, arg sqlcgen.InsertInstanceActorKeyIfAbsentParams) (int64, error)
	CountAcceptedInstanceFollowers(ctx context.Context) (int64, error)
	CountAcceptedInstanceFollowing(ctx context.Context) (int64, error)
	// Inbound instance follows (instance_followers.go).
	InsertInstanceFollowerIfAbsent(ctx context.Context, arg sqlcgen.InsertInstanceFollowerIfAbsentParams) (int64, error)
	GetInstanceFollowerByActor(ctx context.Context, remoteActorURL string) (sqlcgen.GetInstanceFollowerByActorRow, error)
	DeleteInstanceFollowerByActor(ctx context.Context, remoteActorURL string) (int64, error)
	// The instance actor's Announces and outbox (instance_announces.go).
	ListAcceptedInstanceFollowerInboxes(ctx context.Context) ([]string, error)
	ListInstanceOutboxVideoIDs(ctx context.Context, arg sqlcgen.ListInstanceOutboxVideoIDsParams) ([]uuid.UUID, error)
	CountInstanceOutboxVideos(ctx context.Context) (int64, error)
	// The admin's instance-follower decisions (instance_followers.go).
	ListInstanceFollowers(ctx context.Context, arg sqlcgen.ListInstanceFollowersParams) ([]sqlcgen.ListInstanceFollowersRow, error)
	CountInstanceFollowers(ctx context.Context, state *string) (int64, error)
	AcceptPendingInstanceFollowerByID(ctx context.Context, id uuid.UUID) (sqlcgen.AcceptPendingInstanceFollowerByIDRow, error)
	RejectInstanceFollowerByID(ctx context.Context, id uuid.UUID) (sqlcgen.RejectInstanceFollowerByIDRow, error)
	DeleteInstanceFollowerByID(ctx context.Context, id uuid.UUID) (sqlcgen.DeleteInstanceFollowerByIDRow, error)
	// Outbound instance follows (instance_following.go).
	UpsertInstanceFollowing(ctx context.Context, arg sqlcgen.UpsertInstanceFollowingParams) (sqlcgen.InstanceFollowing, error)
	ListInstanceFollowing(ctx context.Context, arg sqlcgen.ListInstanceFollowingParams) ([]sqlcgen.ListInstanceFollowingRow, error)
	CountInstanceFollowing(ctx context.Context, state *string) (int64, error)
	DeleteInstanceFollowingByID(ctx context.Context, id uuid.UUID) (sqlcgen.DeleteInstanceFollowingByIDRow, error)
	AcceptInstanceFollowingByActivity(ctx context.Context, arg sqlcgen.AcceptInstanceFollowingByActivityParams) (int64, error)
	RejectInstanceFollowingByActivity(ctx context.Context, arg sqlcgen.RejectInstanceFollowingByActivityParams) (int64, error)
	HasAcceptedInstanceFollowingForHost(ctx context.Context, lower string) (bool, error)
}

// The production repository provides it; a drift here is a compile error, not
// an instance actor that silently stops being served.
var _ InstanceRepository = (*sqlcgen.Queries)(nil)

// InstanceActorURL is the instance actor's id.
func (s *Service) InstanceActorURL() string {
	return s.baseURL + "/accounts/" + InstanceActorName
}

// InstanceActorAvailable reports whether this instance serves its instance
// actor, and its id when it does. A lookup error reads as unavailable: callers
// refuse to federate as an actor they cannot vouch for.
func (s *Service) InstanceActorAvailable(ctx context.Context) (string, bool) {
	if s.inst == nil || s.baseURL == "" {
		return "", false
	}
	if s.instAvailable.Load() {
		return s.InstanceActorURL(), true
	}
	ok, err := s.inst.InstanceActorNameReserved(ctx, InstanceActorName)
	if err != nil || !ok {
		return "", false
	}
	s.instAvailable.Store(true)
	return s.InstanceActorURL(), true
}

// InstanceActor returns the Application actor document, minting its keypair on
// first request. Not served on this instance → ErrNotFound.
func (s *Service) InstanceActor(ctx context.Context) (*Actor, error) {
	id, ok := s.InstanceActorAvailable(ctx)
	if !ok {
		return nil, ErrNotFound
	}
	pub, err := s.ensureInstanceKey(ctx)
	if err != nil {
		return nil, err
	}
	actor := buildActor(id, actorTypeApplication, InstanceActorName, s.domain(), "", pub)
	actor.URL = s.baseURL + "/about/instance" // the human page, not the AP id
	return actor, nil
}

// isInstanceActorName reports whether name addresses the instance actor and the
// instance actor is served here.
func (s *Service) isInstanceActorName(ctx context.Context, name string) bool {
	if !strings.EqualFold(name, InstanceActorName) {
		return false
	}
	_, ok := s.InstanceActorAvailable(ctx)
	return ok
}

// instanceCollection is AccountCollection for the instance actor: its accepted
// instance followers and followings.
func (s *Service) instanceCollection(ctx context.Context, kind string) (*OrderedCollection, error) {
	var total int64
	var err error
	switch kind {
	case "followers":
		total, err = s.inst.CountAcceptedInstanceFollowers(ctx)
	case "following":
		total, err = s.inst.CountAcceptedInstanceFollowing(ctx)
	case "outbox":
		total, err = s.inst.CountInstanceOutboxVideos(ctx)
	default:
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	col := orderedCollection(s.InstanceActorURL()+"/"+kind, total)
	if kind == "outbox" && total > 0 {
		col.First = col.ID + "?page=1"
	}
	return col, nil
}

// ensureInstanceKey is ensureAccountKey for the instance actor's singleton key.
func (s *Service) ensureInstanceKey(ctx context.Context) (string, error) {
	if row, err := s.inst.GetInstanceActorKey(ctx); err == nil {
		return row.PublicKeyPem, nil
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return "", err
	}
	pub, priv, err := generateActorKeypair()
	if err != nil {
		return "", err
	}
	stored, err := s.storePrivate(priv)
	if err != nil {
		return "", err
	}
	if _, err := s.inst.InsertInstanceActorKeyIfAbsent(ctx, sqlcgen.InsertInstanceActorKeyIfAbsentParams{
		PublicKeyPem:  pub,
		PrivateKeyPem: stored,
	}); err != nil {
		return "", err
	}
	row, err := s.inst.GetInstanceActorKey(ctx)
	if err != nil {
		return "", err
	}
	return row.PublicKeyPem, nil
}

// instanceActorSigner is channelActorSigner for the instance actor.
func (s *Service) instanceActorSigner(ctx context.Context) (httpsig.Signer, error) {
	id, ok := s.InstanceActorAvailable(ctx)
	if !ok {
		return httpsig.Signer{}, ErrNotFound
	}
	if _, err := s.ensureInstanceKey(ctx); err != nil {
		return httpsig.Signer{}, err
	}
	row, err := s.inst.GetInstanceActorKey(ctx)
	if err != nil {
		return httpsig.Signer{}, err
	}
	priv, err := s.unlockPrivateKey(row.PrivateKeyPem)
	if err != nil {
		return httpsig.Signer{}, err
	}
	return httpsig.Signer{KeyID: id + "#main-key", Priv: priv}, nil
}
