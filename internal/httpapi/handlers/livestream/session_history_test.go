package livestream

import (
	"context"
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
}

func (m *mockSessionRepo) ListSessions(_ context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.LiveStreamSession, error) {
	return m.sessions, nil
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
