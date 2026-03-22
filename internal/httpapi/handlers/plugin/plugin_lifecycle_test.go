package plugin

// Plugin lifecycle success-path tests: enable → disable → update config → get settings → uninstall.
// Existing tests (plugin_handlers_sqlmock_test.go) cover list, get, and branch-coverage for
// uninstall, statistics, execution history, and health. This file provides clean success-path
// assertions for:
//   - EnablePlugin (installed state → enabled)
//   - DisablePlugin (enabled state → disabled)
//   - UpdatePluginConfig (config update via SQL)
//   - GetRegisteredSettings (read settings from plugin record)
//   - UpdateCanonicalSettings (update via PeerTube-compatible route)
//   - UninstallPlugin success path (disabled plugin → deleted → 200)
//   - InstallPluginFromURL handler validation (URL must be https://)
//
// NOTE: The full install-from-URL success path (download ZIP + extract + create DB record)
// requires a real HTTPS server and is covered by integration tests. The handler enforces
// https:// to prevent SSRF, which blocks httptest (http://) servers from being used directly.

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"athena/internal/domain"
	coreplugin "athena/internal/plugin"
	"athena/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// stubPlugin is a minimal in-process plugin used for handler tests that need
// a manager with a registered plugin (Enable/Disable require a non-nil manager).
type stubPlugin struct {
	name        string
	version     string
	author      string
	description string
	enabled     bool
}

func newStubPlugin(name string) *stubPlugin {
	return &stubPlugin{name: name, version: "1.0.0", author: "test", description: "stub"}
}

func (p *stubPlugin) Name() string                                     { return p.name }
func (p *stubPlugin) Version() string                                  { return p.version }
func (p *stubPlugin) Author() string                                   { return p.author }
func (p *stubPlugin) Description() string                              { return p.description }
func (p *stubPlugin) Enabled() bool                                    { return p.enabled }
func (p *stubPlugin) SetEnabled(enabled bool)                          { p.enabled = enabled }
func (p *stubPlugin) Initialize(_ context.Context, _ map[string]any) error { return nil }
func (p *stubPlugin) Shutdown(_ context.Context) error                 { return nil }

// newLifecycleHandler creates a handler with both SQL mock repo and a plugin manager
// that has a named plugin registered (needed for Enable/Disable handlers).
func newLifecycleHandler(t *testing.T, pluginName string, initialStatus domain.PluginStatus) (*PluginHandler, sqlmock.Sqlmock, uuid.UUID, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	repo := repository.NewPluginRepository(sqlxDB)

	manager := coreplugin.NewManager(t.TempDir())
	stub := newStubPlugin(pluginName)

	// Pre-enable the stub if the test starts from enabled state.
	if initialStatus == domain.PluginStatusEnabled {
		stub.SetEnabled(true)
	}
	require.NoError(t, manager.RegisterPlugin(stub, map[string]any{"feature": true}))

	handler := NewPluginHandler(repo, manager, nil, false)

	pluginID := uuid.New()
	cleanup := func() { _ = sqlxDB.Close() }
	return handler, mock, pluginID, cleanup
}

func pluginRowForLifecycle(id uuid.UUID, name string, status domain.PluginStatus) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).AddRow(
		id,
		name,
		"1.0.0",
		"alice",
		"test plugin",
		status,
		[]byte(`{"feature":true}`),
		pq.Array([]string{"read_videos"}),
		pq.Array([]string{"video.uploaded"}),
		"/tmp/plugins/"+name,
		"abc123",
		now,
		now,
		nil,
		nil,
		"",
	)
}

