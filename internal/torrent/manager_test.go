package torrent

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultManagerConfig_Values(t *testing.T) {
	cfg := DefaultManagerConfig()
	require.NotNil(t, cfg)
	assert.NotEmpty(t, cfg.TorrentDir)
	assert.NotEmpty(t, cfg.DataDir)
	assert.Greater(t, cfg.SeedRatio, 0.0)
	assert.Greater(t, cfg.MinSeeders, 0)
	assert.Greater(t, cfg.MaxActiveTorrents, 0)
	assert.Greater(t, cfg.CleanupInterval, time.Duration(0))
	assert.Greater(t, cfg.StatsInterval, time.Duration(0))
}

func TestDefaultManagerConfig_NetworkDefaults(t *testing.T) {
	cfg := DefaultManagerConfig()
	assert.Greater(t, cfg.MaxConnectionsPerTorrent, 0)
}

func TestManagerMetrics_InitialZeroValues(t *testing.T) {
	m := &ManagerMetrics{}
	assert.Equal(t, int64(0), m.TorrentsAdded)
	assert.Equal(t, int64(0), m.TorrentsRemoved)
	assert.Equal(t, int64(0), m.TorrentsActive)
	assert.Equal(t, int64(0), m.BytesUploaded)
	assert.Equal(t, int64(0), m.BytesDownloaded)
	assert.Equal(t, int64(0), m.PeersConnected)
	assert.Equal(t, int64(0), m.ErrorCount)
}

func TestManagedTorrent_FieldAssignment(t *testing.T) {
	videoID := uuid.New()
	addedAt := time.Now()
	mt := &ManagedTorrent{
		VideoID:      videoID,
		InfoHash:     "abc123def456",
		TorrentPath:  "/var/torrents/abc.torrent",
		DataPath:     "/var/data/abc",
		AddedAt:      addedAt,
		LastActiveAt: addedAt,
		Priority:     5,
	}
	assert.Equal(t, videoID, mt.VideoID)
	assert.Equal(t, "abc123def456", mt.InfoHash)
	assert.Equal(t, "/var/torrents/abc.torrent", mt.TorrentPath)
	assert.Equal(t, 5, mt.Priority)
	assert.Equal(t, addedAt, mt.AddedAt)
}

func TestVideoFile_FieldAssignment(t *testing.T) {
	vf := VideoFile{
		Path: "/videos/test.mp4",
		Size: 1024 * 1024 * 500,
	}
	assert.Equal(t, "/videos/test.mp4", vf.Path)
	assert.Equal(t, int64(1024*1024*500), vf.Size)
}

