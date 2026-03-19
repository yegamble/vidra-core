package auth

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
)

// chiRequest builds a GET request with a chi URL param injected into context
func chiRequest(path, paramKey, paramVal string) *http.Request {
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add(paramKey, paramVal)
	req := httptest.NewRequest(http.MethodGet, path, nil)
	return req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
}

// TestServeAvatarFromIPFS_HappyPath verifies a valid CID returns image data with correct headers
func TestServeAvatarFromIPFS_HappyPath(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)

	pngMagic := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 520)) // 528 bytes with PNG magic
	testIPFSCat = func(_ string) (io.ReadCloser, error) {
		return io.NopCloser(strings.NewReader(string(pngMagic))), nil
	}
	defer func() { testIPFSCat = nil }()

	validCID := "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
	req := chiRequest("/api/v1/avatars/"+validCID, "cid", validCID)
	rr := httptest.NewRecorder()

	s.ServeAvatarFromIPFS(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	cc := rr.Header().Get("Cache-Control")
	if !strings.Contains(cc, "immutable") {
		t.Errorf("expected immutable Cache-Control header, got %q", cc)
	}
	if rr.Header().Get("Content-Type") == "" {
		t.Error("expected Content-Type header to be set")
	}
}

// TestServeAvatarFromIPFS_InvalidCID verifies invalid CIDs return 400
func TestServeAvatarFromIPFS_InvalidCID(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)

	req := chiRequest("/api/v1/avatars/badcid", "cid", "../../etc/passwd")
	rr := httptest.NewRecorder()

	s.ServeAvatarFromIPFS(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid CID, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// TestServeAvatarFromIPFS_IPFSNotConfigured verifies 503 with IPFS_NOT_CONFIGURED when ipfsAPI is empty
func TestServeAvatarFromIPFS_IPFSNotConfigured(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil) // no ipfsAPI

	validCID := "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
	req := chiRequest("/api/v1/avatars/"+validCID, "cid", validCID)
	rr := httptest.NewRecorder()

	s.ServeAvatarFromIPFS(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 when IPFS not configured, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "IPFS_NOT_CONFIGURED") {
		t.Errorf("expected IPFS_NOT_CONFIGURED error code, got %s", rr.Body.String())
	}
}

// TestServeAvatarFromIPFS_FetchFailure verifies 503 with IPFS_UNAVAILABLE when fetch fails
func TestServeAvatarFromIPFS_FetchFailure(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)

	testIPFSCat = func(_ string) (io.ReadCloser, error) {
		return nil, io.ErrUnexpectedEOF
	}
	defer func() { testIPFSCat = nil }()

	validCID := "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi"
	req := chiRequest("/api/v1/avatars/"+validCID, "cid", validCID)
	rr := httptest.NewRecorder()

	s.ServeAvatarFromIPFS(rr, req)

	if rr.Code != http.StatusServiceUnavailable {
		t.Fatalf("expected 503 on fetch failure, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "IPFS_UNAVAILABLE") {
		t.Errorf("expected IPFS_UNAVAILABLE error code, got %s", rr.Body.String())
	}
}
