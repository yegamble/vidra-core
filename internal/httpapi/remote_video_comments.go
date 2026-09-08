package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/remotevideo"
)

// The mirrored comment thread on a remote video (A29-F8).
//
// READ-ONLY, and the absence of a POST is the design rather than an omission.
// These rows are a MIRROR of a thread the origin already fans out to us; nothing
// on this instance writes one. Authoring a comment against a video hosted
// elsewhere reverses a shipped product decision ("comments, ratings and saving
// live on the origin instance") and raises a moderation question mirroring does
// not — who answers for a comment an instance hosts about a video it does not
// own. See migration 0140.
//
// The route is PUBLIC, like the remote-video read it belongs to, and behind
// optionalAuth so a signed-in viewer's own instance mutes and per-remote-account
// blocks filter the thread. Anonymous callers see the admin filters only, which
// is exactly what they see on the card.

// remoteVideoCommentView is one mirrored comment.
type remoteVideoCommentView struct {
	ID string `json:"id"`
	// AuthorName is the origin's preferredUsername SNAPSHOT, taken when the
	// comment arrived — the thread must still render when the actor row is gone.
	AuthorName string `json:"author_name"`
	// AuthorDomain lets the UI show "name@domain" without a second lookup, and
	// makes a same-thread impersonation across two instances visible.
	AuthorDomain string `json:"author_domain"`
	ActorURL     string `json:"actor_url"`
	// ObjectURL is the comment's ActivityPub id ON THE ORIGIN, so a reader can
	// follow the thread back to where it is actually hosted.
	ObjectURL   string     `json:"object_url"`
	Body        string     `json:"body"`
	Edited      bool       `json:"edited"`
	PublishedAt *time.Time `json:"published_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

type remoteVideoCommentListResponse struct {
	Comments []remoteVideoCommentView `json:"comments"`
	pageMeta
}

// handleListRemoteVideoComments returns the mirrored thread for one remote
// video, oldest first. Public (optionalAuth). An unknown remote video — or one
// hidden because its origin is admin-blocked — is 404, the same answer its
// detail route gives, so the thread cannot be used to probe for a hidden video.
func (s *Server) handleListRemoteVideoComments(c echo.Context) error {
	id, err := pathUUID(c, "id", "remote video not found")
	if err != nil {
		return err
	}
	// The VIDEO's visibility gate first. Reading the thread of a video the
	// caller may not see would leak both its existence and its content.
	if _, err := s.remotevideosvc.Get(c.Request().Context(), id); err != nil {
		if errors.Is(err, remotevideo.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "remote video not found")
		}
		return err
	}
	viewerID, _, _ := principalFromContext(c)
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.fedsvc.ListRemoteVideoComments(
		c.Request().Context(), id, viewerID, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]remoteVideoCommentView, 0, len(items))
	for _, it := range items {
		views = append(views, remoteVideoCommentView{
			ID:           it.ID.String(),
			AuthorName:   it.AuthorName,
			AuthorDomain: it.Domain,
			ActorURL:     it.ActorURL,
			ObjectURL:    it.ObjectURL,
			Body:         it.Body,
			Edited:       it.Edited,
			PublishedAt:  it.PublishedAt,
			CreatedAt:    it.CreatedAt,
		})
	}
	return c.JSON(http.StatusOK, remoteVideoCommentListResponse{
		Comments: views, pageMeta: page.meta(total),
	})
}
