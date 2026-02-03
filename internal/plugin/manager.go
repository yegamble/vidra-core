package plugin

import (
	"athena/internal/domain"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Manager manages the lifecycle of all plugins
type Manager struct {
	// plugins stores all loaded plugins by name
	plugins map[string]Plugin

	// pluginInfo stores metadata for all plugins
	pluginInfo map[string]*PluginInfo

	// hooks stores registered hooks by event type
	hooks *HookManager

	// pluginDir is the directory where plugins are stored
	pluginDir string

	// mu protects concurrent access to plugins
	mu sync.RWMutex

	// ctx is the manager's context
	ctx context.Context

	// cancel cancels the manager's context
	cancel context.CancelFunc

	// wg tracks running goroutines
	wg sync.WaitGroup
}

// NewManager creates a new plugin manager
func NewManager(pluginDir string) *Manager {
	ctx, cancel := context.WithCancel(context.Background())

	return &Manager{
		plugins:    make(map[string]Plugin),
		pluginInfo: make(map[string]*PluginInfo),
		hooks:      NewHookManager(),
		pluginDir:  pluginDir,
		ctx:        ctx,
		cancel:     cancel,
	}
}

// Initialize initializes the plugin manager and loads all plugins
func (m *Manager) Initialize(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Ensure plugin directory exists
	if err := os.MkdirAll(m.pluginDir, 0750); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}

	// Discover and load plugins
	if err := m.discoverPlugins(); err != nil {
		return fmt.Errorf("failed to discover plugins: %w", err)
	}

	// Initialize enabled plugins
	for name, plugin := range m.plugins {
		info := m.pluginInfo[name]
		if info != nil && info.Enabled {
			if err := m.initializePlugin(ctx, plugin, info); err != nil {
				return fmt.Errorf("failed to initialize plugin %s: %w", name, err)
			}
		}
	}

	return nil
}

// Shutdown gracefully shuts down all plugins
func (m *Manager) Shutdown(ctx context.Context) error {
	m.cancel()

	// Wait for all goroutines with timeout
	done := make(chan struct{})
	go func() {
		m.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-ctx.Done():
		return fmt.Errorf("shutdown timeout: %w", ctx.Err())
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Shutdown all plugins
	var errs []error
	for name, plugin := range m.plugins {
		if plugin.Enabled() {
			if err := plugin.Shutdown(ctx); err != nil {
				errs = append(errs, fmt.Errorf("plugin %s shutdown error: %w", name, err))
			}
		}
	}

	if len(errs) > 0 {
		return fmt.Errorf("plugin shutdown errors: %v", errs)
	}

	return nil
}

// RegisterPlugin registers a plugin with the manager
// This allows programmatic plugin registration without file-based discovery
func (m *Manager) RegisterPlugin(plugin Plugin, config map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := plugin.Name()

	// Check if plugin already exists
	if _, exists := m.plugins[name]; exists {
		return fmt.Errorf("plugin %s already registered", name)
	}

	// Store plugin
	m.plugins[name] = plugin

	// Create plugin info
	info := &PluginInfo{
		Name:        plugin.Name(),
		Version:     plugin.Version(),
		Author:      plugin.Author(),
		Description: plugin.Description(),
		Enabled:     false, // Disabled by default
		Config:      config,
		Permissions: []string{},
		Hooks:       []EventType{},
	}

	// Detect hooks based on implemented interfaces
	info.Hooks = m.detectHooks(plugin)

	m.pluginInfo[name] = info

	return nil
}

// LoadPlugin loads a plugin from a manifest file
func (m *Manager) LoadPlugin(manifestPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadPluginUnlocked(manifestPath)
}

// loadPluginUnlocked loads a plugin without acquiring the lock
// NOTE: Caller must hold m.mu lock
func (m *Manager) loadPluginUnlocked(manifestPath string) error {
	// Read manifest file
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	// Parse manifest
	var manifest struct {
		Name        string         `json:"name"`
		Version     string         `json:"version"`
		Author      string         `json:"author"`
		Description string         `json:"description"`
		Enabled     bool           `json:"enabled"`
		Config      map[string]any `json:"config"`
		Permissions []string       `json:"permissions"`
		Hooks       []string       `json:"hooks"`
		Main        string         `json:"main"` // Entry point (for future use)
	}

	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

	// Create plugin info
	info := &PluginInfo{
		Name:        manifest.Name,
		Version:     manifest.Version,
		Author:      manifest.Author,
		Description: manifest.Description,
		Enabled:     manifest.Enabled,
		Config:      manifest.Config,
		Permissions: manifest.Permissions,
		Hooks:       make([]EventType, len(manifest.Hooks)),
	}

	// Convert hooks to EventType
	for i, hook := range manifest.Hooks {
		info.Hooks[i] = EventType(hook)
	}

	m.pluginInfo[manifest.Name] = info

	return nil
}

// EnablePlugin enables a plugin by name
func (m *Manager) EnablePlugin(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, exists := m.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	info := m.pluginInfo[name]
	if info == nil {
		return fmt.Errorf("plugin info for %s not found", name)
	}

	if plugin.Enabled() {
		return nil // Already enabled
	}

	// Initialize plugin
	if err := m.initializePlugin(ctx, plugin, info); err != nil {
		return fmt.Errorf("failed to initialize plugin: %w", err)
	}

	plugin.SetEnabled(true)
	info.Enabled = true

	return nil
}

// DisablePlugin disables a plugin by name
func (m *Manager) DisablePlugin(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, exists := m.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	info := m.pluginInfo[name]
	if info == nil {
		return fmt.Errorf("plugin info for %s not found", name)
	}

	if !plugin.Enabled() {
		return nil // Already disabled
	}

	// Shutdown plugin
	if err := plugin.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown plugin: %w", err)
	}

	// Unregister hooks
	m.hooks.UnregisterPluginHooks(name)

	plugin.SetEnabled(false)
	info.Enabled = false

	return nil
}