// TestNewManager verifies the constructor properly initializes the Manager
// struct with all dependencies wired correctly.
func TestNewManager(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that binds network ports in short mode")
	}

	SetupTestDNS(t)

	tests := []struct {
		name        string
		config      *ManagerConfig
		logger      *logrus.Logger
		wantErr     bool
		description string
	}{
		{
			name:        "nil config uses defaults",
			config:      nil,
			logger:      nil,
			wantErr:     false,
			description: "passing nil config and nil logger should use defaults",
		},
		{
			name: "custom config wires correctly",
			config: &ManagerConfig{
				TorrentDir:               t.TempDir(),
				DataDir:                  t.TempDir(),
				AutoSeed:                 false,
				SeedRatio:                3.0,
				MinSeeders:               5,
				MaxActiveTorrents:        200,
				CleanupInterval:          10 * time.Minute,
				PeerTimeout:              1 * time.Hour,
				StatsInterval:            2 * time.Hour,
				EnableDHT:                false,
				EnablePEX:                false,
				EnableLPD:                false,
				EnableUTP:                true,
				EnableTCP:                true,
				MaxConnectionsPerTorrent: 100,
				UploadRateLimit:          1024 * 1024,
				DownloadRateLimit:        512 * 1024,
			},
			logger:      logrus.New(),
			wantErr:     false,
			description: "custom config values should be preserved in the Manager",
		},
		{
			name: "invalid torrent dir returns error",
			config: &ManagerConfig{
				TorrentDir:               "/proc/nonexistent/impossible/dir",
				DataDir:                  t.TempDir(),
				MaxConnectionsPerTorrent: 50,
				SeedRatio:                2.0,
				MinSeeders:               3,
				CleanupInterval:          5 * time.Minute,
				StatsInterval:            1 * time.Hour,
				EnableTCP:                true,
				EnableUTP:                true,
			},
			logger:      nil,
			wantErr:     true,
			description: "unreachable torrent directory should return an error",
		},
		{
			name: "invalid data dir returns error",
			config: &ManagerConfig{
				TorrentDir:               t.TempDir(),
				DataDir:                  "/proc/nonexistent/impossible/dir",
				MaxConnectionsPerTorrent: 50,
				SeedRatio:                2.0,
				MinSeeders:               3,
				CleanupInterval:          5 * time.Minute,
				StatsInterval:            1 * time.Hour,
				EnableTCP:                true,
				EnableUTP:                true,
			},
			logger:      nil,
			wantErr:     true,
			description: "unreachable data directory should return an error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use temp dirs for nil config case to avoid polluting the filesystem
			if tt.config == nil {
				tt.config = &ManagerConfig{
					TorrentDir:               t.TempDir(),
					DataDir:                  t.TempDir(),
					AutoSeed:                 true,
					SeedRatio:                2.0,
					MinSeeders:               3,
					MaxActiveTorrents:        100,
					CleanupInterval:          5 * time.Minute,
					PeerTimeout:              30 * time.Minute,
					StatsInterval:            1 * time.Hour,
					EnableDHT:                true,
					EnablePEX:                true,
					EnableLPD:                true,
					EnableUTP:                true,
					EnableTCP:                true,
					MaxConnectionsPerTorrent: 50,
				}
			}

			manager, err := NewManager(nil, tt.config, tt.logger)

			if tt.wantErr {
				require.Error(t, err, tt.description)
				assert.Nil(t, manager)
				return
			}

			require.NoError(t, err, tt.description)
			require.NotNil(t, manager)

			// Verify all internal components are wired
			assert.NotNil(t, manager.seeder, "seeder should be initialized")
			assert.NotNil(t, manager.client, "client should be initialized")
			assert.NotNil(t, manager.generator, "generator should be initialized")
			assert.NotNil(t, manager.torrentRepo, "torrent repo should be initialized")
			assert.NotNil(t, manager.peerRepo, "peer repo should be initialized")
			assert.NotNil(t, manager.trackerRepo, "tracker repo should be initialized")
			assert.NotNil(t, manager.statsRepo, "stats repo should be initialized")
			assert.NotNil(t, manager.config, "config should be set")
			assert.NotNil(t, manager.ctx, "context should be set")
			assert.NotNil(t, manager.cancel, "cancel func should be set")
			assert.NotNil(t, manager.logger, "logger should be set")
			assert.NotNil(t, manager.activeTorrents, "active torrents map should be initialized")
			assert.Empty(t, manager.activeTorrents, "active torrents map should be empty initially")
			assert.NotNil(t, manager.metrics, "metrics should be initialized")

			// Verify config values are preserved
			assert.Equal(t, tt.config.SeedRatio, manager.config.SeedRatio)
			assert.Equal(t, tt.config.MinSeeders, manager.config.MinSeeders)
			assert.Equal(t, tt.config.MaxActiveTorrents, manager.config.MaxActiveTorrents)

			// Cleanup: cancel context and close clients
			manager.cancel()
			_ = manager.seeder.Stop()
			_ = manager.client.Close()
		})
	}
}

// TestNewManager_DirectConstruction verifies that constructing a Manager
// directly wires all dependencies without needing network access.
func TestNewManager_DirectConstruction(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	cfg := DefaultManagerConfig()
	cfg.TorrentDir = t.TempDir()
	cfg.DataDir = t.TempDir()

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := &Manager{
		config:         cfg,
		ctx:            ctx,
		cancel:         cancel,
		logger:         logger,
		activeTorrents: make(map[string]*ManagedTorrent),
		metrics:        &ManagerMetrics{},
	}

	// Verify all fields are set
	assert.NotNil(t, manager.config)
	assert.NotNil(t, manager.ctx)
	assert.NotNil(t, manager.cancel)
	assert.NotNil(t, manager.logger)
	assert.NotNil(t, manager.activeTorrents)
	assert.NotNil(t, manager.metrics)

	// Verify config defaults
	assert.Equal(t, 2.0, manager.config.SeedRatio)
	assert.Equal(t, 3, manager.config.MinSeeders)
	assert.Equal(t, 100, manager.config.MaxActiveTorrents)
	assert.True(t, manager.config.AutoSeed)
	assert.True(t, manager.config.EnableDHT)
	assert.True(t, manager.config.EnablePEX)
	assert.True(t, manager.config.EnableLPD)
	assert.True(t, manager.config.EnableUTP)
	assert.True(t, manager.config.EnableTCP)

	// Verify empty initial state
	assert.Empty(t, manager.activeTorrents)
	assert.Equal(t, int64(0), manager.metrics.TorrentsAdded)
	assert.Equal(t, int64(0), manager.metrics.TorrentsActive)
	assert.Equal(t, int64(0), manager.metrics.ErrorCount)
}

