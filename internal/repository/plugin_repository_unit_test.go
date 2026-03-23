package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPluginMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(db, "sqlmock"), mock
}

func newPluginRepo(t *testing.T) (*PluginRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupPluginMockDB(t)
	repo := NewPluginRepository(db)
	return repo, mock, func() { _ = db.Close() }
}

func samplePluginRecord() *domain.PluginRecord {
	now := time.Now().Truncate(time.Second)
	return &domain.PluginRecord{
		ID:          uuid.New(),
		Name:        "sample-plugin",
		Version:     "1.2.3",
		Author:      "Athena",
		Description: "Plugin for tests",
		Status:      domain.PluginStatusInstalled,
		Config:      map[string]any{"enabled": true, "max": 10.0},
		Permissions: []string{"videos:read", "videos:write"},
		Hooks:       []string{"video.created", "video.updated"},
		InstallPath: "/tmp/plugins/sample-plugin",
		Checksum:    "sha256:abc123",
		InstalledAt: now,
		UpdatedAt:   now,
		LastError:   "",
	}
}

func pluginRecordColumns() []string {
	return []string{
		"id", "name", "version", "author", "description", "status", "config",
		"permissions", "hooks", "install_path", "checksum",
		"installed_at", "updated_at", "enabled_at", "disabled_at", "last_error",
	}
}

func pluginRecordRow(p *domain.PluginRecord, configJSON string) []driver.Value {
	return []driver.Value{
		p.ID, p.Name, p.Version, p.Author, p.Description, p.Status, []byte(configJSON),
		"{videos:read,videos:write}", "{video.created,video.updated}", p.InstallPath, p.Checksum,
		p.InstalledAt, p.UpdatedAt, p.EnabledAt, p.DisabledAt, p.LastError,
	}
}

func pluginStatsColumns() []string {
	return []string{
		"plugin_id", "plugin_name", "total_executions", "success_count",
		"failure_count", "avg_duration_ms", "last_executed_at",
	}
}

