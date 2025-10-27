package messaging

import (
	"athena/internal/httpapi/shared"
	"fmt"
	"net/http"

	"athena/internal/domain"
	"athena/internal/middleware"
	ucn "athena/internal/usecase/notification"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type NotificationHandlers struct {
	notificationService ucn.Service
}

func NewNotificationHandlers(notificationService ucn.Service) *NotificationHandlers {
	return &NotificationHandlers{
		notificationService: notificationService,
	}
}

// GetNotifications retrieves notifications for the authenticated user
func (h *NotificationHandlers) GetNotifications(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context (set by auth middleware)
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("ERROR", "Unauthorized"))
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid user ID: %w", err))
		return
	}

	// Parse pagination (preferred page/pageSize; fallback to limit/offset)
	filter := domain.NotificationFilter{UserID: userUUID}
	page, limit, offset, pageSize := shared.ParsePagination(r, 50)
	filter.Limit = limit
	filter.Offset = offset

	// Parse unread filter
	if unreadStr := r.URL.Query().Get("unread"); unreadStr != "" {
		unread := unreadStr == "true"
		filter.Unread = &unread
	}

	// Parse notification types
	// TODO: Implement filtering by notification types when needed
	// if typesStr := r.URL.Query().Get("types"); typesStr != "" {
	//     Parse comma-separated types
	//     e.g., "new_video,comment,mention"
	// }

	// Get notifications
	notifications, err := h.notificationService.GetUserNotifications(r.Context(), userUUID, filter)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to retrieve notifications: %w", err))
		return
	}

	// Return empty array instead of null if no notifications
	if notifications == nil {
		notifications = []domain.Notification{}
	}

	// Get total count for meta via stats
	stats, err := h.notificationService.GetStats(r.Context(), userUUID)
	if err != nil {
		// Fallback if stats fail
		stats = &domain.NotificationStats{TotalCount: len(notifications)}
	}
	meta := &shared.Meta{Total: int64(stats.TotalCount), Limit: filter.Limit, Offset: filter.Offset, Page: page, PageSize: pageSize}
	shared.WriteJSONWithMeta(w, http.StatusOK, notifications, meta)
}

// GetUnreadCount returns the count of unread notifications
func (h *NotificationHandlers) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("ERROR", "Unauthorized"))
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid user ID: %w", err))
		return
	}

	count, err := h.notificationService.GetUnreadCount(r.Context(), userUUID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("Failed to get unread count: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]int{"unread_count": count})
}

// GetNotificationStats returns notification statistics
func (h *NotificationHandlers) GetNotificationStats(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("ERROR", "Unauthorized"))
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid user ID: %w", err))
		return
	}

	stats, err := h.notificationService.GetStats(r.Context(), userUUID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("Failed to get notification stats: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, stats)
}

// MarkAsRead marks a notification as read
func (h *NotificationHandlers) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("ERROR", "Unauthorized"))
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid user ID: %w", err))
		return
	}

	// Get notification ID from URL
	notificationID := chi.URLParam(r, "id")
	notifUUID, err := uuid.Parse(notificationID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid notification ID: %w", err))
		return
	}

	err = h.notificationService.MarkAsRead(r.Context(), notifUUID, userUUID)
	if err != nil {
		if err == domain.ErrNotificationNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("Notification not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to mark notification as read: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// MarkAllAsRead marks all notifications as read for the user
func (h *NotificationHandlers) MarkAllAsRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("ERROR", "Unauthorized"))
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid user ID: %w", err))
		return
	}

	err = h.notificationService.MarkAllAsRead(r.Context(), userUUID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("Failed to mark all notifications as read: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// DeleteNotification deletes a notification
func (h *NotificationHandlers) DeleteNotification(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("ERROR", "Unauthorized"))
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid user ID: %w", err))
		return
	}

	// Get notification ID from URL
	notificationID := chi.URLParam(r, "id")
	notifUUID, err := uuid.Parse(notificationID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("Invalid notification ID: %w", err))
		return
	}

	err = h.notificationService.DeleteNotification(r.Context(), notifUUID, userUUID)
	if err != nil {
		if err == domain.ErrNotificationNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("Notification not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("Failed to delete notification: %w", err))
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
