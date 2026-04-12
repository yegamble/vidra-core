package static

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/port"
)

// mockVideoRepo implements port.VideoRepository for testing.
type mockVideoRepo struct {
	getByIDFn func(ctx context.Context, id string) (*domain.Video, error)
}

func (m *mockVideoRepo) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, id)
	}
	return nil, domain.ErrNotFound
}

// Satisfy the full VideoRepository interface with no-op stubs.
func (m *mockVideoRepo) GetByIDs(_ context.Context, _ []string) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) Create(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockVideoRepo) Update(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockVideoRepo) Delete(_ context.Context, _, _ string) error     { return nil }
func (m *mockVideoRepo) List(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) Search(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) GetByUserID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) UpdateProcessingInfo(_ context.Context, _ port.VideoProcessingParams) error {
	return nil
}
func (m *mockVideoRepo) UpdateProcessingInfoWithCIDs(_ context.Context, _ port.VideoProcessingWithCIDsParams) error {
	return nil
}
func (m *mockVideoRepo) Count(_ context.Context) (int64, error) { return 0, nil }
func (m *mockVideoRepo) GetVideosForMigration(_ context.Context, _ int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) GetByRemoteURI(_ context.Context, _ string) (*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) CreateRemoteVideo(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockVideoRepo) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (m *mockVideoRepo) AppendOutputPath(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

func newTestHandler(t *testing.T, storageDir string, repo *mockVideoRepo) *Handlers {
	t.Helper()
	cfg := &config.Config{StorageDir: storageDir}
	if repo == nil {
		repo = &mockVideoRepo{}
	}
	return NewHandlers(cfg, repo)
}

func newChiRequest(method, path string, urlParams map[string]string) *http.Request {
	req := httptest.NewRequest(method, path, nil)
	rctx := chi.NewRouteContext()
	for k, v := range urlParams {
		rctx.URLParams.Add(k, v)
	}
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

func newAuthenticatedChiRequest(method, path, userID string, urlParams map[string]string) *http.Request {
	req := newChiRequest(method, path, urlParams)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID)
	return req.WithContext(ctx)
}

// --- ServeWebVideo tests ---

func TestServeWebVideo_Success(t *testing.T) {
	dir := t.TempDir()
	webVideosDir := filepath.Join(dir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(webVideosDir, "test.mp4"), []byte("fake-video"), 0o644))

	h := newTestHandler(t, dir, nil)
	req := newChiRequest("GET", "/static/web-videos/test.mp4", map[string]string{"filename": "test.mp4"})
	rec := httptest.NewRecorder()

	h.ServeWebVideo(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Cache-Control"), "public")
}

func TestServeWebVideo_NotFound(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir, nil)
	req := newChiRequest("GET", "/static/web-videos/missing.mp4", map[string]string{"filename": "missing.mp4"})
	rec := httptest.NewRecorder()

	h.ServeWebVideo(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestServeWebVideo_PathTraversal(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir, nil)

	tests := []struct {
		name     string
		filename string
	}{
		{"dot-dot-slash", "../../../etc/passwd"},
		{"backslash", "..\\secret"},
		{"absolute-slash", "/etc/passwd"},
		{"dot-prefix", ".hidden-file"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := newChiRequest("GET", "/static/web-videos/"+tt.filename, map[string]string{"filename": tt.filename})
			rec := httptest.NewRecorder()

			h.ServeWebVideo(rec, req)

			assert.Equal(t, http.StatusBadRequest, rec.Code)
		})
	}
}

