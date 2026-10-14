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

// maxNewRemoteRatingsPerDomainHour caps the NEW votes one remote domain can add
// across this instance per rolling hour. Past it, further new voters are
// accepted and ignored (a voter already on file can still change their vote):
// a domain minting accounts to stuff counts gets a bounded effect, and the
// remedy for a hostile domain is the instance block, which also stops its
// existing votes counting (CountVideoRatings).
const maxNewRemoteRatingsPerDomainHour = 100

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
	// 0 rows = the domain's hourly cap on new votes: accepted and ignored.
	_, err = s.repo.UpsertRemoteVideoRating(ctx, sqlcgen.UpsertRemoteVideoRatingParams{
		VideoID:        videoID,
		RemoteActorUrl: signerActorURL,
		Rating:         strings.ToLower(act.Type),
		ActivityUrl:    act.ID,
		Domain:         hostOf(signerActorURL),
		DomainCap:      maxNewRemoteRatingsPerDomainHour,
	})
	return err
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

// ErrInvalidRating means a vote is neither "like" nor "dislike".
var ErrInvalidRating = errors.New("federation: rating must be like or dislike")

// RemoteVideoRating returns userID's own vote on a remote video ("" = none).
func (s *Service) RemoteVideoRating(ctx context.Context, remoteVideoID, userID uuid.UUID) (string, error) {
	r, err := s.repo.GetRemoteVideoUserRating(ctx, sqlcgen.GetRemoteVideoUserRatingParams{UserID: userID, RemoteVideoID: remoteVideoID})
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	return r, err
}

// RateRemoteVideo records userID's Like/Dislike of a remote video and federates
// it to the origin, which owns the count (PeerTube's sendVideoRateChange): a new
// vote is a Like or Dislike, a switch is Undo of the old one plus the new one,
// and a repeat sends nothing. An unknown video — or one whose origin or self is
// admin-blocked, which GetRemoteVideoByID excludes — is ErrNotFound.
func (s *Service) RateRemoteVideo(ctx context.Context, remoteVideoID, userID uuid.UUID, rating string) error {
	if rating != "like" && rating != "dislike" {
		return ErrInvalidRating
	}
	prev, err := s.RemoteVideoRating(ctx, remoteVideoID, userID)
	if err != nil {
		return err
	}
	if _, err := s.repo.GetRemoteVideoByID(ctx, remoteVideoID); errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	} else if err != nil {
		return err
	}
	if prev == rating {
		return nil
	}
	// Queue FIRST, write second: a failed enqueue leaves the stored vote as the
	// origin last heard it, and the user's retry re-sends (an Undo of a vote the
	// origin no longer holds is a no-op there, so a partial switch converges).
	if err := s.federateRemoteVote(ctx, remoteVideoID, userID, prev, rating); err != nil {
		return err
	}
	return s.repo.UpsertRemoteVideoUserRating(ctx, sqlcgen.UpsertRemoteVideoUserRatingParams{
		RemoteVideoID: remoteVideoID, UserID: userID, Rating: rating,
	})
}

// ClearRemoteVideoRating removes userID's vote (idempotent) and federates its
// Undo to the origin.
func (s *Service) ClearRemoteVideoRating(ctx context.Context, remoteVideoID, userID uuid.UUID) error {
	prev, err := s.RemoteVideoRating(ctx, remoteVideoID, userID)
	if err != nil || prev == "" {
		return err
	}
	// The Undo is queued before the row goes, for the same reason as above.
	if err := s.federateRemoteVote(ctx, remoteVideoID, userID, prev, ""); err != nil {
		return err
	}
	_, err = s.repo.DeleteRemoteVideoUserRating(ctx, sqlcgen.DeleteRemoteVideoUserRatingParams{UserID: userID, RemoteVideoID: remoteVideoID})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil // a concurrent clear got there first
	}
	return err
}

// federateRemoteVote queues, signed as the user's account actor, an Undo of
// `undo` and/or a fresh `vote` to the remote video's origin inbox. The vote is
// kept locally whether or not the origin is reachable. Every activity gets a
// fresh id: a receiver's processed-id dedupe would otherwise drop a re-like
// after an Undo. The Undo embeds the vote's kind, actor and video, which is what
// PeerTube, Mastodon and this inbox match an Undo on.
func (s *Service) federateRemoteVote(ctx context.Context, remoteVideoID, userID uuid.UUID, undo, vote string) error {
	rv, err := s.repo.GetRemoteVideoByID(ctx, remoteVideoID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	_, inbox, ok, err := s.resolveAuthoredCommentOrigin(ctx, remoteVideoID)
	if err != nil || !ok {
		return err
	}
	u, err := s.repo.GetUserActorByID(ctx, userID)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil
	} else if err != nil {
		return err
	}
	if _, err := s.ensureAccountKey(ctx, userID); err != nil {
		return err
	}
	actor := s.baseURL + "/accounts/" + u.Username
	voteActivity := func(kind string) map[string]any {
		return map[string]any{
			"id":     actor + "/" + kind + "s/" + remoteVideoID.String() + "/" + uuid.NewString(),
			"type":   map[string]string{"like": "Like", "dislike": "Dislike"}[kind],
			"actor":  actor,
			"object": rv.ObjectUrl,
		}
	}
	var out []map[string]any
	if undo != "" {
		inner := voteActivity(undo)
		out = append(out, map[string]any{
			"id": actor + "/undo/" + uuid.NewString(), "type": "Undo", "actor": actor, "object": inner,
		})
	}
	if vote != "" {
		out = append(out, voteActivity(vote))
	}
	for _, a := range out {
		a["@context"] = "https://www.w3.org/ns/activitystreams"
		payload, err := json.Marshal(a)
		if err != nil {
			return err
		}
		if err := s.enqueueAccountDelivery(ctx, userID, u.Username, inbox, payload); err != nil {
			return err
		}
	}
	return nil
}
