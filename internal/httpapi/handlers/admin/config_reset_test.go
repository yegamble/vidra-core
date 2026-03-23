package admin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockConfigRepo is a minimal configResetter for reset tests.
type mockConfigRepo struct {
	deleted         bool
	homepageContent string
}

func (m *mockConfigRepo) DeleteAllInstanceConfigs(_ context.Context) error {
	m.deleted = true
	return nil
}

func (m *mockConfigRepo) GetConfigValue(_ context.Context, key string) (string, error) {
	if key == "homepage_content" {
		return m.homepageContent, nil
	}
	return "", nil
}

func (m *mockConfigRepo) SetConfigValue(_ context.Context, key, value string) error {
	if key == "homepage_content" {
		m.homepageContent = value
	}
	return nil
}

func TestDeleteCustomConfig_OK(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewConfigResetHandlers(repo)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/config/custom", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.DeleteCustomConfig(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if !repo.deleted {
		t.Fatal("expected DeleteAllInstanceConfigs to be called")
	}
}

func TestGetCustomHomepage_OK(t *testing.T) {
	repo := &mockConfigRepo{homepageContent: "<h1>Hello</h1>"}
	h := NewConfigResetHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/custom-pages/homepage/instance", nil)
	rr := httptest.NewRecorder()
	h.GetCustomHomepage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "Hello") {
		t.Fatalf("expected homepage content in response, got: %s", rr.Body.String())
	}
}

func TestGetCustomHomepage_Empty(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewConfigResetHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/custom-pages/homepage/instance", nil)
	rr := httptest.NewRecorder()
	h.GetCustomHomepage(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
}

func TestUpdateCustomHomepage_OK(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewConfigResetHandlers(repo)

	body := `{"content":"<h1>Welcome</h1>"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/custom-pages/homepage/instance", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.UpdateCustomHomepage(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if repo.homepageContent != "<h1>Welcome</h1>" {
		t.Fatalf("expected content to be stored, got: %q", repo.homepageContent)
	}
}

func TestGetCustomConfig_ReturnsConfig(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewConfigResetHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/custom", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.GetCustomConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
	// Response must include "instance" section
	if !strings.Contains(rr.Body.String(), "instance") {
		t.Fatalf("expected 'instance' in response, got: %s", rr.Body.String())
	}
}

func TestUpdateCustomConfig_OK(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewConfigResetHandlers(repo)

	body := `{"instance":{"name":"My Instance","shortDescription":"desc"}}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/custom", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.UpdateCustomConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestUpdateCustomConfig_InvalidBody(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewConfigResetHandlers(repo)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/custom", strings.NewReader("not-json"))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.UpdateCustomConfig(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rr.Code)
	}
}
