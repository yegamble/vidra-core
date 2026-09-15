package federation

// Outbound federation for locally-authored comments ON REMOTE videos — the
// home-instance-hosts-and-federates model (migration 0147, the owner's ruling that
// reverses "comments live on the origin instance").
//
// UNLIKE outbox_comments.go, which fans a LOCAL video's comment out to that video
// CHANNEL's remote followers, this delivers ONE reply to ONE inbox: the ORIGIN of
// the remote video. The comment is minted as a Note attributed to and signed as the
// author's ACCOUNT actor, inReplyTo the remote video's origin object id, and
// delivered to the origin actor's inbox through the SAME durable, signed,
// backoff/dead-letter queue everything else uses (deliver.go) — never a second
// delivery path.
//
// The comment is DISPLAYED LOCALLY the instant it is stored (the home instance
// hosts it); delivery_state reports only whether the federation leg landed. The
// authoredremotecomment service invokes these through its create/update/delete
// hooks (wired in cmd/api), so there is no authoredremotecomment→federation
// package coupling.

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/observability"
	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// AnnounceAuthoredRemoteComment delivers a newly-authored comment to the origin as
// a Create{Note}. Best-effort: an origin that cannot be resolved (gone, or its
// instance admin-blocked) leaves the comment displayed locally with delivery_state
// 'failed', which is exactly what the author needs to see.
func (s *Service) AnnounceAuthoredRemoteComment(ctx context.Context, commentID uuid.UUID) error {
	return s.federateAuthoredRemoteComment(ctx, "Create", commentID)
}

// UpdateAuthoredRemoteComment delivers an edit to the origin as an Update{Note}.
func (s *Service) UpdateAuthoredRemoteComment(ctx context.Context, commentID uuid.UUID) error {
	return s.federateAuthoredRemoteComment(ctx, "Update", commentID)
}

// federateAuthoredRemoteComment loads the authored comment + its remote video and
// enqueues a Create/Update{Note} to the origin inbox.
func (s *Service) federateAuthoredRemoteComment(ctx context.Context, activityType string, commentID uuid.UUID) error {
	c, err := s.repo.GetAuthoredRemoteComment(ctx, commentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	originActor, inbox, ok, err := s.resolveAuthoredCommentOrigin(ctx, c.RemoteVideoID)
	if err != nil {
		return err
	}
	if !ok {
		// The origin is unreachable (video retracted, or its instance blocked):
		// the comment stays hosted here, but there is nowhere to federate it to.
		return s.markAuthoredRemoteCommentUndeliverable(ctx, c.ID, c.Attempts)
	}
	u, err := s.repo.GetUserActorByID(ctx, c.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	payload, err := s.buildAuthoredNoteActivity(activityType, u.Username, c, originActor)
	if err != nil {
		return err
	}
	if _, err := s.ensureAccountKey(ctx, c.UserID); err != nil {
		return err
	}
	return s.enqueueAuthoredRemoteCommentDelivery(ctx, c.UserID, u.Username, inbox, payload, c.ID)
}

// DeleteAuthoredRemoteComment delivers a deletion to the origin as a Delete of the
// local Note URL. The row is already gone, so the caller passes the ids the hook
// captured. No delivery_state to update (the comment no longer exists here).
func (s *Service) DeleteAuthoredRemoteComment(ctx context.Context, commentID, remoteVideoID, userID uuid.UUID, objectURL string) error {
	originActor, inbox, ok, err := s.resolveAuthoredCommentOrigin(ctx, remoteVideoID)
	if err != nil || !ok {
		return err // origin gone/blocked → nothing to retract there
	}
	u, err := s.repo.GetUserActorByID(ctx, userID)
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
		"cc":       []string{originActor},
		"object":   objectURL,
	})
	if err != nil {
		return err
	}
	if _, err := s.ensureAccountKey(ctx, userID); err != nil {
		return err
	}
	// The row is gone: the Delete carries no authored_remote_comment_id (its FK
	// would be NULLed anyway), it just needs to reach the origin.
	return s.enqueueAccountDelivery(ctx, userID, u.Username, inbox, payload)
}

