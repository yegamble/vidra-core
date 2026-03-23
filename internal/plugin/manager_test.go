package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// MockPlugin implements the Plugin interface for testing
type MockPlugin struct {
	name           string
	version        string
	author         string
	description    string
	enabled        bool
	initCalled     bool
	shutdownCalled bool
	initError      error
	shutdownError  error
}

func NewMockPlugin(name, version, author, description string) *MockPlugin {
	return &MockPlugin{
		name:        name,
		version:     version,
		author:      author,
		description: description,
		enabled:     false,
	}
}

func (m *MockPlugin) Name() string            { return m.name }
func (m *MockPlugin) Version() string         { return m.version }
func (m *MockPlugin) Author() string          { return m.author }
func (m *MockPlugin) Description() string     { return m.description }
func (m *MockPlugin) Enabled() bool           { return m.enabled }
func (m *MockPlugin) SetEnabled(enabled bool) { m.enabled = enabled }

func (m *MockPlugin) Initialize(ctx context.Context, config map[string]any) error {
	m.initCalled = true
	return m.initError
}

func (m *MockPlugin) Shutdown(ctx context.Context) error {
	m.shutdownCalled = true
	return m.shutdownError
}

// MockVideoPlugin implements VideoPlugin for testing
type MockVideoPlugin struct {
	*MockPlugin
	videoUploadedCalled  bool
	videoProcessedCalled bool
	videoDeletedCalled   bool
	videoUpdatedCalled   bool
}

func NewMockVideoPlugin(name string) *MockVideoPlugin {
	return &MockVideoPlugin{
		MockPlugin: NewMockPlugin(name, "1.0.0", "Test Author", "Test plugin"),
	}
}

func (m *MockVideoPlugin) OnVideoUploaded(ctx context.Context, video *domain.Video) error {
	m.videoUploadedCalled = true
	return nil
}

func (m *MockVideoPlugin) OnVideoProcessed(ctx context.Context, video *domain.Video) error {
	m.videoProcessedCalled = true
	return nil
}

func (m *MockVideoPlugin) OnVideoDeleted(ctx context.Context, videoID string) error {
	m.videoDeletedCalled = true
	return nil
}

func (m *MockVideoPlugin) OnVideoUpdated(ctx context.Context, video *domain.Video) error {
	m.videoUpdatedCalled = true
	return nil
}

func TestNewManager(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	assert.NotNil(t, manager)
	assert.Equal(t, tempDir, manager.pluginDir)
	assert.NotNil(t, manager.plugins)
	assert.NotNil(t, manager.pluginInfo)
	assert.NotNil(t, manager.hooks)
}

func TestManager_RegisterPlugin(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin := NewMockPlugin("test-plugin", "1.0.0", "Test Author", "Test description")
	config := map[string]any{"key": "value"}

	err := manager.RegisterPlugin(plugin, config)
	require.NoError(t, err)

	// Verify plugin was registered
	retrievedPlugin, err := manager.GetPlugin("test-plugin")
	require.NoError(t, err)
	assert.Equal(t, plugin, retrievedPlugin)

	// Verify plugin info was created
	info, err := manager.GetPluginInfo("test-plugin")
	require.NoError(t, err)
	assert.Equal(t, "test-plugin", info.Name)
	assert.Equal(t, "1.0.0", info.Version)
	assert.Equal(t, "Test Author", info.Author)
	assert.False(t, info.Enabled)
	assert.Equal(t, config, info.Config)
}

