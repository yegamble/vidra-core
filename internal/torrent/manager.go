package torrent

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/repository"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/sirupsen/logrus"
)

// Manager coordinates torrent operations across the system
type Manager struct {
	seeder      *Seeder
	client      *Client
	generator   *Generator
	torrentRepo *repository.TorrentRepository
	peerRepo    *repository.TorrentPeerRepository
	trackerRepo *repository.TorrentTrackerRepository
	statsRepo   *repository.TorrentStatsRepository

	config *ManagerConfig
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
	logger *logrus.Logger

	// Caches
	activeTorrents map[string]*ManagedTorrent
	mu             sync.RWMutex

	// Metrics
	metrics *ManagerMetrics
}

// ManagerConfig holds configuration for the torrent manager
type ManagerConfig struct {
	// Paths
	TorrentDir string // Directory to store .torrent files
	DataDir    string // Directory for torrent data

	// Behavior
	AutoSeed          bool    // Automatically seed all videos
	SeedRatio         float64 // Stop seeding after this ratio
	MinSeeders        int     // Minimum seeders to maintain
	MaxActiveTorrents int     // Maximum active torrents

	// Cleanup
	CleanupInterval time.Duration // How often to run cleanup
	PeerTimeout     time.Duration // When to consider a peer dead
	StatsInterval   time.Duration // How often to record stats

	// Network
	EnableDHT bool
	EnablePEX bool
	EnableLPD bool
	EnableUTP bool
	EnableTCP bool

	// Performance
	MaxConnectionsPerTorrent int
	UploadRateLimit          int64 // bytes/sec, 0 = unlimited
	DownloadRateLimit        int64 // bytes/sec, 0 = unlimited
}

// DefaultManagerConfig returns a default manager configuration
func DefaultManagerConfig() *ManagerConfig {
	return &ManagerConfig{
		TorrentDir:               "./torrents",
		DataDir:                  "./torrent_data",
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
		UploadRateLimit:          0,
		DownloadRateLimit:        0,
	}
}

// ManagedTorrent represents a torrent managed by the system
type ManagedTorrent struct {
	VideoID      uuid.UUID
	InfoHash     string
	TorrentPath  string
	DataPath     string
	Status       TorrentStatus
	AddedAt      time.Time
	LastActiveAt time.Time
	Priority     int
}

// ManagerMetrics tracks manager metrics
type ManagerMetrics struct {
	mu              sync.RWMutex
	TorrentsAdded   int64
	TorrentsRemoved int64
	TorrentsActive  int64
	BytesUploaded   int64
	BytesDownloaded int64
	PeersConnected  int64
	ErrorCount      int64
}

// NewManager creates a new torrent manager
func NewManager(
	db *sqlx.DB,
	config *ManagerConfig,
	logger *logrus.Logger,
) (*Manager, error) {
	if config == nil {
		config = DefaultManagerConfig()
	}
	if logger == nil {
		logger = logrus.New()
	}

	// Create directories
	if err := os.MkdirAll(config.TorrentDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create torrent dir: %w", err)
	}
	if err := os.MkdirAll(config.DataDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create data dir: %w", err)
	}

	// Create generator
	generatorConfig := &GeneratorConfig{
		PieceLength: 262144, // 256KB for WebTorrent
		CreatedBy:   "Vidra Core/1.0",
		Trackers: []string{
			"wss://tracker.openwebtorrent.com",
			"wss://tracker.btorrent.xyz",
			"wss://tracker.fastcast.nz",
		},
	}
	generator := NewGenerator(generatorConfig)

	// Create seeder
	seederConfig := &SeederConfig{
		ListenPort:               6881,
		DataDir:                  config.DataDir,
		UploadRateLimit:          config.UploadRateLimit,
		DownloadRateLimit:        config.DownloadRateLimit,
		MaxConnectionsPerTorrent: config.MaxConnectionsPerTorrent,
		SeedRatio:                config.SeedRatio,
		MinSeeders:               config.MinSeeders,
		PrioritizePopular:        true,
		DisableTCP:               !config.EnableTCP,
		DisableUTP:               !config.EnableUTP,
	}

	seeder, err := NewSeeder(seederConfig, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create seeder: %w", err)
	}

	// Create client
	clientConfig := &ClientConfig{
		DataDir:           config.DataDir,
		UploadRateLimit:   config.UploadRateLimit,
		DownloadRateLimit: config.DownloadRateLimit,
		MaxConnections:    config.MaxConnectionsPerTorrent,
		DisableTCP:        !config.EnableTCP,
		DisableUTP:        !config.EnableUTP,
		NoDHT:             !config.EnableDHT,
		DisablePEX:        !config.EnablePEX,
		Seed:              config.AutoSeed,
	}

	client, err := NewClient(clientConfig, logger)
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	manager := &Manager{
		seeder:         seeder,
		client:         client,
		generator:      generator,
		torrentRepo:    repository.NewTorrentRepository(db),
		peerRepo:       repository.NewTorrentPeerRepository(db),
		trackerRepo:    repository.NewTorrentTrackerRepository(db),
		statsRepo:      repository.NewTorrentStatsRepository(db),
		config:         config,
		ctx:            ctx,
		cancel:         cancel,
		logger:         logger,
		activeTorrents: make(map[string]*ManagedTorrent),
		metrics:        &ManagerMetrics{},
	}

	return manager, nil
}

