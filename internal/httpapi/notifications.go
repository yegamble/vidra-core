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
	}
	if it.ActorUsername != "" || it.ActorDisplayName != "" {
		v.Actor = &notificationActorView{Username: it.ActorUsername, DisplayName: it.ActorDisplayName}
	}
	return v
}

// notificationListResponse is the paginated notification list plus the caller's
// current unread count (for a badge).
type notificationListResponse struct {
	Notifications []notificationView `json:"notifications"`
	UnreadCount   int64              `json:"unread_count"`
	Limit         int                `json:"limit"`
	Offset        int                `json:"offset"`
}

type unreadCountResponse struct {
	UnreadCount int64 `json:"unread_count"`
}

// handleListNotifications returns the caller's notifications, newest first.
// Behind requireAuth. ?unread=true returns only unread ones. Pagination via
// ?limit (1–100, default 20) and ?offset.
func (s *Server) handleListNotifications(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	unreadOnly := c.QueryParam("unread") == "true"
	limit := clampInt(queryInt(c, "limit", defaultVideoFeedLimit), 1, maxVideoFeedLimit)
	offset := queryInt(c, "offset", 0)
	if offset < 0 {
		offset = 0
	}
	ctx := c.Request().Context()
	items, err := s.notifsvc.List(ctx, userID, unreadOnly, int32(limit), int32(offset))
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
		Notifications: views, UnreadCount: unread, Limit: limit, Offset: offset,
	})
}

// handleUnreadNotificationCount returns just the caller's unread count (cheap,
// for a header badge). Behind requireAuth.
func (s *Server) handleUnreadNotificationCount(c echo.Context) error {
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
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
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
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
	userID, _, ok := principalFromContext(c)
	if !ok {
		return echo.NewHTTPError(http.StatusUnauthorized, "not authenticated")
	}
	if err := s.notifsvc.MarkAllRead(c.Request().Context(), userID); err != nil {
		return err
	}
	return c.NoContent(http.StatusNoContent)
}
