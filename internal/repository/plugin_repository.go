package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
)

// PluginRepository handles database operations for plugins
type PluginRepository struct {
	db *sqlx.DB
}

// NewPluginRepository creates a new plugin repository
func NewPluginRepository(db *sqlx.DB) *PluginRepository {
	return &PluginRepository{db: db}
}

// Create creates a new plugin record
func (r *PluginRepository) Create(ctx context.Context, plugin *domain.PluginRecord) error {
	if err := plugin.Validate(); err != nil {
		return err
	}
	configJSON, err := json.Marshal(plugin.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	query := `
		INSERT INTO plugins (
			id, name, version, author, description, status, config,
			permissions, hooks, install_path, checksum,
			installed_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)
		RETURNING id, installed_at, updated_at
	`

	return r.db.QueryRowContext(
		ctx, query,
		plugin.ID,
		plugin.Name,
		plugin.Version,
		plugin.Author,
		plugin.Description,
		plugin.Status,
		configJSON,
		pq.Array(plugin.Permissions),
		pq.Array(plugin.Hooks),
		plugin.InstallPath,
		plugin.Checksum,
		plugin.InstalledAt,
		plugin.UpdatedAt,
	).Scan(&plugin.ID, &plugin.InstalledAt, &plugin.UpdatedAt)
}

// GetByID retrieves a plugin by ID
func (r *PluginRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.PluginRecord, error) {
	var plugin domain.PluginRecord
	var configJSON []byte

	query := `
		SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
		WHERE id = $1
	`

	err := r.db.QueryRowContext(ctx, query, id).Scan(
		&plugin.ID,
		&plugin.Name,
		&plugin.Version,
		&plugin.Author,
		&plugin.Description,
		&plugin.Status,
		&configJSON,
		pq.Array(&plugin.Permissions),
		pq.Array(&plugin.Hooks),
		&plugin.InstallPath,
		&plugin.Checksum,
		&plugin.InstalledAt,
		&plugin.UpdatedAt,
		&plugin.EnabledAt,
		&plugin.DisabledAt,
		&plugin.LastError,
	)

	if err == sql.ErrNoRows {
		return nil, domain.ErrPluginNotFound
	}

	if err != nil {
		return nil, err
	}

	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &plugin.Config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %w", err)
		}
	}

	return &plugin, nil
}

// GetByName retrieves a plugin by name
func (r *PluginRepository) GetByName(ctx context.Context, name string) (*domain.PluginRecord, error) {
	var plugin domain.PluginRecord
	var configJSON []byte

	query := `
		SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
		WHERE name = $1
	`

	err := r.db.QueryRowContext(ctx, query, name).Scan(
		&plugin.ID,
		&plugin.Name,
		&plugin.Version,
		&plugin.Author,
		&plugin.Description,
		&plugin.Status,
		&configJSON,
		pq.Array(&plugin.Permissions),
		pq.Array(&plugin.Hooks),
		&plugin.InstallPath,
		&plugin.Checksum,
		&plugin.InstalledAt,
		&plugin.UpdatedAt,
		&plugin.EnabledAt,
		&plugin.DisabledAt,
		&plugin.LastError,
	)

	if err == sql.ErrNoRows {
		return nil, domain.ErrPluginNotFound
	}

	if err != nil {
		return nil, err
	}

	if len(configJSON) > 0 {
		if err := json.Unmarshal(configJSON, &plugin.Config); err != nil {
			return nil, fmt.Errorf("failed to unmarshal config: %w", err)
		}
	}

	return &plugin, nil
}