// resolveAuthoredCommentOrigin returns the origin author actor URL and the inbox to
// deliver to for a remote video. ok is false (nil error) when the video is gone or
// its origin instance is admin-blocked (GetRemoteVideoByID excludes blocked
// origins), or when the cached origin actor has no inbox.
func (s *Service) resolveAuthoredCommentOrigin(ctx context.Context, remoteVideoID uuid.UUID) (originActor, inbox string, ok bool, err error) {
	rv, err := s.repo.GetRemoteVideoByID(ctx, remoteVideoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	// The identity the reply is addressed to: the account that owns the channel
	// when the origin named one (attributed_to), else the channel actor itself.
	originActor = rv.RemoteActorUrl
	if strings.TrimSpace(rv.AttributedTo) != "" {
		originActor = rv.AttributedTo
	}
	ra, err := s.repo.GetRemoteActor(ctx, rv.RemoteActorUrl)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", false, nil
		}
		return "", "", false, err
	}
	inbox = ra.InboxUrl
	if inbox == "" && ra.SharedInboxUrl != nil {
		inbox = *ra.SharedInboxUrl
	}
	if inbox == "" {
		return "", "", false, nil
	}
	return originActor, inbox, true, nil
}

// buildAuthoredNoteActivity renders a Create or Update activity wrapping the
// authored comment as an AS Note, attributed to + signed as the author's account
// actor, inReplyTo the remote video's origin object id, addressed to the public and
// cc'd to the origin author (so the origin recognises the reply as directed at it).
func (s *Service) buildAuthoredNoteActivity(activityType, username string, c sqlcgen.AuthoredRemoteComment, originActor string) ([]byte, error) {
	accountActor := s.baseURL + "/accounts/" + username
	return json.Marshal(map[string]any{
		"@context": "https://www.w3.org/ns/activitystreams",
		"id":       accountActor + "/activities/" + strings.ToLower(activityType) + "/" + uuid.NewString(),
		"type":     activityType,
		"actor":    accountActor,
		"to":       []string{publicAudience},
		"cc":       []string{originActor},
		"object": map[string]any{
			"id":           c.ObjectUrl,
			"type":         "Note",
			"content":      c.Body,
			"inReplyTo":    c.InReplyTo,
			"attributedTo": accountActor,
			"published":    c.CreatedAt.UTC().Format(time.RFC3339),
			"to":           []string{publicAudience},
			"cc":           []string{originActor},
		},
	})
}

// enqueueAuthoredRemoteCommentDelivery queues an account-signed delivery of payload
// to the origin inbox, carrying the authored comment id so the drain reflects the
// result onto its delivery_state. The account key is minted by the caller.
func (s *Service) enqueueAuthoredRemoteCommentDelivery(ctx context.Context, userID uuid.UUID, username, inboxURL string, payload []byte, commentID uuid.UUID) error {
	ids := observability.CorrelationFromContext(ctx)
	return s.repo.EnqueueDelivery(ctx, sqlcgen.EnqueueDeliveryParams{
		InboxUrl:                inboxURL,
		Payload:                 payload,
		SigningUserID:           pgconv.UUID(userID),
		SigningUsername:         username,
		RequestID:               ids.RequestID,
		CorrelationID:           ids.CorrelationID,
		AuthoredRemoteCommentID: pgconv.UUID(commentID),
	})
}

// markAuthoredRemoteCommentUndeliverable stamps a comment 'failed' when its origin
// cannot be resolved at enqueue time — the comment is hosted here regardless.
func (s *Service) markAuthoredRemoteCommentUndeliverable(ctx context.Context, commentID uuid.UUID, attempts int32) error {
	return s.repo.SetAuthoredRemoteCommentDeliveryState(ctx, sqlcgen.SetAuthoredRemoteCommentDeliveryStateParams{
		ID:            commentID,
		DeliveryState: authoredCommentFailed,
		LastError:     "origin is unreachable (retracted or blocked); comment hosted locally only",
		Attempts:      attempts,
	})
}

// AuthoredRemoteCommentObject renders a locally-authored remote comment as an AP
// Note document — the dereferenceable object behind the id we mint, so a peer that
// fetches inReplyTo/id gets a real Note. ErrNotFound for an unknown id.
func (s *Service) AuthoredRemoteCommentObject(ctx context.Context, commentID uuid.UUID) (map[string]any, error) {
	c, err := s.repo.GetAuthoredRemoteComment(ctx, commentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	u, err := s.repo.GetUserActorByID(ctx, c.UserID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	accountActor := s.baseURL + "/accounts/" + u.Username
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           c.ObjectUrl,
		"type":         "Note",
		"content":      c.Body,
		"inReplyTo":    c.InReplyTo,
		"attributedTo": accountActor,
		"published":    c.CreatedAt.UTC().Format(time.RFC3339),
		"to":           []string{publicAudience},
	}, nil
}

// Delivery-state constants for authored remote comments, mirrored from the queue.
const (
	authoredCommentPending   = "pending"
	authoredCommentDelivered = "delivered"
	authoredCommentFailed    = "failed"
)
