package messaging

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	ucn "vidra-core/internal/usecase/notification"

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

	// Parse notification types from query (?types=a,b or ?type=a&type=b)
	types, err := parseNotificationTypes(r)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}
	filter.Types = types

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

func parseNotificationTypes(r *http.Request) ([]domain.NotificationType, error) {
	var rawTypes []string

	if typesCSV := r.URL.Query().Get("types"); typesCSV != "" {
		rawTypes = append(rawTypes, strings.Split(typesCSV, ",")...)
	}

	for _, value := range r.URL.Query()["type"] {
		rawTypes = append(rawTypes, strings.Split(value, ",")...)
	}

	// PeerTube v7.0 compat: accept typeOneOf as alias for types
	if len(rawTypes) == 0 {
		if typeOneOf := r.URL.Query().Get("typeOneOf"); typeOneOf != "" {
			rawTypes = append(rawTypes, strings.Split(typeOneOf, ",")...)
		}
	}

	if len(rawTypes) == 0 {
		return nil, nil
	}

	validTypes := map[domain.NotificationType]struct{}{
		domain.NotificationNewVideo:       {},
		domain.NotificationVideoProcessed: {},
		domain.NotificationVideoFailed:    {},
		domain.NotificationNewSubscriber:  {},
		domain.NotificationComment:        {},
		domain.NotificationMention:        {},
		domain.NotificationSystem:         {},
		domain.NotificationNewMessage:     {},
		domain.NotificationMessageRead:    {},
	}

	types := make([]domain.NotificationType, 0, len(rawTypes))
	seen := make(map[domain.NotificationType]struct{}, len(rawTypes))
	for _, raw := range rawTypes {
		raw = strings.TrimSpace(raw)
		if raw == "" {
			continue
		}

		t := domain.NotificationType(raw)
		if _, ok := validTypes[t]; !ok {
			return nil, fmt.Errorf("invalid notification type: %s", raw)
		}
		if _, exists := seen[t]; exists {
			continue
		}

		seen[t] = struct{}{}
		types = append(types, t)
	}

	if len(types) == 0 {
		return nil, nil
	}

	return types, nil
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
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get unread count: %w", err))
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
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get notification stats: %w", err))
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
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid notification ID: %w", err))
		return
	}

	err = h.notificationService.MarkAsRead(r.Context(), notifUUID, userUUID)
	if err != nil {
		if err == domain.ErrNotificationNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("notification not found: %w", err))
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
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to mark all notifications as read: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// MarkBatchAsRead handles POST /api/v1/notifications/read — marks specific notifications as read.
// If ids is empty, marks all notifications as read (PeerTube-compatible).
func (h *NotificationHandlers) MarkBatchAsRead(w http.ResponseWriter, r *http.Request) {
	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("ERROR", "Unauthorized"))
		return
	}
	userUUID, err := uuid.Parse(userID)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid user ID: %w", err))
		return
	}

	var body struct {
		IDs []uuid.UUID `json:"ids"`
	}
	// Decode if body present; ignore EOF (empty body treated as "mark all")
	_ = json.NewDecoder(r.Body).Decode(&body)

	if len(body.IDs) == 0 {
		if err := h.notificationService.MarkAllAsRead(r.Context(), userUUID); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to mark all as read: %w", err))
			return
		}
		w.WriteHeader(http.StatusNoContent)
		return
	}

	for _, nid := range body.IDs {
		if err := h.notificationService.MarkAsRead(r.Context(), nid, userUUID); err != nil {
			// Log and continue — partial success is better than 500 for the whole batch
			continue
		}
	}
	w.WriteHeader(http.StatusNoContent)
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
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid notification ID: %w", err))
		return
	}

	err = h.notificationService.DeleteNotification(r.Context(), notifUUID, userUUID)
	if err != nil {
		if err == domain.ErrNotificationNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("notification not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to delete notification: %w", err))
		}
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
