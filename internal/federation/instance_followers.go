package federation

// Inbound instance follows (migration 0166): a remote INSTANCE actor — PeerTube's
// `Application` actor — Follows ours. The follow is accepted at once, or waits
// for an admin while federation_follower_approval is on: PeerTube's
// instance-follower approval, which is what that setting was modelled on. A
// follower the admin rejected stays rejected, as on PeerTube: its re-Follow is
// answered with another Reject rather than re-queued. Every answer is signed by
// the instance actor and sent to the follower's own inbox, which is where
// PeerTube's Accept processing requires it (process-accept.ts: "Need to accept
// on explicit inbox").

import (
	"context"
	"errors"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Instance follower states (the instance_followers CHECK).
const (
	instanceFollowAccepted = "accepted"
	instanceFollowPending  = "pending"
	instanceFollowRejected = "rejected"
)

// WithInstanceFollowerRequestHook registers a best-effort callback fired with the
// new instance_followers id when an instance follow lands in the approval queue
// — the staff push signal, like WithFollowerRequestHook for channel followers.
func WithInstanceFollowerRequestHook(fn func(context.Context, uuid.UUID)) Option {
	return func(s *Service) { s.onInstanceFollowerRequest = fn }
}

// actorTypeService is the other ActivityStreams type an instance actor may
// carry (PeerTube's validator accepts both as instance actors).
const actorTypeService = "Service"

// inboxesOnOwnHost reports whether a remote actor's inbox, and its shared
// inbox when it names one, live on the actor's own host.
func inboxesOnOwnHost(ra sqlcgen.RemoteActor) bool {
	if !sameHost(ra.InboxUrl, ra.ActorUrl) {
		return false
	}
	return ra.SharedInboxUrl == nil || *ra.SharedInboxUrl == "" || sameHost(*ra.SharedInboxUrl, ra.ActorUrl)
}

// isInstanceActorID reports whether id is our served instance actor's id.
func (s *Service) isInstanceActorID(ctx context.Context, id string) bool {
	own, ok := s.InstanceActorAvailable(ctx)
	return ok && id == own
}

// handleInstanceFollow records follower (the verified signer, already checked
// to be the Follow's actor) following our instance actor, and answers it.
func (s *Service) handleInstanceFollow(ctx context.Context, act inboxActivity, follower string) error {
	// An admin's block of this one remote actor: dropped unanswered, as a
	// channel owner's block is — a Reject would tell the blocked actor why.
	if blocked, err := s.repo.IsRemoteActorBlockedInstanceWide(ctx, follower); err != nil || blocked {
		return err
	}
	// Signature verification cached the follower. An instance is followed by
	// an instance actor, whose inboxes live on its own server: anything else
	// would have this instance deliver Announces somewhere the follower named.
	ra, err := s.repo.GetRemoteActor(ctx, follower)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if !inboxesOnOwnHost(ra) {
		return nil // dropped unanswered: an answer would go to another server
	}
	if ra.ActorType != actorTypeApplication && ra.ActorType != actorTypeService {
		return s.answerInstanceFollow(ctx, "Reject", follower, act.ID)
	}
	state := instanceFollowAccepted
	if s.followerApprovalRequired() {
		state = instanceFollowPending
	}
	inserted, err := s.inst.InsertInstanceFollowerIfAbsent(ctx, sqlcgen.InsertInstanceFollowerIfAbsentParams{
		RemoteActorUrl:    follower,
		State:             state,
		FollowActivityUrl: act.ID,
	})
	if err != nil {
		return err
	}
	row, err := s.inst.GetInstanceFollowerByActor(ctx, follower)
	if err != nil {
		return err
	}
	switch row.State {
	case instanceFollowRejected:
		return s.answerInstanceFollow(ctx, "Reject", follower, act.ID)
	case instanceFollowPending:
		if inserted == 1 && s.onInstanceFollowerRequest != nil {
			s.onInstanceFollowerRequest(ctx, row.ID)
		}
		return nil
	}
	// Accepted — new, or a re-Follow PeerTube sends when it never saw our
	// Accept. Either way it gets one, naming THIS Follow.
	if err := s.answerInstanceFollow(ctx, "Accept", follower, act.ID); err != nil {
		return err
	}
	if inserted == 1 {
		s.backfillBestEffort(ctx, follower)
	}
	return nil
}

// backfillBestEffort runs backfillInstanceFollower without letting it fail the
// follow it follows: the follow is recorded and accepted either way, and the
// follower's own outbox crawl (PeerTube) covers what the push missed.
func (s *Service) backfillBestEffort(ctx context.Context, follower string) {
	if err := s.backfillInstanceFollower(ctx, follower); err != nil && s.logger != nil {
		s.logger.Warn("federation: instance follower backfill failed", "actor", follower, "error", err)
	}
}

// undoInstanceFollow handles Undo{Follow} of our instance actor: the signer
// stops following. A rejected follower stays rejected (see the query).
func (s *Service) undoInstanceFollow(ctx context.Context, follower string) error {
	_, err := s.inst.DeleteInstanceFollowerByActor(ctx, follower)
	return err
}

// answerInstanceFollow queues the instance actor's Accept or Reject of an
// inbound Follow to the follower's own inbox. Like enqueueAcceptFollow it reads
// the actor cache only (signature verification just filled it); an unknown
// follower or one without an inbox gets no answer.
func (s *Service) answerInstanceFollow(ctx context.Context, typ, follower, followID string) error {
	ra, err := s.repo.GetRemoteActor(ctx, follower)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if ra.InboxUrl == "" || !sameHost(ra.InboxUrl, follower) {
		return nil
	}
	payload, err := followAnswer(typ, s.InstanceActorURL(), follower, followID)
	if err != nil {
		return err
	}
	return s.enqueueInstanceDelivery(ctx, ra.InboxUrl, payload)
}

// enqueueInstanceDelivery queues payload to inboxURL, signed as the instance
// actor at send time (the 0166 signer column).
func (s *Service) enqueueInstanceDelivery(ctx context.Context, inboxURL string, payload []byte) error {
	ids := observability.CorrelationFromContext(ctx)
	return s.repo.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{
		InboxUrl:             inboxURL,
		Payload:              payload,
		SigningInstanceActor: true,
		RequestID:            ids.RequestID,
		CorrelationID:        ids.CorrelationID,
	})
}