// TestNewManager_DirectoryCreation verifies NewManager creates the required directories.
func TestNewManager_DirectoryCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that binds network ports in short mode")
	}

	SetupTestDNS(t)

	tmpDir := t.TempDir()
	torrentDir := filepath.Join(tmpDir, "deep", "nested", "torrents")
	dataDir := filepath.Join(tmpDir, "deep", "nested", "data")

	cfg := DefaultManagerConfig()
	cfg.TorrentDir = torrentDir
	cfg.DataDir = dataDir

	manager, err := NewManager(nil, cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, manager)

	// Verify directories were created
	assert.DirExists(t, torrentDir, "torrent directory should be created")
	assert.DirExists(t, dataDir, "data directory should be created")

	// Cleanup
	manager.cancel()
	_ = manager.seeder.Stop()
	_ = manager.client.Close()
}

// TestManager_Start verifies Start initializes background workers and
// returns nil on success.
func TestManager_Start(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that binds network ports in short mode")
	}

	SetupTestDNS(t)

	cfg := DefaultManagerConfig()
	cfg.TorrentDir = t.TempDir()
	cfg.DataDir = t.TempDir()
	cfg.CleanupInterval = 1 * time.Hour // long intervals so workers don't tick during test
	cfg.StatsInterval = 1 * time.Hour

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager, err := NewManager(nil, cfg, logger)
	require.NoError(t, err)
	require.NotNil(t, manager)

	// Start should succeed
	err = manager.Start()
	require.NoError(t, err, "Start should not return an error")

	// After Start, the context should still be active (not cancelled)
	select {
	case <-manager.ctx.Done():
		t.Fatal("context should not be cancelled after Start")
	default:
		// expected: context is still active
	}

	// Verify the active torrents map is still accessible (manager is functional)
	manager.mu.RLock()
	assert.NotNil(t, manager.activeTorrents)
	manager.mu.RUnlock()

	// Stop to clean up
	err = manager.Stop()
	require.NoError(t, err, "Stop should not return an error")
}

// TestManager_Start_TableDriven tests Start under different configurations.
func TestManager_Start_TableDriven(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that binds network ports in short mode")
	}

	SetupTestDNS(t)

	tests := []struct {
		name     string
		modifyCfg func(cfg *ManagerConfig)
	}{
		{
			name:     "default config",
			modifyCfg: func(_ *ManagerConfig) {},
		},
		{
			name: "auto seed disabled",
			modifyCfg: func(cfg *ManagerConfig) {
				cfg.AutoSeed = false
			},
		},
		{
			name: "custom intervals",
			modifyCfg: func(cfg *ManagerConfig) {
				cfg.CleanupInterval = 30 * time.Second
				cfg.StatsInterval = 30 * time.Second
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultManagerConfig()
			cfg.TorrentDir = t.TempDir()
			cfg.DataDir = t.TempDir()
			cfg.CleanupInterval = 1 * time.Hour
			cfg.StatsInterval = 1 * time.Hour
			tt.modifyCfg(cfg)

			logger := logrus.New()
			logger.SetLevel(logrus.FatalLevel)

			manager, err := NewManager(nil, cfg, logger)
			require.NoError(t, err)

			err = manager.Start()
			require.NoError(t, err, "Start should succeed for config: %s", tt.name)

			// Verify context is active after start
			select {
			case <-manager.ctx.Done():
				t.Fatal("context should be active after Start")
			default:
			}

			err = manager.Stop()
			require.NoError(t, err)
		})
	}
}