// Start starts the torrent manager
func (m *Manager) Start() error {
	m.logger.Info("Starting torrent manager")

	// Start seeder
	if err := m.seeder.Start(); err != nil {
		return fmt.Errorf("failed to start seeder: %w", err)
	}

	// Load existing torrents
	if err := m.loadExistingTorrents(); err != nil {
		m.logger.WithError(err).Error("Failed to load existing torrents")
	}

	// Start background workers
	m.wg.Add(3)
	go m.cleanupWorker()
	go m.statsWorker()
	go m.healthCheckWorker()

	m.logger.Info("Torrent manager started")

	return nil
}

// Stop stops the torrent manager
func (m *Manager) Stop() error {
	m.logger.Info("Stopping torrent manager")

	// Cancel context
	m.cancel()

	// Wait for workers
	m.wg.Wait()

	// Save state
	if err := m.saveState(); err != nil {
		m.logger.WithError(err).Error("Failed to save state")
	}

	// Stop components
	if err := m.seeder.Stop(); err != nil {
		m.logger.WithError(err).Error("Failed to stop seeder")
	}

	if err := m.client.Close(); err != nil {
		m.logger.WithError(err).Error("Failed to close client")
	}

	m.logger.Info("Torrent manager stopped")

	return nil
}

// AddVideoTorrent creates and starts seeding a torrent for a video
func (m *Manager) AddVideoTorrent(ctx context.Context, videoID uuid.UUID, files []VideoFile) (*domain.VideoTorrent, error) {
	// Generate torrent
	info, err := m.generator.GenerateFromVideo(ctx, videoID, files)
	if err != nil {
		return nil, fmt.Errorf("failed to generate torrent: %w", err)
	}

	// Save torrent file
	torrentPath := filepath.Join(m.config.TorrentDir, fmt.Sprintf("%s.torrent", videoID))
	if err := os.WriteFile(torrentPath, info.TorrentFile, 0600); err != nil {
		return nil, fmt.Errorf("failed to save torrent file: %w", err)
	}

	// Create database record
	videoTorrent := &domain.VideoTorrent{
		ID:              uuid.New(),
		VideoID:         videoID,
		InfoHash:        info.InfoHash,
		TorrentFilePath: torrentPath,
		MagnetURI:       info.MagnetURI,
		PieceLength:     262144,
		TotalSizeBytes:  info.TotalSize,
		IsSeeding:       true,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := m.torrentRepo.CreateTorrent(ctx, videoTorrent); err != nil {
		return nil, fmt.Errorf("failed to save torrent to database: %w", err)
	}

	// Add to seeder
	if m.config.AutoSeed {
		if err := m.seeder.AddTorrent(info.TorrentFile, videoID); err != nil {
			m.logger.WithError(err).Error("Failed to add torrent to seeder")
			// Don't fail the whole operation
		}
	}

	// Cache managed torrent
	m.mu.Lock()
	m.activeTorrents[info.InfoHash] = &ManagedTorrent{
		VideoID:      videoID,
		InfoHash:     info.InfoHash,
		TorrentPath:  torrentPath,
		DataPath:     filepath.Join(m.config.DataDir, info.InfoHash),
		Status:       TorrentStatus{InfoHash: info.InfoHash, IsSeeding: true},
		AddedAt:      time.Now(),
		LastActiveAt: time.Now(),
	}
	m.mu.Unlock()

	// Update metrics
	m.metrics.mu.Lock()
	m.metrics.TorrentsAdded++
	m.metrics.TorrentsActive++
	m.metrics.mu.Unlock()

	m.logger.WithFields(logrus.Fields{
		"video_id":  videoID,
		"info_hash": info.InfoHash,
		"size":      info.TotalSize,
	}).Info("Added video torrent")

	return videoTorrent, nil
}

// RemoveVideoTorrent removes a video's torrent
func (m *Manager) RemoveVideoTorrent(ctx context.Context, videoID uuid.UUID) error {
	// Get torrent from database
	torrent, err := m.torrentRepo.GetTorrentByVideoID(ctx, videoID)
	if err != nil {
		return fmt.Errorf("failed to get torrent: %w", err)
	}

	// Remove from seeder
	if err := m.seeder.RemoveTorrent(torrent.InfoHash); err != nil {
		m.logger.WithError(err).Error("Failed to remove torrent from seeder")
	}

	// Remove torrent file
	if err := os.Remove(torrent.TorrentFilePath); err != nil && !os.IsNotExist(err) {
		m.logger.WithError(err).Error("Failed to remove torrent file")
	}

	// Remove from database
	if err := m.torrentRepo.DeleteTorrent(ctx, videoID); err != nil {
		return fmt.Errorf("failed to delete torrent from database: %w", err)
	}

	// Remove from cache
	m.mu.Lock()
	delete(m.activeTorrents, torrent.InfoHash)
	m.mu.Unlock()

	// Update metrics
	m.metrics.mu.Lock()
	m.metrics.TorrentsRemoved++
	m.metrics.TorrentsActive--
	m.metrics.mu.Unlock()

	m.logger.WithFields(logrus.Fields{
		"video_id":  videoID,
		"info_hash": torrent.InfoHash,
	}).Info("Removed video torrent")

	return nil
}

// GetVideoTorrent returns the torrent record for a video
func (m *Manager) GetVideoTorrent(ctx context.Context, videoID uuid.UUID) (*domain.VideoTorrent, error) {
	return m.torrentRepo.GetTorrentByVideoID(ctx, videoID)
}

// GetVideoTorrentStatus returns the status of a video's torrent
func (m *Manager) GetVideoTorrentStatus(ctx context.Context, videoID uuid.UUID) (*TorrentStatus, error) {
	// Get torrent from database
	torrent, err := m.torrentRepo.GetTorrentByVideoID(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("torrent not found: %w", err)
	}

	// Get status from seeder
	return m.seeder.GetTorrentStatus(torrent.InfoHash)
}

// GetGlobalStats returns global torrent statistics
func (m *Manager) GetGlobalStats(ctx context.Context) (map[string]interface{}, error) {
	dbStats, err := m.statsRepo.GetGlobalStats(ctx)
	if err != nil {
		return nil, err
	}

	// Add runtime stats
	seederStats := m.seeder.GetStats()
	clientStats := m.client.GetStats()

	m.metrics.mu.RLock()
	dbStats["torrents_added"] = m.metrics.TorrentsAdded
	dbStats["torrents_removed"] = m.metrics.TorrentsRemoved
	dbStats["errors"] = m.metrics.ErrorCount
	m.metrics.mu.RUnlock()

	dbStats["seeder_active"] = seederStats.ActiveTorrents
	dbStats["seeder_connections"] = seederStats.TotalConnections
	dbStats["client_downloads"] = clientStats.ActiveDownloads
	dbStats["uptime"] = time.Since(seederStats.StartTime).String()

	return dbStats, nil
}

// loadExistingTorrents loads existing torrents from the database
func (m *Manager) loadExistingTorrents() error {
	ctx := context.Background()

	// Get active torrents from database
	torrents, err := m.torrentRepo.GetActiveTorrents(ctx)
	if err != nil {
		return fmt.Errorf("failed to get active torrents: %w", err)
	}

	m.logger.Infof("Loading %d existing torrents", len(torrents))

	for _, torrent := range torrents {
		// Check if torrent file exists
		if _, err := os.Stat(torrent.TorrentFilePath); os.IsNotExist(err) {
			m.logger.WithField("info_hash", torrent.InfoHash).Warn("Torrent file not found")
			continue
		}

		// Read torrent file
		torrentData, err := os.ReadFile(torrent.TorrentFilePath)
		if err != nil {
			m.logger.WithError(err).WithField("info_hash", torrent.InfoHash).Error("Failed to read torrent file")
			continue
		}

		// Add to seeder
		if m.config.AutoSeed {
			if err := m.seeder.AddTorrent(torrentData, torrent.VideoID); err != nil {
				m.logger.WithError(err).WithField("info_hash", torrent.InfoHash).Error("Failed to add torrent to seeder")
				continue
			}
		}

		// Cache
		m.mu.Lock()
		m.activeTorrents[torrent.InfoHash] = &ManagedTorrent{
			VideoID:      torrent.VideoID,
			InfoHash:     torrent.InfoHash,
			TorrentPath:  torrent.TorrentFilePath,
			DataPath:     filepath.Join(m.config.DataDir, torrent.InfoHash),
			AddedAt:      torrent.CreatedAt,
			LastActiveAt: time.Now(),
		}
		m.mu.Unlock()
	}

	return nil
}

// saveState saves the current state
func (m *Manager) saveState() error {
	ctx := context.Background()

	// Update seeding status for all torrents
	m.mu.RLock()
	for _, mt := range m.activeTorrents {
		if err := m.torrentRepo.SetSeedingStatus(ctx, mt.VideoID, mt.Status.IsSeeding); err != nil {
			m.logger.WithError(err).Error("Failed to save torrent status")
		}
	}
	m.mu.RUnlock()

	return nil
}

// cleanupWorker periodically cleans up old data
func (m *Manager) cleanupWorker() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.CleanupInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			ctx := context.Background()

			// Cleanup old peers
			if err := m.peerRepo.CleanupOldPeers(ctx); err != nil {
				m.logger.WithError(err).Error("Failed to cleanup old peers")
			}

			// Check torrent health
			m.checkTorrentHealth()

		case <-m.ctx.Done():
			return
		}
	}
}