// GetPlugin returns a plugin by name
func (m *Manager) GetPlugin(name string) (Plugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugin, exists := m.plugins[name]
	if !exists {
		return nil, fmt.Errorf("plugin %s not found", name)
	}

	return plugin, nil
}

// GetPluginInfo returns plugin metadata by name
func (m *Manager) GetPluginInfo(name string) (*PluginInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, exists := m.pluginInfo[name]
	if !exists {
		return nil, fmt.Errorf("plugin info for %s not found", name)
	}

	return info, nil
}

// ListPlugins returns all loaded plugins
func (m *Manager) ListPlugins() []*PluginInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*PluginInfo, 0, len(m.pluginInfo))
	for _, info := range m.pluginInfo {
		result = append(result, info)
	}

	return result
}

// UpdatePluginConfig updates a plugin's configuration
func (m *Manager) UpdatePluginConfig(ctx context.Context, name string, config map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	plugin, exists := m.plugins[name]
	if !exists {
		return fmt.Errorf("plugin %s not found", name)
	}

	info := m.pluginInfo[name]
	if info == nil {
		return fmt.Errorf("plugin info for %s not found", name)
	}

	// If plugin is enabled, reinitialize with new config
	if plugin.Enabled() {
		// Shutdown first
		if err := plugin.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown plugin for config update: %w", err)
		}

		// Update config
		info.Config = config

		// Reinitialize
		if err := m.initializePlugin(ctx, plugin, info); err != nil {
			return fmt.Errorf("failed to reinitialize plugin with new config: %w", err)
		}
	} else {
		// Just update config
		info.Config = config
	}

	return nil
}

// GetHookManager returns the hook manager
func (m *Manager) GetHookManager() *HookManager {
	return m.hooks
}

// TriggerEvent triggers a hook event for all registered plugins
func (m *Manager) TriggerEvent(ctx context.Context, eventType EventType, data any) error {
	return m.hooks.Trigger(ctx, eventType, data)
}

// Private methods