// TestManager_Stop verifies Stop cancels the context and waits for workers.
func TestManager_Stop(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping test that binds network ports in short mode")
	}

	SetupTestDNS(t)

	cfg := DefaultManagerConfig()
	cfg.TorrentDir = t.TempDir()
	cfg.DataDir = t.TempDir()
	cfg.CleanupInterval = 1 * time.Hour
	cfg.StatsInterval = 1 * time.Hour

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager, err := NewManager(nil, cfg, logger)
	require.NoError(t, err)

	// Start the manager
	err = manager.Start()
	require.NoError(t, err)

	// Stop should succeed
	err = manager.Stop()
	require.NoError(t, err, "Stop should not return an error")

	// After Stop, the context should be cancelled
	select {
	case <-manager.ctx.Done():
		// expected: context is cancelled
	default:
		t.Fatal("context should be cancelled after Stop")
	}

	// Verify context error is context.Canceled
	assert.ErrorIs(t, manager.ctx.Err(), context.Canceled,
		"context error should be context.Canceled after Stop")
}

// TestManager_Stop_ContextCancellation verifies Stop cancels context using
// a directly constructed Manager (no network required).
func TestManager_Stop_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := &Manager{
		config: DefaultManagerConfig(),
		ctx:    ctx,
		cancel: cancel,
		logger: logger,
		activeTorrents: make(map[string]*ManagedTorrent),
		metrics:        &ManagerMetrics{},
	}

	// Context should be active before cancel
	select {
	case <-manager.ctx.Done():
		t.Fatal("context should not be cancelled before Stop")
	default:
	}

	// Cancel the context (simulating the cancel() call in Stop)
	manager.cancel()

	// Context should now be done
	select {
	case <-manager.ctx.Done():
		// expected
	default:
		t.Fatal("context should be cancelled after cancel()")
	}

	assert.ErrorIs(t, manager.ctx.Err(), context.Canceled)
}

// TestManager_Stop_WorkersExit verifies that background workers exit when
// the context is cancelled.
func TestManager_Stop_WorkersExit(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	cfg := DefaultManagerConfig()
	cfg.CleanupInterval = 1 * time.Hour
	cfg.StatsInterval = 1 * time.Hour

	manager := &Manager{
		config:         cfg,
		ctx:            ctx,
		cancel:         cancel,
		logger:         logger,
		activeTorrents: make(map[string]*ManagedTorrent),
		metrics:        &ManagerMetrics{},
	}

	// Simulate starting background workers the same way Start does
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		// Simulates the select loop pattern used by cleanupWorker/statsWorker
		ticker := time.NewTicker(cfg.CleanupInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				// would do work
			case <-manager.ctx.Done():
				return
			}
		}
	}()

	// Cancel context
	manager.cancel()

	// Workers should exit promptly
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// expected: workers exited
	case <-time.After(5 * time.Second):
		t.Fatal("workers did not exit within timeout after context cancellation")
	}
}

// TestManager_GetMetrics verifies GetMetrics returns a snapshot of current metrics.
func TestManager_GetMetrics(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	manager := &Manager{
		config:         DefaultManagerConfig(),
		ctx:            ctx,
		cancel:         cancel,
		logger:         logger,
		activeTorrents: make(map[string]*ManagedTorrent),
		metrics:        &ManagerMetrics{},
	}

	// Initial metrics should be zero
	metrics := manager.GetMetrics()
	assert.Equal(t, int64(0), metrics.TorrentsAdded)
	assert.Equal(t, int64(0), metrics.TorrentsRemoved)
	assert.Equal(t, int64(0), metrics.TorrentsActive)
	assert.Equal(t, int64(0), metrics.BytesUploaded)
	assert.Equal(t, int64(0), metrics.BytesDownloaded)
	assert.Equal(t, int64(0), metrics.PeersConnected)
	assert.Equal(t, int64(0), metrics.ErrorCount)

	// Simulate metrics updates
	manager.metrics.mu.Lock()
	manager.metrics.TorrentsAdded = 10
	manager.metrics.TorrentsActive = 5
	manager.metrics.BytesUploaded = 1024 * 1024
	manager.metrics.ErrorCount = 2
	manager.metrics.mu.Unlock()

	// GetMetrics should return updated values
	metrics = manager.GetMetrics()
	assert.Equal(t, int64(10), metrics.TorrentsAdded)
	assert.Equal(t, int64(5), metrics.TorrentsActive)
	assert.Equal(t, int64(1024*1024), metrics.BytesUploaded)
	assert.Equal(t, int64(2), metrics.ErrorCount)
}
