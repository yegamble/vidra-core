package channel

import (
	"bytes"
	"context"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"vidra-core/internal/middleware"

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

// TestDeleteChannelBanner_Forbidden verifies 403 when non-owner deletes banner.
func TestDeleteChannelBanner_Forbidden(t *testing.T) {
	chID := uuid.New()
	repo := &mockChannelMediaRepo{ownerID: "other-user"}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/"+chID.String()+"/banner", chID.String(), "user-1")
	w := httptest.NewRecorder()
	h.DeleteBanner(w, req)

	assert.Equal(t, http.StatusForbidden, w.Code)
	assert.False(t, repo.bannerCleared)
}

// TestDeleteChannelAvatar_Unauthenticated verifies 401 when no user in context.
func TestDeleteChannelAvatar_Unauthenticated(t *testing.T) {
	chID := uuid.New()
	repo := &mockChannelMediaRepo{ownerID: "user-1"}
	h := NewChannelMediaHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/channels/"+chID.String()+"/avatar", nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", chID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	w := httptest.NewRecorder()
	h.DeleteAvatar(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestDeleteChannelBanner_Unauthenticated verifies 401 when no user in context.
func TestDeleteChannelBanner_Unauthenticated(t *testing.T) {
	chID := uuid.New()
	repo := &mockChannelMediaRepo{ownerID: "user-1"}
	h := NewChannelMediaHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/channels/"+chID.String()+"/banner", nil)
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", chID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	w := httptest.NewRecorder()
	h.DeleteBanner(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestDeleteChannelAvatar_InvalidID verifies 400 for malformed channel UUID.
func TestDeleteChannelAvatar_InvalidID(t *testing.T) {
	repo := &mockChannelMediaRepo{ownerID: "user-1"}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/bad-uuid/avatar", "bad-uuid", "user-1")
	w := httptest.NewRecorder()
	h.DeleteAvatar(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestUploadChannelAvatar_Unauthenticated verifies 401 when no user in context.
func TestUploadChannelAvatar_Unauthenticated(t *testing.T) {
	chID := uuid.New()
	repo := &mockChannelMediaRepo{ownerID: "user-1"}
	h := NewChannelMediaHandlers(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("avatarfile", "avatar.png")
	fw.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/channels/"+chID.String()+"/avatar", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", chID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	w := httptest.NewRecorder()
	h.UploadAvatar(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestUploadChannelAvatar_InvalidFileType verifies 400 for non-image uploads.
func TestUploadChannelAvatar_InvalidFileType(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("avatarfile", "exploit.exe")
	fw.Write([]byte("MZ\x90\x00")) // PE magic bytes
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/channels/"+chID.String()+"/avatar", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", chID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.UploadAvatar(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestUploadChannelAvatar_MissingFormField verifies 400 when avatarfile field absent.
func TestUploadChannelAvatar_MissingFormField(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("other_field", "value")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/channels/"+chID.String()+"/avatar", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", chID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.UploadAvatar(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestUploadChannelBanner_Unauthenticated verifies 401 when no user in context.
func TestUploadChannelBanner_Unauthenticated(t *testing.T) {
	chID := uuid.New()
	repo := &mockChannelMediaRepo{ownerID: "user-1"}
	h := NewChannelMediaHandlers(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("bannerfile", "banner.png")
	fw.Write([]byte{0x89, 0x50, 0x4E, 0x47})
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/channels/"+chID.String()+"/banner", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", chID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx))
	w := httptest.NewRecorder()
	h.UploadBanner(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

// TestUploadChannelBanner_InvalidID verifies 400 for invalid channel UUID.
func TestUploadChannelBanner_InvalidID(t *testing.T) {
	repo := &mockChannelMediaRepo{ownerID: "user-1"}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, "not-a-uuid", "user-1", "bannerfile", "banner.png")
	w := httptest.NewRecorder()
	h.UploadBanner(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestUploadChannelAvatar_InvalidID verifies 400 for invalid channel UUID.
func TestUploadChannelAvatar_InvalidID(t *testing.T) {
	repo := &mockChannelMediaRepo{ownerID: "user-1"}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, "not-a-uuid", "user-1", "avatarfile", "avatar.png")
	w := httptest.NewRecorder()
	h.UploadAvatar(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestDeleteChannelBanner_InvalidID verifies 400 for invalid channel UUID.
func TestDeleteChannelBanner_InvalidID(t *testing.T) {
	repo := &mockChannelMediaRepo{ownerID: "user-1"}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/bad-uuid/banner", "bad-uuid", "user-1")
	w := httptest.NewRecorder()
	h.DeleteBanner(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestUploadChannelBanner_MissingFormField verifies 400 when bannerfile field absent.
func TestUploadChannelBanner_MissingFormField(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	mw.WriteField("other", "value")
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/channels/"+chID.String()+"/banner", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", chID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.UploadBanner(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

// TestUploadChannelAvatar_ResponseShape verifies 200 response has avatars key.
func TestUploadChannelAvatar_ResponseShape(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), userID, "avatarfile", "avatar.png")
	w := httptest.NewRecorder()
	h.UploadAvatar(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "avatars")
}

// TestUploadChannelBanner_ResponseShape verifies 200 response has banners key.
func TestUploadChannelBanner_ResponseShape(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), userID, "bannerfile", "banner.png")
	w := httptest.NewRecorder()
	h.UploadBanner(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "banners")
}

// TestDeleteChannelAvatar_SetsFlag verifies avatarCleared is true after delete.
func TestDeleteChannelAvatar_SetsFlag(t *testing.T) {
	chID := uuid.New()
	userID := "user-2"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/"+chID.String()+"/avatar", chID.String(), userID)
	w := httptest.NewRecorder()
	h.DeleteAvatar(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.True(t, repo.avatarCleared)
	assert.False(t, repo.bannerCleared)
}

// TestDeleteChannelBanner_SetsFlag verifies bannerCleared is true after delete.
func TestDeleteChannelBanner_SetsFlag(t *testing.T) {
	chID := uuid.New()
	userID := "user-2"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/"+chID.String()+"/banner", chID.String(), userID)
	w := httptest.NewRecorder()
	h.DeleteBanner(w, req)

	assert.Equal(t, http.StatusNoContent, w.Code)
	assert.True(t, repo.bannerCleared)
	assert.False(t, repo.avatarCleared)
}

// TestUploadChannelAvatar_OwnerCanUpload verifies owner gets 200 on upload.
func TestUploadChannelAvatar_OwnerCanUpload(t *testing.T) {
	chID := uuid.New()
	userID := "owner-user"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), userID, "avatarfile", "photo.png")
	w := httptest.NewRecorder()
	h.UploadAvatar(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, repo.avatarSet)
}

// TestUploadChannelBanner_OwnerCanUpload verifies owner gets 200 on upload.
func TestUploadChannelBanner_OwnerCanUpload(t *testing.T) {
	chID := uuid.New()
	userID := "owner-user"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), userID, "bannerfile", "banner.png")
	w := httptest.NewRecorder()
	h.UploadBanner(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, repo.bannerSet)
}

// TestUploadChannelAvatar_PathContainsFilename verifies the response path includes the filename.
func TestUploadChannelAvatar_PathContainsFilename(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), userID, "avatarfile", "myavatar.png")
	w := httptest.NewRecorder()
	h.UploadAvatar(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "myavatar.png")
}

// TestUploadChannelBanner_PathContainsFilename verifies banner response includes filename.
func TestUploadChannelBanner_PathContainsFilename(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := buildMultipartRequest(t, chID.String(), userID, "bannerfile", "mybanner.png")
	w := httptest.NewRecorder()
	h.UploadBanner(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Contains(t, w.Body.String(), "mybanner.png")
}

// TestDeleteChannelAvatar_AvatarNotBannerCleared verifies only avatar is cleared on avatar delete.
func TestDeleteChannelAvatar_AvatarNotBannerCleared(t *testing.T) {
	chID := uuid.New()
	userID := "user-3"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/"+chID.String()+"/avatar", chID.String(), userID)
	w := httptest.NewRecorder()
	h.DeleteAvatar(w, req)

	assert.True(t, repo.avatarCleared)
	assert.False(t, repo.bannerCleared)
}

// TestDeleteChannelBanner_BannerNotAvatarCleared verifies only banner is cleared on banner delete.
func TestDeleteChannelBanner_BannerNotAvatarCleared(t *testing.T) {
	chID := uuid.New()
	userID := "user-3"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	req := channelReqWithUser(http.MethodDelete, "/channels/"+chID.String()+"/banner", chID.String(), userID)
	w := httptest.NewRecorder()
	h.DeleteBanner(w, req)

	assert.True(t, repo.bannerCleared)
	assert.False(t, repo.avatarCleared)
}

// TestUploadChannelBanner_InvalidFileType verifies 400 for non-image uploads.
func TestUploadChannelBanner_InvalidFileType(t *testing.T) {
	chID := uuid.New()
	userID := "user-1"
	repo := &mockChannelMediaRepo{ownerID: userID}
	h := NewChannelMediaHandlers(repo)

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("bannerfile", "exploit.exe")
	fw.Write([]byte("MZ\x90\x00"))
	mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/channels/"+chID.String()+"/banner", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	chiCtx := chi.NewRouteContext()
	chiCtx.URLParams.Add("id", chID.String())
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, chiCtx)
	ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	req = req.WithContext(ctx)
	w := httptest.NewRecorder()
	h.UploadBanner(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