// statsWorker periodically records statistics
func (m *Manager) statsWorker() {
	defer m.wg.Done()

	ticker := time.NewTicker(m.config.StatsInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.recordStats()
		case <-m.ctx.Done():
			return
		}
	}
}

// healthCheckWorker monitors torrent health
func (m *Manager) healthCheckWorker() {
	defer m.wg.Done()

	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			m.checkTorrentHealth()
		case <-m.ctx.Done():
			return
		}
	}
}

// checkTorrentHealth checks the health of all torrents
func (m *Manager) checkTorrentHealth() {
	statuses, err := m.seeder.GetAllStatuses()
	if err != nil {
		m.logger.WithError(err).Error("Failed to get torrent statuses")
		return
	}

	ctx := context.Background()

	for _, status := range statuses {
		// Update database
		if err := m.torrentRepo.UpdateTorrentStats(ctx, status.InfoHash, status.Seeders, status.Leechers); err != nil {
			m.logger.WithError(err).Error("Failed to update torrent stats")
		}

		// Check if torrent needs more seeders
		if status.IsSeeding && status.Seeders < m.config.MinSeeders {
			m.logger.WithFields(logrus.Fields{
				"info_hash": status.InfoHash,
				"seeders":   status.Seeders,
			}).Warn("Torrent has insufficient seeders")
		}
	}
}

