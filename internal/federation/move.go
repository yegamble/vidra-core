package federation

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Inbound Move (T53): a remote actor local users follow announces it now lives
// at another actor (Mastodon's account migration). Without it they keep
// following an actor that will never post again. It asks this instance to
// follow somebody on its users' behalf, so it is untrusted and bounded:
//
//   - signer == actor == object (it moves only itself);
//   - the target is not local, not on a blocked instance, not admin-blocked,
//     and neither is the origin;
//   - an origin nobody here follows costs one indexed read: no claim, no fetch;
//   - one Move per origin per cooldown (0167), claimed BEFORE the fetch;
//   - the target is fetched fresh through the SSRF guard, must state the id it
//     was fetched as, and must list the origin in alsoKnownAs — the target
//     vouching, without which anyone could claim to be anyone's new home;
//   - at most maxMoveFollows edges per Move (the rest stay for a later one);
//   - a user who blocks the target keeps the old follow.

// maxMoveFollows bounds the edges one Move re-points (a Follow + Undo each).
const maxMoveFollows = 200

// errMoveUnverified means the target does not vouch for the move.
var errMoveUnverified = errors.New("federation: Move target does not name the origin in alsoKnownAs")

func (s *Service) handleMove(ctx context.Context, act inboxActivity, signerActorURL string) error {
	origin := act.Actor
	if origin != signerActorURL || objectID(act.Object) != origin {
		return ErrActorMismatch
	}
	target := objectID(act.Target)
	if hostOf(target) == "" || target == origin || strings.EqualFold(hostOf(target), s.domain()) {
		return nil
	}
	for _, check := range []func() (bool, error){
		func() (bool, error) { return s.repo.IsInstanceBlocked(ctx, hostOf(target)) },
		func() (bool, error) { return s.repo.IsRemoteActorBlockedInstanceWide(ctx, target) },
		func() (bool, error) { return s.repo.IsRemoteActorBlockedInstanceWide(ctx, origin) },
	} {
		if refused, err := check(); err != nil || refused {
			return err
		}
	}
	follows, err := s.repo.ListRemoteChannelFollowsOfActor(ctx, sqlcgen.ListRemoteChannelFollowsOfActorParams{
		RemoteActorUrl: origin, ResultLimit: maxMoveFollows,
	})
	if err != nil || len(follows) == 0 {
		return err
	}
	if n, err := s.repo.ClaimRemoteActorMove(ctx, sqlcgen.ClaimRemoteActorMoveParams{
		OriginActorUrl: origin, TargetActorUrl: target,
	}); err != nil || n == 0 {
		return err
	}
	ra, err := s.verifiedMoveTarget(ctx, origin, target)
	if err != nil {
		s.logMove("federation: Move refused", origin, target, "error", err)
		return nil
	}
	moved := 0
	for _, f := range follows {
		if s.moveFollow(ctx, f, ra) {
			moved++
		}
	}
	s.logMove("federation: Move applied", origin, target, "moved", moved, "edges", len(follows))
	return nil
}

// verifiedMoveTarget fetches the target fresh (a cached copy may predate the
// alsoKnownAs that proves the move) and caches it only once it vouches.
func (s *Service) verifiedMoveTarget(ctx context.Context, origin, target string) (sqlcgen.RemoteActor, error) {
	fa, err := s.fetchActor(ctx, target)
	if err != nil {
		return sqlcgen.RemoteActor{}, err
	}
	if fa.ID != target || !slices.Contains(attributedActors(fa.AlsoKnownAs), origin) {
		return sqlcgen.RemoteActor{}, errMoveUnverified
	}
	return s.cacheFetchedActor(ctx, target, fa)
}

// moveFollow re-points one edge: Follow the target as the user, then Undo the
// old follow. Best-effort: a failed edge stays on the old actor for a later Move.
func (s *Service) moveFollow(ctx context.Context, f sqlcgen.ListRemoteChannelFollowsOfActorRow, target sqlcgen.RemoteActor) bool {
	if blocked, err := s.remoteActorBlockedBy(ctx, f.UserID, target.ActorUrl); err != nil || blocked {
		return false
	}
	u, err := s.repo.GetUserActorByID(ctx, f.UserID)
	if err == nil {
		_, err = s.followResolvedActor(ctx, f.UserID, u.Username, target)
	}
	if err == nil {
		if err = s.UnfollowRemoteChannel(ctx, f.UserID, f.ID); errors.Is(err, ErrFollowNotFound) {
			err = nil // unfollowed meanwhile: the new follow stands
		}
	}
	if err != nil {
		s.logMove("federation: Move could not re-point a follow", "", target.ActorUrl, "follow_id", f.ID, "error", err)
		return false
	}
	return true
}

func (s *Service) logMove(msg, origin, target string, args ...any) {
	if s.logger != nil {
		s.logger.Info(msg, append([]any{"origin", origin, "target", target}, args...)...)
	}
}
