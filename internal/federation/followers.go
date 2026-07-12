package federation

// Federation policy gates (config-parity W12): the channel-follower approval
// queue and the auto-follow-back machinery. Everything here governs the
// ActivityPub inbox only — vidra's ATProto integration is outbound
// cross-posting and has no inbound path.
//
// Vidra deviation, recorded in the parity ledger §11: PeerTube's follower
// approval applies to INSTANCE followers; vidra has no instance-level AP actor
// (only Person/Group account/channel actors), so the approval queue applies to
// CHANNEL followers — the only inbound follows vidra has. For the same reason
// the auto-follow-back is signed by the FOLLOWED CHANNEL's actor: it is the
// only local actor with a relationship to the follower.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// ErrFollowerRequestNotFound means no PENDING follower request has the given
// id (unknown, or already approved/rejected). The HTTP layer maps it to 404.
var ErrFollowerRequestNotFound = errors.New("federation: follower request not found")

// FollowerRequest is one pending inbound channel Follow awaiting an admin
// decision (federation_follower_approval).
type FollowerRequest struct {
	ID            uuid.UUID
	ChannelID     uuid.UUID
	ChannelHandle string
	ActorURL      string
	// Handle is the follower's cached identity (preferred_username@domain);
	// empty when the remote-actor cache row is gone.
	Handle    string
	Domain    string
	CreatedAt time.Time
}

// ListFollowerRequests returns the pending follower-approval queue, newest
// first. The caller clamps limit/offset.
func (s *Service) ListFollowerRequests(ctx context.Context, limit, offset int32) ([]FollowerRequest, error) {
	rows, err := s.repo.ListPendingRemoteFollows(ctx, sqlcgen.ListPendingRemoteFollowsParams{
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]FollowerRequest, 0, len(rows))
	for _, r := range rows {
		fr := FollowerRequest{
			ID:            r.ID,
			ChannelID:     r.ChannelID,
			ChannelHandle: r.ChannelHandle,
			ActorURL:      r.RemoteActorUrl,
			Domain:        r.Domain,
			CreatedAt:     r.CreatedAt,
		}
		if r.PreferredUsername != "" && r.Domain != "" {
			fr.Handle = r.PreferredUsername + "@" + r.Domain
		}
		out = append(out, fr)
	}
	return out, nil
}

// ApproveFollowerRequest flips one pending follower request to accepted and
// delivers the Accept the follower has been waiting for (plus the auto
// follow-back when that gate is on). Unknown/already-resolved id →
// ErrFollowerRequestNotFound.
func (s *Service) ApproveFollowerRequest(ctx context.Context, id uuid.UUID) error {
	row, err := s.repo.AcceptPendingRemoteFollowByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrFollowerRequestNotFound
		}
		return err
	}
	ch, err := s.repo.GetChannelByID(ctx, row.ChannelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil // channel deleted since — the follow row cascades away too
		}
		return err
	}
	if err := s.enqueueAcceptFollow(ctx, ch.ID, ch.Handle, row.RemoteActorUrl, row.FollowActivityUrl); err != nil {
		return err
	}
	return s.maybeAutoFollowBack(ctx, ch.ID, ch.Handle, row.RemoteActorUrl)
}

// RejectFollowerRequest removes one pending follower request and delivers a
// Reject to the follower. Unknown/already-resolved id →
// ErrFollowerRequestNotFound.
func (s *Service) RejectFollowerRequest(ctx context.Context, id uuid.UUID) error {
	row, err := s.repo.DeletePendingRemoteFollowByID(ctx, id)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return ErrFollowerRequestNotFound
		}
		return err
	}
	ch, err := s.repo.GetChannelByID(ctx, row.ChannelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	return s.enqueueRejectFollow(ctx, ch.ID, ch.Handle, row.RemoteActorUrl, row.FollowActivityUrl)
}

// enqueueRejectFollow queues a Reject of an inbound Follow back to the
// follower's inbox (the refusal counterpart of enqueueAcceptFollow — a
// durable, signed delivery). Like the Accept path it reads the remote-actor
// cache only; if the follower is unknown or has no inbox the Reject is simply
// not queued.
func (s *Service) enqueueRejectFollow(ctx context.Context, channelID uuid.UUID, channelHandle, followerActorURL, followActivityURL string) error {
	follower, err := s.repo.GetRemoteActor(ctx, followerActorURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if follower.InboxUrl == "" {
		return nil
	}
	payload, err := s.buildRejectFollow(channelHandle, followerActorURL, followActivityURL)
	if err != nil {
		return err
	}
	return s.enqueueChannelDelivery(ctx, channelID, channelHandle, follower.InboxUrl, payload)
}

// maybeAutoFollowBack enqueues the federation_auto_follow_back Follow after a
// channel Follow was accepted (auto-accepted, or approved from the queue).
// The follow-back is signed by the FOLLOWED CHANNEL's actor — vidra has no
// instance-level AP actor. Guards, in order: the gate itself; the domain
// blocklist (never follow back into a blocked instance); an existing outbound
// follow edge to the actor (any state — a local user already asked for their
// content); and an existing follow-back (any channel, any state — the
// insert-if-absent is the atomic reciprocal-loop guard, so two instances with
// this gate on exchange at most one follow-back each and then stop).
func (s *Service) maybeAutoFollowBack(ctx context.Context, channelID uuid.UUID, channelHandle, followerActorURL string) error {
	if !s.autoFollowBackEnabled() {
		return nil
	}
	if blocked, err := s.repo.IsInstanceBlocked(ctx, hostOf(followerActorURL)); err != nil {
		return err
	} else if blocked {
		return nil
	}
	if has, err := s.repo.HasRemoteChannelFollow(ctx, followerActorURL); err != nil {
		return err
	} else if has {
		return nil // a local user's follow edge (pending or accepted) already exists
	}
	channelActor := s.baseURL + "/video-channels/" + channelHandle
	followID := channelActor + "/activities/follow/" + uuid.NewString()
	n, err := s.repo.InsertChannelFollowBackIfAbsent(ctx, sqlcgen.InsertChannelFollowBackIfAbsentParams{
		ChannelID:         channelID,
		RemoteActorUrl:    followerActorURL,
		FollowActivityUrl: followID,
	})
	if err != nil {
		return err
	}
	if n == 0 {
		return nil // a follow-back already exists (pending or accepted) → skip
	}
	// The follower was fetched+cached during signature verification (or at the
	// original Follow, for the approval path); absent cache/inbox → the intent
	// row stays and the Follow is simply not queued.
	follower, err := s.repo.GetRemoteActor(ctx, followerActorURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	inbox := follower.InboxUrl
	if follower.SharedInboxUrl != nil && *follower.SharedInboxUrl != "" {
		inbox = *follower.SharedInboxUrl
	}
	if inbox == "" {
		return nil
	}
	payload, err := buildFollow(followID, channelActor, followerActorURL)
	if err != nil {
		return err
	}
	return s.enqueueChannelDelivery(ctx, channelID, channelHandle, inbox, payload)
}

// logDroppedRemoteComment records one comment activity dropped by the
// federation_accept_remote_comments gate (count-by-log; never bodies).
func (s *Service) logDroppedRemoteComment(activityType, activityID, signerActorURL string) {
	if s.logger == nil {
		return
	}
	s.logger.Info("federation: inbound remote comment dropped (federation_accept_remote_comments off)",
		"activity_type", activityType,
		"activity_id", activityID,
		"actor", signerActorURL,
	)
}
