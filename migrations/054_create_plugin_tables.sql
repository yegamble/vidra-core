-- +goose Up
-- +goose StatementBegin
-- Migration: Create Plugin System Tables
-- Description: Adds tables for plugin management, hooks, execution tracking, and statistics
-- Version: 051
-- Date: 2025-10-23

-- =============================================================================
-- Plugin Status Type
-- =============================================================================

CREATE TYPE plugin_status AS ENUM ('installed', 'enabled', 'disabled', 'failed', 'updating');

-- =============================================================================
-- Main Plugins Table
-- =============================================================================

CREATE TABLE plugins (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    name TEXT NOT NULL UNIQUE,
    version TEXT NOT NULL,
    author TEXT NOT NULL,
    description TEXT NOT NULL,
    status plugin_status NOT NULL DEFAULT 'installed',
    config JSONB DEFAULT '{}',
    permissions TEXT[] DEFAULT '{}',
    hooks TEXT[] DEFAULT '{}',
    install_path TEXT NOT NULL,
    checksum TEXT NOT NULL, -- SHA256 checksum for integrity verification
    installed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    enabled_at TIMESTAMP,
    disabled_at TIMESTAMP,
    last_error TEXT,
    CONSTRAINT valid_status_timestamps CHECK (
        (status = 'enabled' AND enabled_at IS NOT NULL) OR
        (status != 'enabled')
    )
);

-- =============================================================================
-- Plugin Hook Executions Table (Audit Log)
-- =============================================================================

