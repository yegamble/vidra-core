package federation

// Outbound federated comments (.ralph/specs/federation-remote-content.md §6):
// a local user's comment on a LOCAL video fans out to the video channel's
// remote followers as Create{Note} (edits as Update{Note}, deletions as
// Delete), attributed to — and signed as — the commenter's ACCOUNT actor via
// the durable delivery queue. The comment service invokes these through its
// create/update/delete hooks (wired in cmd/api), so there is no
// comment→federation package coupling.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// AnnounceComment fans a newly-posted local comment out as a Create{Note}.
// No-op for remote-authored comments and for comments on videos that are not
// public + published.
func (s *Service) AnnounceComment(ctx context.Context, commentID uuid.UUID) error {
	return s.federateComment(ctx, "Create", commentID)
}

// UpdateComment fans a local comment edit out as an Update{Note}. Same no-op
// rules as AnnounceComment.
func (s *Service) UpdateComment(ctx context.Context, commentID uuid.UUID) error {
	return s.federateComment(ctx, "Update", commentID)
}

// DeleteComment fans a local comment deletion out as a Delete of its Note URL.
// The comment row is already gone, so the caller passes the ids the hook
// captured before the delete. No-op when the video (and so its channel /
// follower set) is gone too.
func (s *Service) DeleteComment(ctx context.Context, commentID, videoID, authorID uuid.UUID) error {
	v, err := s.repo.GetVideoByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	ch, err := s.repo.GetChannelByID(ctx, v.ChannelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if !ch.ActivitypubEnabled {
		return nil // channel opted out of ActivityPub (migration 0096)
	}
	u, err := s.repo.GetUserActorByID(ctx, authorID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	accountActor := s.baseURL + "/accounts/" + u.Username
	payload, err := json.Marshal(map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       accountActor + "/activities/delete/" + uuid.NewString(),
		"type":     "Delete",
		"actor":    accountActor,
		"to":       []string{publicAudience},
		"object":   s.localCommentURL(commentID),
	})
	if err != nil {
		return err
	}
	return s.fanOutAsAccount(ctx, v.ChannelID, authorID, u.Username, payload)
}

// federateComment loads the comment + its video/channel and enqueues the
// Create/Update{Note} to every remote follower inbox of the channel.
func (s *Service) federateComment(ctx context.Context, activityType string, commentID uuid.UUID) error {
	c, err := s.repo.GetComment(ctx, commentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if !c.UserID.Valid {
		return nil // remote-authored comments never fan out (§6)
	}
	v, ch, ok, err := s.loadVideoAndChannel(ctx, c.VideoID)
	if err != nil || !ok {
		return err
	}
	if !ch.ActivitypubEnabled {
		return nil // channel opted out of ActivityPub (migration 0096)
	}
	if v.Privacy != "public" || v.State != "published" {
		return nil // only public, published videos carry federated interactions
	}
	authorID := uuid.UUID(c.UserID.Bytes)
	u, err := s.repo.GetUserActorByID(ctx, authorID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	inReplyTo, err := s.commentReplyTarget(ctx, c)
	if err != nil {
		return err
	}
	payload, err := s.buildNoteActivity(activityType, u.Username, c, inReplyTo)
	if err != nil {
		return err
	}
	return s.fanOutAsAccount(ctx, ch.ID, authorID, u.Username, payload)
}

// commentReplyTarget renders a comment's inReplyTo: the parent comment's AP
// object URL when it is a reply (a remote parent keeps its origin object URL;
// a local parent gets our comment URL), else the video's AP object URL.
func (s *Service) commentReplyTarget(ctx context.Context, c sqlcgen.Comment) (string, error) {
	if c.ParentID.Valid {
		p, err := s.repo.GetComment(ctx, uuid.UUID(c.ParentID.Bytes))
		switch {
		case err == nil:
			if p.RemoteObjectUrl != nil && *p.RemoteObjectUrl != "" {
				return *p.RemoteObjectUrl, nil
			}
			return s.localCommentURL(p.ID), nil
		case !errors.Is(err, pgx.ErrNoRows):
			return "", err
		}
		// Parent vanished under us → thread onto the video.
	}
	return s.baseURL + "/videos/" + c.VideoID.String(), nil
}

// buildNoteActivity renders a Create or Update activity wrapping the comment as
// an AS Note object, attributed to the commenter's account actor and addressed
// to the public.
func (s *Service) buildNoteActivity(activityType, username string, c sqlcgen.Comment, inReplyTo string) ([]byte, error) {
	accountActor := s.baseURL + "/accounts/" + username
	return json.Marshal(map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       accountActor + "/activities/" + strings.ToLower(activityType) + "/" + uuid.NewString(),
		"type":     activityType,
		"actor":    accountActor,
		"to":       []string{publicAudience},
		"object": map[string]any{
			"id":           s.localCommentURL(c.ID),
			"type":         "Note",
			"content":      c.Body,
			"inReplyTo":    inReplyTo,
			"attributedTo": accountActor,
			"to":           []string{publicAudience},
		},
	})
}

// fanOutAsAccount enqueues one account-signed delivery of payload per distinct
// remote follower inbox of the channel. The account keypair is minted up front
// so the queued deliveries never sign with a missing key.
func (s *Service) fanOutAsAccount(ctx context.Context, channelID, userID uuid.UUID, username string, payload []byte) error {
	inboxes, err := s.repo.ListRemoteFollowerInboxes(ctx, channelID)
	if err != nil {
		return err
	}
	if len(inboxes) == 0 {
		return nil
	}
	if _, err := s.ensureAccountKey(ctx, userID); err != nil {
		return err
	}
	for _, inbox := range inboxes {
		if inbox == "" {
			continue
		}
		if err := s.enqueueAccountDelivery(ctx, userID, username, inbox, payload); err != nil {
			return err
		}
	}
	return nil
}

// localCommentURL is the ActivityPub object URL we mint for a local comment.
func (s *Service) localCommentURL(commentID uuid.UUID) string {
	return s.baseURL + "/comments/" + commentID.String()
}
