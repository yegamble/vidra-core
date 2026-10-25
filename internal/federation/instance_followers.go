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
	"time"

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
	if !InboxesOnActorHost(ra) {
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

// ErrInstanceFollowNotFound means no instance follow row (follower or
// following) matches the id in a state the action applies to (404).
var ErrInstanceFollowNotFound = errors.New("federation: instance follow not found")

// InstanceFollow is the admin read model of one instance follow, in either
// direction.
type InstanceFollow struct {
	ID       uuid.UUID
	ActorURL string
	// Host is the remote instance; Handle its actor's preferred_username@host.
	Host      string
	Handle    string
	State     string // pending | accepted | rejected
	CreatedAt time.Time
}

// ListInstanceFollowers returns the instances following ours, newest first,
// optionally in one state only (""), with how many match.
func (s *Service) ListInstanceFollowers(ctx context.Context, state string, limit, offset int32) ([]InstanceFollow, int64, error) {
	if s.inst == nil {
		return nil, 0, nil
	}
	var filter *string
	if state != "" {
		filter = &state
	}
	rows, err := s.inst.ListInstanceFollowers(ctx, sqlcgen.ListInstanceFollowersParams{State: filter, ResultLimit: limit, ResultOffset: offset})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.inst.CountInstanceFollowers(ctx, filter)
	if err != nil {
		return nil, 0, err
	}
	out := make([]InstanceFollow, 0, len(rows))
	for _, r := range rows {
		out = append(out, InstanceFollow{ID: r.ID, ActorURL: r.RemoteActorUrl, Host: r.Domain,
			Handle: r.PreferredUsername + "@" + r.Domain, State: r.State, CreatedAt: r.CreatedAt})
	}
	return out, total, nil
}

// AcceptInstanceFollower approves a pending instance follower: it gets the
// Accept it has been waiting for and the same backfill an auto-accepted one
// gets.
func (s *Service) AcceptInstanceFollower(ctx context.Context, id uuid.UUID) error {
	if s.inst == nil {
		return ErrInstanceFollowNotFound
	}
	row, err := s.inst.AcceptPendingInstanceFollowerByID(ctx, id)
	if err != nil {
		return notFoundAs(err, ErrInstanceFollowNotFound)
	}
	if err := s.answerInstanceFollow(ctx, "Accept", row.RemoteActorUrl, row.FollowActivityUrl); err != nil {
		return err
	}
	s.backfillBestEffort(ctx, row.RemoteActorUrl)
	return nil
}

// RejectInstanceFollower refuses a pending or accepted instance follower. It
// stays rejected — its next Follow is refused too — until an admin removes it.
func (s *Service) RejectInstanceFollower(ctx context.Context, id uuid.UUID) error {
	if s.inst == nil {
		return ErrInstanceFollowNotFound
	}
	row, err := s.inst.RejectInstanceFollowerByID(ctx, id)
	if err != nil {
		return notFoundAs(err, ErrInstanceFollowNotFound)
	}
	return s.answerInstanceFollow(ctx, "Reject", row.RemoteActorUrl, row.FollowActivityUrl)
}

// RemoveInstanceFollower deletes an instance follow in any state, telling a
// pending or accepted follower with a Reject (PeerTube's removeFollower). A
// removed instance may follow again.
func (s *Service) RemoveInstanceFollower(ctx context.Context, id uuid.UUID) error {
	if s.inst == nil {
		return ErrInstanceFollowNotFound
	}
	row, err := s.inst.DeleteInstanceFollowerByID(ctx, id)
	if err != nil {
		return notFoundAs(err, ErrInstanceFollowNotFound)
	}
	if row.State == instanceFollowRejected {
		return nil // already told
	}
	return s.answerInstanceFollow(ctx, "Reject", row.RemoteActorUrl, row.FollowActivityUrl)
}

// notFoundAs maps pgx.ErrNoRows to the caller's not-found sentinel.
func notFoundAs(err, notFound error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return notFound
	}
	return err
}
