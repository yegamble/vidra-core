package domain

import (
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

// Plugin domain errors
var (
	ErrPluginNotFound        = errors.New("plugin not found")
	ErrPluginAlreadyExists   = errors.New("plugin already exists")
	ErrPluginInvalidName     = errors.New("invalid plugin name")
	ErrPluginInvalidVersion  = errors.New("invalid plugin version")
	ErrPluginInvalidConfig   = errors.New("invalid plugin configuration")
	ErrPluginNotEnabled      = errors.New("plugin not enabled")
	ErrPluginAlreadyEnabled  = errors.New("plugin already enabled")
	ErrPluginAlreadyDisabled = errors.New("plugin already disabled")
	ErrPluginInstallFailed   = errors.New("plugin installation failed")
	ErrPluginUninstallFailed = errors.New("plugin uninstallation failed")
	ErrPluginInvalidManifest = errors.New("invalid plugin manifest")
	ErrPluginMissingPerm     = errors.New("plugin missing required permission")
	ErrPluginExecFailed      = errors.New("plugin execution failed")
)

// PluginStatus represents the status of a plugin
type PluginStatus string

const (
	PluginStatusInstalled PluginStatus = "installed"
	PluginStatusEnabled   PluginStatus = "enabled"
	PluginStatusDisabled  PluginStatus = "disabled"
	PluginStatusFailed    PluginStatus = "failed"
	PluginStatusUpdating  PluginStatus = "updating"
)

// PluginRecord represents a plugin stored in the database
type PluginRecord struct {
	ID          uuid.UUID      `db:"id" json:"id"`
	Name        string         `db:"name" json:"name"`
	Version     string         `db:"version" json:"version"`
	Author      string         `db:"author" json:"author"`
	Description string         `db:"description" json:"description"`
	Status      PluginStatus   `db:"status" json:"status"`
	Config      map[string]any `db:"config" json:"config"`
	Permissions []string       `db:"permissions" json:"permissions"`
	Hooks       []string       `db:"hooks" json:"hooks"`
	InstallPath string         `db:"install_path" json:"install_path"`
	Checksum    string         `db:"checksum" json:"checksum"`
	InstalledAt time.Time      `db:"installed_at" json:"installed_at"`
	UpdatedAt   time.Time      `db:"updated_at" json:"updated_at"`
	EnabledAt   *time.Time     `db:"enabled_at" json:"enabled_at,omitempty"`
	DisabledAt  *time.Time     `db:"disabled_at" json:"disabled_at,omitempty"`
	LastError   string         `db:"last_error" json:"last_error,omitempty"`
}

// Validate validates a plugin record
func (p *PluginRecord) Validate() error {
	if p.Name == "" {
		return ErrPluginInvalidName
	}

	if p.Version == "" {
		return ErrPluginInvalidVersion
	}

	if p.Author == "" {
		return errors.New("plugin author is required")
	}

	if p.Description == "" {
		return errors.New("plugin description is required")
	}

	if p.InstallPath == "" {
		return errors.New("plugin install path is required")
	}

	// Validate status
	switch p.Status {
	case PluginStatusInstalled, PluginStatusEnabled, PluginStatusDisabled, PluginStatusFailed, PluginStatusUpdating:
		// Valid status
	default:
		return fmt.Errorf("invalid plugin status: %s", p.Status)
	}

	return nil
}

// IsEnabled returns true if the plugin is enabled
func (p *PluginRecord) IsEnabled() bool {
	return p.Status == PluginStatusEnabled
}

// IsDisabled returns true if the plugin is disabled
func (p *PluginRecord) IsDisabled() bool {
	return p.Status == PluginStatusDisabled
}

// IsFailed returns true if the plugin is in a failed state
func (p *PluginRecord) IsFailed() bool {
	return p.Status == PluginStatusFailed
}

// Enable marks the plugin as enabled
func (p *PluginRecord) Enable() error {
	if p.Status == PluginStatusEnabled {
		return ErrPluginAlreadyEnabled
	}

	now := time.Now()
	p.Status = PluginStatusEnabled
	p.EnabledAt = &now
	p.DisabledAt = nil
	p.UpdatedAt = now
	p.LastError = ""

	return nil
}

// Disable marks the plugin as disabled
func (p *PluginRecord) Disable() error {
	if p.Status == PluginStatusDisabled {
		return ErrPluginAlreadyDisabled
	}

	now := time.Now()
	p.Status = PluginStatusDisabled
	p.DisabledAt = &now
	p.UpdatedAt = now

	return nil
}

// MarkFailed marks the plugin as failed with an error message
func (p *PluginRecord) MarkFailed(err error) {
	p.Status = PluginStatusFailed
	p.LastError = err.Error()
	p.UpdatedAt = time.Now()
}

// ClearError clears the last error
func (p *PluginRecord) ClearError() {
	p.LastError = ""
	p.UpdatedAt = time.Now()
}

// UpdateConfig updates the plugin configuration
func (p *PluginRecord) UpdateConfig(config map[string]any) error {
	if config == nil {
		return ErrPluginInvalidConfig
	}

	p.Config = config
	p.UpdatedAt = time.Now()

	return nil
}

// HasPermission checks if the plugin has a specific permission
func (p *PluginRecord) HasPermission(permission string) bool {
	for _, perm := range p.Permissions {
		if perm == permission {
			return true
		}
	}
	return false
}

// HasHook checks if the plugin supports a specific hook
func (p *PluginRecord) HasHook(hook string) bool {
	for _, h := range p.Hooks {
		if h == hook {
			return true
		}
	}
	return false
}

// PluginHookExecution represents a plugin hook execution record
type PluginHookExecution struct {
	ID         uuid.UUID `db:"id" json:"id"`
	PluginID   uuid.UUID `db:"plugin_id" json:"plugin_id"`
	PluginName string    `db:"plugin_name" json:"plugin_name"`
	HookType   string    `db:"hook_type" json:"hook_type"`
	EventData  string    `db:"event_data" json:"event_data"`
	Success    bool      `db:"success" json:"success"`
	Error      string    `db:"error" json:"error,omitempty"`
	Duration   int64     `db:"duration_ms" json:"duration_ms"` // Duration in milliseconds
	ExecutedAt time.Time `db:"executed_at" json:"executed_at"`
}

// PluginStatistics represents aggregated statistics for a plugin
type PluginStatistics struct {
	PluginID        uuid.UUID `db:"plugin_id" json:"plugin_id"`
	PluginName      string    `db:"plugin_name" json:"plugin_name"`
	TotalExecutions int64     `db:"total_executions" json:"total_executions"`
	SuccessCount    int64     `db:"success_count" json:"success_count"`
	FailureCount    int64     `db:"failure_count" json:"failure_count"`
	AvgDuration     float64   `db:"avg_duration_ms" json:"avg_duration_ms"`
	LastExecutedAt  time.Time `db:"last_executed_at" json:"last_executed_at"`
}

// SuccessRate returns the success rate as a percentage
func (ps *PluginStatistics) SuccessRate() float64 {
	if ps.TotalExecutions == 0 {
		return 0
	}
	return (float64(ps.SuccessCount) / float64(ps.TotalExecutions)) * 100
}

// FailureRate returns the failure rate as a percentage
func (ps *PluginStatistics) FailureRate() float64 {
	if ps.TotalExecutions == 0 {
		return 0
	}
	return (float64(ps.FailureCount) / float64(ps.TotalExecutions)) * 100
}

// PluginManifest represents the plugin.json manifest file structure
type PluginManifest struct {
	Name         string            `json:"name"`
	Version      string            `json:"version"`
	Author       string            `json:"author"`
	Description  string            `json:"description"`
	License      string            `json:"license,omitempty"`
	Homepage     string            `json:"homepage,omitempty"`
	Repository   string            `json:"repository,omitempty"`
	Permissions  []string          `json:"permissions"`
	Hooks        []string          `json:"hooks"`
	Config       map[string]any    `json:"config,omitempty"`
	ConfigSchema map[string]any    `json:"config_schema,omitempty"`
	Main         string            `json:"main"` // Entry point
	Dependencies map[string]string `json:"dependencies,omitempty"`
}

// Validate validates the plugin manifest
func (pm *PluginManifest) Validate() error {
	if pm.Name == "" {
		return ErrPluginInvalidName
	}

	if pm.Version == "" {
		return ErrPluginInvalidVersion
	}

	if pm.Author == "" {
		return errors.New("manifest author is required")
	}

	if pm.Description == "" {
		return errors.New("manifest description is required")
	}

	if pm.Main == "" {
		return errors.New("manifest main entry point is required")
	}

	// Validate semantic versioning format (basic check)
	if !isValidSemanticVersion(pm.Version) {
		return fmt.Errorf("invalid semantic version: %s", pm.Version)
	}

	return nil
}

// ToPluginRecord converts a manifest to a plugin record
func (pm *PluginManifest) ToPluginRecord(installPath, checksum string) *PluginRecord {
	now := time.Now()

	return &PluginRecord{
		ID:          uuid.New(),
		Name:        pm.Name,
		Version:     pm.Version,
		Author:      pm.Author,
		Description: pm.Description,
		Status:      PluginStatusInstalled,
		Config:      pm.Config,
		Permissions: pm.Permissions,
		Hooks:       pm.Hooks,
		InstallPath: installPath,
		Checksum:    checksum,
		InstalledAt: now,
		UpdatedAt:   now,
	}
}

// PluginConfig represents a configuration value for a plugin
type PluginConfig struct {
	PluginID    uuid.UUID `db:"plugin_id" json:"plugin_id"`
	Key         string    `db:"key" json:"key"`
	Value       string    `db:"value" json:"value"`
	Type        string    `db:"type" json:"type"` // string, number, boolean, json
	Description string    `db:"description" json:"description"`
	UpdatedAt   time.Time `db:"updated_at" json:"updated_at"`
}

// PluginDependency represents a dependency between plugins
type PluginDependency struct {
	PluginID        uuid.UUID `db:"plugin_id" json:"plugin_id"`
	DependsOnPlugin string    `db:"depends_on_plugin" json:"depends_on_plugin"`
	RequiredVersion string    `db:"required_version" json:"required_version"`
	Optional        bool      `db:"optional" json:"optional"`
}

// Helper functions

// isValidSemanticVersion checks if a version string follows semantic versioning (basic check)
func isValidSemanticVersion(version string) bool {
	// Basic check: version should have at least major.minor.patch format
	// Full semver validation would be more complex
	if len(version) == 0 {
		return false
	}

	// Allow versions like "1.0.0", "1.0.0-alpha", "1.0.0+build"
	// This is a simplified check
	for i, c := range version {
		if i == 0 && (c < '0' || c > '9') {
			return false
		}
	}

	return true
}

// ValidatePluginName validates a plugin name
func ValidatePluginName(name string) error {
	if name == "" {
		return ErrPluginInvalidName
	}

	if len(name) < 3 || len(name) > 100 {
		return fmt.Errorf("plugin name must be between 3 and 100 characters")
	}

	// Check for valid characters (alphanumeric, dash, underscore)
	for _, c := range name {
		if (c < 'a' || c > 'z') && (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '-' && c != '_' {
			return fmt.Errorf("plugin name contains invalid character: %c", c)
		}
	}

	return nil
}

// ValidatePluginVersion validates a plugin version
func ValidatePluginVersion(version string) error {
	if version == "" {
		return ErrPluginInvalidVersion
	}

	if !isValidSemanticVersion(version) {
		return fmt.Errorf("invalid semantic version format: %s", version)
	}

	return nil
}
