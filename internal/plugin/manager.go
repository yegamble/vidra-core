package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"athena/internal/domain"
)

type Manager struct {
	plugins map[string]Plugin

	pluginInfo map[string]*PluginInfo

	hooks *HookManager

	pluginDir string

	mu sync.RWMutex

	ctx context.Context

	cancel context.CancelFunc

	wg sync.WaitGroup
}

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

func (m *Manager) Initialize(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if err := os.MkdirAll(m.pluginDir, 0750); err != nil {
		return fmt.Errorf("failed to create plugin directory: %w", err)
	}

	if err := m.discoverPlugins(); err != nil {
		return fmt.Errorf("failed to discover plugins: %w", err)
	}

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

func (m *Manager) Shutdown(ctx context.Context) error {
	m.cancel()

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

func (m *Manager) RegisterPlugin(plugin Plugin, config map[string]any) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	name := plugin.Name()

	if _, exists := m.plugins[name]; exists {
		return fmt.Errorf("plugin %s already registered", name)
	}

	m.plugins[name] = plugin

	info := &PluginInfo{
		Name:        plugin.Name(),
		Version:     plugin.Version(),
		Author:      plugin.Author(),
		Description: plugin.Description(),
		Enabled:     false,
		Config:      config,
		Permissions: []string{},
		Hooks:       []EventType{},
	}

	info.Hooks = m.detectHooks(plugin)

	m.pluginInfo[name] = info

	return nil
}

func (m *Manager) LoadPlugin(manifestPath string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.loadPluginUnlocked(manifestPath)
}

// NOTE: Caller must hold m.mu lock
func (m *Manager) loadPluginUnlocked(manifestPath string) error {
	data, err := os.ReadFile(manifestPath)
	if err != nil {
		return fmt.Errorf("failed to read manifest: %w", err)
	}

	var manifest struct {
		Name        string         `json:"name"`
		Version     string         `json:"version"`
		Author      string         `json:"author"`
		Description string         `json:"description"`
		Enabled     bool           `json:"enabled"`
		Config      map[string]any `json:"config"`
		Permissions []string       `json:"permissions"`
		Hooks       []string       `json:"hooks"`
		Main        string         `json:"main"`
	}

	if err := json.Unmarshal(data, &manifest); err != nil {
		return fmt.Errorf("failed to parse manifest: %w", err)
	}

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

	for i, hook := range manifest.Hooks {
		info.Hooks[i] = EventType(hook)
	}

	m.pluginInfo[manifest.Name] = info

	return nil
}

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
		return nil
	}

	if err := m.initializePlugin(ctx, plugin, info); err != nil {
		return fmt.Errorf("failed to initialize plugin: %w", err)
	}

	plugin.SetEnabled(true)
	info.Enabled = true

	return nil
}

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
		return nil
	}

	if err := plugin.Shutdown(ctx); err != nil {
		return fmt.Errorf("failed to shutdown plugin: %w", err)
	}

	m.hooks.UnregisterPluginHooks(name)

	plugin.SetEnabled(false)
	info.Enabled = false

	return nil
}

func (m *Manager) GetPlugin(name string) (Plugin, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plugin, exists := m.plugins[name]
	if !exists {
		return nil, fmt.Errorf("plugin %s not found", name)
	}

	return plugin, nil
}

func (m *Manager) GetPluginInfo(name string) (*PluginInfo, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	info, exists := m.pluginInfo[name]
	if !exists {
		return nil, fmt.Errorf("plugin info for %s not found", name)
	}

	return info, nil
}

func (m *Manager) ListPlugins() []*PluginInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*PluginInfo, 0, len(m.pluginInfo))
	for _, info := range m.pluginInfo {
		result = append(result, info)
	}

	return result
}

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

	if plugin.Enabled() {
		if err := plugin.Shutdown(ctx); err != nil {
			return fmt.Errorf("failed to shutdown plugin for config update: %w", err)
		}

		info.Config = config

		if err := m.initializePlugin(ctx, plugin, info); err != nil {
			return fmt.Errorf("failed to reinitialize plugin with new config: %w", err)
		}
	} else {
		info.Config = config
	}

	return nil
}

func (m *Manager) GetHookManager() *HookManager {
	return m.hooks
}

func (m *Manager) TriggerEvent(ctx context.Context, eventType EventType, data any) error {
	return m.hooks.Trigger(ctx, eventType, data)
}

// NOTE: Caller must hold m.mu lock
func (m *Manager) discoverPlugins() error {
	manifests, err := filepath.Glob(filepath.Join(m.pluginDir, "*", "plugin.json"))
	if err != nil {
		return fmt.Errorf("failed to glob plugin manifests: %w", err)
	}

	for _, manifestPath := range manifests {
		if err := m.loadPluginUnlocked(manifestPath); err != nil {
			slog.Warn("failed to load plugin", "path", manifestPath, "error", err)
			continue
		}
	}

	return nil
}

func (m *Manager) initializePlugin(ctx context.Context, plugin Plugin, info *PluginInfo) error {
	initCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	if err := plugin.Initialize(initCtx, info.Config); err != nil {
		return fmt.Errorf("plugin initialization failed: %w", err)
	}

	if err := m.registerPluginHooks(plugin); err != nil {
		_ = plugin.Shutdown(ctx)
		return fmt.Errorf("failed to register hooks: %w", err)
	}

	return nil
}

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

func (m *Manager) registerPluginHooks(plugin Plugin) error {
	name := plugin.Name()

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

	return nil
}

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

func (m *Manager) GetPluginDir() string {
	return m.pluginDir
}

