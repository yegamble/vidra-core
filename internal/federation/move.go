package federation

import (
	"context"
	"errors"
	"slices"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Inbound Move (T53): a remote actor local users follow announces it now lives
// at another actor (Mastodon's account migration). It asks this instance to
// follow somebody on its users' behalf, so it is untrusted and bounded:
// signer == actor == object; target and origin not refused here; an origin
// nobody here follows or blocks costs no claim and no fetch; one Move per
// origin per cooldown (0167), claimed before the SSRF-guarded fetch and
// released if the target does not verify (its id, inboxes on its own host,
// alsoKnownAs naming the origin). The handler does O(1) work — it records the
// move and carries blocks; the federation worker re-points the follows.

// errMoveUnverified means the target does not vouch for the move.
var errMoveUnverified = errors.New("federation: Move target does not verify (id, inboxes or alsoKnownAs)")

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
	if relevant, err := s.moveIsRelevant(ctx, origin, target); err != nil || !relevant {
		return err
	}
	if n, err := s.repo.ClaimRemoteActorMove(ctx, sqlcgen.ClaimRemoteActorMoveParams{
		OriginActorUrl: origin, TargetActorUrl: target,
	}); err != nil || n == 0 {
		return err
	}
	if err := s.verifyMoveTarget(ctx, origin, target); err != nil {
		s.logMove("federation: Move refused", origin, target, "error", err)
		return s.repo.ReleaseRemoteActorMove(ctx, origin)
	}
	if _, err := s.repo.CarryRemoteActorBlocks(ctx, sqlcgen.CarryRemoteActorBlocksParams{
		OriginActorUrl: origin, TargetActorUrl: target,
	}); err != nil {
		return err
	}
	s.logMove("federation: Move accepted", origin, target)
	return s.repo.SetRemoteActorMoveState(ctx, sqlcgen.SetRemoteActorMoveStateParams{OriginActorUrl: origin, State: "moving"})
}

// moveIsRelevant: does anyone here follow the origin, or block it?
func (s *Service) moveIsRelevant(ctx context.Context, origin, target string) (bool, error) {
	follows, err := s.repo.ListRemoteChannelFollowsOfActor(ctx, sqlcgen.ListRemoteChannelFollowsOfActorParams{
		RemoteActorUrl: origin, TargetActorUrl: target, ResultLimit: 1,
	})
	if err != nil || len(follows) > 0 {
		return len(follows) > 0, err
	}
	return s.repo.IsRemoteActorBlockedByAnyone(ctx, origin)
}

// verifyMoveTarget fetches the target fresh (a cached copy may predate the
// alsoKnownAs that proves the move) and caches it only once it vouches. Its
// inboxes must be on its own host — the rule #361 adds as inboxesOnOwnHost;
// dedupe once that lands.
func (s *Service) verifyMoveTarget(ctx context.Context, origin, target string) error {
	fa, err := s.fetchActor(ctx, target)
	if err != nil {
		return err
	}
	ownHost := sameHost(fa.Inbox, target) && (fa.Endpoints.SharedInbox == "" || sameHost(fa.Endpoints.SharedInbox, target))
	if fa.ID != target || !ownHost || !slices.Contains(attributedActors(fa.AlsoKnownAs), origin) {
		return errMoveUnverified
	}
	_, err = s.cacheFetchedActor(ctx, target, fa)
	return err
}

// maxMoveFollows bounds the edges one worker batch re-points (a Follow + Undo each).
const maxMoveFollows = 200

// DrainMoves re-points one batch of the follows of the move whose turn it is,
// and reports how many were re-pointed (plus one when a move finished), so the
// worker's drain stops when a batch makes no progress. A follow that fails
// stays on the old actor and is retried on a later pass.
func (s *Service) DrainMoves(ctx context.Context) (int, error) {
	mv, err := s.repo.NextMovingRemoteActorMove(ctx)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, nil
	} else if err != nil {
		return 0, err
	}
	edges, err := s.repo.ListRemoteChannelFollowsOfActor(ctx, sqlcgen.ListRemoteChannelFollowsOfActorParams{
		RemoteActorUrl: mv.OriginActorUrl, TargetActorUrl: mv.TargetActorUrl, ResultLimit: maxMoveFollows,
	})
	if err != nil {
		return 0, err
	}
	target, err := s.repo.GetRemoteActor(ctx, mv.TargetActorUrl)
	if len(edges) == 0 || errors.Is(err, pgx.ErrNoRows) { // done, or the target was deleted since
		return 1, s.repo.SetRemoteActorMoveState(ctx, sqlcgen.SetRemoteActorMoveStateParams{OriginActorUrl: mv.OriginActorUrl, State: "done"})
	} else if err != nil {
		return 0, err
	}
	moved := 0
	for _, f := range edges {
		if s.moveFollow(ctx, f, target) {
			moved++
		}
	}
	return moved, nil
}

// moveFollow re-points one edge: Follow the target as the user, then Undo the
// old follow. Best-effort: a failed edge stays on the old actor for a later pass.
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
