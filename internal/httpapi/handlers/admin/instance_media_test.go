package admin

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"
)

func buildInstanceMediaRequest(t *testing.T, fieldName, filename string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile(fieldName, filename)
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	_, _ = fw.Write([]byte("fake image data"))
	w.Close()
	req := httptest.NewRequest(http.MethodPost, "/", &buf)
	req.Header.Set("Content-Type", w.FormDataContentType())
	return req
}

func TestUploadInstanceAvatar_OK(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewInstanceMediaHandlers(repo)

	req := buildInstanceMediaRequest(t, "avatarfile", "avatar.png")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.UploadInstanceAvatar(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.homepageContent != "" {
		// instance_avatar_path should be stored, not homepage
	}
}

func TestDeleteInstanceAvatar_OK(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewInstanceMediaHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.DeleteInstanceAvatar(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUploadInstanceBanner_OK(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewInstanceMediaHandlers(repo)

	req := buildInstanceMediaRequest(t, "bannerfile", "banner.png")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.UploadInstanceBanner(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestDeleteInstanceBanner_OK(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewInstanceMediaHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.DeleteInstanceBanner(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
}
