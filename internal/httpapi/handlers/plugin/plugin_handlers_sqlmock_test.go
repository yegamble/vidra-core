package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/domain"
	coreplugin "vidra-core/internal/plugin"
	"vidra-core/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newSQLMockPluginHandler(t *testing.T) (*PluginHandler, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	sqlxDB := sqlx.NewDb(db, "sqlmock")
	repo := repository.NewPluginRepository(sqlxDB)
	handler := NewPluginHandler(repo, nil, nil, false)
	cleanup := func() {
		_ = sqlxDB.Close()
	}
	return handler, mock, cleanup
}

func samplePluginRow(recordID uuid.UUID, status domain.PluginStatus, name string) *sqlmock.Rows {
	now := time.Now()
	return sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).AddRow(
		recordID,
		name,
		"1.0.0",
		"alice",
		"plugin description",
		status,
		[]byte(`{"feature":true}`),
		pq.Array([]string{"read_videos"}),
		pq.Array([]string{"video.uploaded"}),
		"/tmp/plugins/"+name,
		"sum",
		now,
		now,
		nil,
		nil,
		"",
	)
}

func TestPluginHandler_ListPlugins_SQLMockSuccess(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	id1 := uuid.New()
	id2 := uuid.New()

	listRows := sqlmock.NewRows([]string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}).AddRow(
		id1, "plugin-a", "1.0.0", "alice", "desc", domain.PluginStatusInstalled, []byte("{}"),
		pq.Array([]string{"read_videos"}), pq.Array([]string{"video.uploaded"}), "/tmp/a", "sum", time.Now(), time.Now(), nil, nil, "",
	).AddRow(
		id2, "plugin-b", "1.1.0", "bob", "desc2", domain.PluginStatusDisabled, []byte("{}"),
		pq.Array([]string{"read_users"}), pq.Array([]string{"user.registered"}), "/tmp/b", "sum2", time.Now(), time.Now(), nil, nil, "",
	)

	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*ORDER BY name ASC").
		WillReturnRows(listRows)

	statsRows := sqlmock.NewRows([]string{
		"plugin_id", "plugin_name", "total_executions", "success_count",
		"failure_count", "avg_duration_ms", "last_executed_at",
	}).AddRow(
		id1, "plugin-a", int64(10), int64(9), int64(1), float64(12.3), time.Now(),
	).AddRow(
		id2, "plugin-b", int64(4), int64(3), int64(1), float64(7.8), time.Now(),
	)

	mock.ExpectQuery("(?s)SELECT plugin_id, plugin_name, total_executions, success_count,.*FROM plugin_statistics.*IN").
		WillReturnRows(statsRows)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins", nil)
	rr := httptest.NewRecorder()
	handler.ListPlugins(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &payload))
	assert.Equal(t, true, payload["success"])

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginHandler_GetPlugin_SQLMockSuccess(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	pluginID := uuid.New()

	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
		WithArgs(pluginID).
		WillReturnRows(samplePluginRow(pluginID, domain.PluginStatusInstalled, "plugin-a"))

	mock.ExpectQuery("(?s)SELECT plugin_id, plugin_name, total_executions, success_count,.*FROM plugin_statistics.*WHERE plugin_id = \\$1").
		WithArgs(pluginID).
		WillReturnRows(sqlmock.NewRows([]string{
			"plugin_id", "plugin_name", "total_executions", "success_count",
			"failure_count", "avg_duration_ms", "last_executed_at",
		}).AddRow(pluginID, "plugin-a", int64(5), int64(4), int64(1), float64(10.0), time.Now()))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM get_plugin_health($1)")).
		WithArgs(pluginID).
		WillReturnRows(sqlmock.NewRows([]string{
			"plugin_id", "plugin_name", "status", "success_rate", "avg_duration_ms", "last_executed_at",
		}).AddRow(pluginID, "plugin-a", domain.PluginStatusInstalled, float64(80), float64(12.5), time.Now()))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/"+pluginID.String(), nil)
	req = withPluginParam(req, "id", pluginID.String())
	rr := httptest.NewRecorder()
	handler.GetPlugin(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &payload))
	assert.Equal(t, true, payload["success"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginHandler_GetPlugin_ByName_SQLMockSuccess(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	pluginID := uuid.New()

	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE name = \\$1").
		WithArgs("plugin-a").
		WillReturnRows(samplePluginRow(pluginID, domain.PluginStatusInstalled, "plugin-a"))

	mock.ExpectQuery("(?s)SELECT plugin_id, plugin_name, total_executions, success_count,.*FROM plugin_statistics.*WHERE plugin_id = \\$1").
		WithArgs(pluginID).
		WillReturnRows(sqlmock.NewRows([]string{
			"plugin_id", "plugin_name", "total_executions", "success_count",
			"failure_count", "avg_duration_ms", "last_executed_at",
		}).AddRow(pluginID, "plugin-a", int64(5), int64(4), int64(1), float64(10.0), time.Now()))

	mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM get_plugin_health($1)")).
		WithArgs(pluginID).
		WillReturnRows(sqlmock.NewRows([]string{
			"plugin_id", "plugin_name", "status", "success_rate", "avg_duration_ms", "last_executed_at",
		}).AddRow(pluginID, "plugin-a", domain.PluginStatusInstalled, float64(80), float64(12.5), time.Now()))

	req := httptest.NewRequest(http.MethodGet, "/api/v1/plugins/plugin-a", nil)
	req = withPluginParam(req, "npmName", "plugin-a")
	rr := httptest.NewRecorder()
	handler.GetPlugin(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &payload))
	assert.Equal(t, true, payload["success"])
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginHandler_StatisticsAndCleanup_SQLMockSuccess(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	t.Run("GetAllStatistics", func(t *testing.T) {
		mock.ExpectQuery("(?s)SELECT plugin_id, plugin_name, total_executions, success_count,.*FROM plugin_statistics.*ORDER BY plugin_name ASC").
			WillReturnRows(sqlmock.NewRows([]string{
				"plugin_id", "plugin_name", "total_executions", "success_count",
				"failure_count", "avg_duration_ms", "last_executed_at",
			}).AddRow(uuid.New(), "plugin-a", int64(2), int64(2), int64(0), float64(5.0), time.Now()))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/statistics", nil)
		rr := httptest.NewRecorder()
		handler.GetAllStatistics(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("CleanupExecutions", func(t *testing.T) {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT cleanup_old_plugin_executions()")).
			WillReturnRows(sqlmock.NewRows([]string{"cleanup_old_plugin_executions"}).AddRow(int64(12)))

		req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/plugins/cleanup", nil)
		rr := httptest.NewRecorder()
		handler.CleanupExecutions(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)
	})

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginHandler_ToggleAndUninstall_SQLMockBranches(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	enabledID := uuid.New()
	disabledID := uuid.New()
	uninstallID := uuid.New()

	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
		WithArgs(enabledID).
		WillReturnRows(samplePluginRow(enabledID, domain.PluginStatusEnabled, "plugin-enabled"))

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/"+enabledID.String()+"/enable", nil)
	req = withPluginParam(req, "id", enabledID.String())
	rr := httptest.NewRecorder()
	handler.EnablePlugin(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
		WithArgs(disabledID).
		WillReturnRows(samplePluginRow(disabledID, domain.PluginStatusDisabled, "plugin-disabled"))

	req = httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/"+disabledID.String()+"/disable", nil)
	req = withPluginParam(req, "id", disabledID.String())
	rr = httptest.NewRecorder()
	handler.DisablePlugin(rr, req)
	require.Equal(t, http.StatusBadRequest, rr.Code)

	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
		WithArgs(uninstallID).
		WillReturnRows(samplePluginRow(uninstallID, domain.PluginStatusDisabled, "plugin-uninstall"))
	mock.ExpectExec(regexp.QuoteMeta("DELETE FROM plugins WHERE id = $1")).
		WithArgs(uninstallID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	req = httptest.NewRequest(http.MethodDelete, "/api/v1/admin/plugins/"+uninstallID.String(), nil)
	req = withPluginParam(req, "id", uninstallID.String())
	rr = httptest.NewRecorder()
	handler.UninstallPlugin(rr, req)
	require.Equal(t, http.StatusOK, rr.Code)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginHandler_UpdateConfig_NotFound_SQLMock(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	pluginID := uuid.New()
	mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
		WithArgs(pluginID).
		WillReturnError(domain.ErrPluginNotFound)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/"+pluginID.String()+"/config", strings.NewReader(`{"config":{"a":1}}`))
	req = withPluginParam(req, "id", pluginID.String())
	rr := httptest.NewRecorder()
	handler.UpdatePluginConfig(rr, req)
	require.Equal(t, http.StatusNotFound, rr.Code)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginHandler_StatisticsHistoryHealth_SQLMockBranches(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	pluginID := uuid.New()

	t.Run("GetPluginStatistics success", func(t *testing.T) {
		mock.ExpectQuery("(?s)SELECT plugin_id, plugin_name, total_executions, success_count,.*FROM plugin_statistics.*WHERE plugin_id = \\$1").
			WithArgs(pluginID).
			WillReturnRows(sqlmock.NewRows([]string{
				"plugin_id", "plugin_name", "total_executions", "success_count",
				"failure_count", "avg_duration_ms", "last_executed_at",
			}).AddRow(pluginID, "plugin-a", int64(7), int64(6), int64(1), float64(9.5), time.Now()))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/"+pluginID.String()+"/statistics", nil)
		req = withPluginParam(req, "id", pluginID.String())
		rr := httptest.NewRecorder()
		handler.GetPluginStatistics(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("GetPluginStatistics repository error", func(t *testing.T) {
		mock.ExpectQuery("(?s)SELECT plugin_id, plugin_name, total_executions, success_count,.*FROM plugin_statistics.*WHERE plugin_id = \\$1").
			WithArgs(pluginID).
			WillReturnError(assert.AnError)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/"+pluginID.String()+"/statistics", nil)
		req = withPluginParam(req, "id", pluginID.String())
		rr := httptest.NewRecorder()
		handler.GetPluginStatistics(rr, req)

		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("GetExecutionHistory success", func(t *testing.T) {
		execID := uuid.New()
		mock.ExpectQuery("(?s)SELECT id, plugin_id, plugin_name, hook_type, event_data,.*FROM plugin_hook_executions.*WHERE plugin_id = \\$1.*LIMIT \\$2").
			WithArgs(pluginID, 100).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "plugin_id", "plugin_name", "hook_type", "event_data",
				"success", "error", "duration_ms", "executed_at",
			}).AddRow(execID, pluginID, "plugin-a", "video.uploaded", "{}", true, "", int64(12), time.Now()))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/"+pluginID.String()+"/executions", nil)
		req = withPluginParam(req, "id", pluginID.String())
		rr := httptest.NewRecorder()
		handler.GetExecutionHistory(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("GetExecutionHistory repository error", func(t *testing.T) {
		mock.ExpectQuery("(?s)SELECT id, plugin_id, plugin_name, hook_type, event_data,.*FROM plugin_hook_executions.*WHERE plugin_id = \\$1.*LIMIT \\$2").
			WithArgs(pluginID, 100).
			WillReturnError(assert.AnError)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/"+pluginID.String()+"/executions", nil)
		req = withPluginParam(req, "id", pluginID.String())
		rr := httptest.NewRecorder()
		handler.GetExecutionHistory(rr, req)

		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("GetPluginHealth success", func(t *testing.T) {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM get_plugin_health($1)")).
			WithArgs(pluginID).
			WillReturnRows(sqlmock.NewRows([]string{
				"plugin_id", "plugin_name", "status", "success_rate", "avg_duration_ms", "last_executed_at",
			}).AddRow(pluginID, "plugin-a", domain.PluginStatusEnabled, float64(95), float64(10), time.Now()))

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/"+pluginID.String()+"/health", nil)
		req = withPluginParam(req, "id", pluginID.String())
		rr := httptest.NewRecorder()
		handler.GetPluginHealth(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
	})

	t.Run("GetPluginHealth repository error", func(t *testing.T) {
		mock.ExpectQuery(regexp.QuoteMeta("SELECT * FROM get_plugin_health($1)")).
			WithArgs(pluginID).
			WillReturnError(assert.AnError)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/admin/plugins/"+pluginID.String()+"/health", nil)
		req = withPluginParam(req, "id", pluginID.String())
		rr := httptest.NewRecorder()
		handler.GetPluginHealth(rr, req)

		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginHandler_ManagerErrorBranches_SQLMock(t *testing.T) {
	handler, mock, cleanup := newSQLMockPluginHandler(t)
	defer cleanup()

	handler.pluginManager = coreplugin.NewManager(t.TempDir())

	enableID := uuid.New()
	disableID := uuid.New()
	updateID := uuid.New()

	t.Run("EnablePlugin manager error", func(t *testing.T) {
		mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
			WithArgs(enableID).
			WillReturnRows(samplePluginRow(enableID, domain.PluginStatusInstalled, "plugin-enable-missing"))

		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/"+enableID.String()+"/enable", nil)
		req = withPluginParam(req, "id", enableID.String())
		rr := httptest.NewRecorder()
		handler.EnablePlugin(rr, req)

		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("DisablePlugin manager error", func(t *testing.T) {
		mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
			WithArgs(disableID).
			WillReturnRows(samplePluginRow(disableID, domain.PluginStatusEnabled, "plugin-disable-missing"))

		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/"+disableID.String()+"/disable", nil)
		req = withPluginParam(req, "id", disableID.String())
		rr := httptest.NewRecorder()
		handler.DisablePlugin(rr, req)

		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("UpdatePluginConfig manager error", func(t *testing.T) {
		mock.ExpectQuery("(?s)SELECT id, name, version, author, description, status, config,.*FROM plugins.*WHERE id = \\$1").
			WithArgs(updateID).
			WillReturnRows(samplePluginRow(updateID, domain.PluginStatusInstalled, "plugin-config-missing"))

		req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/plugins/"+updateID.String()+"/config", strings.NewReader(`{"config":{"feature":true}}`))
		req = withPluginParam(req, "id", updateID.String())
		rr := httptest.NewRecorder()
		handler.UpdatePluginConfig(rr, req)

		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	require.NoError(t, mock.ExpectationsWereMet())
}
