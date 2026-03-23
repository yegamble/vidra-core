package auth

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/generated"
	"vidra-core/internal/testutil"
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
	u := &domain.User{ID: "11111111-1111-1111-1111-111111111111", Username: "alice", Email: "a@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)
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
	var got generated.User
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if got.Avatar == nil || got.Avatar.IpfsCid == nil || *got.Avatar.IpfsCid != "CID123" {
		t.Fatalf("expected CID123, got %+v", got)
	}
	if got.Avatar == nil || got.Avatar.WebpIpfsCid == nil || *got.Avatar.WebpIpfsCid != "CIDWEBP" {
		t.Fatalf("expected CIDWEBP for webp, got %+v", got)
	}
}

// Test a happy path upload with a valid JPEG image
func TestUploadAvatar_ValidJPEG_WithMockIPFS(t *testing.T) {
	// Mock user repo with a user
	repo := newMockUserRepo()
	u := &domain.User{ID: "22222222-2222-2222-2222-222222222222", Username: "bob", Email: "b@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)
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
	var got generated.User
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if got.Avatar == nil || got.Avatar.IpfsCid == nil || *got.Avatar.IpfsCid != "CID456" {
		t.Fatalf("expected CID456, got %+v", got)
	}
	if got.Avatar == nil || got.Avatar.WebpIpfsCid == nil || *got.Avatar.WebpIpfsCid != "CIDWEBP2" {
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

	// Check that the error message is about image validation (magic bytes or decoding)
	bodyStr := strings.ToLower(rr.Body.String())
	if !strings.Contains(bodyStr, "invalid or corrupted image file") && !strings.Contains(bodyStr, "file validation failed") && !strings.Contains(bodyStr, "file content does not match") {
		t.Fatalf("expected image validation error, got %s", rr.Body.String())
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

	// Check that the error message is about image validation (magic bytes or corruption)
	bodyStr := strings.ToLower(rr.Body.String())
	if !strings.Contains(bodyStr, "invalid or corrupted image file") && !strings.Contains(bodyStr, "file validation failed") && !strings.Contains(bodyStr, "file content does not match") {
		t.Fatalf("expected image validation error, got %s", rr.Body.String())
	}
}

// Test successful WebP upload with valid image data
func TestUploadAvatar_ValidWebP_WithMockIPFS(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{ID: "33333333-3333-3333-3333-333333333333", Username: "charlie", Email: "c@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)
	testIPFSAdd = func(localPath string) (string, error) {
		if strings.HasSuffix(localPath, ".webp") {
			return "CIDWEBP3", nil
		}
		return "CID789", nil
	}
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(cid string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(src, dst string) error {
		return os.WriteFile(dst, []byte("webp"), 0600)
	}
	defer func() { testEncodeToWebP = nil }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.webp")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	webpBytes := testutil.CreateTestWebP()
	if _, err := fw.Write(webpBytes); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	var got generated.User
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if got.Avatar == nil || got.Avatar.IpfsCid == nil || *got.Avatar.IpfsCid != "CIDWEBP3" {
		t.Fatalf("expected CIDWEBP3, got avatar CID: %v", func() string {
			if got.Avatar == nil || got.Avatar.IpfsCid == nil {
				return "<nil>"
			}
			return *got.Avatar.IpfsCid
		}())
	}
	if got.Avatar == nil || got.Avatar.WebpIpfsCid == nil || *got.Avatar.WebpIpfsCid != "CIDWEBP3" {
		t.Fatalf("expected CIDWEBP3 for webp, got WebP CID: %v", func() string {
			if got.Avatar == nil || got.Avatar.WebpIpfsCid == nil {
				return "<nil>"
			}
			return *got.Avatar.WebpIpfsCid
		}())
	}
}

// Test successful GIF upload with valid image data
func TestUploadAvatar_ValidGIF_WithMockIPFS(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{ID: "44444444-4444-4444-4444-444444444444", Username: "dave", Email: "d@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)
	testIPFSAdd = func(localPath string) (string, error) {
		if strings.HasSuffix(localPath, ".webp") {
			return "CIDWEBP4", nil
		}
		return "CIDGIF1", nil
	}
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(cid string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(src, dst string) error {
		return os.WriteFile(dst, []byte("webp"), 0600)
	}
	defer func() { testEncodeToWebP = nil }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.gif")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	gifBytes := testutil.CreateTestGIF()
	if _, err := fw.Write(gifBytes); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	var got generated.User
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if got.Avatar == nil || got.Avatar.IpfsCid == nil || *got.Avatar.IpfsCid != "CIDGIF1" {
		t.Fatalf("expected CIDGIF1, got %+v", got)
	}
	if got.Avatar == nil || got.Avatar.WebpIpfsCid == nil || *got.Avatar.WebpIpfsCid != "CIDWEBP4" {
		t.Fatalf("expected CIDWEBP4 for webp, got %+v", got)
	}
}

// Test successful TIFF upload with valid image data
func TestUploadAvatar_ValidTIFF_WithMockIPFS(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{ID: "55555555-5555-5555-5555-555555555555", Username: "eve", Email: "e@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)
	testIPFSAdd = func(localPath string) (string, error) {
		if strings.HasSuffix(localPath, ".webp") {
			return "CIDWEBP5", nil
		}
		return "CIDTIFF1", nil
	}
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(cid string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(src, dst string) error {
		return os.WriteFile(dst, []byte("webp"), 0600)
	}
	defer func() { testEncodeToWebP = nil }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.tiff")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	tiffBytes := testutil.CreateTestTIFF()
	if _, err := fw.Write(tiffBytes); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	var got generated.User
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if got.Avatar == nil || got.Avatar.IpfsCid == nil || *got.Avatar.IpfsCid != "CIDTIFF1" {
		t.Fatalf("expected CIDTIFF1, got %+v", got)
	}
	if got.Avatar == nil || got.Avatar.WebpIpfsCid == nil || *got.Avatar.WebpIpfsCid != "CIDWEBP5" {
		t.Fatalf("expected CIDWEBP5 for webp, got %+v", got)
	}
}

// Test successful HEIC upload with valid image data
func TestUploadAvatar_ValidHEIC_WithMockIPFS(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{ID: "66666666-6666-6666-6666-666666666666", Username: "frank", Email: "f@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)
	testIPFSAdd = func(localPath string) (string, error) {
		if strings.HasSuffix(localPath, ".webp") {
			return "CIDWEBP6", nil
		}
		return "CIDHEIC1", nil
	}
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(cid string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(src, dst string) error {
		return os.WriteFile(dst, []byte("webp"), 0600)
	}
	defer func() { testEncodeToWebP = nil }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.heic")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	heicBytes := testutil.CreateTestHEIC()
	if _, err := fw.Write(heicBytes); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	var resp Response
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if !resp.Success {
		t.Fatalf("expected success=true")
	}
	var got generated.User
	b, _ := json.Marshal(resp.Data)
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("decode user: %v", err)
	}
	if got.Avatar == nil || got.Avatar.IpfsCid == nil || *got.Avatar.IpfsCid != "CIDHEIC1" {
		t.Fatalf("expected CIDHEIC1, got %+v", got)
	}
	if got.Avatar == nil || got.Avatar.WebpIpfsCid == nil || *got.Avatar.WebpIpfsCid != "CIDWEBP6" {
		t.Fatalf("expected CIDWEBP6 for webp, got %+v", got)
	}
}

// Test that verifyIPFSContent is called after successful IPFS upload
func TestUploadAvatar_VerifyCalled(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{ID: "77777777-7777-7777-7777-777777777777", Username: "grace", Email: "g@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)

	verifyCalled := false
	testIPFSAdd = func(_ string) (string, error) { return "CID_VERIFY_TEST", nil }
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(_ string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(_, dst string) error { return os.WriteFile(dst, []byte("webp"), 0600) }
	defer func() { testEncodeToWebP = nil }()
	testIPFSVerify = func(_ string) error {
		verifyCalled = true
		return nil
	}
	defer func() { testIPFSVerify = nil }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "avatar.png")
	_, _ = fw.Write(testutil.CreateTestPNG())
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}
	if !verifyCalled {
		t.Fatal("expected testIPFSVerify to be called after successful upload")
	}
}

// Test that upload succeeds even when IPFS verification fails
func TestUploadAvatar_VerifyFailureDoesNotFailUpload(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{ID: "88888888-8888-8888-8888-888888888888", Username: "hank", Email: "h@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)

	testIPFSAdd = func(_ string) (string, error) { return "CID_VERIFY_FAIL", nil }
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(_ string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(_, dst string) error { return os.WriteFile(dst, []byte("webp"), 0600) }
	defer func() { testEncodeToWebP = nil }()
	testIPFSVerify = func(_ string) error {
		return fmt.Errorf("simulated verification failure")
	}
	defer func() { testIPFSVerify = nil }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "avatar.png")
	_, _ = fw.Write(testutil.CreateTestPNG())
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	// Upload must succeed even though verification failed
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200 even with verify failure, got %d body=%s", rr.Code, rr.Body.String())
	}
}

// Test that gateway_url and local_gateway_url appear in avatar response
func TestUploadAvatar_GatewayURLsInResponse(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{ID: "99999999-9999-9999-9999-999999999999", Username: "ivan", Email: "i@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)
	// Set gateway URLs via cfg
	s.cfg = &config.Config{
		IPFSGatewayURLs:     []string{"https://ipfs.io", "https://dweb.link"},
		IPFSLocalGatewayURL: "http://localhost:8080",
	}

	testIPFSAdd = func(_ string) (string, error) { return "CID_GATEWAY_TEST", nil }
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(_ string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(_, dst string) error { return os.WriteFile(dst, []byte("webp"), 0600) }
	defer func() { testEncodeToWebP = nil }()
	testIPFSVerify = func(_ string) error { return nil }
	defer func() { testIPFSVerify = nil }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "avatar.png")
	_, _ = fw.Write(testutil.CreateTestPNG())
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Decode the raw response to check gateway_url fields
	var envResp struct {
		Success bool `json:"success"`
		Data    struct {
			Avatar struct {
				IpfsCid         string `json:"ipfs_cid"`
				GatewayURL      string `json:"gateway_url"`
				LocalGatewayURL string `json:"local_gateway_url"`
			} `json:"avatar"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &envResp); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !envResp.Success {
		t.Fatal("expected success=true")
	}
	wantGateway := "https://ipfs.io/ipfs/CID_GATEWAY_TEST"
	if envResp.Data.Avatar.GatewayURL != wantGateway {
		t.Errorf("expected gateway_url=%q, got %q", wantGateway, envResp.Data.Avatar.GatewayURL)
	}
	wantLocal := "http://localhost:8080/ipfs/CID_GATEWAY_TEST"
	if envResp.Data.Avatar.LocalGatewayURL != wantLocal {
		t.Errorf("expected local_gateway_url=%q, got %q", wantLocal, envResp.Data.Avatar.LocalGatewayURL)
	}
}

// Test gateway_url is omitted when IPFSGatewayURLs is empty (no panic)
func TestUploadAvatar_EmptyGatewayURLsNoPanic(t *testing.T) {
	repo := newMockUserRepo()
	u := &domain.User{ID: "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", Username: "jane", Email: "j@e.com", Role: domain.RoleUser, IsActive: true, CreatedAt: time.Now(), UpdatedAt: time.Now()}
	repo.users[u.ID] = u

	s := NewServer(repo, nil, "test", nil, 0, "http://test-ipfs:5001", "", 0, nil)
	s.cfg = &config.Config{IPFSGatewayURLs: []string{}} // empty — should not panic

	testIPFSAdd = func(_ string) (string, error) { return "CID_NOGW", nil }
	defer func() { testIPFSAdd = nil }()
	testIPFSPin = func(_ string) error { return nil }
	defer func() { testIPFSPin = nil }()
	testEncodeToWebP = func(_, dst string) error { return nil }
	defer func() { testEncodeToWebP = nil }()
	testIPFSVerify = func(_ string) error { return nil }
	defer func() { testIPFSVerify = nil }()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, _ := mw.CreateFormFile("file", "avatar.png")
	_, _ = fw.Write(testutil.CreateTestPNG())
	_ = mw.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), u.ID))
	rr := httptest.NewRecorder()

	// Must not panic
	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	// gateway_url should not be present in response
	if strings.Contains(rr.Body.String(), "gateway_url") && strings.Contains(rr.Body.String(), "ipfs.io") {
		t.Error("expected no gateway_url with empty IPFSGatewayURLs")
	}
}

// Test directly that User.MarshalJSON emits gateway_url when Avatar has GatewayURL set
func TestUserMarshalJSON_AvatarGatewayURLs(t *testing.T) {
	u := domain.User{
		ID:       "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb",
		Username: "kate",
		Email:    "k@e.com",
		Role:     domain.RoleUser,
		IsActive: true,
		Avatar: &domain.Avatar{
			ID:              "avatar-1",
			IPFSCID:         sql.NullString{String: "bafyCID123", Valid: true},
			WebPIPFSCID:     sql.NullString{String: "bafyCIDWEBP", Valid: true},
			GatewayURL:      "https://ipfs.io/ipfs/bafyCID123",
			LocalGatewayURL: "http://localhost:8080/ipfs/bafyCID123",
		},
	}

	data, err := json.Marshal(u)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	s := string(data)
	if !strings.Contains(s, `"gateway_url":"https://ipfs.io/ipfs/bafyCID123"`) {
		t.Errorf("expected gateway_url in JSON, got: %s", s)
	}
	if !strings.Contains(s, `"local_gateway_url":"http://localhost:8080/ipfs/bafyCID123"`) {
		t.Errorf("expected local_gateway_url in JSON, got: %s", s)
	}
}

// Test that GIF extension is accepted
func TestUploadAvatar_GIFExtensionAccepted(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.gif")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("not-an-image")); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid image data, got %d body=%s", rr.Code, rr.Body.String())
	}

	bodyStr := strings.ToLower(rr.Body.String())
	if !strings.Contains(bodyStr, "invalid or corrupted image file") && !strings.Contains(bodyStr, "file validation failed") && !strings.Contains(bodyStr, "file content does not match") {
		t.Fatalf("expected image validation error, got %s", rr.Body.String())
	}
}

// Test that TIFF extension is accepted
func TestUploadAvatar_TIFFExtensionAccepted(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "avatar.tiff")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	if _, err := fw.Write([]byte("not-an-image")); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for invalid image data, got %d body=%s", rr.Code, rr.Body.String())
	}

	bodyStr := strings.ToLower(rr.Body.String())
	if !strings.Contains(bodyStr, "invalid or corrupted image file") && !strings.Contains(bodyStr, "file validation failed") && !strings.Contains(bodyStr, "file content does not match") {
		t.Fatalf("expected image validation error, got %s", rr.Body.String())
	}
}

// Test that non-image files are rejected (e.g., text file with image extension)
func TestUploadAvatar_NonImageFileRejected(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "document.jpg") // Image extension but not image content
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// Write plain text content that's not an image
	textContent := "This is a plain text document, not an image file at all. It should be rejected."
	if _, err := fw.Write([]byte(textContent)); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for non-image file, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Should fail with image decoding error
	bodyStr := strings.ToLower(rr.Body.String())
	if !strings.Contains(bodyStr, "invalid or corrupted image file") && !strings.Contains(bodyStr, "file validation failed") && !strings.Contains(bodyStr, "file content does not match") {
		t.Fatalf("expected image validation error, got %s", rr.Body.String())
	}
}

// Test that executable files are rejected even with image extension
func TestUploadAvatar_ExecutableFileRejected(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "malware.png") // PNG extension but executable content
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// Write ELF executable header (Linux executable)
	elfHeader := []byte{0x7f, 0x45, 0x4c, 0x46, 0x02, 0x01, 0x01, 0x00}
	if _, err := fw.Write(elfHeader); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for executable file, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Should fail with image decoding error since it's not a valid image
	bodyStr := strings.ToLower(rr.Body.String())
	if !strings.Contains(bodyStr, "invalid or corrupted image file") && !strings.Contains(bodyStr, "file validation failed") && !strings.Contains(bodyStr, "file content does not match") {
		t.Fatalf("expected image validation error, got %s", rr.Body.String())
	}
}

// Test that PDF files are rejected
func TestUploadAvatar_PDFFileRejected(t *testing.T) {
	s := NewServer(nil, nil, "test", nil, 0, "", "", 0, nil)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "document.pdf")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	// Write PDF header
	pdfHeader := []byte("%PDF-1.4\n%âãÏÓ\n")
	if _, err := fw.Write(pdfHeader); err != nil {
		t.Fatalf("write file bytes: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/v1/users/me/avatar", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(withUserID(req.Context(), "user-1"))
	rr := httptest.NewRecorder()

	s.UploadAvatar(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400 for PDF file, got %d body=%s", rr.Code, rr.Body.String())
	}

	// Should fail with unsupported image format or file validation error
	bodyStr := strings.ToLower(rr.Body.String())
	if !strings.Contains(bodyStr, "unsupported image format") && !strings.Contains(bodyStr, "file validation failed") && !strings.Contains(bodyStr, "file content does not match") {
		t.Fatalf("expected 'unsupported image format' error, got %s", rr.Body.String())
	}
}
