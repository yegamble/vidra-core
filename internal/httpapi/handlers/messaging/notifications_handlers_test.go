package messaging

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/domain"
)

type mockNotificationService struct {
	markReadCalled bool
	markReadID     uuid.UUID
	markAllCalled  bool
	deleteCalled   bool
	deleteID       uuid.UUID
	lastFilter     domain.NotificationFilter
	notifications  []domain.Notification
}

func (m *mockNotificationService) CreateVideoNotificationForSubscribers(context.Context, *domain.Video, string) error {
	return nil
}
func (m *mockNotificationService) CreateMessageNotification(context.Context, *domain.Message, string) error {
	return nil
}
func (m *mockNotificationService) CreateMessageReadNotification(context.Context, uuid.UUID, uuid.UUID, string) error {
	return nil
}
func (m *mockNotificationService) GetUserNotifications(_ context.Context, _ uuid.UUID, filter domain.NotificationFilter) ([]domain.Notification, error) {
	m.lastFilter = filter
	return m.notifications, nil
}
func (m *mockNotificationService) MarkAsRead(_ context.Context, nid, _ uuid.UUID) error {
	m.markReadCalled = true
	m.markReadID = nid
	return nil
}
func (m *mockNotificationService) MarkAllAsRead(context.Context, uuid.UUID) error {
	m.markAllCalled = true
	return nil
}
func (m *mockNotificationService) DeleteNotification(_ context.Context, nid, _ uuid.UUID) error {
	m.deleteCalled = true
	m.deleteID = nid
	return nil
}
func (m *mockNotificationService) GetUnreadCount(context.Context, uuid.UUID) (int, error) {
	return 0, nil
}
func (m *mockNotificationService) GetStats(context.Context, uuid.UUID) (*domain.NotificationStats, error) {
	return &domain.NotificationStats{TotalCount: 0}, nil
}

func TestNotificationHandlers_MarkAsRead_OK(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)
	id := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/notifications/"+id.String()+"/read", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Put("/api/v1/notifications/{id}/read", h.MarkAsRead)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !svc.markReadCalled || svc.markReadID != id {
		t.Fatalf("expected mark read called with %s", id)
	}
	// The response is wrapped in the standard API format: {"data": {...}, "success": true}
	var resp struct {
		Data    map[string]bool `json:"data"`
		Success bool            `json:"success"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed to unmarshal response: %v, body=%s", err, rr.Body.String())
	}
	if !resp.Success {
		t.Fatalf("expected success=true in response: %v, body=%s", resp, rr.Body.String())
	}
	if !resp.Data["success"] {
		t.Fatalf("expected data.success=true in response: %v, body=%s", resp, rr.Body.String())
	}
}

func TestNotificationHandlers_MarkAllAsRead_OK(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/notifications/read-all", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Put("/api/v1/notifications/read-all", h.MarkAllAsRead)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if !svc.markAllCalled {
		t.Fatalf("expected mark all called")
	}
}

func TestNotificationHandlers_Delete_OK(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)
	id := uuid.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v1/notifications/"+id.String(), nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()
	router := chi.NewRouter()
	router.Delete("/api/v1/notifications/{id}", h.DeleteNotification)
	router.ServeHTTP(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d", rr.Code)
	}
	if !svc.deleteCalled || svc.deleteID != id {
		t.Fatalf("expected delete called with %s", id)
	}
}

func TestNotificationHandlers_GetNotifications_TypeFilter_OK(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications?types=new_video,comment&type=mention", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications", h.GetNotifications)
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	if len(svc.lastFilter.Types) != 3 {
		t.Fatalf("expected 3 notification types, got %d", len(svc.lastFilter.Types))
	}

	expected := map[domain.NotificationType]bool{
		domain.NotificationNewVideo: true,
		domain.NotificationComment:  true,
		domain.NotificationMention:  true,
	}
	for _, tpe := range svc.lastFilter.Types {
		if !expected[tpe] {
			t.Fatalf("unexpected notification type in filter: %s", tpe)
		}
		delete(expected, tpe)
	}
	if len(expected) != 0 {
		t.Fatalf("missing expected notification types: %+v", expected)
	}
}

func TestNotificationHandlers_GetNotifications_InvalidType_BadRequest(t *testing.T) {
	svc := &mockNotificationService{}
	h := NewNotificationHandlers(svc)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/notifications?types=not-a-real-type", nil)
	req = req.WithContext(withUserID(req.Context(), uuid.NewString()))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/notifications", h.GetNotifications)
	router.ServeHTTP(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
