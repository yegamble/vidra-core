package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/auth"
	"github.com/vidra/vidra-core/internal/channel"
	"github.com/vidra/vidra-core/internal/profileimage"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// imageFakeRepo is an in-memory profileimage.Repository for handler tests.
type imageFakeRepo struct {
	users    map[string]sqlcgen.UserImage
	channels map[string]sqlcgen.ChannelImage
}

func newImageFakeRepo() *imageFakeRepo {
	return &imageFakeRepo{users: map[string]sqlcgen.UserImage{}, channels: map[string]sqlcgen.ChannelImage{}}
}

func imageKeyOf(id uuid.UUID, kind string) string { return id.String() + "|" + kind }

func (f *imageFakeRepo) UpsertUserImage(_ context.Context, a sqlcgen.UpsertUserImageParams) (sqlcgen.UserImage, error) {
	row := sqlcgen.UserImage{UserID: a.UserID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, SizeBytes: a.SizeBytes, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.users[imageKeyOf(a.UserID, a.Kind)] = row
	return row, nil
}

func (f *imageFakeRepo) GetUserImage(_ context.Context, a sqlcgen.GetUserImageParams) (sqlcgen.UserImage, error) {
	row, ok := f.users[imageKeyOf(a.UserID, a.Kind)]
	if !ok {
		return sqlcgen.UserImage{}, errors.New("no rows")
	}
	return row, nil
}

func (f *imageFakeRepo) DeleteUserImage(_ context.Context, a sqlcgen.DeleteUserImageParams) (int64, error) {
	k := imageKeyOf(a.UserID, a.Kind)
	if _, ok := f.users[k]; !ok {
		return 0, nil
	}
	delete(f.users, k)
	return 1, nil
}

func (f *imageFakeRepo) UpsertChannelImage(_ context.Context, a sqlcgen.UpsertChannelImageParams) (sqlcgen.ChannelImage, error) {
	row := sqlcgen.ChannelImage{ChannelID: a.ChannelID, Kind: a.Kind, StorageKey: a.StorageKey,
		ContentType: a.ContentType, SizeBytes: a.SizeBytes, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	f.channels[imageKeyOf(a.ChannelID, a.Kind)] = row
	return row, nil
}

func (f *imageFakeRepo) GetChannelImage(_ context.Context, a sqlcgen.GetChannelImageParams) (sqlcgen.ChannelImage, error) {
	row, ok := f.channels[imageKeyOf(a.ChannelID, a.Kind)]
	if !ok {
		return sqlcgen.ChannelImage{}, errors.New("no rows")
	}
	return row, nil
}

func (f *imageFakeRepo) DeleteChannelImage(_ context.Context, a sqlcgen.DeleteChannelImageParams) (int64, error) {
	k := imageKeyOf(a.ChannelID, a.Kind)
	if _, ok := f.channels[k]; !ok {
		return 0, nil
	}
	delete(f.channels, k)
	return 1, nil
}

// profileImageServer wires real auth + channel + profile-image services over
// in-memory fakes, with a real local blob backend for storage/serving.
func profileImageServer(t *testing.T) *Server {
	t.Helper()
	issuer := auth.NewTokenIssuer("test-secret-test-secret-test-secret-0", "vidra", "vidra", 15*time.Minute)
	authsvc := auth.NewService(newAuthFakeRepo(), issuer, 720*time.Hour)
	chansvc := channel.NewService(newChannelFakeRepo())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("storage.NewLocal: %v", err)
	}
	return New(testConfig(), nil, nil,
		WithAuthService(authsvc, 15*time.Minute),
		WithChannelService(chansvc),
		WithProfileImageService(profileimage.NewService(newImageFakeRepo(), blobs)),
		WithMediaStorage(blobs),
	)
}

// registerUser registers an account and returns its access token and user id.
func registerUser(t *testing.T, srv *Server, body string) (token, userID string) {
	t.Helper()
	rec := postTo(srv, "/api/v1/auth/register", body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("register status = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var ar authResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &ar); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return ar.Token, ar.User.ID
}

// uploadImage posts a multipart "file" part to path with a bearer token.
func uploadImage(srv *Server, path, filename, content, token string) *httptest.ResponseRecorder {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	part, _ := w.CreateFormFile("file", filename)
	_, _ = part.Write([]byte(content))
	_ = w.Close()

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, path, &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	if token != "" {
		req.Header.Set("authorization", "Bearer "+token)
	}
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

func getPath(srv *Server, path string) *httptest.ResponseRecorder {
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	srv.Handler().ServeHTTP(rec, req)
	return rec
}

// TestUserAvatarLifecycle: upload → served publicly with the derived content
// type → replace (new type served) → delete → 404; /auth/me tracks the flag.
func TestUserAvatarLifecycle(t *testing.T) {
	srv := profileImageServer(t)
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	serveURL := "/api/v1/users/" + userID + "/avatar"

	// Unset: serving 404s and /auth/me reports has_avatar=false.
	if g := getPath(srv, serveURL); g.Code != http.StatusNotFound {
		t.Fatalf("avatar before upload = %d, want 404", g.Code)
	}
	var me userView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", tok).Body.Bytes(), &me)
	if me.HasAvatar == nil || *me.HasAvatar {
		t.Fatalf("me.has_avatar before upload = %v, want false", me.HasAvatar)
	}

	// Upload.
	rec := uploadImage(srv, "/api/v1/me/avatar", "me.png", "\x89PNG-fake", tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("set avatar = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	var iv profileImageView
	_ = json.Unmarshal(rec.Body.Bytes(), &iv)
	if iv.Kind != "avatar" || iv.ContentType != "image/png" {
		t.Fatalf("image view = %+v, want kind=avatar content_type=image/png", iv)
	}

	// Served publicly (no token) with the derived content type.
	g := getPath(srv, serveURL)
	if g.Code != http.StatusOK {
		t.Fatalf("avatar after upload = %d, want 200", g.Code)
	}
	if ct := g.Header().Get("Content-Type"); ct != "image/png" {
		t.Errorf("served content-type = %q, want image/png", ct)
	}
	if g.Body.String() != "\x89PNG-fake" {
		t.Errorf("served body = %q, want the uploaded bytes", g.Body.String())
	}

	// /auth/me now reports has_avatar=true (banner still false).
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", tok).Body.Bytes(), &me)
	if me.HasAvatar == nil || !*me.HasAvatar {
		t.Errorf("me.has_avatar after upload = %v, want true", me.HasAvatar)
	}
	if me.HasBanner == nil || *me.HasBanner {
		t.Errorf("me.has_banner = %v, want false", me.HasBanner)
	}

	// Replace with a different type; the new type is served.
	if rec := uploadImage(srv, "/api/v1/me/avatar", "me2.jpg", "jpeg-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("replace avatar = %d, want 201", rec.Code)
	}
	g = getPath(srv, serveURL)
	if g.Code != http.StatusOK || g.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("replaced avatar = %d/%q, want 200/image/jpeg", g.Code, g.Header().Get("Content-Type"))
	}

	// Delete: 204, then serving 404s and the flag drops.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/avatar", "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("delete avatar = %d, want 204", rec.Code)
	}
	if g := getPath(srv, serveURL); g.Code != http.StatusNotFound {
		t.Fatalf("avatar after delete = %d, want 404", g.Code)
	}
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", tok).Body.Bytes(), &me)
	if me.HasAvatar == nil || *me.HasAvatar {
		t.Errorf("me.has_avatar after delete = %v, want false", me.HasAvatar)
	}
	// Deleting again is 404.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/avatar", "", tok); rec.Code != http.StatusNotFound {
		t.Fatalf("second delete = %d, want 404", rec.Code)
	}
}

// TestUserBannerLifecycle exercises the banner variant of the /me routes.
func TestUserBannerLifecycle(t *testing.T) {
	srv := profileImageServer(t)
	tok, userID := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	serveURL := "/api/v1/users/" + userID + "/banner"

	if rec := uploadImage(srv, "/api/v1/me/banner", "wide.webp", "webp-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("set banner = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	g := getPath(srv, serveURL)
	if g.Code != http.StatusOK || g.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("banner = %d/%q, want 200/image/webp", g.Code, g.Header().Get("Content-Type"))
	}
	var me userView
	_ = json.Unmarshal(getWithAuth(srv, "/api/v1/auth/me", tok).Body.Bytes(), &me)
	if me.HasBanner == nil || !*me.HasBanner {
		t.Errorf("me.has_banner = %v, want true", me.HasBanner)
	}
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/me/banner", "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("delete banner = %d, want 204", rec.Code)
	}
	if g := getPath(srv, serveURL); g.Code != http.StatusNotFound {
		t.Fatalf("banner after delete = %d, want 404", g.Code)
	}
}

// TestProfileImageRejectsNonImage: a non-image extension is 415 on every
// upload surface.
func TestProfileImageRejectsNonImage(t *testing.T) {
	srv := profileImageServer(t)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada","display_name":"Ada"}`, tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create channel = %d", rec.Code)
	}

	for _, path := range []string{"/api/v1/me/avatar", "/api/v1/me/banner", "/api/v1/channels/ada/avatar", "/api/v1/channels/ada/banner"} {
		if rec := uploadImage(srv, path, "nope.txt", "text", tok); rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("POST %s with .txt = %d, want 415", path, rec.Code)
		}
	}
}

