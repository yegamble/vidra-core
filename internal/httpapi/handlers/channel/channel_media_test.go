package channel

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

type mockChannelMediaRepo struct {
	ownerID      string
	avatarCleared bool
	bannerCleared bool
}

func (m *mockChannelMediaRepo) GetOwnerID(_ context.Context, channelID uuid.UUID) (string, error) {
	return m.ownerID, nil
}
func (m *mockChannelMediaRepo) SetAvatar(_ context.Context, channelID uuid.UUID, filename, ipfsCID string) error {
	return nil
}
func (m *mockChannelMediaRepo) ClearAvatar(_ context.Context, channelID uuid.UUID) error {
	m.avatarCleared = true
	return nil
}
func (m *mockChannelMediaRepo) SetBanner(_ context.Context, channelID uuid.UUID, filename, ipfsCID string) error {
	return nil
}
func (m *mockChannelMediaRepo) ClearBanner(_ context.Context, channelID uuid.UUID) error {
	m.bannerCleared = true
	return nil
}

func channelReqWithUser(method, path, channelID, userID string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", channelID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	return req.WithContext(ctx)
}

func TestDeleteChannelAvatar_NoContent(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/"+chID.String()+"/avatar", chID.String(), userID)
	w := httptest.NewRecorder()
	h.DeleteAvatar(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.True(t, repo.avatarCleared)
}

func TestDeleteChannelBanner_NoContent(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/"+chID.String()+"/banner", chID.String(), userID)
	w := httptest.NewRecorder()
	h.DeleteBanner(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.True(t, repo.bannerCleared)
}

func TestDeleteChannelAvatar_Forbidden(t *testing.T) {
	chID := uuid.New()
	repo := &mockChannelMediaRepo{ownerID: "other-user"}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/"+chID.String()+"/avatar", chID.String(), "user-1")
	w := httptest.NewRecorder()
	h.DeleteAvatar(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
}