// InstallFromURL downloads a plugin zip from the given URL, extracts it,
// validates the manifest, moves files to the plugin directory, and loads the plugin.
func (m *Manager) InstallFromURL(_ context.Context, pluginURL string) error {
	resp, err := http.Get(pluginURL) //nolint:gosec // URL validated by handler (https-only)
	if err != nil {
		return fmt.Errorf("download plugin: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download plugin: unexpected status %d", resp.StatusCode)
	}

	// Read the archive into memory (limit to 100MB)
	archiveData, err := io.ReadAll(io.LimitReader(resp.Body, 100<<20))
	if err != nil {
		return fmt.Errorf("read plugin archive: %w", err)
	}

	// Extract and validate the manifest from the ZIP
	manifest, err := m.extractManifestFromZip(archiveData)
	if err != nil {
		return fmt.Errorf("extract manifest: %w", err)
	}

	if err := ValidatePermissions(manifest.Permissions); err != nil {
		return fmt.Errorf("invalid permissions: %w", err)
	}

	// Extract the archive to a temp directory
	tempDir, err := os.MkdirTemp("", "plugin-url-install-*")
	if err != nil {
		return fmt.Errorf("create temp directory: %w", err)
	}
	defer func() { _ = os.RemoveAll(tempDir) }()

	pluginDir, err := m.extractZipArchive(archiveData, tempDir, manifest.Name)
	if err != nil {
		return fmt.Errorf("extract plugin archive: %w", err)
	}

	// Ensure the plugin directory exists
	if err := os.MkdirAll(m.pluginDir, 0750); err != nil {
		return fmt.Errorf("prepare plugin directory: %w", err)
	}

	// Move extracted files to the final plugin directory
	finalPath := filepath.Join(m.pluginDir, manifest.Name)
	if !strings.HasPrefix(filepath.Clean(finalPath), filepath.Clean(m.pluginDir)+string(os.PathSeparator)) {
		return fmt.Errorf("plugin path escapes plugin directory")
	}

	// Remove any existing installation
	_ = os.RemoveAll(finalPath)

	if err := os.Rename(pluginDir, finalPath); err != nil {
		return fmt.Errorf("move plugin into place: %w", err)
	}

	// Load the plugin manifest so the manager registers it
	if err := m.LoadPlugin(filepath.Join(finalPath, "plugin.json")); err != nil {
		// Clean up on failure
		_ = os.RemoveAll(finalPath)
		return fmt.Errorf("load plugin manifest: %w", err)
	}

	slog.Info("plugin installed from URL", "name", manifest.Name, "version", manifest.Version, "url", pluginURL)
	return nil
}

// extractManifestFromZip reads and validates plugin.json from a ZIP archive.
func (m *Manager) extractManifestFromZip(zipData []byte) (*PluginInfo, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return nil, fmt.Errorf("failed to read ZIP: %w", err)
	}

	for _, f := range zipReader.File {
		if f.Name == "plugin.json" || strings.HasSuffix(f.Name, "/plugin.json") {
			rc, err := f.Open()
			if err != nil {
				return nil, fmt.Errorf("failed to open plugin.json: %w", err)
			}
			defer func() { _ = rc.Close() }()

			var manifest PluginInfo
			if err := json.NewDecoder(rc).Decode(&manifest); err != nil {
				return nil, fmt.Errorf("failed to parse plugin.json: %w", err)
			}

			if manifest.Name == "" {
				return nil, fmt.Errorf("plugin name is required")
			}
			if manifest.Version == "" {
				return nil, fmt.Errorf("plugin version is required")
			}
			if manifest.Author == "" {
				return nil, fmt.Errorf("plugin author is required")
			}

			return &manifest, nil
		}
	}

	return nil, fmt.Errorf("plugin.json not found in ZIP")
}

// extractZipArchive extracts a ZIP archive to destDir/<pluginName>/ with path traversal protection.
func (m *Manager) extractZipArchive(zipData []byte, destDir, pluginName string) (string, error) {
	zipReader, err := zip.NewReader(bytes.NewReader(zipData), int64(len(zipData)))
	if err != nil {
		return "", fmt.Errorf("failed to read ZIP: %w", err)
	}

	pluginDir := filepath.Join(destDir, pluginName)
	if err := os.MkdirAll(pluginDir, 0750); err != nil {
		return "", fmt.Errorf("failed to create plugin directory: %w", err)
	}

	const maxFileSize = 100 * 1024 * 1024

	for _, f := range zipReader.File {
		if strings.Contains(f.Name, "..") {
			return "", fmt.Errorf("invalid file path: %s", f.Name)
		}

		destPath := filepath.Clean(filepath.Join(pluginDir, f.Name))
		if !strings.HasPrefix(destPath, filepath.Clean(pluginDir)+string(os.PathSeparator)) {
			return "", fmt.Errorf("invalid file path (path traversal): %s", f.Name)
		}

		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(destPath, f.Mode()); err != nil {
				return "", fmt.Errorf("failed to create directory: %w", err)
			}
			continue
		}

		if err := os.MkdirAll(filepath.Dir(destPath), 0750); err != nil {
			return "", fmt.Errorf("failed to create parent directory: %w", err)
		}

		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("failed to open file in ZIP: %w", err)
		}

		destFile, err := os.OpenFile(destPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, f.Mode())
		if err != nil {
			_ = rc.Close()
			return "", fmt.Errorf("failed to create file: %w", err)
		}

		if _, err := io.Copy(destFile, io.LimitReader(rc, maxFileSize)); err != nil {
			_ = destFile.Close()
			_ = rc.Close()
			return "", fmt.Errorf("failed to write file: %w", err)
		}

		_ = destFile.Close()
		_ = rc.Close()
	}

	return pluginDir, nil
}
