package httpapi

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/audit"
	"github.com/vidra/vidra-core/internal/comment"
	"github.com/vidra/vidra-core/internal/observability"
)

// adminCommentView is the admin/moderator comments-overview projection. Remote
// (federated) comments are flagged with their origin domain (remote-content §6).
type adminCommentView struct {
	ID                string    `json:"id"`
	VideoID           string    `json:"video_id"`
	VideoTitle        string    `json:"video_title"`
	Body              string    `json:"body"`
	AuthorUsername    string    `json:"author_username"`
	AuthorDisplayName string    `json:"author_display_name"`
	Remote            bool      `json:"remote"`
	AuthorDomain      string    `json:"author_domain,omitempty"`
	CreatedAt         time.Time `json:"created_at"`
	// Deleted marks a §1 tombstone; the body renders as "[deleted]".
	Deleted bool `json:"deleted"`
	// AutomaticTags: "external-link" and the names of the instance watched-word
	// lists the body matched (0177). Never the matched words.
	AutomaticTags []string `json:"automatic_tags"`
}

// adminCommentListResponse is the paginated admin comments overview.
type adminCommentListResponse struct {
	Comments []adminCommentView `json:"comments"`
	pageMeta
}

// handleListAdminComments returns all comments newest first for moderators/admins,
// each with its author + video context. Behind requireRole(admin, moderator).
// Optional ?q filters by body; ?auto_tag keeps comments carrying one automatic tag; pagination via ?limit (1–100, default 20)/?offset.
func (s *Server) handleListAdminComments(c echo.Context) error {
	q := c.QueryParam("q")
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	items, total, err := s.commentsvc.ListForAdmin(c.Request().Context(), q, c.QueryParam("auto_tag"), page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	views := make([]adminCommentView, 0, len(items))
	for _, it := range items {
		v := adminCommentView{
			ID:                it.ID.String(),
			VideoID:           it.VideoID.String(),
			VideoTitle:        it.VideoTitle,
			Body:              it.Body,
			AuthorUsername:    it.AuthorUsername,
			AuthorDisplayName: it.AuthorDisplayName,
			Remote:            it.Remote,
			AuthorDomain:      it.AuthorDomain,
			CreatedAt:         it.CreatedAt,
			Deleted:           it.Deleted,
			AutomaticTags:     nonNilTags(it.AutomaticTags),
		}
		if it.Deleted {
			v.Body = tombstoneBody
		}
		views = append(views, v)
	}
	return c.JSON(http.StatusOK, adminCommentListResponse{Comments: views, pageMeta: page.meta(total)})
}

// bulkRemoveCommentsRequest is the POST /admin/comments/bulk-remove body.
type bulkRemoveCommentsRequest struct {
	// Account is a local username, or a remote actor URL (http:// or https://).
	Account string `json:"account"`
}

func (r bulkRemoveCommentsRequest) Validate() []FieldError {
	if strings.TrimSpace(r.Account) == "" {
		return []FieldError{{Field: "account", Message: "is required"}}
	}
	if len(r.Account) > 2048 {
		return []FieldError{{Field: "account", Message: "is too long"}}
	}
	return nil
}

// bulkRemoveCommentsResponse reports how many comments were removed.
type bulkRemoveCommentsResponse struct {
	Removed int `json:"removed"`
}

// handleBulkRemoveComments deletes every comment by one account, instance-wide
// (PeerTube's bulk/remove-comments-of). Behind requireRole(admin, moderator).
// One audit event records actor, target and count; comment bodies are never
// logged. A removal that matched nothing is still a 200 with removed=0.
func (s *Server) handleBulkRemoveComments(c echo.Context) error {
	actorID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	var in bulkRemoveCommentsRequest
	if err := bindAndValidate(c, &in); err != nil {
		return err
	}
	n, targetID, err := s.commentsvc.RemoveByAccount(c.Request().Context(), in.Account)
	if err != nil {
		if errors.Is(err, comment.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "account not found")
		}
		return err
	}
	// The resource id must match the audit id pattern (no "/"), so a remote
	// actor URL is recorded by fingerprint, a local account by its id.
	resourceID := queryFingerprint(strings.TrimSpace(in.Account))
	if targetID != uuid.Nil {
		resourceID = targetID.String()
	}
	s.auditEvent(c, audit.Event{
		Action: observability.ActionAdminCommentBulkRemove, Result: observability.ResultSuccess,
		ActorID: actorID.String(), ResourceType: "account", ResourceID: resourceID,
		Metadata: []audit.MetadataField{{Key: "count", Value: strconv.Itoa(n)}},
	})
	return c.JSON(http.StatusOK, bulkRemoveCommentsResponse{Removed: n})
}

// nonNilTags keeps the JSON a [] rather than null for an untagged comment.
func nonNilTags(t []string) []string {
	if t == nil {
		return []string{}
	}
	return t
}
