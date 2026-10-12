package federation

// The instance actor's Announces (migration 0166): what an instance following
// ours actually receives. PeerTube's followed instance shares every public
// video it publishes with its followers as an Announce from its Application
// actor (lib/activitypub/share.ts, shareByServerIfNeeded); the follower fetches
// the video by the announced id. Every later Update/Delete of the video, and
// its comments, reach those followers too — PeerTube sends them to the
// followers of every actor that shared the video — so the instance followers
// are part of a video's audience from its Announce on (videoAudience).
//
// A new follower gets a bounded backfill of the newest videos pushed to it,
// and the outbox pages every one of them: PeerTube crawls the followed actor's
// outbox once its follow is accepted (process-accept.ts → addFetchOutboxJob).

import (
	"context"
	"encoding/json"
	"strconv"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// instanceBackfillLimit bounds the Announces pushed to a newly accepted
// instance follower. Each makes the follower fetch one video, so the push is
// the newest slice; the outbox serves the rest to a follower that crawls it.
const instanceBackfillLimit = 50

// buildInstanceAnnounce renders the instance actor's Announce of one local
// video, addressed as PeerTube addresses a share (to Public, cc its followers).
func (s *Service) buildInstanceAnnounce(id string, videoID uuid.UUID) map[string]any {
	actor := s.InstanceActorURL()
	return map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       id,
		"type":     "Announce",
		"actor":    actor,
		"to":       []string{publicAudience},
		"cc":       []string{actor + "/followers"},
		"object":   s.baseURL + "/videos/" + videoID.String(),
	}
}

// announceToInstanceFollowers sends the instance actor's Announce of a video
// that just became federable to every instance following ours. The caller has
// applied the federation predicate (AnnounceVideo). Each push gets a fresh id:
// a re-announce after a block is lifted must not be deduplicated away by a
// receiver that already processed the first one.
func (s *Service) announceToInstanceFollowers(ctx context.Context, videoID uuid.UUID) error {
	if _, ok := s.InstanceActorAvailable(ctx); !ok {
		return nil
	}
	inboxes, err := s.inst.ListAcceptedInstanceFollowerInboxes(ctx)
	if err != nil || len(inboxes) == 0 {
		return err
	}
	payload, err := json.Marshal(s.buildInstanceAnnounce(s.InstanceActorURL()+"/activities/announce/"+uuid.NewString(), videoID))
	if err != nil {
		return err
	}
	for _, inbox := range inboxes {
		if err := s.enqueueInstanceDelivery(ctx, inbox, payload); err != nil {
			return err
		}
	}
	return nil
}

// backfillInstanceFollower pushes the newest federable videos, oldest first, to
// an instance follower that was just accepted.
func (s *Service) backfillInstanceFollower(ctx context.Context, follower string) error {
	ra, err := s.repo.GetRemoteActor(ctx, follower)
	if err != nil {
		return err
	}
	inbox := ra.InboxUrl
	if ra.SharedInboxUrl != nil && *ra.SharedInboxUrl != "" {
		inbox = *ra.SharedInboxUrl
	}
	if inbox == "" {
		return nil
	}
	ids, err := s.inst.ListInstanceOutboxVideoIDs(ctx, sqlcgen.ListInstanceOutboxVideoIDsParams{ResultLimit: instanceBackfillLimit})
	if err != nil {
		return err
	}
	for i := len(ids) - 1; i >= 0; i-- {
		payload, err := json.Marshal(s.buildInstanceAnnounce(s.InstanceActorURL()+"/activities/announce/"+uuid.NewString(), ids[i]))
		if err != nil {
			return err
		}
		if err := s.enqueueInstanceDelivery(ctx, inbox, payload); err != nil {
			return err
		}
	}
	return nil
}

// videoAudience is every inbox a video's activities after its Create reach:
// the channel's remote followers and the instances following ours, distinct.
func (s *Service) videoAudience(ctx context.Context, channelID uuid.UUID) ([]string, error) {
	inboxes, err := s.repo.ListRemoteFollowerInboxes(ctx, channelID)
	if err != nil {
		return nil, err
	}
	if _, ok := s.InstanceActorAvailable(ctx); !ok {
		return inboxes, nil
	}
	more, err := s.inst.ListAcceptedInstanceFollowerInboxes(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[string]bool, len(inboxes))
	for _, in := range inboxes {
		seen[in] = true
	}
	for _, in := range more {
		if !seen[in] {
			seen[in] = true
			inboxes = append(inboxes, in)
		}
	}
	return inboxes, nil
}

// InstanceOutboxPage is ChannelOutboxPage for the instance actor: one page of
// Announces of the federable videos, newest first. Each item's id is stable
// per video, as a collection item must be. Not served → ErrNotFound.
func (s *Service) InstanceOutboxPage(ctx context.Context, page int) (*OrderedCollectionPage, error) {
	if _, ok := s.InstanceActorAvailable(ctx); !ok {
		return nil, ErrNotFound
	}
	if page < 1 {
		page = 1
	}
	ids, err := s.inst.ListInstanceOutboxVideoIDs(ctx, sqlcgen.ListInstanceOutboxVideoIDsParams{
		ResultLimit:  outboxPageSize,
		ResultOffset: int32((page - 1) * outboxPageSize),
	})
	if err != nil {
		return nil, err
	}
	outbox := s.InstanceActorURL() + "/outbox"
	items := make([]map[string]any, 0, len(ids))
	for _, id := range ids {
		item := s.buildInstanceAnnounce(s.InstanceActorURL()+"/activities/announce/"+id.String(), id)
		delete(item, "@context") // the page carries it
		items = append(items, item)
	}
	pg := &OrderedCollectionPage{
		Context:      "https://www.w3.org/ns/activitystreams",
		ID:           outbox + "?page=" + strconv.Itoa(page),
		Type:         "OrderedCollectionPage",
		PartOf:       outbox,
		OrderedItems: items,
	}
	if len(ids) == outboxPageSize {
		pg.Next = outbox + "?page=" + strconv.Itoa(page+1)
	}
	return pg, nil
}
