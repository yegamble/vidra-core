package federation

// Dereferenceable object ids (A29-F2, A29-F9).
//
// An ActivityPub object id is a PROMISE: "GET this URL with an ActivityPub
// Accept and you will get this object back". A29 measured vidra breaking that
// promise for the two ids it mints most — the video object id and the comment
// (Note) id — because both live on paths the FRONTEND owns, so an
// `Accept: application/activity+json` got 200 and a page of HTML. Two
// consequences, both measured: handleAnnounce's fetch-the-object arm can never
// ingest from a vidra origin, and pasting a vidra video URL into another
// instance's search silently resolves to nothing.
//
// The core handlers live here; the routing decision that sends an AP-Accept
// request for /videos/* to the api rather than the frontend is the operator's
// (the shipped Caddy config, meta repo). Core answers a non-AP Accept with 406,
// exactly as the actor routes already do: it has no HTML to serve, and pretending
// otherwise would make the negotiation depend on which process answered.
//
// PUBLIC OBJECTS ONLY. A private or unpublished video, an unlisted one, and a
// comment on any of those are 404 as ActivityPub — the same answer a peer gets
// for a video that does not exist, because "this id exists but you may not see
// it" is itself a disclosure. Unlisted keeps its shipped semantics: reachable by
// its own URL to a viewer who holds it, never enumerable and never federated, so
// it has no AP representation at all.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

// ErrGone means the object once existed and was deleted — 410 + Tombstone.
var ErrGone = errors.New("federation: object has been deleted")

// VideoObject returns the AS Video document for a LOCAL video id, for a peer
// dereferencing the object id we minted.
//
// Three answers, and the difference between them is the whole point:
//   - the object, for a public + published video;
//   - ErrGone, for a video we recorded a federated deletion for (410 +
//     Tombstone, so a peer that dereferences the retraction it was just sent
//     learns that the retraction was real);
//   - ErrNotFound for everything else — never seen, private, unlisted, or
//     unpublished. A well-formed uuid that was never a video and a private
//     video answer identically, which is what stops the endpoint being an
//     enumeration oracle.
func (s *Service) VideoObject(ctx context.Context, videoID uuid.UUID) (map[string]any, error) {
	v, err := s.repo.GetVideoByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, s.tombstoneOrNotFound(ctx, videoID)
		}
		return nil, err
	}
	if v.Privacy != "public" || v.State != "published" {
		return nil, ErrNotFound
	}
	ch, err := s.repo.GetChannelByID(ctx, v.ChannelID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	// A channel that opted out of ActivityPub (migration 0096) emits no
	// outbound activity, so it has no AP representation to dereference either.
	if !ch.ActivitypubEnabled {
		return nil, ErrNotFound
	}
	obj := s.videoObjectFromDetail(s.baseURL+"/video-channels/"+ch.Handle, v)
	obj["@context"] = "https://www.w3.org/ns/activitystreams"
	return obj, nil
}

// tombstoneOrNotFound distinguishes "deleted" from "never existed".
func (s *Service) tombstoneOrNotFound(ctx context.Context, videoID uuid.UUID) error {
	if _, err := s.repo.GetFederatedVideoTombstone(ctx, videoID); err == nil {
		return ErrGone
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return ErrNotFound
}

// VideoTombstone renders the AS Tombstone served with a 410 for a deleted
// video's object id. It carries the id, the type it replaces and when it was
// deleted, and DELIBERATELY nothing else: a retraction that leaked the title or
// the channel of the thing retracted would defeat the deletion it announces.
func (s *Service) VideoTombstone(ctx context.Context, videoID uuid.UUID) (map[string]any, error) {
	row, err := s.repo.GetFederatedVideoTombstone(ctx, videoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return map[string]any{
		"@context":   "https://www.w3.org/ns/activitystreams",
		"id":         s.baseURL + "/videos/" + videoID.String(),
		"type":       "Tombstone",
		"formerType": "Video",
		"deleted":    row.DeletedAt.UTC().Format(time.RFC3339),
	}, nil
}

// NoteObject returns the AS Note document for a LOCAL comment id.
//
// Only LOCALLY AUTHORED comments have an id here: a federated comment's id
// belongs to its origin (comments.remote_object_url), and re-serving it under
// our own URL would mint a second identity for one object — the exact way a
// thread forks into two on a third server.
func (s *Service) NoteObject(ctx context.Context, commentID uuid.UUID) (map[string]any, error) {
	c, err := s.repo.GetComment(ctx, commentID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	if !c.UserID.Valid {
		return nil, ErrNotFound
	}
	visible, err := s.videoIsPubliclyVisible(ctx, c.VideoID)
	if err != nil {
		return nil, err
	}
	if !visible {
		return nil, ErrNotFound
	}
	u, err := s.repo.GetUserActorByID(ctx, uuid.UUID(c.UserID.Bytes))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	inReplyTo, err := s.commentReplyTarget(ctx, c)
	if err != nil {
		return nil, err
	}
	accountActor := s.baseURL + "/accounts/" + u.Username
	return map[string]any{
		"@context":     "https://www.w3.org/ns/activitystreams",
		"id":           s.localCommentURL(c.ID),
		"type":         "Note",
		"content":      c.Body,
		"inReplyTo":    inReplyTo,
		"attributedTo": accountActor,
		"published":    c.CreatedAt.UTC().Format(time.RFC3339),
		"to":           []string{publicAudience},
	}, nil
}

// RecordVideoTombstone remembers that a federated video's id existed, so its
// retraction is dereferenceable afterwards. Called from the same delete path
// that fans the Delete out, for PUBLIC videos only — a video that was never
// federated has no peer to answer.
func (s *Service) RecordVideoTombstone(ctx context.Context, videoID uuid.UUID) error {
	return s.repo.InsertFederatedVideoTombstone(ctx, videoID)
}
