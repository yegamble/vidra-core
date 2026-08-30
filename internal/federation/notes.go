package federation

// Inbound federated comments (.ralph/specs/federation-remote-content.md §6-7):
// a remote actor's Create{Note} whose inReplyTo resolves to a LOCAL video's AP
// object URL (or to a comment on one, for threading) is stored as a comment
// attributed to that remote actor; Update{Note} edits it and Delete removes it
// under the origin's authority. A Delete of a remote actor cascades away that
// actor's videos and comments.

import (
	"context"
	"encoding/json"
	"errors"
	"html"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/pgconv"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// maxRemoteCommentLen bounds a stored federated comment body (§6: truncate,
// never reject on length). The spec fixes 5000 chars for remote bodies; the
// local composer cap (2000) applies only to locally-written comments.
const maxRemoteCommentLen = 5000

// apNoteObject is the subset of an ActivityStreams Note object we ingest.
type apNoteObject struct {
	ID           string          `json:"id"`
	Type         string          `json:"type"`
	Content      string          `json:"content"`
	InReplyTo    json.RawMessage `json:"inReplyTo"`
	AttributedTo json.RawMessage `json:"attributedTo"`
}

// handleCreateNote ingests an inbound Create{Note} as a federated comment (§6).
// Authority: the activity actor must be the request signer, the Note must live
// on the signer's origin and be attributed to the signer. The Note's inReplyTo
// must resolve to a local, public, published video (directly, or through a
// comment on one for threading) — otherwise accept-and-ignore.
func (s *Service) handleCreateNote(ctx context.Context, act inboxActivity, signerActorURL string) error {
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	var note apNoteObject
	if err := json.Unmarshal(act.Object, &note); err != nil || note.Type != "Note" {
		return nil // not a Note object → accept-and-ignore
	}
	if note.ID == "" || !sameHost(note.ID, signerActorURL) {
		return nil
	}
	if !attributedToIncludes(note.AttributedTo, signerActorURL) {
		return nil
	}
	videoID, parentID, ok, err := s.resolveNoteTarget(ctx, objectID(note.InReplyTo))
	if err != nil || !ok {
		return err
	}
	body := truncate(stripHTMLTags(note.Content), maxRemoteCommentLen)
	if body == "" {
		return nil
	}
	// The author-name snapshot comes from the cached signer actor (it was
	// fetched+cached during signature verification).
	author, err := s.repo.GetRemoteActor(ctx, signerActorURL)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	name := author.PreferredUsername
	if name == "" {
		name = hostOf(signerActorURL)
	}
	row, err := s.repo.CreateRemoteComment(ctx, sqlcgen.CreateRemoteCommentParams{
		VideoID:          videoID,
		Body:             body,
		ParentID:         parentID,
		RemoteActorUrl:   &signerActorURL,
		RemoteAuthorName: &name,
		RemoteObjectUrl:  &note.ID,
	})
	if err != nil {
		return err
	}
	s.flagRemoteComment(ctx, row.ID, row.Body)
	return nil
}

// handleUpdateNote applies an inbound Update{Note} to a known federated
// comment (§6): only the original attributed actor may edit it. Unknown notes
// and foreign edits are accepted-and-ignored.
func (s *Service) handleUpdateNote(ctx context.Context, act inboxActivity, signerActorURL string) error {
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	var note apNoteObject
	if err := json.Unmarshal(act.Object, &note); err != nil || note.Type != "Note" || note.ID == "" {
		return nil
	}
	c, err := s.repo.GetCommentByRemoteObjectURL(ctx, note.ID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil
		}
		return err
	}
	if c.RemoteActorUrl == nil || *c.RemoteActorUrl != signerActorURL {
		return nil // not the original author → ignore
	}
	body := truncate(stripHTMLTags(note.Content), maxRemoteCommentLen)
	if body == "" {
		return nil
	}
	updated, err := s.repo.UpdateComment(ctx, sqlcgen.UpdateCommentParams{ID: c.ID, Body: body})
	if err != nil {
		return err
	}
	s.flagRemoteComment(ctx, updated.ID, updated.Body)
	return nil
}