// TestProfileImageMissingFilePart: a multipart body without "file" is 400.
func TestProfileImageMissingFilePart(t *testing.T) {
	srv := profileImageServer(t)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)

	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	_ = w.WriteField("other", "x")
	_ = w.Close()
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/me/avatar", &body)
	req.Header.Set("Content-Type", w.FormDataContentType())
	req.Header.Set("authorization", "Bearer "+tok)
	srv.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing file part = %d, want 400", rec.Code)
	}
}

// TestProfileImageAuth: all mutating routes require auth (401 anonymous).
func TestProfileImageAuth(t *testing.T) {
	srv := profileImageServer(t)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada","display_name":"Ada"}`, tok); rec.Code != http.StatusCreated {
		t.Fatalf("create channel = %d", rec.Code)
	}

	for _, path := range []string{"/api/v1/me/avatar", "/api/v1/me/banner", "/api/v1/channels/ada/avatar", "/api/v1/channels/ada/banner"} {
		if rec := uploadImage(srv, path, "a.png", "img", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon POST %s = %d, want 401", path, rec.Code)
		}
		if rec := sendJSONAuth(srv, http.MethodDelete, path, "", ""); rec.Code != http.StatusUnauthorized {
			t.Errorf("anon DELETE %s = %d, want 401", path, rec.Code)
		}
	}
}

