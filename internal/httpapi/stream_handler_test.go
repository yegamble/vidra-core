package httpapi

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

// mockStreamRepo reuses mockVideoRepo behavior for GetByID
type mockStreamRepo struct{ vid *domain.Video }

func (m *mockStreamRepo) Create(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockStreamRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	return m.vid, nil
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

func TestStreamVideoHandler_DBOutputMasterRedirectsToHLS(t *testing.T) {
	videoID := "vid-stream-1"
	// Prepare file under ./storage/streaming-playlists/hls/{id}/master.m3u8
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
	// Create a file outside of the HLS root that must never be served via traversal
	outside := filepath.Join("./storage", "streaming-playlists", "secrets.m3u8")
	if err := os.MkdirAll(filepath.Dir(outside), 0o750); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(outside, []byte("#EXTM3U\n# secret"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	// Repo returns nil video; privacy check bypasses to path handling
	repo := &mockStreamRepo{vid: nil}
	h := HLSHandler(repo)

	// Attempt traversal to "../secrets.m3u8"
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
	// Inject owner user id into context
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "owner"))
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 for owner, got %d", rr.Code)
	}
}
