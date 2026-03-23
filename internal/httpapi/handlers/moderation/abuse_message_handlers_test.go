package moderation

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

type mockAbuseMessageRepo struct {
	messages    []*domain.AbuseMessage
	reportOwner string // the reporter's user ID
}

func (m *mockAbuseMessageRepo) GetAbuseReportOwner(_ context.Context, reportID uuid.UUID) (string, error) {
	return m.reportOwner, nil
}

func (m *mockAbuseMessageRepo) ListAbuseMessages(_ context.Context, reportID uuid.UUID) ([]*domain.AbuseMessage, error) {
	return m.messages, nil
}

func (m *mockAbuseMessageRepo) CreateAbuseMessage(_ context.Context, msg *domain.AbuseMessage) error {
	msg.ID = uuid.New()
	msg.CreatedAt = time.Now()
	m.messages = append(m.messages, msg)
	return nil
}

func (m *mockAbuseMessageRepo) DeleteAbuseMessage(_ context.Context, reportID, msgID uuid.UUID) error {
	for i, msg := range m.messages {
		if msg.ID == msgID {
			m.messages = append(m.messages[:i], m.messages[i+1:]...)
			return nil
		}
	}
	return domain.ErrNotFound
}

func abuseReqWithUser(method, path, reportID, userID, role string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	chiCtx := chi.NewRouteContext()
	if reportID != "" {
		chiCtx.URLParams.Add("id", reportID)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	ctx = context.WithValue(ctx, middleware.UserRoleKey, role)
	return req.WithContext(ctx)
}

func abuseReqWithBody(method, path, reportID, userID, role string, body interface{}) *http.Request {
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(method, path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	chiCtx := chi.NewRouteContext()
	if reportID != "" {
		chiCtx.URLParams.Add("id", reportID)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	ctx = context.WithValue(ctx, middleware.UserRoleKey, role)
	return req.WithContext(ctx)
}

func TestListAbuseMessages_ReturnsMessages(t *testing.T) {
	reportID := uuid.New()
	repo := &mockAbuseMessageRepo{
		messages: []*domain.AbuseMessage{
			{ID: uuid.New(), AbuseReportID: reportID, SenderID: "user-1", Message: "hello"},
		},
	}
	h := NewAbuseMessageHandlers(repo)

	req := abuseReqWithUser(http.MethodGet, "/admin/abuse-reports/"+reportID.String()+"/messages", reportID.String(), "user-1", "admin")
	w := httptest.NewRecorder()
	h.ListMessages(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestCreateAbuseMessage_Created(t *testing.T) {
	reportID := uuid.New()
	userID := "user-1"
	repo := &mockAbuseMessageRepo{reportOwner: userID}
	h := NewAbuseMessageHandlers(repo)

	req := abuseReqWithBody(http.MethodPost, "/admin/abuse-reports/"+reportID.String()+"/messages",
		reportID.String(), userID, "user",
		map[string]string{"message": "This is a message"})
	w := httptest.NewRecorder()
	h.CreateMessage(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
	assert.Len(t, repo.messages, 1)
}

func TestCreateAbuseMessage_AdminCanPost(t *testing.T) {
	reportID := uuid.New()
	repo := &mockAbuseMessageRepo{reportOwner: "reporter-user"}
	h := NewAbuseMessageHandlers(repo)

	req := abuseReqWithBody(http.MethodPost, "/admin/abuse-reports/"+reportID.String()+"/messages",
		reportID.String(), "admin-user", "admin",
		map[string]string{"message": "Admin note"})
	w := httptest.NewRecorder()
	h.CreateMessage(w, req)

	assert.Equal(t, http.StatusCreated, w.Code)
}

func TestDeleteAbuseMessage_NoContent(t *testing.T) {
	reportID := uuid.New()
	msgID := uuid.New()
	repo := &mockAbuseMessageRepo{
		messages: []*domain.AbuseMessage{
			{ID: msgID, AbuseReportID: reportID, SenderID: "user-1"},
		},
	}
	h := NewAbuseMessageHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/admin/abuse-reports/"+reportID.String()+"/messages/"+msgID.String(), nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", reportID.String())
	chiCtx.URLParams.Add("messageId", msgID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, "user-1")
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "admin")
	req = req.WithContext(ctx)

	w := httptest.NewRecorder()
	h.DeleteMessage(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.Len(t, repo.messages, 0)
}
