package federation

// Mirrored comment threads on remote videos (A29-F8).
//
// WHAT A29 MEASURED. A's creator replied to their own video; the reply fanned
// out as Create{Note} to all three follower inboxes; all three deliveries
// succeeded; B stored ZERO comments. resolveNoteTarget requires inReplyTo to
// resolve to a LOCAL video, so a follower receives every federated comment for
// the videos it follows and drops every one — while the sender's ledger records
// a successful delivery. A remote video carried no thread anywhere but its
// origin, and nothing on either side said so.
//
// WHAT THIS IS AND IS NOT. It is a MIRROR of a thread the origin already sends
// us, stored so the follower's watch page can show it. It is NOT an authoring
// path: nothing here writes a comment on this instance's behalf, and migration
// 0140 has no user_id column to hold one. Authoring against a remote video
// reverses a shipped product decision ("comments, ratings and saving live on the
// origin instance") and raises a moderation question — who answers for a comment
// an instance hosts about a video it does not own — that mirroring does not.
// Mirrored rows are the same class of content as the remote video row itself and
// sit under the same controls: instance mute, instance block, per-remote-account
// block, per-video block, reports.
//
// THE AUTHORITY RULE, which is the whole security surface here. Three checks,
// and the third is the one that is specific to this path:
//
//  1. the activity's actor is the request signer (every inbox arm);
//  2. the Note lives on the signer's origin and is attributed to the signer;
//  3. THE NOTE'S ORIGIN IS THE VIDEO'S ORIGIN. Without it, any instance we
//     follow could post comments onto any mirrored video from any other
//     instance — the thread would become writable by every peer in our follow
//     graph rather than by the server that hosts it.

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// storeRemoteVideoNote mirrors one inbound Note onto a remote video we hold.
// ok is false (nil error) when the Note is not for a remote video of ours, so
// the caller can fall through to the local-video path.
func (s *Service) storeRemoteVideoNote(ctx context.Context, note apNoteObject, signerActorURL, inReplyTo string) (bool, error) {
	rv, err := s.repo.GetRemoteVideoByURL(ctx, inReplyTo)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	// Check (3): the comment must come from the video's own origin.
	if !sameHost(note.ID, rv.ObjectUrl) {
		return true, nil // ours to answer for, and the answer is no
	}
	body := truncate(stripHTMLTags(note.Content), maxRemoteCommentLen)
	if body == "" {
		return true, nil
	}
	name := s.remoteAuthorName(ctx, signerActorURL)
	var published pgtype.Timestamptz
	if t, perr := time.Parse(time.RFC3339, note.Published); perr == nil {
		published = pgtype.Timestamptz{Time: t, Valid: true}
	}
	_, err = s.repo.UpsertRemoteVideoComment(ctx, sqlcgen.UpsertRemoteVideoCommentParams{
		RemoteVideoID:    rv.ID,
		RemoteActorUrl:   signerActorURL,
		RemoteAuthorName: name,
		ObjectUrl:        note.ID,
		Body:             body,
		PublishedAt:      published,
	})
	return true, err
}

// updateRemoteVideoNote applies an inbound Update{Note} to a mirrored comment.
// Only the original attributed actor may edit it.
func (s *Service) updateRemoteVideoNote(ctx context.Context, note apNoteObject, signerActorURL string) (bool, error) {
	row, err := s.repo.GetRemoteVideoCommentByObjectURL(ctx, note.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if row.RemoteActorUrl != signerActorURL {
		return true, nil // not the original author → accepted and ignored
	}
	body := truncate(stripHTMLTags(note.Content), maxRemoteCommentLen)
	if body == "" {
		return true, nil
	}
	_, err = s.repo.UpsertRemoteVideoComment(ctx, sqlcgen.UpsertRemoteVideoCommentParams{
		RemoteVideoID:    row.RemoteVideoID,
		RemoteActorUrl:   row.RemoteActorUrl,
		RemoteAuthorName: s.remoteAuthorName(ctx, signerActorURL),
		ObjectUrl:        note.ID,
		Body:             body,
	})
	return true, err
}

// deleteRemoteVideoNote applies an inbound Delete to a mirrored comment. The
// signer must be its author, or share its origin host (a server retracting its
// own content) — the same authority the remote-video and local-comment arms use.
func (s *Service) deleteRemoteVideoNote(ctx context.Context, objectURL, signerActorURL string) (bool, error) {
	row, err := s.repo.GetRemoteVideoCommentByObjectURL(ctx, objectURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if row.RemoteActorUrl != signerActorURL && !sameHost(objectURL, signerActorURL) {
		return true, nil
	}
	_, err = s.repo.DeleteRemoteVideoCommentByObjectURL(ctx, objectURL)
	return true, err
}

// remoteAuthorName is the display-name snapshot a mirrored row keeps, so the
// thread still renders when the actor row is later evicted. It falls back to the
// origin host rather than to an empty string: "peer.example said" is a worse
// answer than a username and a better one than a blank line.
func (s *Service) remoteAuthorName(ctx context.Context, actorURL string) string {
	if ra, err := s.repo.GetRemoteActor(ctx, actorURL); err == nil && ra.PreferredUsername != "" {
		return ra.PreferredUsername
	}
	return hostOf(actorURL)
}

// RemoteVideoComment is one mirrored comment, as the read surface sees it.
type RemoteVideoComment struct {
	ID          uuid.UUID
	ActorURL    string
	AuthorName  string
	Domain      string
	ObjectURL   string
	Body        string
	Edited      bool
	PublishedAt *time.Time
	CreatedAt   time.Time
}

// ListRemoteVideoComments returns one remote video's mirrored thread for a
// viewer, oldest first, with the total. viewerID is uuid.Nil for an anonymous
// caller.
func (s *Service) ListRemoteVideoComments(ctx context.Context, remoteVideoID, viewerID uuid.UUID, limit, offset int32) ([]RemoteVideoComment, int64, error) {
	var viewer pgtype.UUID
	if viewerID != uuid.Nil {
		viewer = pgtype.UUID{Bytes: viewerID, Valid: true}
	}
	rows, err := s.repo.ListRemoteVideoComments(ctx, sqlcgen.ListRemoteVideoCommentsParams{
		RemoteVideoID: remoteVideoID,
		ViewerID:      viewer,
		ResultLimit:   limit,
		ResultOffset:  offset,
	})
	if err != nil {
		return nil, 0, err
	}
	total, err := s.repo.CountRemoteVideoComments(ctx, sqlcgen.CountRemoteVideoCommentsParams{
		RemoteVideoID: remoteVideoID,
		ViewerID:      viewer,
	})
	if err != nil {
		return nil, 0, err
	}
	out := make([]RemoteVideoComment, 0, len(rows))
	for _, r := range rows {
		c := RemoteVideoComment{
			ID:         r.ID,
			ActorURL:   r.RemoteActorUrl,
			AuthorName: r.RemoteAuthorName,
			Domain:     r.Domain,
			ObjectURL:  r.ObjectUrl,
			Body:       r.Body,
			Edited:     r.Edited,
			CreatedAt:  r.CreatedAt,
		}
		if r.PublishedAt.Valid {
			t := r.PublishedAt.Time
			c.PublishedAt = &t
		}
		out = append(out, c)
	}
	return out, total, nil
}
