package federation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// maxRateIDLen bounds a Like/Dislike activity id (the column's CHECK, 0178).
const maxRateIDLen = 2048

// handleRate records a remote actor's Like or Dislike (PeerTube's two rating
// activities) of one of OUR videos, as one row per (actor, video) in
// remote_video_ratings: a repeat changes nothing, a switch flips the row. The
// totals are always counted from rows, never taken from the sender.
//
// It follows PeerTube's processLikeVideo/processDislike: only an account
// (Person) actor votes, only on a local video that federates. Every refusal
// below except the spoof and the admin block is accept-and-ignore, for the same
// reason as the comment path: a distinguishable answer would make a creator's
// block or a private video a probe target.
func (s *Service) handleRate(ctx context.Context, act inboxActivity, signerActorURL string) error {
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	// The activity id names the vote for a later Undo, so it must belong to
	// the signer's own host, like every other inbound id.
	if len(act.ID) > maxRateIDLen || !sameHost(act.ID, signerActorURL) {
		return ErrBadResource
	}
	if blocked, err := s.repo.IsRemoteActorBlockedInstanceWide(ctx, signerActorURL); err != nil {
		return err
	} else if blocked {
		return ErrSenderBlocked
	}
	videoID, ok, err := s.rateTarget(ctx, objectID(act.Object), signerActorURL)
	if err != nil || !ok {
		return err
	}
	return s.repo.UpsertRemoteVideoRating(ctx, sqlcgen.UpsertRemoteVideoRatingParams{
		VideoID:        videoID,
		RemoteActorUrl: signerActorURL,
		Rating:         strings.ToLower(act.Type),
		ActivityUrl:    act.ID,
	})
}

// rateTarget resolves a Like/Dislike object to a local video this actor may
// vote on: the video carries public interactions (videoIsPubliclyVisible, the
// predicate inbound comments use), its owner does not block the actor, and the
// actor is a cached Person — a channel or an instance actor has no vote.
func (s *Service) rateTarget(ctx context.Context, object, actorURL string) (uuid.UUID, bool, error) {
	videoID, ok := s.localVideoID(object)
	if !ok {
		return uuid.Nil, false, nil
	}
	if visible, err := s.videoIsPubliclyVisible(ctx, videoID); err != nil || !visible {
		return uuid.Nil, false, err
	}
	if blocked, err := s.videoOwnerBlocksRemoteActor(ctx, videoID, actorURL); err != nil || blocked {
		return uuid.Nil, false, err
	}
	actor, err := s.repo.GetRemoteActor(ctx, actorURL)
	if errors.Is(err, pgx.ErrNoRows) {
		return uuid.Nil, false, nil
	} else if err != nil {
		return uuid.Nil, false, err
	}
	return videoID, actor.ActorType == "Person", nil
}

// handleUndoRate applies Undo{Like|Dislike} (embedded) or an Undo naming a
// rating by bare activity id. A removal is always honoured — no visibility or
// block gate — but it only ever touches the SIGNER's own vote, and an
// Undo{Like} after a switch to Dislike leaves the newer vote alone.
func (s *Service) handleUndoRate(ctx context.Context, act inboxActivity, signerActorURL string) error {
	var inner struct {
		ID     string          `json:"id"`
		Type   string          `json:"type"`
		Actor  string          `json:"actor"`
		Object json.RawMessage `json:"object"`
	}
	if err := json.Unmarshal(act.Object, &inner); err != nil {
		if err := json.Unmarshal(act.Object, &inner.ID); err != nil || inner.ID == "" {
			return nil
		}
	} else if inner.Type != "Like" && inner.Type != "Dislike" {
		return nil
	}
	if act.Actor != signerActorURL || (inner.Actor != "" && inner.Actor != signerActorURL) {
		return ErrActorMismatch
	}
	videoID, _ := s.localVideoID(objectID(inner.Object))
	return s.repo.DeleteRemoteVideoRating(ctx, sqlcgen.DeleteRemoteVideoRatingParams{
		RemoteActorUrl: signerActorURL,
		VideoID:        videoID,
		Rating:         strings.ToLower(inner.Type),
		ActivityUrl:    inner.ID,
	})
}