// TestPluginLifecycle_Enable tests the success path for enabling an installed plugin.
func TestPluginLifecycle_Enable(t *testing.T) {
	pluginName := "lifecycle-plugin-enable"
	handler, mock, pluginID, cleanup := newLifecycleHandler(t, pluginName, domain.PluginStatusInstalled)
	defer cleanup()

	// GET plugin (resolvePluginRecord uses id param → GetByID)
	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
		WithArgs(pluginID).
		WillReturnRows(pluginRowForLifecycle(pluginID, pluginName, domain.PluginStatusInstalled))

	// UPDATE after enable
	mock.ExpectExec(regexp.QuoteMeta("UPDATE plugins")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/"+pluginID.String()+"/enable", nil)
	req = withPluginParam(req, "id", pluginID.String())
	rr := httptest.NewRecorder()
	handler.EnablePlugin(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, true, body["success"])
	data := body["data"].(map[string]any)
	assert.Equal(t, "success", data["status"])
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPluginLifecycle_Disable tests the success path for disabling an enabled plugin.
func TestPluginLifecycle_Disable(t *testing.T) {
	pluginName := "lifecycle-plugin-disable"
	handler, mock, pluginID, cleanup := newLifecycleHandler(t, pluginName, domain.PluginStatusEnabled)
	defer cleanup()

	// GET plugin — status is "enabled"
	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
		WithArgs(pluginID).
		WillReturnRows(pluginRowForLifecycle(pluginID, pluginName, domain.PluginStatusEnabled))

	// UPDATE after disable
	mock.ExpectExec(regexp.QuoteMeta("UPDATE plugins")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/"+pluginID.String()+"/disable", nil)
	req = withPluginParam(req, "id", pluginID.String())
	rr := httptest.NewRecorder()
	handler.DisablePlugin(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, true, body["success"])
	data := body["data"].(map[string]any)
	assert.Equal(t, "success", data["status"])
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPluginLifecycle_UpdateConfig tests the UpdatePluginConfig success path.
// The manager silently ignores "plugin not found" errors via isMissingRuntimePluginError,
// so this test only requires SQL mock expectations for get + update.
func TestPluginLifecycle_UpdateConfig(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	pluginID := uuid.New()

	// GET plugin by ID
	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
		WithArgs(pluginID).
		WillReturnRows(samplePluginRow(pluginID, domain.PluginStatusEnabled, "config-plugin"))

	// UPDATE after config change
	mock.ExpectExec(regexp.QuoteMeta("UPDATE plugins")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	body := strings.NewReader(`{"config":{"new_key":"new_value","enabled":true}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/"+pluginID.String()+"/config", body)
	req = withPluginParam(req, "id", pluginID.String())
	rr := httptest.NewRecorder()
	handler.UpdatePluginConfig(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
	data := resp["data"].(map[string]any)
	assert.Equal(t, "success", data["status"])
	assert.Equal(t, "Plugin configuration updated successfully", data["message"])
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPluginLifecycle_GetRegisteredSettings tests reading plugin settings (PeerTube route).
func TestPluginLifecycle_GetRegisteredSettings(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	pluginID := uuid.New()

	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE name = \\$1").
		WithArgs("settings-plugin").
		WillReturnRows(samplePluginRow(pluginID, domain.PluginStatusEnabled, "settings-plugin"))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/settings-plugin/registered-settings", nil)
	req = withPluginParam(req, "npmName", "settings-plugin")
	rr := httptest.NewRecorder()
	handler.GetRegisteredSettings(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
	data := resp["data"].(map[string]any)
	assert.Equal(t, "settings-plugin", data["npmName"])
	assert.NotNil(t, data["settings"])
	assert.NotNil(t, data["registeredSettings"])
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPluginLifecycle_UpdateCanonicalSettings tests PeerTube-compatible settings update.
func TestPluginLifecycle_UpdateCanonicalSettings(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	pluginID := uuid.New()

	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE name = \\$1").
		WithArgs("canonical-plugin").
		WillReturnRows(samplePluginRow(pluginID, domain.PluginStatusEnabled, "canonical-plugin"))

	mock.ExpectExec(regexp.QuoteMeta("UPDATE plugins")).
		WillReturnResult(sqlmock.NewResult(0, 1))

	body := strings.NewReader(`{"settings":{"theme":"dark","notifications":true}}`)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/plugins/canonical-plugin/settings", body)
	req = withPluginParam(req, "npmName", "canonical-plugin")
	rr := httptest.NewRecorder()
	handler.UpdateCanonicalSettings(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, true, resp["success"])
	data := resp["data"].(map[string]any)
	assert.Equal(t, "canonical-plugin", data["npmName"])
	assert.Equal(t, "Plugin settings updated successfully", data["message"])
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPluginLifecycle_Uninstall tests the success path for uninstalling a disabled plugin.
// Expects: GET plugin → DELETE from DB → 200 with success message.
func TestPluginLifecycle_Uninstall(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	pluginID := uuid.New()

	// GET plugin by ID — status disabled (uninstall of an enabled plugin requires disable first)
	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
		WithArgs(pluginID).
		WillReturnRows(samplePluginRow(pluginID, domain.PluginStatusDisabled, "uninstall-plugin"))

	// DELETE from plugins table
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM plugins WHERE id = $1")).
		WithArgs(pluginID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/plugins/"+pluginID.String(), nil)
	req = withPluginParam(req, "id", pluginID.String())
	rr := httptest.NewRecorder()
	handler.UninstallPlugin(rr, req)

	require.Equal(t, http.StatusOK, rr.Code, rr.Body.String())
	var body map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &body))
	assert.Equal(t, true, body["success"])
	data := body["data"].(map[string]any)
	assert.Equal(t, "success", data["status"])
	assert.Contains(t, data["message"].(string), "uninstalled successfully")
	require.NoError(t, mock.ExpectationsWereMet())
}

// TestPluginLifecycle_InstallFromURL_RequiresHTTPS validates that the InstallPluginFromURL
// handler enforces HTTPS to prevent SSRF. The full install success path (download + extract +
// DB insert) requires a real HTTPS server and is covered by integration tests only.
func TestPluginLifecycle_InstallFromURL_RequiresHTTPS(t *testing.T) {
	handler, _, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	// http:// URL should be rejected with 400
	body := strings.NewReader(`{"pluginURL":"http://example.com/plugin.zip"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/install", body)
	rr := httptest.NewRecorder()
	handler.InstallPluginFromURL(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"])
}

// TestPluginLifecycle_InstallFromURL_MissingURL validates the handler rejects empty pluginURL.
func TestPluginLifecycle_InstallFromURL_MissingURL(t *testing.T) {
	handler, _, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	body := strings.NewReader(`{}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/install", body)
	rr := httptest.NewRecorder()
	handler.InstallPluginFromURL(rr, req)

	require.Equal(t, http.StatusBadRequest, rr.Code, rr.Body.String())
	var resp map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &resp))
	assert.Equal(t, false, resp["success"])
}
