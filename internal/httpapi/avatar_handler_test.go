package httpapi

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"
)

// Test that filenames with backslash in extension are rejected (defense-in-depth)
func TestUploadAvatar_InvalidExtensionRejected(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.jpg\\..\\evil")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("avatar-bytes")); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// Inject a user ID to pass auth guard
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid extension, got %d body=%s", rr.Code, rr.Body.String())
	}
}

func TestUploadAvatar_InvalidMIMETypeRejected(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.jpg")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// Write non-image data (text pretending to be JPEG)
	if _, err := fw.Write([]byte("not-an-image")); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// Inject a user ID to pass auth guard
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid mime type, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// Test a happy path upload with a valid filename and a mocked IPFS API server.
func TestUploadAvatar_ValidPNG_WithMockIPFS(t *testing.T) {
	// Mock user repo with a user
	repo := newMockUserRepo()
	u := &domain.User{ID: "u1", Username: "alice", Email: "a@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "", "", 0, nil)
	// Override test hooks to bypass network and create webp
	testIPFSAdd = func(localPath string) (string, error) {
		if strings.HasSuffix(localPath, ".webp") {
			return "CIDWEBP", nil
		}
		return "CID123", nil
	}
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(cid string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(src, dst string) error {
		return os.WriteFile(dst, []byte("webp"), 0600)
	}
	defer func() { testEncodeToWebP = nil }()

	// Build multipart form with a valid PNG filename
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.png")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// Use a real PNG image from testutil
	pngBytes := testutil.CreateTestPNG()
	if _, err := fw.Write(pngBytes); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// Inject a user ID to pass auth guard
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	// Decode envelope and user
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	var got domain.User
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if got.AvatarIPFSCID == nil || *got.AvatarIPFSCID != "CID123" {
		t.Fatalf("expected CID123, got %+v", got)
	}
	if got.AvatarWebPIPFSCID == nil || *got.AvatarWebPIPFSCID != "CIDWEBP" {
		t.Fatalf("expected CIDWEBP for webp, got %+v", got)
	}
}

// Test a happy path upload with a valid JPEG image
func TestUploadAvatar_ValidJPEG_WithMockIPFS(t *testing.T) {
	// Mock user repo with a user
	repo := newMockUserRepo()
	u := &domain.User{ID: "u2", Username: "bob", Email: "b@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "", "", 0, nil)
	// Override test hooks to bypass network and create webp
	testIPFSAdd = func(localPath string) (string, error) {
		if strings.HasSuffix(localPath, ".webp") {
			return "CIDWEBP2", nil
		}
		return "CID456", nil
	}
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(cid string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(src, dst string) error {
		return os.WriteFile(dst, []byte("webp"), 0600)
	}
	defer func() { testEncodeToWebP = nil }()

	// Build multipart form with a valid JPEG filename
	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.jpg")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// Use a real JPEG image from testutil
	jpegBytes := testutil.CreateTestJPEG()
	if _, err := fw.Write(jpegBytes); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// Inject a user ID to pass auth guard
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	// Decode envelope and user
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	var got domain.User
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if got.AvatarIPFSCID == nil || *got.AvatarIPFSCID != "CID456" {
		t.Fatalf("expected CID456, got %+v", got)
	}
	if got.AvatarWebPIPFSCID == nil || *got.AvatarWebPIPFSCID != "CIDWEBP2" {
		t.Fatalf("expected CIDWEBP2 for webp, got %+v", got)
	}
}

// Test that WebP extension is now accepted (will fail with invalid image data in decoding, but passes extension check)
func TestUploadAvatar_WebPExtensionAccepted(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.webp")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// Write non-image data - this should fail at image decoding stage, not extension validation
	if _, err := fw.Write([]byte("not-an-image")); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// Inject a user ID to pass auth guard
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	// Should fail at image decoding stage with "invalid or corrupted image file", not at extension validation
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid image data, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Check that the error message is about image corruption, not extension
	if !strings.Contains(rr.Body.String(), "invalid or corrupted image file") {
		t.Fatalf("expected 'invalid or corrupted image file' error, got %s", rr.Body.String())
	}
}

// Test that HEIC extension is now accepted
func TestUploadAvatar_HEICExtensionAccepted(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.heic")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// Write non-image data - this should fail at image decoding stage, not extension validation
	if _, err := fw.Write([]byte("not-an-image")); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	// Inject a user ID to pass auth guard
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	// Should fail at image decoding stage with "invalid or corrupted image file", not at extension validation
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid image data, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Check that the error message is about image corruption, not extension
	if !strings.Contains(rr.Body.String(), "invalid or corrupted image file") {
		t.Fatalf("expected 'invalid or corrupted image file' error, got %s", rr.Body.String())
	}
}
