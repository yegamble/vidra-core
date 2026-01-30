package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/plugin"
	"athena/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockPlugin implements plugin.Plugin interface for testing
type MockPlugin struct {
	name        string
	version     string
	author      string
	description string
	enabled     bool
	initCalled  bool
	closeCalled bool
	config      map[string]any
}

func NewMockPlugin(name, version string) *MockPlugin {
	return &MockPlugin{
		name:        name,
		version:     version,
		author:      "Test Author",
		description: "Test Description",
		enabled:     false,
	}
}

func (m *MockPlugin) Name() string        { return m.name }
func (m *MockPlugin) Version() string     { return m.version }
func (m *MockPlugin) Author() string      { return m.author }
func (m *MockPlugin) Description() string { return m.description }

func (m *MockPlugin) Initialize(ctx context.Context, config map[string]any) error {
	m.initCalled = true
	m.config = config
	return nil
}

func (m *MockPlugin) Shutdown(ctx context.Context) error {
	m.closeCalled = true
	return nil
}

func (m *MockPlugin) Enabled() bool {
	return m.enabled
}

func (m *MockPlugin) SetEnabled(enabled bool) {
	m.enabled = enabled
}

func setupTestEnv(t *testing.T) (*PluginHandler, sqlmock.Sqlmock, *plugin.Manager, *repository.PluginRepository) {
	// Setup sqlmock
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	// Create sqlx DB
	sqlxDB := sqlx.NewDb(db, "sqlmock")

	// Create repository
	repo := repository.NewPluginRepository(sqlxDB)

	// Create PluginManager with temp dir
	tempDir := t.TempDir()
	manager := plugin.NewManager(tempDir)

	// Initialize manager (creates directories)
	err = manager.Initialize(context.Background())
	require.NoError(t, err)

	// Create handler
	handler := NewPluginHandler(repo, manager, nil, false)

	return handler, mock, manager, repo
}

func TestPluginHandler_ListPlugins(t *testing.T) {
	handler, mock, _, _ := setupTestEnv(t)

	// Create dummy plugins
	p1ID := uuid.New()
	p2ID := uuid.New()

	now := time.Now()

	// Mock List query
	rows := sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).
	AddRow(
		p1ID, "plugin-1", "1.0.0", "author1", "desc1", domain.PluginStatusInstalled, []byte("{}"),
		pq.Array([]string{}), pq.Array([]string{}), "/path/1", "sum1",
		now, now, nil, nil, "",
	).
	AddRow(
		p2ID, "plugin-2", "2.0.0", "author2", "desc2", domain.PluginStatusEnabled, []byte("{}"),
		pq.Array([]string{}), pq.Array([]string{}), "/path/2", "sum2",
		now, now, nil, nil, "",
	)

	mock.ExpectQuery("(?s)SELECT .* FROM plugins").WillReturnRows(rows)

	// Mock GetStatistics query
	statsRows := sqlmock.NewRows([]string{
		"plugin_id", "plugin_name", "total_executions", "success_count",
		"failure_count", "avg_duration_ms", "last_executed_at",
	}).
	AddRow(p1ID, "plugin-1", 10, 8, 2, 100.0, now).
	AddRow(p2ID, "plugin-2", 20, 19, 1, 50.0, now)

	mock.ExpectQuery("(?s)SELECT .* FROM plugin_statistics").WillReturnRows(statsRows)

	req, _ := http.NewRequest("GET", "/api/v1/admin/plugins", nil)
	w := httptest.NewRecorder()

	handler.ListPlugins(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response []map[string]any
	// Check if wrapped in data/success
	var wrapped struct {
		Data    []map[string]any `json:"data"`
		Success bool             `json:"success"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrapped); err == nil && wrapped.Data != nil {
		response = wrapped.Data
	} else {
		// Try unwrapped
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
	}

	assert.Len(t, response, 2)
	assert.Equal(t, "plugin-1", response[0]["name"])
	assert.Equal(t, "plugin-2", response[1]["name"])

	// Check statistics presence
	stats1 := response[0]["statistics"].(map[string]any)
	assert.Equal(t, 10.0, stats1["total_executions"])
}

func TestPluginHandler_GetPlugin(t *testing.T) {
	handler, mock, _, _ := setupTestEnv(t)

	pluginID := uuid.New()
	now := time.Now()

	// Mock GetByID query
	rows := sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).
	AddRow(
		pluginID, "test-plugin", "1.0.0", "author", "desc", domain.PluginStatusInstalled, []byte(`{"key":"val"}`),
		pq.Array([]string{"read_videos"}), pq.Array([]string{}), "/path", "sum",
		now, now, nil, nil, "",
	)

	mock.ExpectQuery("SELECT .* FROM plugins WHERE id =").
		WithArgs(pluginID).
		WillReturnRows(rows)

	// Mock stats query (single plugin)
	mock.ExpectQuery("SELECT .* FROM plugin_statistics WHERE plugin_id =").
		WithArgs(pluginID).
		WillReturnRows(sqlmock.NewRows([]string{
			"plugin_id", "plugin_name", "total_executions", "success_count",
			"failure_count", "avg_duration_ms", "last_executed_at",
		}).AddRow(pluginID, "test-plugin", 5, 5, 0, 20.0, now))

	// Mock health query
	mock.ExpectQuery("SELECT .* FROM get_plugin_health").
		WithArgs(pluginID).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "status", "success_rate", "avg_duration_ms", "last_executed_at",
		}).AddRow(pluginID, "test-plugin", domain.PluginStatusInstalled, 1.0, 20.0, now))

	req, _ := http.NewRequest("GET", "/api/v1/admin/plugins/"+pluginID.String(), nil)

	// Inject chi URL param
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", pluginID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.GetPlugin(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response map[string]any
	// Check if wrapped in data/success
	var wrapped struct {
		Data    map[string]any `json:"data"`
		Success bool           `json:"success"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &wrapped); err == nil && wrapped.Data != nil {
		response = wrapped.Data
	} else {
		err = json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)
	}

	assert.Equal(t, pluginID.String(), response["id"])
	assert.Equal(t, "test-plugin", response["name"])
	config := response["config"].(map[string]any)
	assert.Equal(t, "val", config["key"])
}