// TestChannelImageOwnerOnly: a non-owner (and an unknown handle) gets 404 on
// the channel mutating routes — existence of the ability to act is not leaked.
func TestChannelImageOwnerOnly(t *testing.T) {
	srv := profileImageServer(t)
	ownerTok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada","display_name":"Ada"}`, ownerTok); rec.Code != http.StatusCreated {
		t.Fatalf("create channel = %d", rec.Code)
	}
	otherTok, _ := registerUser(t, srv, `{"username":"bob","email":"bob@example.test","password":"supersecret"}`)

	for _, path := range []string{"/api/v1/channels/ada/avatar", "/api/v1/channels/ada/banner"} {
		if rec := uploadImage(srv, path, "a.png", "img", otherTok); rec.Code != http.StatusNotFound {
			t.Errorf("non-owner POST %s = %d, want 404", path, rec.Code)
		}
		if rec := sendJSONAuth(srv, http.MethodDelete, path, "", otherTok); rec.Code != http.StatusNotFound {
			t.Errorf("non-owner DELETE %s = %d, want 404", path, rec.Code)
		}
	}
	// Unknown handle → 404 for an authenticated caller too.
	if rec := uploadImage(srv, "/api/v1/channels/ghost/avatar", "a.png", "img", ownerTok); rec.Code != http.StatusNotFound {
		t.Errorf("unknown handle POST = %d, want 404", rec.Code)
	}
}

