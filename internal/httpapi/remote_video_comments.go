package httpapi

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/authoredremotecomment"
	"github.com/vidra/vidra-core/internal/remotevideo"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Comment threads on a remote (federated) video have TWO halves, and the response
// keeps them as two arrays because they are two different KINDS of thing:
//
//   - `comments` — the MIRRORED origin thread (A29-F8, migration 0140). Read-only,
//     carries a REMOTE actor + origin object id, paginated (it can be large).
//   - `authored` — comments a LOCAL user authored HERE about this remote video
//     (migration 0147, the home-instance-hosts-and-federates ruling). Each carries
//     a LOCAL author id + a delivery_state (the federation status of the reply we
//     sent to the origin). The complete local set (subject to the viewer's mutes),
//     not paginated: it is this instance's own content and is small.
//
// The two sets are disjoint by construction: this instance never mirrors a comment
// it authored (the origin does not re-broadcast content it did not originate), so a
// client can concatenate them without deduping.

// maxAuthoredRemoteCommentsReturned bounds the un-paginated `authored` array.
const maxAuthoredRemoteCommentsReturned = 500

// remoteVideoCommentView is one MIRRORED comment (origin actor, origin object id).
type remoteVideoCommentView struct {
	ID string `json:"id"`
	// Local is always false for a mirrored row — the discriminator a client uses
	// to tell a mirrored comment from a locally-authored one in a merged view.
	Local bool `json:"local"`
	// AuthorName is the origin's preferredUsername SNAPSHOT, taken when the
	// comment arrived — the thread must still render when the actor row is gone.
	AuthorName string `json:"author_name"`
	// AuthorDomain lets the UI show "name@domain" without a second lookup, and
	// makes a same-thread impersonation across two instances visible.
	AuthorDomain string `json:"author_domain"`
	ActorURL     string `json:"actor_url"`
	// ObjectURL is the comment's ActivityPub id ON THE ORIGIN, so a reader can
	// follow the thread back to where it is actually hosted.
	ObjectURL string `json:"object_url"`
	// ParentObjectURL is the origin object id of the comment this one answers,
	// absent for a reply to the video itself.
	ParentObjectURL string     `json:"parent_object_url,omitempty"`
	Body            string     `json:"body"`
	Edited          bool       `json:"edited"`
	PublishedAt     *time.Time `json:"published_at,omitempty"`
	CreatedAt       time.Time  `json:"created_at"`
}