func TestPluginRepository_Unit_Create(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newPluginRepo(t)
	defer cleanup()

	invalid := samplePluginRecord()
	invalid.Name = ""
	err := repo.Create(ctx, invalid)
	assert.ErrorIs(t, err, domain.ErrPluginInvalidName)

	plugin := samplePluginRecord()
	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO plugins (
			id, name, version, author, description, status, config,
			permissions, hooks, install_path, checksum,
			installed_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)
		RETURNING id, installed_at, updated_at`)).
		WithArgs(
			plugin.ID, plugin.Name, plugin.Version, plugin.Author, plugin.Description, plugin.Status,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), plugin.InstallPath, plugin.Checksum,
			plugin.InstalledAt, plugin.UpdatedAt,
		).
		WillReturnRows(sqlmock.NewRows([]string{"id", "installed_at", "updated_at"}).
			AddRow(plugin.ID, plugin.InstalledAt, plugin.UpdatedAt))

	require.NoError(t, repo.Create(ctx, plugin))

	mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO plugins`)).
		WillReturnError(errors.New("insert failed"))
	err = repo.Create(ctx, samplePluginRecord())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "insert failed")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginRepository_Unit_GetByID_And_GetByName(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newPluginRepo(t)
	defer cleanup()

	plugin := samplePluginRecord()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
		WHERE id = $1`)).
		WithArgs(plugin.ID).
		WillReturnRows(sqlmock.NewRows(pluginRecordColumns()).AddRow(pluginRecordRow(plugin, `{"enabled":true,"max":10}`)...))

	gotByID, err := repo.GetByID(ctx, plugin.ID)
	require.NoError(t, err)
	require.NotNil(t, gotByID)
	assert.Equal(t, plugin.ID, gotByID.ID)
	assert.Equal(t, plugin.Name, gotByID.Name)
	assert.Equal(t, true, gotByID.Config["enabled"])

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
		WHERE name = $1`)).
		WithArgs(plugin.Name).
		WillReturnRows(sqlmock.NewRows(pluginRecordColumns()).AddRow(pluginRecordRow(plugin, `{"enabled":true}`)...))

	gotByName, err := repo.GetByName(ctx, plugin.Name)
	require.NoError(t, err)
	require.NotNil(t, gotByName)
	assert.Equal(t, plugin.Name, gotByName.Name)

	missingID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
		WHERE id = $1`)).
		WithArgs(missingID).
		WillReturnError(sql.ErrNoRows)

	_, err = repo.GetByID(ctx, missingID)
	assert.ErrorIs(t, err, domain.ErrPluginNotFound)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
		WHERE name = $1`)).
		WithArgs("bad-config").
		WillReturnRows(sqlmock.NewRows(pluginRecordColumns()).AddRow(
			plugin.ID, plugin.Name, plugin.Version, plugin.Author, plugin.Description, plugin.Status, []byte("{bad"),
			"{videos:read}", "{video.created}", plugin.InstallPath, plugin.Checksum,
			plugin.InstalledAt, plugin.UpdatedAt, nil, nil, "",
		))

	_, err = repo.GetByName(ctx, "bad-config")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal config")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginRepository_Unit_List(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newPluginRepo(t)
	defer cleanup()

	plugin := samplePluginRecord()

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
	 ORDER BY name ASC`)).
		WillReturnRows(sqlmock.NewRows(pluginRecordColumns()).AddRow(pluginRecordRow(plugin, `{"enabled":true}`)...))

	all, err := repo.List(ctx, nil)
	require.NoError(t, err)
	require.Len(t, all, 1)

	status := domain.PluginStatusEnabled
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
	 WHERE status = $1 ORDER BY name ASC`)).
		WithArgs(status).
		WillReturnRows(sqlmock.NewRows(pluginRecordColumns()).AddRow(pluginRecordRow(plugin, `{"enabled":true}`)...))

	enabled, err := repo.List(ctx, &status)
	require.NoError(t, err)
	require.Len(t, enabled, 1)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
	 ORDER BY name ASC`)).
		WillReturnRows(sqlmock.NewRows(pluginRecordColumns()).AddRow(
			plugin.ID, plugin.Name, plugin.Version, plugin.Author, plugin.Description, plugin.Status, []byte("{oops"),
			"{videos:read}", "{video.created}", plugin.InstallPath, plugin.Checksum,
			plugin.InstalledAt, plugin.UpdatedAt, nil, nil, "",
		))
	_, err = repo.List(ctx, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal config")

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginRepository_Unit_UpdateDeleteAndConfig(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newPluginRepo(t)
	defer cleanup()

	plugin := samplePluginRecord()

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins
		SET version = $1, author = $2, description = $3, status = $4,
		    config = $5, permissions = $6, hooks = $7,
		    install_path = $8, checksum = $9,
		    enabled_at = $10, disabled_at = $11, last_error = $12,
		    updated_at = $13
		WHERE id = $14`)).
		WithArgs(
			plugin.Version, plugin.Author, plugin.Description, plugin.Status,
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			plugin.InstallPath, plugin.Checksum,
			plugin.EnabledAt, plugin.DisabledAt, plugin.LastError, sqlmock.AnyArg(), plugin.ID,
		).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Update(ctx, plugin))

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins
		SET version = $1, author = $2, description = $3, status = $4,
		    config = $5, permissions = $6, hooks = $7,
		    install_path = $8, checksum = $9,
		    enabled_at = $10, disabled_at = $11, last_error = $12,
		    updated_at = $13
		WHERE id = $14`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, repo.Update(ctx, plugin), domain.ErrPluginNotFound)

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM plugins WHERE id = $1`)).
		WithArgs(plugin.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Delete(ctx, plugin.ID))

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM plugins WHERE id = $1`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, repo.Delete(ctx, uuid.New()), domain.ErrPluginNotFound)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins
		SET status = $1, enabled_at = $2, disabled_at = $3, updated_at = $4
		WHERE id = $5`)).
		WithArgs(domain.PluginStatusEnabled, sqlmock.AnyArg(), nil, sqlmock.AnyArg(), plugin.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateStatus(ctx, plugin.ID, domain.PluginStatusEnabled))

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins
		SET status = $1, enabled_at = $2, disabled_at = $3, updated_at = $4
		WHERE id = $5`)).
		WithArgs(domain.PluginStatusDisabled, nil, sqlmock.AnyArg(), sqlmock.AnyArg(), plugin.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateStatus(ctx, plugin.ID, domain.PluginStatusDisabled))

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins
		SET status = $1, enabled_at = $2, disabled_at = $3, updated_at = $4
		WHERE id = $5`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, repo.UpdateStatus(ctx, uuid.New(), domain.PluginStatusInstalled), domain.ErrPluginNotFound)

	cfg := map[string]any{"a": 1.0}
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins SET config = $1, updated_at = $2 WHERE id = $3`)).
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), plugin.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateConfig(ctx, plugin.ID, cfg))

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins SET config = $1, updated_at = $2 WHERE id = $3`)).
		WillReturnResult(sqlmock.NewResult(0, 0))
	assert.ErrorIs(t, repo.UpdateConfig(ctx, uuid.New(), cfg), domain.ErrPluginNotFound)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginRepository_Unit_ExecutionAndStats(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newPluginRepo(t)
	defer cleanup()

	plugin := samplePluginRecord()
	exec := &domain.PluginHookExecution{
		ID:         uuid.New(),
		PluginID:   plugin.ID,
		PluginName: plugin.Name,
		HookType:   "video.created",
		EventData:  `{"video_id":"v1"}`,
		Success:    true,
		Duration:   42,
		ExecutedAt: time.Now().Truncate(time.Second),
	}

	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO plugin_hook_executions (
			id, plugin_id, plugin_name, hook_type, event_data,
			success, error, duration_ms, executed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`)).
		WithArgs(exec.ID, exec.PluginID, exec.PluginName, exec.HookType, exec.EventData, exec.Success, exec.Error, exec.Duration, exec.ExecutedAt).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.RecordExecution(ctx, exec))

	historyRows := sqlmock.NewRows([]string{
		"id", "plugin_id", "plugin_name", "hook_type", "event_data",
		"success", "error", "duration_ms", "executed_at",
	}).AddRow(exec.ID, exec.PluginID, exec.PluginName, exec.HookType, exec.EventData, exec.Success, exec.Error, exec.Duration, exec.ExecutedAt)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, plugin_id, plugin_name, hook_type, event_data,
		       success, error, duration_ms, executed_at
		FROM plugin_hook_executions
		WHERE plugin_id = $1
		ORDER BY executed_at DESC
		LIMIT $2`)).
		WithArgs(plugin.ID, 20).
		WillReturnRows(historyRows)

	history, err := repo.GetExecutionHistory(ctx, plugin.ID, 20)
	require.NoError(t, err)
	require.Len(t, history, 1)

	statsRows := sqlmock.NewRows(pluginStatsColumns()).AddRow(
		plugin.ID, plugin.Name, int64(10), int64(9), int64(1), 12.5, time.Now().Truncate(time.Second),
	)
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT plugin_id, plugin_name, total_executions, success_count,
		       failure_count, avg_duration_ms, last_executed_at
		FROM plugin_statistics
		WHERE plugin_id = $1`)).
		WithArgs(plugin.ID).
		WillReturnRows(statsRows)

	stats, err := repo.GetStatistics(ctx, plugin.ID)
	require.NoError(t, err)
	require.NotNil(t, stats)
	assert.Equal(t, int64(10), stats.TotalExecutions)

	missingStatsID := uuid.New()
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT plugin_id, plugin_name, total_executions, success_count,
		       failure_count, avg_duration_ms, last_executed_at
		FROM plugin_statistics
		WHERE plugin_id = $1`)).
		WithArgs(missingStatsID).
		WillReturnError(sql.ErrNoRows)

	emptyStats, err := repo.GetStatistics(ctx, missingStatsID)
	require.NoError(t, err)
	require.NotNil(t, emptyStats)
	assert.Equal(t, int64(0), emptyStats.TotalExecutions)

	none, err := repo.GetStatisticsForPlugins(ctx, nil)
	require.NoError(t, err)
	assert.Empty(t, none)

	statsRowsMany := sqlmock.NewRows(pluginStatsColumns()).
		AddRow(plugin.ID, plugin.Name, int64(2), int64(2), int64(0), 3.5, time.Now()).
		AddRow(uuid.New(), "other", int64(4), int64(3), int64(1), 6.0, time.Now())
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT plugin_id, plugin_name, total_executions, success_count,
		       failure_count, avg_duration_ms, last_executed_at
		FROM plugin_statistics
		WHERE plugin_id IN (?, ?)`)).
		WillReturnRows(statsRowsMany)

	many, err := repo.GetStatisticsForPlugins(ctx, []uuid.UUID{plugin.ID, uuid.New()})
	require.NoError(t, err)
	require.Len(t, many, 2)

	allRows := sqlmock.NewRows(pluginStatsColumns()).
		AddRow(plugin.ID, plugin.Name, int64(3), int64(3), int64(0), 5.0, time.Now())
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT plugin_id, plugin_name, total_executions, success_count,
		       failure_count, avg_duration_ms, last_executed_at
		FROM plugin_statistics
		ORDER BY plugin_name ASC`)).
		WillReturnRows(allRows)

	allStats, err := repo.GetAllStatistics(ctx)
	require.NoError(t, err)
	require.Len(t, allStats, 1)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT cleanup_old_plugin_executions()`)).
		WillReturnRows(sqlmock.NewRows([]string{"cleanup_old_plugin_executions"}).AddRow(int64(7)))
	count, err := repo.CleanupOldExecutions(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 7, count)

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginRepository_Unit_HealthDependenciesAndHelpers(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newPluginRepo(t)
	defer cleanup()

	plugin := samplePluginRecord()

	status := domain.PluginStatusEnabled
	mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
	 WHERE status = $1 ORDER BY name ASC`)).
		WithArgs(status).
		WillReturnRows(sqlmock.NewRows(pluginRecordColumns()).AddRow(pluginRecordRow(plugin, `{"enabled":true}`)...))
	enabled, err := repo.GetEnabledPlugins(ctx)
	require.NoError(t, err)
	require.Len(t, enabled, 1)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM get_plugin_health($1)`)).
		WithArgs(plugin.ID).
		WillReturnRows(sqlmock.NewRows([]string{
			"plugin_id", "plugin_name", "status", "success_rate", "avg_duration_ms", "last_executed_at",
		}).AddRow(plugin.ID, plugin.Name, domain.PluginStatusEnabled, 99.0, 8.5, time.Now()))
	health, err := repo.GetPluginHealth(ctx, plugin.ID)
	require.NoError(t, err)
	assert.Equal(t, plugin.Name, health["plugin_name"])

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT * FROM check_plugin_dependencies($1)`)).
		WithArgs(plugin.Name).
		WillReturnRows(sqlmock.NewRows([]string{
			"dependency_name", "required_version", "optional", "installed", "installed_version", "satisfied",
		}).
			AddRow("core-utils", ">=1.0.0", false, true, "1.2.0", true).
			AddRow("analytics", ">=2.0.0", true, false, nil, false))
	deps, err := repo.CheckDependencies(ctx, plugin.Name)
	require.NoError(t, err)
	require.Len(t, deps, 2)
	assert.Equal(t, "1.2.0", deps[0]["installed_version"])

	dep := &domain.PluginDependency{
		PluginID:        plugin.ID,
		DependsOnPlugin: "core-utils",
		RequiredVersion: ">=1.0.0",
		Optional:        false,
	}
	mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO plugin_dependencies (plugin_id, depends_on_plugin, required_version, optional)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (plugin_id, depends_on_plugin) DO UPDATE
		SET required_version = $3, optional = $4`)).
		WithArgs(dep.PluginID, dep.DependsOnPlugin, dep.RequiredVersion, dep.Optional).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.AddDependency(ctx, dep))

	mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM plugin_dependencies WHERE plugin_id = $1 AND depends_on_plugin = $2`)).
		WithArgs(dep.PluginID, dep.DependsOnPlugin).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.RemoveDependency(ctx, dep.PluginID, dep.DependsOnPlugin))

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM plugins`)).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(4)))
	total, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 4, total)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM plugins WHERE status = $1`)).
		WithArgs(domain.PluginStatusEnabled).
		WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(3)))
	countByStatus, err := repo.CountByStatus(ctx, domain.PluginStatusEnabled)
	require.NoError(t, err)
	assert.EqualValues(t, 3, countByStatus)

	mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM plugins WHERE name = $1)`)).
		WithArgs(plugin.Name).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	exists, err := repo.Exists(ctx, plugin.Name)
	require.NoError(t, err)
	assert.True(t, exists)

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins SET last_error = $1, status = $2, updated_at = $3 WHERE id = $4`)).
		WithArgs("boom", domain.PluginStatusFailed, sqlmock.AnyArg(), plugin.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.UpdateError(ctx, plugin.ID, "boom"))

	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins SET last_error = '', updated_at = $1 WHERE id = $2`)).
		WithArgs(sqlmock.AnyArg(), plugin.ID).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.ClearError(ctx, plugin.ID))

	require.NoError(t, mock.ExpectationsWereMet())
}

func TestPluginRepository_Unit_WithTransaction(t *testing.T) {
	ctx := context.Background()
	repo, mock, cleanup := newPluginRepo(t)
	defer cleanup()

	mock.ExpectBegin()
	mock.ExpectExec(regexp.QuoteMeta(`UPDATE plugins SET status = 'enabled' WHERE id = $1`)).
		WithArgs("p1").
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()

	err := repo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
		_, execErr := tx.ExecContext(ctx, `UPDATE plugins SET status = 'enabled' WHERE id = $1`, "p1")
		return execErr
	})
	require.NoError(t, err)

	mock.ExpectBegin()
	mock.ExpectRollback()
	err = repo.WithTransaction(ctx, func(_ *sqlx.Tx) error {
		return errors.New("fn failed")
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "fn failed")

	mock.ExpectBegin()
	mock.ExpectCommit().WillReturnError(errors.New("commit failed"))
	err = repo.WithTransaction(ctx, func(_ *sqlx.Tx) error {
		return nil
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to commit transaction")

	require.NoError(t, mock.ExpectationsWereMet())
}
