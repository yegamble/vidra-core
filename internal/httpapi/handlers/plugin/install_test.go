package plugin

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
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

	require.Equal(t, http.StatusNoContent, rr.Code, rr.Body.String())
	require.Equal(t, "https://example.com/my-plugin.zip", installer.installedURL)
}

func TestInstallPlugin_MissingURL(t *testing.T) {
	installer := &mockPluginInstaller{}
	h := NewPluginInstallHandlers(installer)

	body := `{}`
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/install", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.InstallPlugin(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
}

func TestListAvailablePlugins_OK(t *testing.T) {
	installer := &mockPluginInstaller{}
	h := NewPluginInstallHandlers(installer)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/available", nil)
	rr := httptest.NewRecorder()
	h.ListAvailablePlugins(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
}

func TestInstallPlugin_HTTPUrl(t *testing.T) {
	installer := &mockPluginInstaller{}
	h := NewPluginInstallHandlers(installer)

	body := `{"pluginURL":"http://example.com/plugin.zip"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/install", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.InstallPlugin(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for http URL")
}

func TestInstallPlugin_FTPUrl(t *testing.T) {
	installer := &mockPluginInstaller{}
	h := NewPluginInstallHandlers(installer)

	body := `{"pluginURL":"ftp://example.com/plugin.zip"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/install", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.InstallPlugin(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for ftp URL")
}

func TestInstallPlugin_InvalidJSON(t *testing.T) {
	installer := &mockPluginInstaller{}
	h := NewPluginInstallHandlers(installer)

	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/install", strings.NewReader("{bad json"))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.InstallPlugin(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, "expected 400 for invalid JSON")
}

func TestInstallPlugin_ServiceError(t *testing.T) {
	installer := &mockPluginInstaller{err: context.DeadlineExceeded}
	h := NewPluginInstallHandlers(installer)

	body := `{"pluginURL":"https://example.com/plugin.zip"}`
	req := httptest.NewRequest(http.MethodPost, "/admin/plugins/install", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h.InstallPlugin(rr, req)

	require.Equal(t, http.StatusInternalServerError, rr.Code, "expected 500 for service error")
}

func TestListAvailablePlugins_ResponseShape(t *testing.T) {
	installer := &mockPluginInstaller{}
	h := NewPluginInstallHandlers(installer)

	req := httptest.NewRequest(http.MethodGet, "/admin/plugins/available", nil)
	rr := httptest.NewRecorder()
	h.ListAvailablePlugins(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	require.NotEmpty(t, rr.Header().Get("Content-Type"), "expected Content-Type header")
}
