package federation

// The new-follower channel backfill. A remote actor that follows a local channel
// used to receive only FUTURE uploads, so the channel looked empty on the
// follower's side until its next video. After the Accept is queued, the
// follower is sent a Create{Video} for each of the channel's newest 20 public
// videos, selected newest-first and sent oldest-first, bounded and gated like the instance-follow backfill (core#362,
// backfillInstanceFollower — TWIN: dedupe the host/inbox checks and the claim
// once that PR lands).

import (
	"context"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// channelBackfillLimit bounds the Creates pushed to one new follower. Each makes
// the follower fetch and render one video, so this is the newest slice only.
const channelBackfillLimit = 20

// channelBackfillCooldown is how long one remote HOST waits for its next
// backfill OF THE SAME CHANNEL (persisted, migration 0169, so a restart does not
// reset it). A re-Follow inside the window is accepted and answered, and
// backfilled with nothing. A different channel of ours has its own window.
const channelBackfillCooldown = 24 * time.Hour

// channelBackfillHostChannelCap bounds how many of our channels one remote host
// may have backfilled in a day, so a host cannot multiply the per-channel bound
// by following everything we have.
const channelBackfillHostChannelCap = 10

// channelBackfillDailyCap bounds how many distinct hosts this instance backfills in any
// one day, whoever asks: the per-host cooldown does not stop one server that
// mints many host names. A follower over the cap is still accepted and gets
// every live activity.
const channelBackfillDailyCap = 50

// backfillChannelFollower pushes the channel's newest public videos, oldest
// first, to a follower whose Follow was just accepted. Best-effort: the Follow
// is already recorded and Accepted, so a failure here is logged by the caller
// and never fails the inbound activity. Everything is queued on the durable
// delivery queue — nothing is sent inline.
func (s *Service) backfillChannelFollower(ctx context.Context, channelID uuid.UUID, channelHandle, followerActorURL string) error {
	ch, err := s.repo.GetChannelByID(ctx, channelID)
	if err != nil {
		return err
	}
	if !ch.ActivitypubEnabled {
		return nil
	}
	ra, err := s.repo.GetRemoteActor(ctx, followerActorURL)
	if err != nil {
		return nil // no cached actor row: nowhere safe to deliver
	}
	host := hostOf(followerActorURL)
	// Only to the follower's own server, re-checked against the row as it is
	// now; and never to a blocked instance or an admin-blocked actor.
	if host == "" || !sameHost(ra.ActorUrl, followerActorURL) || !InboxesOnActorHost(ra) {
		return nil
	}
	if blocked, err := s.repo.IsInstanceBlocked(ctx, host); err != nil || blocked {
		return err
	}
	if blocked, err := s.repo.IsRemoteActorBlockedInstanceWide(ctx, followerActorURL); err != nil || blocked {
		return err
	}
	ids, err := s.repo.ListChannelBackfillVideoIDs(ctx, sqlcgen.ListChannelBackfillVideoIDsParams{
		ChannelID:   channelID,
		ResultLimit: channelBackfillLimit,
	})
	if err != nil || len(ids) == 0 {
		return err // nothing to push must not burn the host's cooldown
	}
	claimed, err := s.repo.ClaimChannelFollowerBackfill(ctx, sqlcgen.ClaimChannelFollowerBackfillParams{
		Host:            host, // per host: one host can mint any number of actors
		ChannelID:       channelID,
		HostChannelCap:  channelBackfillHostChannelCap,
		CooldownSeconds: int32(channelBackfillCooldown / time.Second),
		DailyCap:        channelBackfillDailyCap,
	})
	if err != nil || claimed == 0 {
		return err // backfilled inside the window, or over the daily cap
	}
	for i := len(ids) - 1; i >= 0; i-- { // oldest first, so the newest lands on top
		v, err := s.repo.GetVideoByID(ctx, ids[i])
		if err != nil {
			continue // vanished since listing
		}
		payload, err := s.buildVideoActivity(ctx, "Create", channelHandle, v)
		if err != nil {
			return err
		}
		if err := s.enqueueChannelDelivery(ctx, channelID, channelHandle, ra.InboxUrl, payload); err != nil {
			return err
		}
	}
	return nil
}

// backfillAfterAccept runs the backfill after an Accept was queued and logs, but
// does not propagate, a failure.
func (s *Service) backfillAfterAccept(ctx context.Context, channelID uuid.UUID, channelHandle, followerActorURL string) {
	if err := s.backfillChannelFollower(ctx, channelID, channelHandle, followerActorURL); err != nil && s.logger != nil {
		s.logger.Warn("federation: channel backfill for a new follower failed",
			"channel_id", channelID, "actor", followerActorURL, "error", err)
	}
}