// discoverPlugins discovers all plugins in the plugin directory
// NOTE: Caller must hold m.mu lock
func (m *Manager) discoverPlugins() error {
	// Find all manifest files
	manifests, err := filepath.Glob(filepath.Join(m.pluginDir, "*", "plugin.json"))
	if err != nil {
		return fmt.Errorf("failed to glob plugin manifests: %w", err)
	}

	// Load each plugin
	for _, manifestPath := range manifests {
		if err := m.loadPluginUnlocked(manifestPath); err != nil {
			// Log error but continue loading other plugins
			fmt.Printf("Warning: failed to load plugin from %s: %v\n", manifestPath, err)
			continue
		}
	}

	return nil
}

// initializePlugin initializes a plugin with its configuration
func (m *Manager) initializePlugin(ctx context.Context, plugin Plugin, info *PluginInfo) error {
	// Create context with timeout
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	// Initialize plugin
	if err := plugin.Initialize(initCtx, info.Config); err != nil {
		return fmt.Errorf("plugin initialization failed: %w", err)
	}

	// Register hooks based on plugin type
	if err := m.registerPluginHooks(plugin); err != nil {
		// Shutdown plugin if hook registration fails
		_ = plugin.Shutdown(ctx)
		return fmt.Errorf("failed to register hooks: %w", err)
	}

	return nil
}

// registerVideoHook registers a hook for video domain entities
func (m *Manager) registerVideoHook(eventType EventType, pluginName string, handler func(context.Context, *domain.Video) error) {
	m.hooks.Register(eventType, pluginName, func(ctx context.Context, data any) error {
		if ed, ok := data.(*EventData); ok {
			if video, ok := ed.Data.(*domain.Video); ok {
				return handler(ctx, video)
			}
		}
		return fmt.Errorf("invalid data type for video event")
	})
}

// registerUserHook registers a hook for user domain entities
func (m *Manager) registerUserHook(eventType EventType, pluginName string, handler func(context.Context, *domain.User) error) {
	m.hooks.Register(eventType, pluginName, func(ctx context.Context, data any) error {
		if ed, ok := data.(*EventData); ok {
			if user, ok := ed.Data.(*domain.User); ok {
				return handler(ctx, user)
			}
		}
		return fmt.Errorf("invalid data type for user event")
	})
}