CREATE TABLE plugin_hook_executions (
    id UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
    plugin_id UUID NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    plugin_name TEXT NOT NULL,
    hook_type TEXT NOT NULL,
    event_data TEXT, -- JSON string of event data
    success BOOLEAN NOT NULL DEFAULT false,
    error TEXT,
    duration_ms BIGINT NOT NULL, -- Execution duration in milliseconds
    executed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================================
-- Plugin Statistics Table (Aggregated)
-- =============================================================================

CREATE TABLE plugin_statistics (
    plugin_id UUID PRIMARY KEY REFERENCES plugins(id) ON DELETE CASCADE,
    plugin_name TEXT NOT NULL,
    total_executions BIGINT NOT NULL DEFAULT 0,
    success_count BIGINT NOT NULL DEFAULT 0,
    failure_count BIGINT NOT NULL DEFAULT 0,
    avg_duration_ms DECIMAL(10,2) NOT NULL DEFAULT 0,
    last_executed_at TIMESTAMP,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP
);

-- =============================================================================
-- Plugin Configuration Table (Key-Value Store)
-- =============================================================================

CREATE TABLE plugin_configs (
    plugin_id UUID NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value TEXT NOT NULL,
    type TEXT NOT NULL DEFAULT 'string', -- string, number, boolean, json
    description TEXT,
    updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
    PRIMARY KEY (plugin_id, key)
);

-- =============================================================================
-- Plugin Dependencies Table
-- =============================================================================

CREATE TABLE plugin_dependencies (
    plugin_id UUID NOT NULL REFERENCES plugins(id) ON DELETE CASCADE,
    depends_on_plugin TEXT NOT NULL,
    required_version TEXT NOT NULL,
    optional BOOLEAN NOT NULL DEFAULT false,
    PRIMARY KEY (plugin_id, depends_on_plugin)
);

-- =============================================================================
-- Indexes
-- =============================================================================

-- Main plugins table indexes
CREATE INDEX idx_plugins_name ON plugins(name);
CREATE INDEX idx_plugins_status ON plugins(status);
CREATE INDEX idx_plugins_enabled_at ON plugins(enabled_at) WHERE enabled_at IS NOT NULL;
CREATE INDEX idx_plugins_updated_at ON plugins(updated_at DESC);

-- Hook executions indexes
CREATE INDEX idx_hook_executions_plugin_id ON plugin_hook_executions(plugin_id);
CREATE INDEX idx_hook_executions_hook_type ON plugin_hook_executions(hook_type);
CREATE INDEX idx_hook_executions_executed_at ON plugin_hook_executions(executed_at DESC);
CREATE INDEX idx_hook_executions_success ON plugin_hook_executions(success);
CREATE INDEX idx_hook_executions_plugin_hook ON plugin_hook_executions(plugin_id, hook_type);

-- Statistics indexes
CREATE INDEX idx_plugin_stats_plugin_name ON plugin_statistics(plugin_name);
CREATE INDEX idx_plugin_stats_last_executed ON plugin_statistics(last_executed_at DESC);

-- Config indexes
CREATE INDEX idx_plugin_configs_plugin_id ON plugin_configs(plugin_id);

-- Dependencies indexes
CREATE INDEX idx_plugin_deps_plugin_id ON plugin_dependencies(plugin_id);
CREATE INDEX idx_plugin_deps_depends_on ON plugin_dependencies(depends_on_plugin);

-- =============================================================================
-- Triggers
-- =============================================================================

-- Trigger to update updated_at timestamp on plugins
CREATE OR REPLACE FUNCTION update_plugin_updated_at()
RETURNS TRIGGER AS $$
BEGIN
    NEW.updated_at = CURRENT_TIMESTAMP;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_plugin_updated_at
    BEFORE UPDATE ON plugins
    FOR EACH ROW
    EXECUTE FUNCTION update_plugin_updated_at();

-- Trigger to update plugin statistics on hook execution
CREATE OR REPLACE FUNCTION update_plugin_statistics()
RETURNS TRIGGER AS $$
BEGIN
    INSERT INTO plugin_statistics (
        plugin_id,
        plugin_name,
        total_executions,
        success_count,
        failure_count,
        avg_duration_ms,
        last_executed_at,
        updated_at
    ) VALUES (
        NEW.plugin_id,
        NEW.plugin_name,
        1,
        CASE WHEN NEW.success THEN 1 ELSE 0 END,
        CASE WHEN NOT NEW.success THEN 1 ELSE 0 END,
        NEW.duration_ms,
        NEW.executed_at,
        CURRENT_TIMESTAMP
    )
    ON CONFLICT (plugin_id) DO UPDATE SET
        total_executions = plugin_statistics.total_executions + 1,
        success_count = plugin_statistics.success_count + CASE WHEN NEW.success THEN 1 ELSE 0 END,
        failure_count = plugin_statistics.failure_count + CASE WHEN NOT NEW.success THEN 1 ELSE 0 END,
        avg_duration_ms = (
            (plugin_statistics.avg_duration_ms * plugin_statistics.total_executions + NEW.duration_ms) /
            (plugin_statistics.total_executions + 1)
        ),
        last_executed_at = NEW.executed_at,
        updated_at = CURRENT_TIMESTAMP;

    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

CREATE TRIGGER trigger_update_plugin_statistics
    AFTER INSERT ON plugin_hook_executions
    FOR EACH ROW
    EXECUTE FUNCTION update_plugin_statistics();

-- =============================================================================
-- Functions
-- =============================================================================

-- Function to clean up old plugin hook execution records (keep last 30 days)
CREATE OR REPLACE FUNCTION cleanup_old_plugin_executions()
RETURNS BIGINT AS $$
DECLARE
    deleted_count BIGINT;
BEGIN
    DELETE FROM plugin_hook_executions
    WHERE executed_at < CURRENT_TIMESTAMP - INTERVAL '30 days';

    GET DIAGNOSTICS deleted_count = ROW_COUNT;
    RETURN deleted_count;
END;
$$ LANGUAGE plpgsql;

-- Function to get plugin health status
CREATE OR REPLACE FUNCTION get_plugin_health(plugin_id_param UUID)
RETURNS TABLE (
    plugin_id UUID,
    plugin_name TEXT,
    status plugin_status,
    success_rate DECIMAL,
    avg_duration_ms DECIMAL,
    last_executed_at TIMESTAMP
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        p.id,
        p.name,
        p.status,
        CASE
            WHEN ps.total_executions > 0 THEN
                (ps.success_count::DECIMAL / ps.total_executions::DECIMAL) * 100
            ELSE 0
        END as success_rate,
        ps.avg_duration_ms,
        ps.last_executed_at
    FROM plugins p
    LEFT JOIN plugin_statistics ps ON p.id = ps.plugin_id
    WHERE p.id = plugin_id_param;
END;
$$ LANGUAGE plpgsql;

-- Function to get all enabled plugins
CREATE OR REPLACE FUNCTION get_enabled_plugins()
RETURNS TABLE (
    id UUID,
    name TEXT,
    version TEXT,
    hooks TEXT[]
) AS $$
BEGIN
    RETURN QUERY
    SELECT p.id, p.name, p.version, p.hooks
    FROM plugins p
    WHERE p.status = 'enabled'
    ORDER BY p.name;
END;
$$ LANGUAGE plpgsql;

-- Function to check plugin dependencies
CREATE OR REPLACE FUNCTION check_plugin_dependencies(plugin_name_param TEXT)
RETURNS TABLE (
    dependency_name TEXT,
    required_version TEXT,
    optional BOOLEAN,
    installed BOOLEAN,
    installed_version TEXT,
    satisfied BOOLEAN
) AS $$
BEGIN
    RETURN QUERY
    SELECT
        pd.depends_on_plugin,
        pd.required_version,
        pd.optional,
        p.id IS NOT NULL as installed,
        p.version as installed_version,
        (p.id IS NOT NULL AND (pd.optional OR p.status = 'enabled')) as satisfied
    FROM plugin_dependencies pd
    LEFT JOIN plugins p ON p.name = pd.depends_on_plugin
    WHERE pd.plugin_id = (SELECT id FROM plugins WHERE name = plugin_name_param);
END;
$$ LANGUAGE plpgsql;

-- =============================================================================
-- Comments
-- =============================================================================

COMMENT ON TABLE plugins IS 'Stores installed plugin metadata and configuration';
COMMENT ON TABLE plugin_hook_executions IS 'Audit log of plugin hook executions';
COMMENT ON TABLE plugin_statistics IS 'Aggregated statistics for plugin performance monitoring';
COMMENT ON TABLE plugin_configs IS 'Plugin configuration key-value store';
COMMENT ON TABLE plugin_dependencies IS 'Plugin dependency relationships';

COMMENT ON COLUMN plugins.checksum IS 'SHA256 checksum for plugin integrity verification';
COMMENT ON COLUMN plugins.config IS 'Plugin configuration as JSON object';
COMMENT ON COLUMN plugins.permissions IS 'Array of permission strings required by plugin';
COMMENT ON COLUMN plugins.hooks IS 'Array of hook types this plugin can handle';

COMMENT ON FUNCTION cleanup_old_plugin_executions() IS 'Removes plugin execution records older than 30 days';
COMMENT ON FUNCTION get_plugin_health(UUID) IS 'Returns health and performance metrics for a plugin';
COMMENT ON FUNCTION get_enabled_plugins() IS 'Returns all currently enabled plugins';
COMMENT ON FUNCTION check_plugin_dependencies(TEXT) IS 'Checks if all plugin dependencies are satisfied';
-- +goose StatementEnd

-- +goose Down
-- NOTE: Add rollback statements here if needed
-- For now, we'll keep migrations forward-only for safety
