package livestream

import (
	"context"
	"encoding/json"
	"errors"
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

type mockSessionRepo struct {
	sessions []*domain.LiveStreamSession
	err      error
}

func (m *mockSessionRepo) ListSessions(_ context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.LiveStreamSession, error) {
	return m.sessions, m.err
}

func sessionReqWithStream(streamID, userID string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/streams/"+streamID+"/sessions", nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", streamID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	return req.WithContext(ctx)
}

func TestGetSessionHistory_ReturnsSessions(t *testing.T) {
	streamID := uuid.New()
	now := time.Now()
	ended := now.Add(time.Hour)
	peak := 42
	repo := &mockSessionRepo{
		sessions: []*domain.LiveStreamSession{
			{
				ID:          uuid.New(),
				StreamID:    streamID,
				StartedAt:   now,
				EndedAt:     &ended,
				PeakViewers: &peak,
			},
		},
	}
	h := NewSessionHistoryHandlers(repo)

	req := sessionReqWithStream(streamID.String(), "user-1")
	w := httptest.NewRecorder()
	h.GetSessionHistory(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetSessionHistory_EmptyList(t *testing.T) {
	repo := &mockSessionRepo{}
	h := NewSessionHistoryHandlers(repo)

	req := sessionReqWithStream(uuid.New().String(), "user-1")
	w := httptest.NewRecorder()
	h.GetSessionHistory(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestGetSessionHistory_InvalidID(t *testing.T) {
	repo := &mockSessionRepo{}
	h := NewSessionHistoryHandlers(repo)

	req := sessionReqWithStream("not-a-valid-uuid", "user-1")
	w := httptest.NewRecorder()
	h.GetSessionHistory(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetSessionHistory_RepoError(t *testing.T) {
	repo := &mockSessionRepo{err: errors.New("database unavailable")}
	h := NewSessionHistoryHandlers(repo)

	req := sessionReqWithStream(uuid.New().String(), "user-1")
	w := httptest.NewRecorder()
	h.GetSessionHistory(w, req)

	assert.Equal(t, http.StatusInternalServerError, w.Code)
}

func TestGetSessionHistory_ResponseShape(t *testing.T) {
	streamID := uuid.New()
	now := time.Now()
	repo := &mockSessionRepo{
		sessions: []*domain.LiveStreamSession{
			{ID: uuid.New(), StreamID: streamID, StartedAt: now},
		},
	}
	h := NewSessionHistoryHandlers(repo)

	req := sessionReqWithStream(streamID.String(), "user-1")
	w := httptest.NewRecorder()
	h.GetSessionHistory(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var out map[string]interface{}
	assert.NoError(t, json.NewDecoder(w.Body).Decode(&out))
	assert.True(t, out["success"].(bool))
	assert.Contains(t, out, "data")
}

func TestGetSessionHistory_NilSessions(t *testing.T) {
	repo := &mockSessionRepo{sessions: nil}
	h := NewSessionHistoryHandlers(repo)

	req := sessionReqWithStream(uuid.New().String(), "user-1")
	w := httptest.NewRecorder()
	h.GetSessionHistory(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}