func TestServeWebVideo_EmptyFilename(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir, nil)
	req := newChiRequest("GET", "/static/web-videos/", map[string]string{"filename": ""})
	rec := httptest.NewRecorder()

	h.ServeWebVideo(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

// --- ServePrivateWebVideo tests ---

func TestServePrivateWebVideo_Success(t *testing.T) {
	dir := t.TempDir()
	privateDir := filepath.Join(dir, "web-videos", "private")
	require.NoError(t, os.MkdirAll(privateDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(privateDir, "private.mp4"), []byte("private-video"), 0o644))

	h := newTestHandler(t, dir, nil)
	req := newChiRequest("GET", "/static/web-videos/private/private.mp4", map[string]string{"filename": "private.mp4"})
	rec := httptest.NewRecorder()

	h.ServePrivateWebVideo(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServePrivateWebVideo_NotFound(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir, nil)
	req := newChiRequest("GET", "/static/web-videos/private/missing.mp4", map[string]string{"filename": "missing.mp4"})
	rec := httptest.NewRecorder()

	h.ServePrivateWebVideo(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- ServeHLSFile tests ---

func TestServeHLSFile_Success(t *testing.T) {
	dir := t.TempDir()
	hlsDir := filepath.Join(dir, "streaming-playlists", "hls")
	require.NoError(t, os.MkdirAll(hlsDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(hlsDir, "playlist.m3u8"), []byte("#EXTM3U"), 0o644))

	h := newTestHandler(t, dir, nil)
	req := newChiRequest("GET", "/static/streaming-playlists/hls/playlist.m3u8", map[string]string{"filename": "playlist.m3u8"})
	rec := httptest.NewRecorder()

	h.ServeHLSFile(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestServeHLSFile_NotFound(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir, nil)
	req := newChiRequest("GET", "/static/streaming-playlists/hls/missing.m3u8", map[string]string{"filename": "missing.m3u8"})
	rec := httptest.NewRecorder()

	h.ServeHLSFile(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- ServePrivateHLSFile tests ---

func TestServePrivateHLSFile_Success(t *testing.T) {
	dir := t.TempDir()
	privateHLS := filepath.Join(dir, "streaming-playlists", "hls", "private")
	require.NoError(t, os.MkdirAll(privateHLS, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(privateHLS, "seg.ts"), []byte("segment-data"), 0o644))

	h := newTestHandler(t, dir, nil)
	req := newChiRequest("GET", "/static/streaming-playlists/hls/private/seg.ts", map[string]string{"filename": "seg.ts"})
	rec := httptest.NewRecorder()

	h.ServePrivateHLSFile(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

// --- DownloadVideo tests ---

func TestDownloadVideo_Success(t *testing.T) {
	dir := t.TempDir()
	videoID := uuid.New().String()
	webVideosDir := filepath.Join(dir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(webVideosDir, videoID+".mp4"), []byte("video-content"), 0o644))

	repo := &mockVideoRepo{
		getByIDFn: func(_ context.Context, id string) (*domain.Video, error) {
			return &domain.Video{
				ID:      id,
				Title:   "Test Video",
				Privacy: domain.PrivacyPublic,
				UserID:  uuid.New().String(),
			}, nil
		},
	}

	h := newTestHandler(t, dir, repo)
	req := newChiRequest("GET", "/download/videos/generate/"+videoID, map[string]string{"videoId": videoID})
	rec := httptest.NewRecorder()

	h.DownloadVideo(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "Test Video.mp4")
}

func TestDownloadVideo_VideoNotFound(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir, &mockVideoRepo{})
	videoID := uuid.New().String()
	req := newChiRequest("GET", "/download/videos/generate/"+videoID, map[string]string{"videoId": videoID})
	rec := httptest.NewRecorder()

	h.DownloadVideo(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestDownloadVideo_InvalidVideoID(t *testing.T) {
	dir := t.TempDir()
	h := newTestHandler(t, dir, nil)
	req := newChiRequest("GET", "/download/videos/generate/not-a-uuid", map[string]string{"videoId": "not-a-uuid"})
	rec := httptest.NewRecorder()

	h.DownloadVideo(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestDownloadVideo_PrivateVideo_Unauthorized(t *testing.T) {
	dir := t.TempDir()
	videoID := uuid.New().String()
	repo := &mockVideoRepo{
		getByIDFn: func(_ context.Context, _ string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID,
				Title:   "Private Video",
				Privacy: domain.PrivacyPrivate,
				UserID:  uuid.New().String(),
			}, nil
		},
	}

	h := newTestHandler(t, dir, repo)
	// No auth context
	req := newChiRequest("GET", "/download/videos/generate/"+videoID, map[string]string{"videoId": videoID})
	rec := httptest.NewRecorder()

	h.DownloadVideo(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDownloadVideo_PrivateVideo_WrongUser(t *testing.T) {
	dir := t.TempDir()
	videoID := uuid.New().String()
	ownerID := uuid.New().String()
	otherUserID := uuid.New().String()

	repo := &mockVideoRepo{
		getByIDFn: func(_ context.Context, _ string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID,
				Title:   "Private Video",
				Privacy: domain.PrivacyPrivate,
				UserID:  ownerID,
			}, nil
		},
	}

	h := newTestHandler(t, dir, repo)
	req := newAuthenticatedChiRequest("GET", "/download/videos/generate/"+videoID, otherUserID, map[string]string{"videoId": videoID})
	rec := httptest.NewRecorder()

	h.DownloadVideo(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestDownloadVideo_PrivateVideo_OwnerAllowed(t *testing.T) {
	dir := t.TempDir()
	videoID := uuid.New().String()
	ownerID := uuid.New().String()
	webVideosDir := filepath.Join(dir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(webVideosDir, videoID+".mp4"), []byte("video-content"), 0o644))

	repo := &mockVideoRepo{
		getByIDFn: func(_ context.Context, _ string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID,
				Title:   "Private Video",
				Privacy: domain.PrivacyPrivate,
				UserID:  ownerID,
			}, nil
		},
	}

	h := newTestHandler(t, dir, repo)
	req := newAuthenticatedChiRequest("GET", "/download/videos/generate/"+videoID, ownerID, map[string]string{"videoId": videoID})
	rec := httptest.NewRecorder()

	h.DownloadVideo(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	assert.Contains(t, rec.Header().Get("Content-Disposition"), "attachment")
}

func TestDownloadVideo_FileNotOnDisk(t *testing.T) {
	dir := t.TempDir()
	videoID := uuid.New().String()

	repo := &mockVideoRepo{
		getByIDFn: func(_ context.Context, _ string) (*domain.Video, error) {
			return &domain.Video{
				ID:      videoID,
				Title:   "Test Video",
				Privacy: domain.PrivacyPublic,
				UserID:  uuid.New().String(),
			}, nil
		},
	}

	h := newTestHandler(t, dir, repo)
	req := newChiRequest("GET", "/download/videos/generate/"+videoID, map[string]string{"videoId": videoID})
	rec := httptest.NewRecorder()

	h.DownloadVideo(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

// --- validateFilename tests ---

func TestValidateFilename(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"valid mp4", "video.mp4", false},
		{"valid m3u8", "playlist.m3u8", false},
		{"valid segment", "seg001.ts", false},
		{"empty", "", true},
		{"dot-dot", "../secret", true},
		{"backslash", "a\\b", true},
		{"forward-slash", "a/b", true},
		{"starts-with-dot", ".hidden", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFilename(tt.input)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// --- contentTypeForExt tests ---

func TestContentTypeForExt(t *testing.T) {
	tests := []struct {
		ext      string
		expected string
	}{
		{".mp4", "video/mp4"},
		{".webm", "video/webm"},
		{".m3u8", "application/vnd.apple.mpegurl"},
		{".ts", "video/mp2t"},
		{".m4s", "video/iso.segment"},
		{".mkv", "video/x-matroska"},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			assert.Equal(t, tt.expected, contentTypeForExt(tt.ext))
		})
	}
}