func TestPluginHandler_EnablePlugin(t *testing.T) {
	handler, mock, manager, _ := setupTestEnv(t)

	pluginID := uuid.New()
	pluginName := "mock-plugin"

	// Register mock plugin with manager
	mockPlugin := NewMockPlugin(pluginName, "1.0.0")
	err := manager.RegisterPlugin(mockPlugin, nil)
	require.NoError(t, err)

	// Mock GetByID query (check current status)
	rows := sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).
	AddRow(
		pluginID, pluginName, "1.0.0", "author", "desc", domain.PluginStatusInstalled, []byte("{}"),
		pq.Array([]string{}), pq.Array([]string{}), "/path", "sum",
		time.Now(), time.Now(), nil, nil, "",
	)

	mock.ExpectQuery("SELECT .* FROM plugins WHERE id =").
		WithArgs(pluginID).
		WillReturnRows(rows)

	// Mock repo.Update(plugin)
	mock.ExpectExec("UPDATE plugins SET .* WHERE id =").
		WithArgs(
			"1.0.0", "author", "desc", domain.PluginStatusEnabled,
			sqlmock.AnyArg(), // config JSON
			sqlmock.AnyArg(), // permissions array
			sqlmock.AnyArg(), // hooks array
			sqlmock.AnyArg(), // enabled_at
			sqlmock.AnyArg(), // disabled_at
			sqlmock.AnyArg(), // last_error
			sqlmock.AnyArg(), // updated_at
			pluginID,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req, _ := http.NewRequest("PUT", "/api/v1/admin/plugins/"+pluginID.String()+"/enable", nil)

	// Inject chi URL param
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", pluginID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.EnablePlugin(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mockPlugin.initCalled, "Plugin Initialize should have been called")
	assert.True(t, mockPlugin.Enabled(), "Plugin should be enabled in manager")
}