// List retrieves all plugins with optional filtering
func (r *PluginRepository) List(ctx context.Context, status *domain.PluginStatus) ([]*domain.PluginRecord, error) {
	query := `
		SELECT id, name, version, author, description, status, config,
		       permissions, hooks, install_path, checksum,
		       installed_at, updated_at, enabled_at, disabled_at, last_error
		FROM plugins
	`

	args := []any{}
	if status != nil {
		query += " WHERE status = $1"
		args = append(args, *status)
	}

	query += " ORDER BY name ASC"

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var plugins []*domain.PluginRecord
	for rows.Next() {
		var plugin domain.PluginRecord
		var configJSON []byte
		err := rows.Scan(
			&plugin.ID,
			&plugin.Name,
			&plugin.Version,
			&plugin.Author,
			&plugin.Description,
			&plugin.Status,
			&configJSON,
			pq.Array(&plugin.Permissions),
			pq.Array(&plugin.Hooks),
			&plugin.InstallPath,
			&plugin.Checksum,
			&plugin.InstalledAt,
			&plugin.UpdatedAt,
			&plugin.EnabledAt,
			&plugin.DisabledAt,
			&plugin.LastError,
		)
		if err != nil {
			return nil, err
		}
		if len(configJSON) > 0 {
			if err := json.Unmarshal(configJSON, &plugin.Config); err != nil {
				return nil, fmt.Errorf("failed to unmarshal config: %w", err)
			}
		}
		plugins = append(plugins, &plugin)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return plugins, nil
}

// Update updates a plugin record
func (r *PluginRepository) Update(ctx context.Context, plugin *domain.PluginRecord) error {
	if err := plugin.Validate(); err != nil {
		return err
	}
	configJSON, err := json.Marshal(plugin.Config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	query := `
		UPDATE plugins
		SET version = $1, author = $2, description = $3, status = $4,
		    config = $5, permissions = $6, hooks = $7,
		    install_path = $8, checksum = $9,
		    enabled_at = $10, disabled_at = $11, last_error = $12,
		    updated_at = $13
		WHERE id = $14
	`

	result, err := r.db.ExecContext(
		ctx, query,
		plugin.Version,
		plugin.Author,
		plugin.Description,
		plugin.Status,
		configJSON,
		pq.Array(plugin.Permissions),
		pq.Array(plugin.Hooks),
		plugin.InstallPath,
		plugin.Checksum,
		plugin.EnabledAt,
		plugin.DisabledAt,
		plugin.LastError,
		time.Now(),
		plugin.ID,
	)

	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return domain.ErrPluginNotFound
	}

	return nil
}

// Delete deletes a plugin by ID
func (r *PluginRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM plugins WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return domain.ErrPluginNotFound
	}

	return nil
}

// UpdateStatus updates the status of a plugin
func (r *PluginRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.PluginStatus) error {
	var enabledAt, disabledAt *time.Time
	now := time.Now()

	switch status {
	case domain.PluginStatusEnabled:
		enabledAt = &now
	case domain.PluginStatusDisabled:
		disabledAt = &now
	}

	query := `
		UPDATE plugins
		SET status = $1, enabled_at = $2, disabled_at = $3, updated_at = $4
		WHERE id = $5
	`

	result, err := r.db.ExecContext(ctx, query, status, enabledAt, disabledAt, now, id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return domain.ErrPluginNotFound
	}

	return nil
}

// UpdateConfig updates the configuration of a plugin
func (r *PluginRepository) UpdateConfig(ctx context.Context, id uuid.UUID, config map[string]any) error {
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	query := `UPDATE plugins SET config = $1, updated_at = $2 WHERE id = $3`

	result, err := r.db.ExecContext(ctx, query, configJSON, time.Now(), id)
	if err != nil {
		return err
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return domain.ErrPluginNotFound
	}

	return nil
}

// RecordExecution records a plugin hook execution
func (r *PluginRepository) RecordExecution(ctx context.Context, execution *domain.PluginHookExecution) error {
	query := `
		INSERT INTO plugin_hook_executions (
			id, plugin_id, plugin_name, hook_type, event_data,
			success, error, duration_ms, executed_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)
	`

	_, err := r.db.ExecContext(
		ctx, query,
		execution.ID,
		execution.PluginID,
		execution.PluginName,
		execution.HookType,
		execution.EventData,
		execution.Success,
		execution.Error,
		execution.Duration,
		execution.ExecutedAt,
	)

	return err
}