// handleDelete applies an inbound Delete (§7). The object may be a known
// remote video, a known federated comment, or a cached remote actor (whose
// videos + comments then cascade away). Authority: the signer is the object's
// attributed actor, or the signer shares the object's origin host (a server
// retracting its own content). Anything else is accepted-and-ignored.
func (s *Service) handleDelete(ctx context.Context, act inboxActivity, signerActorURL string) error {
	if act.Actor != signerActorURL {
		return ErrActorMismatch
	}
	objURL := objectID(act.Object)
	if objURL == "" {
		return nil
	}
	// A known remote video?
	if rv, err := s.repo.GetRemoteVideoByObjectURL(ctx, objURL); err == nil {
		if rv.RemoteActorUrl != signerActorURL && !sameHost(objURL, signerActorURL) {
			return nil
		}
		_, err := s.repo.DeleteRemoteVideoByObjectURL(ctx, objURL)
		return err
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	// A known federated comment?
	if c, err := s.repo.GetCommentByRemoteObjectURL(ctx, objURL); err == nil {
		if c.RemoteActorUrl == nil || (*c.RemoteActorUrl != signerActorURL && !sameHost(objURL, signerActorURL)) {
			return nil
		}
		return s.repo.DeleteComment(ctx, c.ID)
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	// A cached remote actor? (An actor deleting itself, or its server.)
	if _, err := s.repo.GetRemoteActor(ctx, objURL); err == nil {
		if objURL != signerActorURL && !sameHost(objURL, signerActorURL) {
			return nil
		}
		_, err := s.repo.DeleteRemoteActor(ctx, objURL)
		return err
	} else if !errors.Is(err, pgx.ErrNoRows) {
		return err
	}
	return nil
}

// resolveNoteTarget resolves a Note's inReplyTo to the local video it comments
// on: directly (the video's AP object URL), via a local comment's URL, or via
// another federated comment's object URL (remote sibling threading). The video
// must be public + published to carry public interactions; ok is false when
// the target is not one of ours.
func (s *Service) resolveNoteTarget(ctx context.Context, inReplyTo string) (uuid.UUID, pgtype.UUID, bool, error) {
	if inReplyTo == "" {
		return uuid.UUID{}, pgtype.UUID{}, false, nil
	}
	if vid, ok := s.localVideoID(inReplyTo); ok {
		ok, err := s.videoIsPubliclyVisible(ctx, vid)
		return vid, pgtype.UUID{}, ok, err
	}
	var parent sqlcgen.Comment
	if cid, ok := s.localCommentID(inReplyTo); ok {
		p, err := s.repo.GetComment(ctx, cid)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.UUID{}, pgtype.UUID{}, false, nil
			}
			return uuid.UUID{}, pgtype.UUID{}, false, err
		}
		parent = p
	} else {
		p, err := s.repo.GetCommentByRemoteObjectURL(ctx, inReplyTo)
		if err != nil {
			if errors.Is(err, pgx.ErrNoRows) {
				return uuid.UUID{}, pgtype.UUID{}, false, nil
			}
			return uuid.UUID{}, pgtype.UUID{}, false, err
		}
		parent = p
	}
	ok, err := s.videoIsPubliclyVisible(ctx, parent.VideoID)
	return parent.VideoID, pgconv.UUID(parent.ID), ok, err
}

// videoIsPubliclyVisible reports whether a local video exists and is public +
// published (the only videos that carry public interactions).
func (s *Service) videoIsPubliclyVisible(ctx context.Context, videoID uuid.UUID) (bool, error) {
	v, err := s.repo.GetVideoByID(ctx, videoID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return v.Privacy == "public" && v.State == "published", nil
}

// flagRemoteComment runs the watched-words moderation flagging over a stored
// federated comment (§6: remote comments are subject to the same flagging).
// Best-effort: a flagging failure never fails ingestion.
func (s *Service) flagRemoteComment(ctx context.Context, commentID uuid.UUID, body string) {
	if s.flagger == nil {
		return
	}
	_, _ = s.flagger.FlagComment(ctx, commentID, body)
}

// localVideoID parses video object URLs we mint back to the video id.
//
// Two forms are accepted. The legacy prefix (/videos/watch/<uuid>) is tried
// first and kept forever so replies to videos federated under the old format
// still resolve — those AP ids live in remote servers' databases, out of our
// reach. New emissions use the canonical form (/videos/<uuid>): the same URL
// RSS, the sitemap and oEmbed already advertise and the only one the frontend
// actually routes (the legacy path was never routed — every such link 404s).
// Order matters only conceptually: under the shorter prefix the legacy form
// leaves "watch/<uuid>", which fails uuid.Parse anyway.
func (s *Service) localVideoID(objectURL string) (uuid.UUID, bool) {
	for _, prefix := range []string{s.baseURL + "/videos/watch/", s.baseURL + "/videos/"} {
		rest, ok := strings.CutPrefix(objectURL, prefix)
		if !ok {
			continue
		}
		if id, err := uuid.Parse(rest); err == nil {
			return id, true
		}
	}
	return uuid.UUID{}, false
}

// localCommentID parses the comment object URLs we mint
// (baseURL + /comments/<uuid>) back to the comment id.
func (s *Service) localCommentID(objectURL string) (uuid.UUID, bool) {
	rest, ok := strings.CutPrefix(objectURL, s.baseURL+"/comments/")
	if !ok {
		return uuid.UUID{}, false
	}
	id, err := uuid.Parse(rest)
	if err != nil {
		return uuid.UUID{}, false
	}
	return id, true
}

// stripHTMLTags reduces an AP Note's HTML content to plain text: tags are
// dropped and entities unescaped. Vidra stores comments as plain text and
// clients render them escaped; this keeps remote bodies readable without ever
// persisting markup.
func stripHTMLTags(in string) string {
	if !strings.ContainsRune(in, '<') {
		return strings.TrimSpace(html.UnescapeString(in))
	}
	var b strings.Builder
	b.Grow(len(in))
	inTag := false
	for _, r := range in {
		switch {
		case inTag:
			if r == '>' {
				inTag = false
			}
		case r == '<':
			inTag = true
		default:
			b.WriteRune(r)
		}
	}
	return strings.TrimSpace(html.UnescapeString(b.String()))
}