func TestPluginHandler_DisablePlugin(t *testing.T) {
	handler, mock, manager, _ := setupTestEnv(t)

	pluginID := uuid.New()
	pluginName := "mock-plugin"

	// Register and enable mock plugin
	mockPlugin := NewMockPlugin(pluginName, "1.0.0")
	err := manager.RegisterPlugin(mockPlugin, nil)
	require.NoError(t, err)
	_ = manager.EnablePlugin(context.Background(), pluginName)

	// Mock GetByID query (check current status - must be enabled)
	rows := sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).
	AddRow(
		pluginID, pluginName, "1.0.0", "author", "desc", domain.PluginStatusEnabled, []byte("{}"),
		pq.Array([]string{}), pq.Array([]string{}), "/path", "sum",
		time.Now(), time.Now(), time.Now(), nil, "",
	)

	mock.ExpectQuery("SELECT .* FROM plugins WHERE id =").
		WithArgs(pluginID).
		WillReturnRows(rows)

	// Mock repo.Update(plugin)
	mock.ExpectExec("UPDATE plugins SET .* WHERE id =").
		WithArgs(
			"1.0.0", "author", "desc", domain.PluginStatusDisabled,
			sqlmock.AnyArg(), // config JSON
			sqlmock.AnyArg(), // permissions array
			sqlmock.AnyArg(), // hooks array
			sqlmock.AnyArg(), // enabled_at
			sqlmock.AnyArg(), // disabled_at
			sqlmock.AnyArg(), // last_error
			sqlmock.AnyArg(), // updated_at
			pluginID,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req, _ := http.NewRequest("PUT", "/api/v1/admin/plugins/"+pluginID.String()+"/disable", nil)

	// Inject chi URL param
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", pluginID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.DisablePlugin(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.True(t, mockPlugin.closeCalled, "Plugin Shutdown should have been called")
	assert.False(t, mockPlugin.Enabled(), "Plugin should be disabled in manager")
}

func TestPluginHandler_UninstallPlugin(t *testing.T) {
	handler, mock, _, _ := setupTestEnv(t)

	pluginID := uuid.New()
	pluginName := "test-plugin"

	// Mock GetByID
	rows := sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).
	AddRow(
		pluginID, pluginName, "1.0.0", "author", "desc", domain.PluginStatusInstalled, []byte("{}"),
		pq.Array([]string{}), pq.Array([]string{}), "/path", "sum",
		time.Now(), time.Now(), nil, nil, "",
	)

	mock.ExpectQuery("SELECT .* FROM plugins WHERE id =").
		WithArgs(pluginID).
		WillReturnRows(rows)

	// Mock Delete
	mock.ExpectExec("DELETE FROM plugins WHERE id =").
		WithArgs(pluginID).
		WillReturnResult(sqlmock.NewResult(1, 1))

	req, _ := http.NewRequest("DELETE", "/api/v1/admin/plugins/"+pluginID.String(), nil)

	// Inject chi URL param
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", pluginID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.UninstallPlugin(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
}

func TestPluginHandler_UpdatePluginConfig(t *testing.T) {
	handler, mock, manager, _ := setupTestEnv(t)

	pluginID := uuid.New()
	pluginName := "mock-plugin"

	// Register mock plugin
	mockPlugin := NewMockPlugin(pluginName, "1.0.0")
	_ = manager.RegisterPlugin(mockPlugin, map[string]any{"key": "old"})

	// Mock GetByID
	rows := sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).
	AddRow(
		pluginID, pluginName, "1.0.0", "author", "desc", domain.PluginStatusInstalled, []byte(`{"key":"old"}`),
		pq.Array([]string{}), pq.Array([]string{}), "/path", "sum",
		time.Now(), time.Now(), nil, nil, "",
	)

	mock.ExpectQuery("SELECT .* FROM plugins WHERE id =").
		WithArgs(pluginID).
		WillReturnRows(rows)

	// Mock Update
	mock.ExpectExec("UPDATE plugins SET .* WHERE id =").
		WithArgs(
			"1.0.0", "author", "desc", domain.PluginStatusInstalled,
			sqlmock.AnyArg(), // new config
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			pluginID,
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	// Request body with new config
	reqBody := `{"config": {"key": "new"}}`
	req, _ := http.NewRequest("PUT", "/api/v1/admin/plugins/"+pluginID.String()+"/config", strings.NewReader(reqBody))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", pluginID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler.UpdatePluginConfig(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	// Verify config updated in manager
	info, err := manager.GetPluginInfo(pluginName)
	require.NoError(t, err)
	assert.Equal(t, "new", info.Config["key"])
}