// GetExecutionHistory retrieves hook execution history for a plugin
func (r *PluginRepository) GetExecutionHistory(ctx context.Context, pluginID uuid.UUID, limit int) ([]*domain.PluginHookExecution, error) {
	query := `
		SELECT id, plugin_id, plugin_name, hook_type, event_data,
		       success, error, duration_ms, executed_at
		FROM plugin_hook_executions
		WHERE plugin_id = $1
		ORDER BY executed_at DESC
		LIMIT $2
	`

	rows, err := r.db.QueryContext(ctx, query, pluginID, limit)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var executions []*domain.PluginHookExecution
	for rows.Next() {
		var exec domain.PluginHookExecution
		err := rows.Scan(
			&exec.ID,
			&exec.PluginID,
			&exec.PluginName,
			&exec.HookType,
			&exec.EventData,
			&exec.Success,
			&exec.Error,
			&exec.Duration,
			&exec.ExecutedAt,
		)
		if err != nil {
			return nil, err
		}
		executions = append(executions, &exec)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return executions, nil
}

// GetStatistics retrieves statistics for a plugin
func (r *PluginRepository) GetStatistics(ctx context.Context, pluginID uuid.UUID) (*domain.PluginStatistics, error) {
	var stats domain.PluginStatistics

	query := `
		SELECT plugin_id, plugin_name, total_executions, success_count,
		       failure_count, avg_duration_ms, last_executed_at
		FROM plugin_statistics
		WHERE plugin_id = $1
	`

	err := r.db.QueryRowContext(ctx, query, pluginID).Scan(
		&stats.PluginID,
		&stats.PluginName,
		&stats.TotalExecutions,
		&stats.SuccessCount,
		&stats.FailureCount,
		&stats.AvgDuration,
		&stats.LastExecutedAt,
	)

	if err == sql.ErrNoRows {
		// Return empty statistics if none exist
		return &domain.PluginStatistics{
			PluginID: pluginID,
		}, nil
	}

	if err != nil {
		return nil, err
	}

	return &stats, nil
}

// GetStatisticsForPlugins retrieves statistics for multiple plugins
func (r *PluginRepository) GetStatisticsForPlugins(ctx context.Context, pluginIDs []uuid.UUID) (map[uuid.UUID]*domain.PluginStatistics, error) {
	if len(pluginIDs) == 0 {
		return make(map[uuid.UUID]*domain.PluginStatistics), nil
	}

	query, args, err := sqlx.In(`
		SELECT plugin_id, plugin_name, total_executions, success_count,
		       failure_count, avg_duration_ms, last_executed_at
		FROM plugin_statistics
		WHERE plugin_id IN (?)
	`, pluginIDs)
	if err != nil {
		return nil, err
	}
	query = r.db.Rebind(query)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	result := make(map[uuid.UUID]*domain.PluginStatistics)
	for rows.Next() {
		var stats domain.PluginStatistics
		err := rows.Scan(
			&stats.PluginID,
			&stats.PluginName,
			&stats.TotalExecutions,
			&stats.SuccessCount,
			&stats.FailureCount,
			&stats.AvgDuration,
			&stats.LastExecutedAt,
		)
		if err != nil {
			return nil, err
		}
		result[stats.PluginID] = &stats
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return result, nil
}

// GetAllStatistics retrieves statistics for all plugins
func (r *PluginRepository) GetAllStatistics(ctx context.Context) ([]*domain.PluginStatistics, error) {
	query := `
		SELECT plugin_id, plugin_name, total_executions, success_count,
		       failure_count, avg_duration_ms, last_executed_at
		FROM plugin_statistics
		ORDER BY plugin_name ASC
	`

	rows, err := r.db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var statistics []*domain.PluginStatistics
	for rows.Next() {
		var stats domain.PluginStatistics
		err := rows.Scan(
			&stats.PluginID,
			&stats.PluginName,
			&stats.TotalExecutions,
			&stats.SuccessCount,
			&stats.FailureCount,
			&stats.AvgDuration,
			&stats.LastExecutedAt,
		)
		if err != nil {
			return nil, err
		}
		statistics = append(statistics, &stats)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return statistics, nil
}

// CleanupOldExecutions removes old plugin execution records
func (r *PluginRepository) CleanupOldExecutions(ctx context.Context) (int64, error) {
	query := `SELECT cleanup_old_plugin_executions()`

	var count int64
	err := r.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetEnabledPlugins retrieves all enabled plugins
func (r *PluginRepository) GetEnabledPlugins(ctx context.Context) ([]*domain.PluginRecord, error) {
	enabled := domain.PluginStatusEnabled
	return r.List(ctx, &enabled)
}

// GetPluginHealth retrieves health status for a plugin
func (r *PluginRepository) GetPluginHealth(ctx context.Context, pluginID uuid.UUID) (map[string]any, error) {
	query := `SELECT * FROM get_plugin_health($1)`

	var (
		id             uuid.UUID
		name           string
		status         domain.PluginStatus
		successRate    float64
		avgDuration    float64
		lastExecutedAt time.Time
	)

	err := r.db.QueryRowContext(ctx, query, pluginID).Scan(
		&id,
		&name,
		&status,
		&successRate,
		&avgDuration,
		&lastExecutedAt,
	)

	if err != nil {
		return nil, err
	}

	return map[string]any{
		"plugin_id":        id,
		"plugin_name":      name,
		"status":           status,
		"success_rate":     successRate,
		"avg_duration_ms":  avgDuration,
		"last_executed_at": lastExecutedAt,
	}, nil
}

// CheckDependencies checks if all dependencies for a plugin are satisfied
func (r *PluginRepository) CheckDependencies(ctx context.Context, pluginName string) ([]map[string]any, error) {
	query := `SELECT * FROM check_plugin_dependencies($1)`

	rows, err := r.db.QueryContext(ctx, query, pluginName)
	if err != nil {
		return nil, err
	}
	defer func() {
		_ = rows.Close()
	}()

	var dependencies []map[string]any
	for rows.Next() {
		var (
			dependencyName   string
			requiredVersion  string
			optional         bool
			installed        bool
			installedVersion sql.NullString
			satisfied        bool
		)

		err := rows.Scan(
			&dependencyName,
			&requiredVersion,
			&optional,
			&installed,
			&installedVersion,
			&satisfied,
		)
		if err != nil {
			return nil, err
		}

		dep := map[string]any{
			"dependency_name":  dependencyName,
			"required_version": requiredVersion,
			"optional":         optional,
			"installed":        installed,
			"satisfied":        satisfied,
		}

		if installedVersion.Valid {
			dep["installed_version"] = installedVersion.String
		}

		dependencies = append(dependencies, dep)
	}

	if err = rows.Err(); err != nil {
		return nil, err
	}

	return dependencies, nil
}

// AddDependency adds a plugin dependency
func (r *PluginRepository) AddDependency(ctx context.Context, dep *domain.PluginDependency) error {
	query := `
		INSERT INTO plugin_dependencies (plugin_id, depends_on_plugin, required_version, optional)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (plugin_id, depends_on_plugin) DO UPDATE
		SET required_version = $3, optional = $4
	`

	_, err := r.db.ExecContext(ctx, query, dep.PluginID, dep.DependsOnPlugin, dep.RequiredVersion, dep.Optional)
	return err
}

// RemoveDependency removes a plugin dependency
func (r *PluginRepository) RemoveDependency(ctx context.Context, pluginID uuid.UUID, dependsOn string) error {
	query := `DELETE FROM plugin_dependencies WHERE plugin_id = $1 AND depends_on_plugin = $2`

	_, err := r.db.ExecContext(ctx, query, pluginID, dependsOn)
	return err
}

// Count returns the total number of plugins
func (r *PluginRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	query := `SELECT COUNT(*) FROM plugins`

	err := r.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// CountByStatus returns the number of plugins with a specific status
func (r *PluginRepository) CountByStatus(ctx context.Context, status domain.PluginStatus) (int64, error) {
	var count int64
	query := `SELECT COUNT(*) FROM plugins WHERE status = $1`

	err := r.db.QueryRowContext(ctx, query, status).Scan(&count)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// Exists checks if a plugin with the given name exists
func (r *PluginRepository) Exists(ctx context.Context, name string) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM plugins WHERE name = $1)`

	err := r.db.QueryRowContext(ctx, query, name).Scan(&exists)
	if err != nil {
		return false, err
	}

	return exists, nil
}

// UpdateError updates the last error for a plugin
func (r *PluginRepository) UpdateError(ctx context.Context, id uuid.UUID, errorMsg string) error {
	query := `UPDATE plugins SET last_error = $1, status = $2, updated_at = $3 WHERE id = $4`

	_, err := r.db.ExecContext(ctx, query, errorMsg, domain.PluginStatusFailed, time.Now(), id)
	return err
}

// ClearError clears the last error for a plugin
func (r *PluginRepository) ClearError(ctx context.Context, id uuid.UUID) error {
	query := `UPDATE plugins SET last_error = '', updated_at = $1 WHERE id = $2`

	_, err := r.db.ExecContext(ctx, query, time.Now(), id)
	return err
}

// Transaction helper methods

// WithTransaction executes a function within a transaction
func (r *PluginRepository) WithTransaction(ctx context.Context, fn func(tx *sqlx.Tx) error) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		}
	}()

	if err := fn(tx); err != nil {
		if rbErr := tx.Rollback(); rbErr != nil {
			return fmt.Errorf("tx error: %w, rollback error: %v", err, rbErr)
		}
		return err
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}