// authoredRemoteCommentView is one LOCALLY-AUTHORED comment on a remote video —
// hosted + moderated here, federated to the origin.
type authoredRemoteCommentView struct {
	ID            string `json:"id"`
	RemoteVideoID string `json:"remote_video_id"`
	// Local is always true — the discriminator against a mirrored row.
	Local bool `json:"local"`
	// AuthorID is the LOCAL author's account id (so a viewer can mute them).
	AuthorID          string `json:"author_id"`
	AuthorUsername    string `json:"author_username"`
	AuthorDisplayName string `json:"author_display_name"`
	Body              string `json:"body"`
	// ObjectURL is the LOCAL AP object id this instance minted for the comment.
	ObjectURL string `json:"object_url"`
	// InReplyTo is the origin video's object id the federated reply threads onto.
	InReplyTo string `json:"in_reply_to"`
	// DeliveryState is the federation status of the reply to the origin —
	// 'pending' | 'delivered' | 'failed'. The comment is shown locally regardless.
	DeliveryState string `json:"delivery_state"`
	// LastError is the last delivery error, present only when delivery_state=failed.
	LastError string    `json:"last_error,omitempty"`
	Edited    bool      `json:"edited"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type remoteVideoCommentListResponse struct {
	// Comments is the paginated MIRRORED origin thread (pageMeta describes it).
	Comments []remoteVideoCommentView `json:"comments"`
	// Authored is the complete set of locally-authored comments on this remote
	// video (not paginated). Empty when nobody here has commented, or when the
	// write side is not mounted.
	Authored []authoredRemoteCommentView `json:"authored"`
	pageMeta
}

// newAuthoredRemoteCommentView shapes a stored authored comment (single-row form,
// e.g. a create/edit response) as its public view.
func newAuthoredRemoteCommentView(c sqlcgen.AuthoredRemoteComment, username, displayName string) authoredRemoteCommentView {
	v := authoredRemoteCommentView{
		ID:                c.ID.String(),
		RemoteVideoID:     c.RemoteVideoID.String(),
		Local:             true,
		AuthorID:          c.UserID.String(),
		AuthorUsername:    username,
		AuthorDisplayName: displayName,
		Body:              c.Body,
		ObjectURL:         c.ObjectUrl,
		InReplyTo:         c.InReplyTo,
		DeliveryState:     c.DeliveryState,
		Edited:            c.Edited,
		CreatedAt:         c.CreatedAt,
		UpdatedAt:         c.UpdatedAt,
	}
	if c.DeliveryState == "failed" {
		v.LastError = c.LastError
	}
	return v
}

// handleListRemoteVideoComments returns a remote video's mirrored thread (paged)
// AND the locally-authored comments on it. Public (optionalAuth); an unknown or
// origin-blocked remote video is 404, the same answer its detail route gives.
func (s *Server) handleListRemoteVideoComments(c echo.Context) error {
	id, err := pathUUID(c, "id", "remote video not found")
	if err != nil {
		return err
	}
	// The VIDEO's visibility gate first. Reading the thread of a video the caller
	// may not see would leak both its existence and its content.
	if _, err := s.remotevideosvc.Get(c.Request().Context(), id); err != nil {
		if errors.Is(err, remotevideo.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
		}
		return err
	}
	viewerID, _, authed := principalFromContext(c)
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.fedsvc.ListRemoteVideoComments(
		c.Request().Context(), id, viewerID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]remoteVideoCommentView, 0, len(items))
	for _, it := range items {
		views = append(views, remoteVideoCommentView{
			ID:              it.ID.String(),
			Local:           false,
			AuthorName:      it.AuthorName,
			AuthorDomain:    it.Domain,
			ActorURL:        it.ActorURL,
			ObjectURL:       it.ObjectURL,
			ParentObjectURL: it.ParentObjectURL,
			Body:            it.Body,
			Edited:          it.Edited,
			PublishedAt:     it.PublishedAt,
			CreatedAt:       it.CreatedAt,
		})
	}
	authored := s.listAuthoredRemoteComments(c, id, viewerID, authed)
	return c.JSON(http.StatusOK, remoteVideoCommentListResponse{
		Comments: views, Authored: authored, pageMeta: page.meta(total),
	})
}

// listAuthoredRemoteComments loads the complete local-authored set for a remote
// video (best-effort; a nil write service or a read error yields an empty array —
// the mirrored thread is still served).
func (s *Server) listAuthoredRemoteComments(c echo.Context, remoteVideoID, viewerID uuid.UUID, authed bool) []authoredRemoteCommentView {
	if s.authoredrcsvc == nil {
		return []authoredRemoteCommentView{}
	}
	rows, _, err := s.authoredrcsvc.ListByRemoteVideo(
		c.Request().Context(), remoteVideoID, viewerID, authed, maxAuthoredRemoteCommentsReturned, 0)
	if err != nil {
		s.logger.WarnContext(c.Request().Context(), "list authored remote comments failed", "error", err, "remote_video_id", remoteVideoID)
		return []authoredRemoteCommentView{}
	}
	out := make([]authoredRemoteCommentView, 0, len(rows))
	for _, r := range rows {
		v := authoredRemoteCommentView{
			ID:                r.ID.String(),
			RemoteVideoID:     r.RemoteVideoID.String(),
			Local:             true,
			AuthorID:          r.UserID.String(),
			AuthorUsername:    r.AuthorUsername,
			AuthorDisplayName: r.AuthorDisplayName,
			Body:              r.Body,
			ObjectURL:         r.ObjectUrl,
			InReplyTo:         r.InReplyTo,
			DeliveryState:     r.DeliveryState,
			Edited:            r.Edited,
			CreatedAt:         r.CreatedAt,
			UpdatedAt:         r.UpdatedAt,
		}
		if r.DeliveryState == "failed" {
			v.LastError = r.LastError
		}
		out = append(out, v)
	}
	return out
}

// createRemoteVideoCommentRequest is the POST /remote-videos/{id}/comments body.
type createRemoteVideoCommentRequest struct {
	Body string `json:"body"`
}

func (r createRemoteVideoCommentRequest) Validate() []FieldError {
	switch body := strings.TrimSpace(r.Body); {
	case body == "":
		return []FieldError{{Field: "body", Message: "is required"}}
	case len(body) > maxCommentLen:
		return []FieldError{{Field: "body", Message: "must be at most 2000 characters"}}
	}
	return nil
}

// handleCreateRemoteVideoComment authors a comment on a remote video. Behind
// requireAuth. The comment is stored + displayed locally and federated to the
// origin (the ruling: the home instance hosts and moderates it).
func (s *Server) handleCreateRemoteVideoComment(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	// The instance comment toggle gates authoring here too; reading stays open.
	if !s.commentsEnabled() {
		return &FeatureDisabledError{Feature: "comments"}
	}
	id, err := pathUUID(c, "id", "remote video not found")
	if err != nil {
		return err
	}
	ctx := c.Request().Context()
	// The video visibility gate: an unknown or origin-blocked remote video is 404,
	// which also means a comment can never be authored against a blocked origin.
	rv, err := s.remotevideosvc.Get(ctx, id)
	if err != nil {
		if errors.Is(err, remotevideo.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
		}
		return err
	}
	var in createRemoteVideoCommentRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	// Resolve the author BEFORE the write (same JWT-vs-account ordering as local
	// comments): a still-unexpired token from a deactivated/deleted account is
	// refused instead of committing a comment and then failing the response.
	author, err := s.authsvc.UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	created, err := s.authoredrcsvc.Create(ctx, rv.ID, userID, strings.TrimSpace(in.Body), rv.ObjectURL)
	if err != nil {
		return err
	}
	// Watched-words flagging (best-effort): the home instance moderates these rows
	// exactly as it does local comments (the ruling).
	if s.watchwordsvc != nil {
		if _, werr := s.watchwordsvc.FlagAuthoredRemoteComment(ctx, created.ID, created.Body); werr != nil {
			s.logger.WarnContext(ctx, "watched-word flagging failed", "error", werr, "authored_remote_comment_id", created.ID)
		}
	}
	return c.JSON(http.StatusCreated, newAuthoredRemoteCommentView(created, author.Username, author.DisplayName))
}

// updateRemoteVideoCommentRequest is the PATCH /remote-video-comments/{id} body.
type updateRemoteVideoCommentRequest struct {
	Body string `json:"body"`
}

func (r updateRemoteVideoCommentRequest) Validate() []FieldError {
	switch body := strings.TrimSpace(r.Body); {
	case body == "":
		return []FieldError{{Field: "body", Message: "is required"}}
	case len(body) > maxCommentLen:
		return []FieldError{{Field: "body", Message: "must be at most 2000 characters"}}
	}
	return nil
}

// handleUpdateRemoteVideoComment edits the caller's OWN authored remote comment.
// Behind requireAuth. Only the author may edit; another user's is 403, unknown is
// 404. The edit re-federates as an Update{Note}.
func (s *Server) handleUpdateRemoteVideoComment(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "comment not found")
	if err != nil {
		return err
	}
	var in updateRemoteVideoCommentRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	ctx := c.Request().Context()
	author, err := s.authsvc.UserByID(ctx, userID)
	if err != nil {
		if errors.Is(err, auth.ErrAccountNotFound) {
			return echo.NewHTTPError(http.StatusUnauthorized, "account no longer available")
		}
		return err
	}
	updated, err := s.authoredrcsvc.Edit(ctx, id, userID, strings.TrimSpace(in.Body))
	if err != nil {
		switch {
		case errors.Is(err, authoredremotecomment.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		case errors.Is(err, authoredremotecomment.ErrForbidden):
			return echo.NewHTTPError(http.StatusForbidden, "not your comment")
		}
		return err
	}
	if s.watchwordsvc != nil {
		if _, werr := s.watchwordsvc.FlagAuthoredRemoteComment(ctx, updated.ID, updated.Body); werr != nil {
			s.logger.WarnContext(ctx, "watched-word flagging failed", "error", werr, "authored_remote_comment_id", updated.ID)
		}
	}
	return c.JSON(http.StatusOK, newAuthoredRemoteCommentView(updated, author.Username, author.DisplayName))
}

// handleDeleteRemoteVideoComment removes an authored remote comment. Behind
// requireAuth. The author may always delete their own; a moderator/admin may
// delete anyone's (the home instance owns moderation). The delete re-federates as
// a Delete of the local Note.
func (s *Server) handleDeleteRemoteVideoComment(c echo.Context) error {
	userID, role, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := pathUUID(c, "id", "comment not found")
	if err != nil {
		return err
	}
	if err := s.authoredrcsvc.Delete(c.Request().Context(), id, userID, isStaff(role)); err != nil {
		switch {
		case errors.Is(err, authoredremotecomment.ErrNotFound):
			return echo.NewHTTPError(http.StatusNotFound, "comment not found")
		case errors.Is(err, authoredremotecomment.ErrForbidden):
			return echo.NewHTTPError(http.StatusForbidden, "not your comment")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