// TestChannelAvatarLifecycle: owner upload → public serving → channel view
// flags → delete → 404.
func TestChannelAvatarLifecycle(t *testing.T) {
	srv := profileImageServer(t)
	tok, _ := registerUser(t, srv, `{"username":"ada","email":"ada@example.test","password":"supersecret"}`)
	if rec := postJSONAuth(srv, "/api/v1/channels", `{"handle":"ada","display_name":"Ada"}`, tok); rec.Code != http.StatusCreated {
		t.Fatalf("create channel = %d", rec.Code)
	}

	// Unset: 404 and has_avatar=false on the public channel view.
	if g := getPath(srv, "/api/v1/channels/ada/avatar"); g.Code != http.StatusNotFound {
		t.Fatalf("channel avatar before upload = %d, want 404", g.Code)
	}
	var ch channelView
	_ = json.Unmarshal(getPath(srv, "/api/v1/channels/ada").Body.Bytes(), &ch)
	if ch.HasAvatar == nil || *ch.HasAvatar {
		t.Fatalf("channel has_avatar before upload = %v, want false", ch.HasAvatar)
	}

	rec := uploadImage(srv, "/api/v1/channels/ada/avatar", "logo.webp", "webp-bytes", tok)
	if rec.Code != http.StatusCreated {
		t.Fatalf("set channel avatar = %d, want 201; body=%s", rec.Code, rec.Body.String())
	}
	g := getPath(srv, "/api/v1/channels/ada/avatar")
	if g.Code != http.StatusOK || g.Header().Get("Content-Type") != "image/webp" {
		t.Fatalf("channel avatar = %d/%q, want 200/image/webp", g.Code, g.Header().Get("Content-Type"))
	}
	_ = json.Unmarshal(getPath(srv, "/api/v1/channels/ada").Body.Bytes(), &ch)
	if ch.HasAvatar == nil || !*ch.HasAvatar {
		t.Errorf("channel has_avatar after upload = %v, want true", ch.HasAvatar)
	}
	if ch.HasBanner == nil || *ch.HasBanner {
		t.Errorf("channel has_banner = %v, want false", ch.HasBanner)
	}

	// Banner is independent.
	if rec := uploadImage(srv, "/api/v1/channels/ada/banner", "wide.jpg", "jpg-bytes", tok); rec.Code != http.StatusCreated {
		t.Fatalf("set channel banner = %d, want 201", rec.Code)
	}
	if g := getPath(srv, "/api/v1/channels/ada/banner"); g.Code != http.StatusOK || g.Header().Get("Content-Type") != "image/jpeg" {
		t.Fatalf("channel banner = %d/%q, want 200/image/jpeg", g.Code, g.Header().Get("Content-Type"))
	}

	// Delete the avatar; the banner survives.
	if rec := sendJSONAuth(srv, http.MethodDelete, "/api/v1/channels/ada/avatar", "", tok); rec.Code != http.StatusNoContent {
		t.Fatalf("delete channel avatar = %d, want 204", rec.Code)
	}
	if g := getPath(srv, "/api/v1/channels/ada/avatar"); g.Code != http.StatusNotFound {
		t.Fatalf("channel avatar after delete = %d, want 404", g.Code)
	}
	_ = json.Unmarshal(getPath(srv, "/api/v1/channels/ada").Body.Bytes(), &ch)
	if ch.HasAvatar == nil || *ch.HasAvatar || ch.HasBanner == nil || !*ch.HasBanner {
		t.Errorf("after avatar delete: has_avatar=%v has_banner=%v, want false/true", ch.HasAvatar, ch.HasBanner)
	}
}

// TestGetUserImageUnknownUser: an unknown or malformed user id serves 404.
func TestGetUserImageUnknownUser(t *testing.T) {
	srv := profileImageServer(t)
	if g := getPath(srv, "/api/v1/users/"+uuid.NewString()+"/avatar"); g.Code != http.StatusNotFound {
		t.Errorf("unknown user avatar = %d, want 404", g.Code)
	}
	if g := getPath(srv, "/api/v1/users/not-a-uuid/banner"); g.Code != http.StatusNotFound {
		t.Errorf("malformed user id banner = %d, want 404", g.Code)
	}
}
