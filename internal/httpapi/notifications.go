package httpapi

import (
	"net/http"
	"strconv"

	"athena/internal/domain"
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
	userID, ok := r.Context().Value("user_id").(string)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	userUUID, err := uuid.Parse(userID)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid user ID", err)
		return
	}

	// Parse query parameters
	filter := domain.NotificationFilter{
		UserID: userUUID,
		Limit:  50, // Default limit
		Offset: 0,
	}

	// Parse limit
	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil && limit > 0 && limit <= 100 {
			filter.Limit = limit
		}
	}

	// Parse offset
	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil && offset >= 0 {
			filter.Offset = offset
		}
	}

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

	respondWithJSON(w, http.StatusOK, notifications)
}

// GetUnreadCount returns the count of unread notifications
func (h *NotificationHandlers) GetUnreadCount(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value("user_id").(string)
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
	userID, ok := r.Context().Value("user_id").(string)
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
	userID, ok := r.Context().Value("user_id").(string)
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
	userID, ok := r.Context().Value("user_id").(string)
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
	userID, ok := r.Context().Value("user_id").(string)
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