func TestManager_RegisterPlugin_Duplicate(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin := NewMockPlugin("test-plugin", "1.0.0", "Test Author", "Test description")

	// Register once
	err := manager.RegisterPlugin(plugin, nil)
	require.NoError(t, err)

	// Try to register again
	err = manager.RegisterPlugin(plugin, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already registered")
}

func TestManager_EnablePlugin(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin := NewMockPlugin("test-plugin", "1.0.0", "Test Author", "Test description")
	config := map[string]any{"key": "value"}

	// Register plugin
	err := manager.RegisterPlugin(plugin, config)
	require.NoError(t, err)

	// Enable plugin
	ctx := context.Background()
	err = manager.EnablePlugin(ctx, "test-plugin")
	require.NoError(t, err)

	// Verify plugin is enabled
	assert.True(t, plugin.Enabled())
	assert.True(t, plugin.initCalled)

	// Verify plugin info is updated
	info, err := manager.GetPluginInfo("test-plugin")
	require.NoError(t, err)
	assert.True(t, info.Enabled)
}

func TestManager_EnablePlugin_AlreadyEnabled(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin := NewMockPlugin("test-plugin", "1.0.0", "Test Author", "Test description")
	err := manager.RegisterPlugin(plugin, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Enable once
	err = manager.EnablePlugin(ctx, "test-plugin")
	require.NoError(t, err)

	// Try to enable again (should be no-op)
	err = manager.EnablePlugin(ctx, "test-plugin")
	assert.NoError(t, err) // Should not error, just return early
}

func TestManager_DisablePlugin(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin := NewMockPlugin("test-plugin", "1.0.0", "Test Author", "Test description")
	err := manager.RegisterPlugin(plugin, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Enable plugin
	err = manager.EnablePlugin(ctx, "test-plugin")
	require.NoError(t, err)

	// Disable plugin
	err = manager.DisablePlugin(ctx, "test-plugin")
	require.NoError(t, err)

	// Verify plugin is disabled
	assert.False(t, plugin.Enabled())
	assert.True(t, plugin.shutdownCalled)

	// Verify plugin info is updated
	info, err := manager.GetPluginInfo("test-plugin")
	require.NoError(t, err)
	assert.False(t, info.Enabled)
}

func TestManager_UpdatePluginConfig(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin := NewMockPlugin("test-plugin", "1.0.0", "Test Author", "Test description")
	initialConfig := map[string]any{"key": "value1"}
	err := manager.RegisterPlugin(plugin, initialConfig)
	require.NoError(t, err)

	ctx := context.Background()

	// Update config while disabled
	newConfig := map[string]any{"key": "value2"}
	err = manager.UpdatePluginConfig(ctx, "test-plugin", newConfig)
	require.NoError(t, err)

	// Verify config was updated
	info, err := manager.GetPluginInfo("test-plugin")
	require.NoError(t, err)
	assert.Equal(t, newConfig, info.Config)
}

func TestManager_UpdatePluginConfig_WhileEnabled(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin := NewMockPlugin("test-plugin", "1.0.0", "Test Author", "Test description")
	initialConfig := map[string]any{"key": "value1"}
	err := manager.RegisterPlugin(plugin, initialConfig)
	require.NoError(t, err)

	ctx := context.Background()

	// Enable plugin
	err = manager.EnablePlugin(ctx, "test-plugin")
	require.NoError(t, err)

	// Update config while enabled (should reinitialize)
	newConfig := map[string]any{"key": "value2"}
	err = manager.UpdatePluginConfig(ctx, "test-plugin", newConfig)
	require.NoError(t, err)

	// Verify config was updated
	info, err := manager.GetPluginInfo("test-plugin")
	require.NoError(t, err)
	assert.Equal(t, newConfig, info.Config)

	// Verify plugin was shutdown and reinitialized
	assert.True(t, plugin.shutdownCalled)
	assert.True(t, plugin.initCalled)
}

func TestManager_ListPlugins(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	// Register multiple plugins
	plugin1 := NewMockPlugin("plugin1", "1.0.0", "Author1", "Description1")
	plugin2 := NewMockPlugin("plugin2", "2.0.0", "Author2", "Description2")

	err := manager.RegisterPlugin(plugin1, nil)
	require.NoError(t, err)

	err = manager.RegisterPlugin(plugin2, nil)
	require.NoError(t, err)

	// List plugins
	plugins := manager.ListPlugins()
	assert.Len(t, plugins, 2)

	names := make([]string, len(plugins))
	for i, p := range plugins {
		names[i] = p.Name
	}
	assert.Contains(t, names, "plugin1")
	assert.Contains(t, names, "plugin2")
}

func TestManager_GetPlugin_NotFound(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	_, err := manager.GetPlugin("nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestManager_EnablePlugin_NotFound(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	ctx := context.Background()
	err := manager.EnablePlugin(ctx, "nonexistent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")
}

func TestManager_Shutdown(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin1 := NewMockPlugin("plugin1", "1.0.0", "Author1", "Description1")
	plugin2 := NewMockPlugin("plugin2", "2.0.0", "Author2", "Description2")

	err := manager.RegisterPlugin(plugin1, nil)
	require.NoError(t, err)
	err = manager.RegisterPlugin(plugin2, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Enable both plugins
	err = manager.EnablePlugin(ctx, "plugin1")
	require.NoError(t, err)
	err = manager.EnablePlugin(ctx, "plugin2")
	require.NoError(t, err)

	// Shutdown manager
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = manager.Shutdown(shutdownCtx)
	require.NoError(t, err)

	// Verify both plugins were shut down
	assert.True(t, plugin1.shutdownCalled)
	assert.True(t, plugin2.shutdownCalled)
}

func TestManager_LoadPlugin_FromManifest(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	// Create a test manifest file
	manifestPath := filepath.Join(tempDir, "plugin.json")
	manifestContent := `{
		"name": "test-plugin",
		"version": "1.0.0",
		"author": "Test Author",
		"description": "Test description",
		"enabled": false,
		"config": {
			"key": "value"
		},
		"permissions": ["read_videos"],
		"hooks": ["video.uploaded"],
		"main": "plugin.so"
	}`

	err := os.WriteFile(manifestPath, []byte(manifestContent), 0644)
	require.NoError(t, err)

	// Load plugin from manifest
	err = manager.LoadPlugin(manifestPath)
	require.NoError(t, err)

	// Verify plugin info was created
	info, err := manager.GetPluginInfo("test-plugin")
	require.NoError(t, err)
	assert.Equal(t, "test-plugin", info.Name)
	assert.Equal(t, "1.0.0", info.Version)
	assert.Equal(t, "Test Author", info.Author)
	assert.Equal(t, "Test description", info.Description)
	assert.False(t, info.Enabled)
	assert.Equal(t, []string{"read_videos"}, info.Permissions)
	assert.Len(t, info.Hooks, 1)
	assert.Equal(t, EventType("video.uploaded"), info.Hooks[0])
}

func TestManager_RegisterPluginHooks_VideoPlugin(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin := NewMockVideoPlugin("video-plugin")
	// Register the VideoPlugin interface, not just the base Plugin
	err := manager.RegisterPlugin(plugin, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Enable plugin (should register hooks)
	err = manager.EnablePlugin(ctx, "video-plugin")
	require.NoError(t, err)

	// Verify hooks were registered
	hookManager := manager.GetHookManager()
	hooks := hookManager.GetRegisteredHooks(EventVideoUploaded)
	assert.Contains(t, hooks, "video-plugin")
}

func TestManager_TriggerEvent(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	plugin := NewMockVideoPlugin("video-plugin")
	// Register the VideoPlugin interface, not just the base Plugin
	err := manager.RegisterPlugin(plugin, nil)
	require.NoError(t, err)

	ctx := context.Background()

	// Enable plugin
	err = manager.EnablePlugin(ctx, "video-plugin")
	require.NoError(t, err)

	// Trigger event
	video := &domain.Video{
		ID:      "test-video-id",
		Title:   "Test Video",
		Privacy: domain.PrivacyPublic,
		Status:  domain.StatusQueued,
		UserID:  "test-user-id",
	}

	// Pass the video directly, not wrapped in EventData
	// The Trigger function will wrap it in EventData automatically
	err = manager.TriggerEvent(ctx, EventVideoUploaded, video)
	require.NoError(t, err)

	// Verify hook was called
	assert.True(t, plugin.videoUploadedCalled)
}

func TestManager_DetectHooks(t *testing.T) {
	tempDir := t.TempDir()
	manager := NewManager(tempDir)

	// Test with VideoPlugin
	videoPlugin := NewMockVideoPlugin("video-plugin")
	hooks := manager.detectHooks(videoPlugin)

	assert.Contains(t, hooks, EventVideoUploaded)
	assert.Contains(t, hooks, EventVideoProcessed)
	assert.Contains(t, hooks, EventVideoDeleted)
	assert.Contains(t, hooks, EventVideoUpdated)
	assert.Len(t, hooks, 4)
}

func TestManager_Initialize_EmptyDirectory(t *testing.T) {
	tempDir := t.TempDir()

	manager := NewManager(tempDir)
	ctx := context.Background()

	// Initialize with no plugins should succeed
	err := manager.Initialize(ctx)
	require.NoError(t, err)

	// Should have no plugins
	plugins := manager.ListPlugins()
	assert.Empty(t, plugins)
}

func TestManager_Initialize_WithManifest(t *testing.T) {
	// FIXED: Deadlock issue resolved by creating loadPluginUnlocked() internal method
	// Initialize() now safely calls discoverPlugins() which calls loadPluginUnlocked()

	// Create temporary plugin directory
	tmpDir := t.TempDir()
	pluginDir := filepath.Join(tmpDir, "test-plugin")
	if err := os.MkdirAll(pluginDir, 0755); err != nil {
		t.Fatalf("Failed to create plugin directory: %v", err)
	}

	// Create a test plugin manifest
	manifest := map[string]interface{}{
		"name":        "test-plugin",
		"version":     "1.0.0",
		"author":      "Test Author",
		"description": "Test plugin for deadlock fix verification",
		"enabled":     true,
		"config":      map[string]interface{}{},
		"permissions": []string{},
		"hooks":       []string{},
	}

	manifestData, err := json.Marshal(manifest)
	if err != nil {
		t.Fatalf("Failed to marshal manifest: %v", err)
	}

	manifestPath := filepath.Join(pluginDir, "plugin.json")
	if err := os.WriteFile(manifestPath, manifestData, 0644); err != nil {
		t.Fatalf("Failed to write manifest: %v", err)
	}

	// Create manager with test directory
	ctx := context.Background()
	manager := NewManager(tmpDir)

	// This should not deadlock anymore
	err = manager.Initialize(ctx)
	if err != nil {
		t.Fatalf("Initialize failed: %v", err)
	}

	// Verify plugin was discovered
	plugins := manager.ListPlugins()
	if len(plugins) != 1 {
		t.Errorf("Expected 1 plugin, got %d", len(plugins))
	}

	if plugins[0].Name != "test-plugin" {
		t.Errorf("Expected plugin name 'test-plugin', got '%s'", plugins[0].Name)
	}
}

func makeTestZipBytes(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		require.NoError(t, err)
		_, err = w.Write([]byte(content))
		require.NoError(t, err)
	}
	require.NoError(t, zw.Close())
	return buf.Bytes()
}

func TestInstallFromURL_Success(t *testing.T) {
	zipData := makeTestZipBytes(t, map[string]string{
		"plugin.json": `{"name":"url-plugin","version":"1.0.0","author":"alice","permissions":["read_videos"]}`,
		"main.js":     `console.log("ok")`,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(zipData)
	}))
	defer server.Close()

	pluginDir := t.TempDir()
	manager := NewManager(pluginDir)

	err := manager.InstallFromURL(context.Background(), server.URL+"/plugin.zip")
	require.NoError(t, err)

	// Verify plugin files were extracted
	finalPath := filepath.Join(pluginDir, "url-plugin")
	assert.DirExists(t, finalPath)
	assert.FileExists(t, filepath.Join(finalPath, "plugin.json"))
	assert.FileExists(t, filepath.Join(finalPath, "main.js"))

	// Verify plugin was registered via LoadPlugin
	info, err := manager.GetPluginInfo("url-plugin")
	require.NoError(t, err)
	require.NotNil(t, info)
	assert.Equal(t, "url-plugin", info.Name)
	assert.Equal(t, "1.0.0", info.Version)
	assert.Equal(t, "alice", info.Author)
}

func TestInstallFromURL_MissingManifest(t *testing.T) {
	zipData := makeTestZipBytes(t, map[string]string{
		"README.md": "no manifest here",
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(zipData)
	}))
	defer server.Close()

	manager := NewManager(t.TempDir())
	err := manager.InstallFromURL(context.Background(), server.URL+"/plugin.zip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "plugin.json not found")
}

func TestInstallFromURL_DownloadFailure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	manager := NewManager(t.TempDir())
	err := manager.InstallFromURL(context.Background(), server.URL+"/missing.zip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unexpected status 404")
}

func TestInstallFromURL_InvalidPermissions(t *testing.T) {
	zipData := makeTestZipBytes(t, map[string]string{
		"plugin.json": `{"name":"bad-perms","version":"1.0.0","author":"alice","permissions":["not_a_permission"]}`,
	})

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(zipData)
	}))
	defer server.Close()

	manager := NewManager(t.TempDir())
	err := manager.InstallFromURL(context.Background(), server.URL+"/plugin.zip")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "invalid permissions")
}