// recordStats records hourly statistics
func (m *Manager) recordStats() {
	ctx := context.Background()

	m.mu.RLock()
	defer m.mu.RUnlock()

	for infoHash, mt := range m.activeTorrents {
		status, err := m.seeder.GetTorrentStatus(infoHash)
		if err != nil {
			continue
		}

		// Get torrent ID
		torrent, err := m.torrentRepo.GetTorrentByInfoHash(ctx, infoHash)
		if err != nil {
			continue
		}

		// Create stats record
		stats := &domain.TorrentStats{
			ID:                 uuid.New(),
			VideoTorrentID:     torrent.ID,
			Hour:               time.Now().Truncate(time.Hour),
			TotalPeers:         status.Seeders + status.Leechers,
			TotalSeeds:         status.Seeders,
			BytesUploaded:      status.TotalUploaded,
			BytesDownloaded:    status.TotalDownloaded,
			CompletedDownloads: 0, // Would need to track this separately
			CreatedAt:          time.Now(),
		}

		if err := m.statsRepo.RecordStats(ctx, stats); err != nil {
			m.logger.WithError(err).Error("Failed to record stats")
		}

		// Update last active time
		mt.LastActiveAt = time.Now()
	}
}

// GetMetrics returns current manager metrics
func (m *Manager) GetMetrics() ManagerMetrics {
	m.metrics.mu.RLock()
	defer m.metrics.mu.RUnlock()
	return ManagerMetrics{
		TorrentsAdded:   m.metrics.TorrentsAdded,
		TorrentsRemoved: m.metrics.TorrentsRemoved,
		TorrentsActive:  m.metrics.TorrentsActive,
		BytesUploaded:   m.metrics.BytesUploaded,
		BytesDownloaded: m.metrics.BytesDownloaded,
		PeersConnected:  m.metrics.PeersConnected,
		ErrorCount:      m.metrics.ErrorCount,
	}
}