// registerPluginHooks registers all hooks for a plugin
func (m *Manager) registerPluginHooks(plugin Plugin) error {
	name := plugin.Name()

	// Video hooks
	if vp, ok := plugin.(VideoPlugin); ok {
		m.registerVideoHook(EventVideoUploaded, name, vp.OnVideoUploaded)
		m.registerVideoHook(EventVideoProcessed, name, vp.OnVideoProcessed)
		m.registerVideoHook(EventVideoUpdated, name, vp.OnVideoUpdated)

		m.hooks.Register(EventVideoDeleted, name, func(ctx context.Context, data any) error {
			if ed, ok := data.(*EventData); ok {
				if videoID, ok := ed.Data.(string); ok {
					return vp.OnVideoDeleted(ctx, videoID)
				}
			}
			return fmt.Errorf("invalid data type for video.deleted event")
		})
	}

	// User hooks
	if up, ok := plugin.(UserPlugin); ok {
		m.registerUserHook(EventUserRegistered, name, up.OnUserRegistered)
		m.registerUserHook(EventUserLogin, name, up.OnUserLogin)
		m.registerUserHook(EventUserUpdated, name, up.OnUserUpdated)

		m.hooks.Register(EventUserDeleted, name, func(ctx context.Context, data any) error {
			if ed, ok := data.(*EventData); ok {
				if userID, ok := ed.Data.(string); ok {
					return up.OnUserDeleted(ctx, userID)
				}
			}
			return fmt.Errorf("invalid data type for user.deleted event")
		})
	}

	// Channel hooks
	if cp, ok := plugin.(ChannelPlugin); ok {
		m.hooks.Register(EventChannelCreated, name, func(ctx context.Context, data any) error {
			if ed, ok := data.(*EventData); ok {
				return cp.OnChannelCreated(ctx, ed.Data.(*domain.Channel))
			}
			return fmt.Errorf("invalid data type for channel.created event")
		})

		m.hooks.Register(EventChannelUpdated, name, func(ctx context.Context, data any) error {
			if ed, ok := data.(*EventData); ok {
				return cp.OnChannelUpdated(ctx, ed.Data.(*domain.Channel))
			}
			return fmt.Errorf("invalid data type for channel.updated event")
		})

		m.hooks.Register(EventChannelDeleted, name, func(ctx context.Context, data any) error {
			if ed, ok := data.(*EventData); ok {
				if channelID, ok := ed.Data.(string); ok {
					return cp.OnChannelDeleted(ctx, channelID)
				}
			}
			return fmt.Errorf("invalid data type for channel.deleted event")
		})
	}

	// Analytics hooks
	if ap, ok := plugin.(AnalyticsPlugin); ok {
		m.hooks.Register(EventAnalyticsEvent, name, func(ctx context.Context, data any) error {
			if ed, ok := data.(*EventData); ok {
				return ap.OnAnalyticsEvent(ctx, ed.Data.(*domain.AnalyticsEvent))
			}
			return fmt.Errorf("invalid data type for analytics.event")
		})

		m.hooks.Register(EventDailyAggregation, name, func(ctx context.Context, data any) error {
			if ed, ok := data.(*EventData); ok {
				if meta, ok := ed.Data.(map[string]any); ok {
					videoID := meta["video_id"].(string)
					date := meta["date"].(string)
					return ap.OnDailyAggregation(ctx, videoID, date)
				}
			}
			return fmt.Errorf("invalid data type for analytics.aggregation")
		})
	}

	// Add more hook registrations for other plugin types...
	// (LiveStreamPlugin, CommentPlugin, StoragePlugin, etc.)

	return nil
}

// detectHooks detects which hooks a plugin can handle based on its interface
func (m *Manager) detectHooks(plugin Plugin) []EventType {
	var hooks []EventType

	if _, ok := plugin.(VideoPlugin); ok {
		hooks = append(hooks, EventVideoUploaded, EventVideoProcessed, EventVideoDeleted, EventVideoUpdated)
	}

	if _, ok := plugin.(UserPlugin); ok {
		hooks = append(hooks, EventUserRegistered, EventUserLogin, EventUserDeleted, EventUserUpdated)
	}

	if _, ok := plugin.(ChannelPlugin); ok {
		hooks = append(hooks, EventChannelCreated, EventChannelUpdated, EventChannelDeleted, EventChannelSubscribed)
	}

	if _, ok := plugin.(LiveStreamPlugin); ok {
		hooks = append(hooks, EventStreamStarted, EventStreamEnded, EventViewerJoined, EventViewerLeft)
	}

	if _, ok := plugin.(CommentPlugin); ok {
		hooks = append(hooks, EventCommentCreated, EventCommentDeleted, EventCommentReported)
	}

	if _, ok := plugin.(StoragePlugin); ok {
		hooks = append(hooks, EventFileStored, EventFileDeleted)
	}

	if _, ok := plugin.(ModerationPlugin); ok {
		hooks = append(hooks, EventContentReported, EventContentModerated)
	}

	if _, ok := plugin.(AnalyticsPlugin); ok {
		hooks = append(hooks, EventAnalyticsEvent, EventDailyAggregation)
	}

	if _, ok := plugin.(NotificationPlugin); ok {
		hooks = append(hooks, EventNotificationCreated, EventNotificationSent)
	}

	if _, ok := plugin.(FederationPlugin); ok {
		hooks = append(hooks, EventActivityReceived, EventActivitySent)
	}

	if _, ok := plugin.(SearchPlugin); ok {
		hooks = append(hooks, EventSearchQuery, EventVideoIndexed)
	}

	return hooks
}

// GetPluginDir returns the plugin directory path
func (m *Manager) GetPluginDir() string {
	return m.pluginDir
}
