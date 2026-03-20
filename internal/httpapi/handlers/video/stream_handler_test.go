package video

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/middleware"
)

type mockStreamRepo struct{ vid *domain.Video }

func (m *mockStreamRepo) Create(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockStreamRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	return m.vid, nil
}
func (m *mockStreamRepo) GetByIDs(_ context.Context, _ []string) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockStreamRepo) GetByUserID(_ context.Context, _ string, _ int, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockStreamRepo) Update(_ context.Context, _ *domain.Video) error    { return nil }
func (m *mockStreamRepo) Delete(_ context.Context, _ string, _ string) error { return nil }
func (m *mockStreamRepo) List(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockStreamRepo) Search(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockStreamRepo) UpdateProcessingInfo(_ context.Context, _ string, _ domain.ProcessingStatus, _ map[string]string, _ string, _ string) error {
	return nil
}
func (m *mockStreamRepo) UpdateProcessingInfoWithCIDs(_ context.Context, _ string, _ domain.ProcessingStatus, _ map[string]string, _ string, _ string, _ map[string]string, _ string, _ string) error {
	return nil
}
func (m *mockStreamRepo) Count(_ context.Context) (int64, error) {
	return 0, nil
}
func (m *mockStreamRepo) GetVideosForMigration(_ context.Context, _ int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockStreamRepo) GetByRemoteURI(_ context.Context, _ string) (*domain.Video, error) {
	return nil, nil
}
func (m *mockStreamRepo) CreateRemoteVideo(_ context.Context, _ *domain.Video) error {
	return nil
}
func (m *mockStreamRepo) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func TestStreamVideoHandler_DBOutputMasterRedirectsToHLS(t *testing.T) {
	videoID := "vid-stream-1"
	base := filepath.Join("./storage", "streaming-playlists", "hls", videoID)
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	master := filepath.Join(base, "master.m3u8")
	if err := os.WriteFile(master, []byte("#EXTM3U\n# dummy"), 0o600); err != nil {
		t.Fatalf("write master: %v", err)
	}

	repo := &mockStreamRepo{vid: &domain.Video{ID: videoID, Privacy: domain.PrivacyPublic, UserID: "u1", OutputPaths: map[string]string{"master": master}}}
	h := StreamVideoHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/stream", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect, got %d", rr.Code)
	}
	loc := rr.Header().Get("Location")
	want := "/api/v1/hls/" + videoID + "/master.m3u8"
	if loc != want {
		t.Fatalf("expected redirect to %s, got %s", want, loc)
	}
}

func TestHLSHandler_ServesPlaylist_WithContentType(t *testing.T) {
	videoID := "vid-hls-1"
	base := filepath.Join("./storage", "streaming-playlists", "hls", videoID)
	if err := os.MkdirAll(base, 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	master := filepath.Join(base, "master.m3u8")
	if err := os.WriteFile(master, []byte("#EXTM3U\n# sample"), 0o600); err != nil {
		t.Fatalf("write master: %v", err)
	}
	repo := &mockStreamRepo{vid: &domain.Video{ID: videoID, Privacy: domain.PrivacyPublic, UserID: "u1"}}
	h := HLSHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/hls/"+videoID+"/master.m3u8", nil)
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/vnd.apple.mpegurl" {
		t.Fatalf("expected HLS content-type, got %s", ct)
	}
}

func TestHLSHandler_PathTraversalBlocked(t *testing.T) {
	outside := filepath.Join("./storage", "streaming-playlists", "secrets.m3u8")
	if err := os.MkdirAll(filepath.Dir(outside), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(outside, []byte("#EXTM3U\n# secret"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	repo := &mockStreamRepo{vid: nil}
	h := HLSHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/hls/../secrets.m3u8", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for traversal attempt, got %d", rr.Code)
	}
}

func TestHLSHandler_ForbiddenForPrivate_NotOwner(t *testing.T) {
	videoID := "vid-private-1"
	repo := &mockStreamRepo{vid: &domain.Video{ID: videoID, Privacy: domain.PrivacyPrivate, UserID: "owner"}}
	h := HLSHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/hls/"+videoID+"/master.m3u8", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Fatalf("expected 403, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestHLSHandler_PrivateOwnerAllowed(t *testing.T) {
	videoID := "vid-private-2"
	base := filepath.Join("./storage", "streaming-playlists", "hls", videoID)
	_ = os.MkdirAll(base, 0o750)
	master := filepath.Join(base, "master.m3u8")
	_ = os.WriteFile(master, []byte("#EXTM3U\n# ok"), 0o600)

	repo := &mockStreamRepo{vid: &domain.Video{ID: videoID, Privacy: domain.PrivacyPrivate, UserID: "owner"}}
	h := HLSHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/hls/"+videoID+"/master.m3u8", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "owner"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for owner, got %d", rr.Code)
	}
}

