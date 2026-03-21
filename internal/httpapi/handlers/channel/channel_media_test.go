package channel

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

type mockChannelMediaRepo struct {
	ownerID       string
	avatarCleared bool
	bannerCleared bool
	avatarSet     bool
	bannerSet     bool
}

func (m *mockChannelMediaRepo) GetOwnerID(_ context.Context, channelID uuid.UUID) (string, error) {
	return m.ownerID, nil
}
func (m *mockChannelMediaRepo) SetAvatar(_ context.Context, channelID uuid.UUID, filename, ipfsCID string) error {
	m.avatarSet = true
	return nil
}
func (m *mockChannelMediaRepo) ClearAvatar(_ context.Context, channelID uuid.UUID) error {
	m.avatarCleared = true
	return nil
}
func (m *mockChannelMediaRepo) SetBanner(_ context.Context, channelID uuid.UUID, filename, ipfsCID string) error {
	m.bannerSet = true
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

// buildMultipartRequest creates a POST request with a multipart file upload,
// injecting chi URL params and userID into context.
func buildMultipartRequest(t *testing.T, channelID, userID, fieldName, filename string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, err := mw.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// Minimal PNG magic bytes so content-type detection works.
	fw.Write([]byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A})
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/channels/"+channelID+"/avatar", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", channelID)
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	return req.WithContext(ctx)
}

func TestUploadChannelAvatar_OK(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), userID, "avatarfile", "avatar.png")
	w := httptest.NewRecorder()
	h.UploadAvatar(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, repo.avatarSet)
}

func TestUploadChannelAvatar_Forbidden(t *testing.T) {
	chID := uuid.New()
	repo := &mockChannelMediaRepo{ownerID: "other-user"}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), "user-1", "avatarfile", "avatar.png")
	w := httptest.NewRecorder()
	h.UploadAvatar(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, repo.avatarSet)
}

func TestUploadChannelBanner_OK(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), userID, "bannerfile", "banner.png")
	w := httptest.NewRecorder()
	h.UploadBanner(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, repo.bannerSet)
}

func TestUploadChannelBanner_Forbidden(t *testing.T) {
	chID := uuid.New()
	repo := &mockChannelMediaRepo{ownerID: "other-user"}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), "user-1", "bannerfile", "banner.png")
	w := httptest.NewRecorder()
	h.UploadBanner(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, repo.bannerSet)
}
