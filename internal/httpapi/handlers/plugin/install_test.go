package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type mockPluginInstaller struct {
	installedURL string
	err          error
}

func (m *mockPluginInstaller) InstallFromURL(_ context.Context, url string) error {
	m.installedURL = url
	return m.err
}

func TestInstallPlugin_OK(t *testing.T) {
	installer := &mockPluginInstaller{}
	h := NewPluginInstallHandlers(installer)

	body := `{"pluginURL":"https://example.com/my-plugin.zip"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/install", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.InstallPlugin(rr, req)

	if rr.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", rr.Code, rr.Body.String())
	}
	if installer.installedURL != "https://example.com/my-plugin.zip" {
		t.Fatalf("expected installedURL=https://example.com/my-plugin.zip, got %q", installer.installedURL)
	}
}

func TestInstallPlugin_MissingURL(t *testing.T) {
	installer := &mockPluginInstaller{}
	h := NewPluginInstallHandlers(installer)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/install", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.InstallPlugin(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", rr.Code, rr.Body.String())
	}
}

func TestListAvailablePlugins_OK(t *testing.T) {
	installer := &mockPluginInstaller{}
	h := NewPluginInstallHandlers(installer)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/available", nil)
	rr := httptest.NewRecorder()
	h.ListAvailablePlugins(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rr.Code, rr.Body.String())
	}
}