// TestStreamVideoHandler_S3URLs_MasterRedirect verifies that when a video has an S3URL for master,
// the handler redirects to the S3 URL rather than serving a mock playlist.
func TestStreamVideoHandler_S3URLs_MasterRedirect(t *testing.T) {
	videoID := "vid-s3-master"
	s3URL := "https://s3.backblaze.com/bucket/videos/" + videoID + "/hls/master.m3u8"

	vid := &domain.Video{
		ID:      videoID,
		Privacy: domain.PrivacyPublic,
		UserID:  "u1",
		S3URLs:  map[string]string{"master": s3URL},
	}
	repo := &mockStreamRepo{vid: vid}
	h := StreamVideoHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/stream", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect to S3 URL, got %d body=%s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if loc != s3URL {
		t.Fatalf("expected redirect to %s, got %s", s3URL, loc)
	}
}

// TestStreamVideoHandler_S3URLs_QualityRedirect verifies that a quality-specific S3URL redirects.
func TestStreamVideoHandler_S3URLs_QualityRedirect(t *testing.T) {
	videoID := "vid-s3-quality"
	s3URL := "https://s3.backblaze.com/bucket/videos/" + videoID + "/hls/1080p/stream.m3u8"

	vid := &domain.Video{
		ID:      videoID,
		Privacy: domain.PrivacyPublic,
		UserID:  "u1",
		S3URLs:  map[string]string{"1080p": s3URL},
	}
	repo := &mockStreamRepo{vid: vid}
	h := StreamVideoHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/stream?quality=1080p", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect to S3 URL, got %d body=%s", rr.Code, rr.Body.String())
	}
	loc := rr.Header().Get("Location")
	if loc != s3URL {
		t.Fatalf("expected redirect to %s, got %s", s3URL, loc)
	}
}

// TestHLSHandler_S3Redirect_PlaylistFile verifies that when a local .m3u8 is missing but the video
// has an S3URLs["master"] entry, the HLS handler redirects to the S3 URL for that file.
func TestHLSHandler_S3Redirect_PlaylistFile(t *testing.T) {
	videoID := "vid-hls-s3-playlist"
	s3Base := "https://s3.example.com/bucket/videos/" + videoID + "/hls/"

	vid := &domain.Video{
		ID:      videoID,
		Privacy: domain.PrivacyPublic,
		UserID:  "u1",
		S3URLs:  map[string]string{"master": s3Base + "master.m3u8"},
	}
	repo := &mockStreamRepo{vid: vid}
	h := HLSHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/hls/"+videoID+"/720p/stream.m3u8", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect to S3, got %d body=%s", rr.Code, rr.Body.String())
	}
	want := s3Base + "720p/stream.m3u8"
	if loc := rr.Header().Get("Location"); loc != want {
		t.Fatalf("expected Location=%s, got %s", want, loc)
	}
}

// TestHLSHandler_S3Redirect_SegmentFile verifies that .ts segment requests redirect to S3.
func TestHLSHandler_S3Redirect_SegmentFile(t *testing.T) {
	videoID := "vid-hls-s3-segment"
	s3Base := "https://s3.example.com/bucket/videos/" + videoID + "/hls/"

	vid := &domain.Video{
		ID:      videoID,
		Privacy: domain.PrivacyPublic,
		UserID:  "u1",
		S3URLs:  map[string]string{"master": s3Base + "master.m3u8"},
	}
	repo := &mockStreamRepo{vid: vid}
	h := HLSHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/hls/"+videoID+"/720p/segment_00000.ts", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusTemporaryRedirect {
		t.Fatalf("expected 307 redirect to S3 for segment, got %d body=%s", rr.Code, rr.Body.String())
	}
	want := s3Base + "720p/segment_00000.ts"
	if loc := rr.Header().Get("Location"); loc != want {
		t.Fatalf("expected Location=%s, got %s", want, loc)
	}
}

// TestHLSHandler_NoS3_LocalMissing_Returns404 verifies 404 when no local file and no S3URLs.
func TestHLSHandler_NoS3_LocalMissing_Returns404(t *testing.T) {
	videoID := "vid-hls-no-s3"
	vid := &domain.Video{ID: videoID, Privacy: domain.PrivacyPublic, UserID: "u1"}
	repo := &mockStreamRepo{vid: vid}
	h := HLSHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/hls/"+videoID+"/720p/stream.m3u8", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when local missing and no S3, got %d", rr.Code)
	}
}

// TestStreamVideoHandler_NoHLSFiles_Returns404 verifies that when a video exists but has no HLS files
// (no OutputPaths, no S3URLs, no local directory), a 404 is returned instead of a mock playlist.
func TestStreamVideoHandler_NoHLSFiles_Returns404(t *testing.T) {
	videoID := "vid-no-hls"

	vid := &domain.Video{
		ID:      videoID,
		Privacy: domain.PrivacyPublic,
		UserID:  "u1",
	}
	repo := &mockStreamRepo{vid: vid}
	h := StreamVideoHandler(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/stream", nil)
	rc := chi.NewRouteContext()
	rc.URLParams.Add("id", videoID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rc))
	rr := httptest.NewRecorder()

	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("expected 404 when no HLS files exist, got %d body=%s", rr.Code, rr.Body.String())
	}
}
