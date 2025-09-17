package httpapi

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
func (m *mockNotificationService) GetUserNotifications(context.Context, uuid.UUID, domain.NotificationFilter) ([]domain.Notification, error) {
	return nil, nil
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
	var resp map[string]bool
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil || !resp["success"] {
		t.Fatalf("expected success response: %v, body=%s", resp, rr.Body.String())
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
