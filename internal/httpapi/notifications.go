package httpapi

import (
	"errors"
	"net/http"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"

	"github.com/vidra/vidra-core/internal/notification"
)

// notificationActorView identifies who triggered a notification.
type notificationActorView struct {
	Username    string `json:"username"`
	DisplayName string `json:"display_name"`
}

// notificationView is the public projection of a notification. Context fields are
// type-dependent and omitted when not applicable.
type notificationView struct {
	ID        string                 `json:"id"`
	Type      string                 `json:"type"`
	Read      bool                   `json:"read"`
	CreatedAt time.Time              `json:"created_at"`
	Actor     *notificationActorView `json:"actor,omitempty"`
	// Follow context.
	ChannelHandle      string `json:"channel_handle,omitempty"`
	ChannelDisplayName string `json:"channel_display_name,omitempty"`
	// Comment context.
	VideoID    string `json:"video_id,omitempty"`
	VideoTitle string `json:"video_title,omitempty"`
	CommentID  string `json:"comment_id,omitempty"`
	// Message context.
	ConversationID string `json:"conversation_id,omitempty"`
	// Report-resolution context. The moderator's identity is never exposed.
	ReportID         string `json:"report_id,omitempty"`
	ReportStatus     string `json:"report_status,omitempty"`
	ReportTargetType string `json:"report_target_type,omitempty"`
}

func newNotificationView(it notification.Item) notificationView {
	v := notificationView{
		ID:                 it.ID.String(),
		Type:               it.Type,
		Read:               it.Read,
		CreatedAt:          it.CreatedAt,
		ChannelHandle:      it.ChannelHandle,
		ChannelDisplayName: it.ChannelDisplayName,
		VideoID:            it.VideoID,
		VideoTitle:         it.VideoTitle,
		CommentID:          it.CommentID,
		ConversationID:     it.ConversationID,
		ReportID:           it.ReportID,
		ReportStatus:       it.ReportStatus,
		ReportTargetType:   it.ReportTargetType,
	}
	if it.ActorUsername != "" || it.ActorDisplayName != "" {
		v.Actor = &notificationActorView{Username: it.ActorUsername, DisplayName: it.ActorDisplayName}
	}
	return v
}

// notificationListResponse is the paginated notification list plus the caller's
// current unread count (for a badge). Total and UnreadCount answer DIFFERENT
// questions: an unread_count of 3 says nothing about how many read
// notifications sit behind the current page, which is what Total is for.
type notificationListResponse struct {
	Notifications []notificationView `json:"notifications"`
	UnreadCount   int64              `json:"unread_count"`
	pageMeta
}

type unreadCountResponse struct {
	UnreadCount int64 `json:"unread_count"`
}

// handleListNotifications returns the caller's notifications, newest first.
// Behind requireAuth. ?unread=true returns only unread ones. Pagination via
// ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListNotifications(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	unreadOnly := c.QueryParam("unread") == "true"
	page := parsePage(c, defaultVideoFeedLimit, maxVideoFeedLimit)
	ctx := c.Request().Context()
	items, total, err := s.notifsvc.List(ctx, userID, unreadOnly, page.Limit32(), page.Offset32())
	if err != nil {
		return err
	}
	unread, err := s.notifsvc.UnreadCount(ctx, userID)
	if err != nil {
		return err
	}
	views := make([]notificationView, 0, len(items))
	for _, it := range items {
		views = append(views, newNotificationView(it))
	}
	return c.JSON(http.StatusOK, notificationListResponse{
		Notifications: views, UnreadCount: unread, pageMeta: page.meta(total),
	})
}

// handleUnreadNotificationCount returns just the caller's unread count (cheap,
// for a header badge). Behind requireAuth.
func (s *Server) handleUnreadNotificationCount(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	n, err := s.notifsvc.UnreadCount(c.Request().Context(), userID)
	if err != nil {
		return err
	}
	return c.JSON(http.StatusOK, unreadCountResponse{UnreadCount: n})
}

// handleMarkNotificationRead marks one of the caller's notifications read
// (idempotent). Behind requireAuth. An unknown id, or one belonging to another
// user, is 404.
func (s *Server) handleMarkNotificationRead(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		return echo.NewHTTPError(http.StatusNotFound, "notification not found")
	}
	if err := s.notifsvc.MarkRead(c.Request().Context(), userID, id); err != nil {
		if errors.Is(err, notification.ErrNotFound) {
			return echo.NewHTTPError(http.StatusNotFound, "notification not found")
		}
		return err
	}
	return c.NoContent(http.StatusNoContent)
}

// handleMarkAllNotificationsRead marks all of the caller's notifications read
// (idempotent). Behind requireAuth.
func (s *Server) handleMarkAllNotificationsRead(c echo.Context) error {
	userID, _, err := mustPrincipal(c)
	if err != nil {
		return err
	}
	if err := s.notifsvc.MarkAllRead(c.Request().Context(), userID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
