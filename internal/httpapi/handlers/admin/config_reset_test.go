package admin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// mockConfigRepo is a minimal configResetter for reset tests.
// Stores every SetConfigValue in an internal map so round-trip tests can observe
// the full set of persisted keys.
type mockConfigRepo struct {
	deleted         bool
	homepageContent string
	values          map[string]string
}

func (m *mockConfigRepo) DeleteAllInstanceConfigs(_ context.Context) error {
	m.deleted = true
	m.values = map[string]string{}
	return nil
}

func (m *mockConfigRepo) GetConfigValue(_ context.Context, key string) (string, error) {
	if key == "homepage_content" {
		return m.homepageContent, nil
	}
	if m.values != nil {
		if v, ok := m.values[key]; ok {
			return v, nil
		}
	}
	return "", nil
}

func (m *mockConfigRepo) SetConfigValue(_ context.Context, key, value string) error {
	if key == "homepage_content" {
		m.homepageContent = value
		return nil
	}
	if m.values == nil {
		m.values = map[string]string{}
	}
	m.values[key] = value
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

// TestUpdateCustomConfig_RoundTrip verifies every expanded field is persisted and
// returned via a subsequent GET. This is the regression gate for the silent
// field-drop bug where the frontend sent transcoding/live/user/import but the
// backend dropped everything except instance.name/description and signup.enabled.
func TestUpdateCustomConfig_RoundTrip(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewConfigResetHandlers(repo)

	body := `{
		"instance": {
			"name": "My Instance",
			"shortDescription": "short",
			"description": "long desc",
			"terms": "tos",
			"isNSFW": true,
			"defaultNSFWPolicy": "do_not_list"
		},
		"signup": {
			"enabled": false,
			"requiresEmailVerification": false,
			"limit": 100
		},
		"user": {
			"videoQuota": 1073741824,
			"videoQuotaDaily": 10485760
		},
		"transcoding": {
			"enabled": true,
			"resolutions": {"720p": true, "1080p": true, "1440p": false}
		},
		"live": {
			"enabled": false,
			"maxDuration": 3600
		},
		"import": {
			"videos": {
				"http": {"enabled": true},
				"torrent": {"enabled": true}
			}
		}
	}`

	req := httptest.NewRequest(http.MethodPut, "/api/v1/config/custom", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.UpdateCustomConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}

	// GET returns what was PUT.
	getReq := httptest.NewRequest(http.MethodGet, "/api/v1/config/custom", nil)
	getReq = withAdminContext(getReq, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	getRR := httptest.NewRecorder()
	h.GetCustomConfig(getRR, getReq)

	if getRR.Code != http.StatusOK {
		t.Fatalf("GET: expected 200, got %d: %s", getRR.Code, getRR.Body.String())
	}

	var envelope struct {
		Data    customConfigBody `json:"data"`
		Success bool             `json:"success"`
	}
	if err := json.NewDecoder(getRR.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data

	if got.Instance.Name != "My Instance" {
		t.Errorf("instance.name: got %q, want %q", got.Instance.Name, "My Instance")
	}
	if got.Instance.Terms != "tos" {
		t.Errorf("instance.terms: got %q, want %q", got.Instance.Terms, "tos")
	}
	if got.Instance.DefaultNSFWPolicy != "do_not_list" {
		t.Errorf("instance.defaultNSFWPolicy: got %q", got.Instance.DefaultNSFWPolicy)
	}
	if !got.Instance.IsNSFW {
		t.Errorf("instance.isNSFW: expected true")
	}

	if got.Signup.Enabled {
		t.Errorf("signup.enabled: expected false")
	}
	if got.Signup.RequiresEmailVerification {
		t.Errorf("signup.requiresEmailVerification: expected false")
	}
	if got.Signup.Limit != 100 {
		t.Errorf("signup.limit: got %d, want 100", got.Signup.Limit)
	}

	if got.User.VideoQuota != 1073741824 {
		t.Errorf("user.videoQuota: got %d", got.User.VideoQuota)
	}
	if got.User.VideoQuotaDaily != 10485760 {
		t.Errorf("user.videoQuotaDaily: got %d", got.User.VideoQuotaDaily)
	}

	if !got.Transcoding.Enabled {
		t.Errorf("transcoding.enabled: expected true")
	}
	if got.Transcoding.Resolutions["720p"] != true {
		t.Errorf("transcoding.resolutions[720p]: expected true")
	}
	if got.Transcoding.Resolutions["1440p"] != false {
		t.Errorf("transcoding.resolutions[1440p]: expected false")
	}

	if got.Live.Enabled {
		t.Errorf("live.enabled: expected false")
	}
	if got.Live.MaxDuration != 3600 {
		t.Errorf("live.maxDuration: got %d, want 3600", got.Live.MaxDuration)
	}

	if !got.Import.Videos.HTTP.Enabled {
		t.Errorf("import.videos.http.enabled: expected true")
	}
	if !got.Import.Videos.Torrent.Enabled {
		t.Errorf("import.videos.torrent.enabled: expected true")
	}
}

// TestGetCustomConfig_EmptyDefaults verifies sensible defaults are returned when
// no config has ever been written. This is the first-boot / reset path.
func TestGetCustomConfig_EmptyDefaults(t *testing.T) {
	repo := &mockConfigRepo{}
	h := NewConfigResetHandlers(repo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/config/custom", nil)
	req = withAdminContext(req, "cccccccc-cccc-cccc-cccc-cccccccccccc")
	rr := httptest.NewRecorder()
	h.GetCustomConfig(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var envelope struct {
		Data    customConfigBody `json:"data"`
		Success bool             `json:"success"`
	}
	if err := json.NewDecoder(rr.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode: %v", err)
	}
	got := envelope.Data

	// Defaults that match documented behaviour.
	if got.Instance.DefaultNSFWPolicy != "blur" {
		t.Errorf("default NSFW policy: got %q, want %q", got.Instance.DefaultNSFWPolicy, "blur")
	}
	if !got.Signup.Enabled {
		t.Errorf("signup.enabled: expected true by default")
	}
	if !got.Signup.RequiresEmailVerification {
		t.Errorf("signup.requiresEmailVerification: expected true by default")
	}
	if got.User.VideoQuota != 50 {
		t.Errorf("user.videoQuota: got %d, want 50", got.User.VideoQuota)
	}
	if !got.Transcoding.Enabled {
		t.Errorf("transcoding.enabled: expected true by default")
	}
	if got.Transcoding.Resolutions == nil {
		t.Errorf("transcoding.resolutions: expected non-nil map")
	}
	if !got.Live.Enabled {
		t.Errorf("live.enabled: expected true by default")
	}
	if !got.Import.Videos.HTTP.Enabled {
		t.Errorf("import.http.enabled: expected true by default")
	}
}
