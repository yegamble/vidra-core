package httpapi

import (
	"net/http"
	"strconv"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type NotificationHandlers struct {
	notificationService usecase.NotificationService
}

func NewNotificationHandlers(notificationService usecase.NotificationService) *NotificationHandlers {
	return &NotificationHandlers{
		notificationService: notificationService,
	}
}

// GetNotifications retrieves notifications for the authenticated user
func (h *NotificationHandlers) GetNotifications(w http.ResponseWriter, r *http.Request) {
	// Get user ID from context (set by auth middleware)
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user ID", err)
		return
	}

	// Parse pagination (preferred page/pageSize; fallback to limit/offset)
	filter := domain.NotificationFilter{UserID: userUUID}
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if pageSize <= 0 || pageSize > 100 {
		if limit > 0 && limit <= 100 {
			pageSize = limit
		} else {
			pageSize = 50
		}
	}
	if page <= 0 {
		if offset < 0 {
			offset = 0
		}
		page = (offset / pageSize) + 1
		if page <= 0 {
			page = 1
		}
	}
	filter.Limit = pageSize
	filter.Offset = (page - 1) * pageSize

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
		respondWithError(w, http.StatusInternalServerError, "Failed to retrieve notifications", err)
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
	meta := &Meta{Total: int64(stats.TotalCount), Limit: filter.Limit, Offset: filter.Offset, Page: page, PageSize: pageSize}
	WriteJSONWithMeta(w, http.StatusOK, notifications, meta)
}

// GetUnreadCount returns the count of unread notifications
func (h *NotificationHandlers) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user ID", err)
		return
	}

	count, err := h.notificationService.GetUnreadCount(r.Context(), userUUID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get unread count", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]int{"unread_count": count})
}

// GetNotificationStats returns notification statistics
func (h *NotificationHandlers) GetNotificationStats(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user ID", err)
		return
	}

	stats, err := h.notificationService.GetStats(r.Context(), userUUID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to get notification stats", err)
		return
	}

	respondWithJSON(w, http.StatusOK, stats)
}

// MarkAsRead marks a notification as read
func (h *NotificationHandlers) MarkAsRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user ID", err)
		return
	}

	// Get notification ID from URL
	notificationID := chi.URLParam(r, "id")
	notifUUID, err := uuid.Parse(notificationID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid notification ID", err)
		return
	}

	err = h.notificationService.MarkAsRead(r.Context(), notifUUID, userUUID)
	if err != nil {
		if err == domain.ErrNotificationNotFound {
			respondWithError(w, http.StatusNotFound, "Notification not found", err)
		} else {
			respondWithError(w, http.StatusInternalServerError, "Failed to mark notification as read", err)
		}
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// MarkAllAsRead marks all notifications as read for the user
func (h *NotificationHandlers) MarkAllAsRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user ID", err)
		return
	}

	err = h.notificationService.MarkAllAsRead(r.Context(), userUUID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to mark all notifications as read", err)
		return
	}

	respondWithJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// DeleteNotification deletes a notification
func (h *NotificationHandlers) DeleteNotification(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user ID", err)
		return
	}

	// Get notification ID from URL
	notificationID := chi.URLParam(r, "id")
	notifUUID, err := uuid.Parse(notificationID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid notification ID", err)
		return
	}

	err = h.notificationService.DeleteNotification(r.Context(), notifUUID, userUUID)
	if err != nil {
		if err == domain.ErrNotificationNotFound {
			respondWithError(w, http.StatusNotFound, "Notification not found", err)
		} else {
			respondWithError(w, http.StatusInternalServerError, "Failed to delete notification", err)
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
