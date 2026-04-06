package torrent

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"vidra-core/internal/domain"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/google/uuid"
	"golang.org/x/time/rate"
)

// Seeder manages torrent seeding for videos
type Seeder struct {
	client      *torrent.Client
	config      *SeederConfig
	torrents    map[string]*torrent.Torrent // keyed by info hash
	addedAt     map[string]time.Time        // keyed by info hash
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *slog.Logger
	stats       *SeederStats
	prioritizer PrioritizationStrategy
}

// SeederConfig holds configuration for the seeder
type SeederConfig struct {
	// Network settings
	ListenPort int
	DisableTCP bool
	DisableUTP bool

	// Bandwidth limits (bytes per second, 0 = unlimited)
	UploadRateLimit   int64
	DownloadRateLimit int64

	// Connection limits
	MaxConnections           int
	MaxConnectionsPerTorrent int

	// Seeding behavior
	SeedRatio         float64 // Stop seeding after this upload/download ratio
	MinSeeders        int     // Minimum seeders to maintain
	PrioritizePopular bool    // Prioritize seeding popular content

	// Storage
	DataDir   string
	CacheSize int64 // Size of read cache in bytes

	// Timeouts
	HandshakeTimeout time.Duration
	RequestTimeout   time.Duration

	// Debug
	Debug bool
}

// DefaultSeederConfig returns a default seeder configuration
func DefaultSeederConfig() *SeederConfig {
	return &SeederConfig{
		ListenPort:               6881,
		DisableTCP:               false,
		DisableUTP:               false,
		UploadRateLimit:          0, // Unlimited
		DownloadRateLimit:        0, // Unlimited
		MaxConnections:           200,
		MaxConnectionsPerTorrent: 50,
		SeedRatio:                2.0,
		MinSeeders:               3,
		PrioritizePopular:        true,
		DataDir:                  "./torrents",
		CacheSize:                64 * 1024 * 1024, // 64MB
		HandshakeTimeout:         3 * time.Second,
		RequestTimeout:           5 * time.Second,
		Debug:                    false,
	}
}

// SeederStats tracks seeding statistics
type SeederStats struct {
	mu               sync.RWMutex
	TotalUploaded    int64
	TotalDownloaded  int64
	ActiveTorrents   int
	TotalConnections int
	StartTime        time.Time
}

// NewSeeder creates a new torrent seeder
func NewSeeder(config *SeederConfig, logger *slog.Logger) (*Seeder, error) {
	if config == nil {
		config = DefaultSeederConfig()
	}
	if logger == nil {
		logger = slog.Default()
	}

	// Create torrent client config
	clientConfig := torrent.NewDefaultClientConfig()
	clientConfig.ListenPort = config.ListenPort
	clientConfig.DisableTCP = config.DisableTCP
	clientConfig.DisableUTP = config.DisableUTP
	clientConfig.Debug = config.Debug
	clientConfig.Seed = true

	// Set bandwidth limits
	if limiter := newTorrentRateLimiter(config.UploadRateLimit); limiter != nil {
		clientConfig.UploadRateLimiter = limiter
	}
	if limiter := newTorrentRateLimiter(config.DownloadRateLimit); limiter != nil {
		clientConfig.DownloadRateLimiter = limiter
	}

	// Set connection limits
	clientConfig.EstablishedConnsPerTorrent = config.MaxConnectionsPerTorrent
	clientConfig.TotalHalfOpenConns = config.MaxConnections / 2

	// Set timeouts
	clientConfig.HandshakesTimeout = config.HandshakeTimeout

	// Set storage
	clientConfig.DefaultStorage = storage.NewFileByInfoHash(config.DataDir)

	// Create client
	client, err := torrent.NewClient(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create torrent client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	seeder := &Seeder{
		client:   client,
		config:   config,
		torrents: make(map[string]*torrent.Torrent),
		addedAt:  make(map[string]time.Time),
		ctx:      ctx,
		cancel:   cancel,
		logger:   logger,
		stats: &SeederStats{
			StartTime: time.Now(),
		},
	}

	// Set default prioritization strategy
	if config.PrioritizePopular {
		seeder.prioritizer = &PopularityPrioritizer{}
	} else {
		seeder.prioritizer = &FIFOPrioritizer{}
	}

	// Start stats collector
	go seeder.collectStats()

	return seeder, nil
}

// AddTorrent adds a torrent for seeding
func (s *Seeder) AddTorrent(torrentData []byte, videoID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Add torrent to client
	mi, err := metainfo.Load(bytes.NewReader(torrentData))
	if err != nil {
		return fmt.Errorf("failed to load torrent: %w", err)
	}

	t, err := s.client.AddTorrent(mi)
	if err != nil {
		return fmt.Errorf("failed to add torrent: %w", err)
	}

	// Get info hash
	infoHash := t.InfoHash().HexString()

	// Store in map
	s.torrents[infoHash] = t
	s.addedAt[infoHash] = time.Now()

	// Start downloading/seeding
	t.DownloadAll()

	slog.Info("Added torrent for seeding")

	// Apply prioritization
	if s.prioritizer != nil {
		s.applyPrioritization()
	}

	return nil
}

// AddMagnet adds a torrent from a magnet URI
func (s *Seeder) AddMagnet(magnetURI string, videoID uuid.UUID) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Add magnet
	t, err := s.client.AddMagnet(magnetURI)
	if err != nil {
		return fmt.Errorf("failed to add magnet: %w", err)
	}

	// Wait for info (with timeout)
	select {
	case <-t.GotInfo():
	case <-time.After(30 * time.Second):
		t.Drop()
		return fmt.Errorf("timeout waiting for torrent info")
	case <-s.ctx.Done():
		return s.ctx.Err()
	}

	// Get info hash
	infoHash := t.InfoHash().HexString()

	// Store in map
	s.torrents[infoHash] = t
	s.addedAt[infoHash] = time.Now()

	// Start downloading/seeding
	t.DownloadAll()

	slog.Info("Added magnet for seeding")

	return nil
}

// RemoveTorrent stops seeding and removes a torrent
func (s *Seeder) RemoveTorrent(infoHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	t, ok := s.torrents[infoHash]
	if !ok {
		return fmt.Errorf("torrent not found: %s", infoHash)
	}

	// Drop the torrent
	t.Drop()

	// Remove from map
	delete(s.torrents, infoHash)
	delete(s.addedAt, infoHash)

	slog.Info("Removed torrent", "info_hash", infoHash)

	return nil
}

// GetTorrentStatus returns the status of a torrent
func (s *Seeder) GetTorrentStatus(infoHash string) (*TorrentStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	t, ok := s.torrents[infoHash]
	if !ok {
		return nil, fmt.Errorf("torrent not found: %s", infoHash)
	}

	stats := t.Stats()
	addedAt, ok := s.addedAt[infoHash]
	if !ok {
		addedAt = s.stats.StartTime
	}
	totalUploaded := stats.BytesWrittenData.Int64()
	totalDownloaded := stats.BytesReadData.Int64()

	return &TorrentStatus{
		InfoHash:        infoHash,
		Name:            t.Name(),
		BytesCompleted:  t.BytesCompleted(),
		Length:          t.Length(),
		Seeders:         stats.ConnectedSeeders,
		Leechers:        stats.ActivePeers - stats.ConnectedSeeders,
		UploadRate:      calculateRate(totalUploaded, addedAt),
		DownloadRate:    calculateRate(totalDownloaded, addedAt),
		TotalUploaded:   totalUploaded,
		TotalDownloaded: totalDownloaded,
		IsSeeding:       t.Seeding(),
		IsComplete:      t.BytesCompleted() == t.Length(),
	}, nil
}

// GetAllStatuses returns status for all torrents
func (s *Seeder) GetAllStatuses() ([]*TorrentStatus, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	statuses := make([]*TorrentStatus, 0, len(s.torrents))

	for infoHash, t := range s.torrents {
		stats := t.Stats()
		addedAt, ok := s.addedAt[infoHash]
		if !ok {
			addedAt = s.stats.StartTime
		}
		totalUploaded := stats.BytesWrittenData.Int64()
		totalDownloaded := stats.BytesReadData.Int64()

		status := &TorrentStatus{
			InfoHash:        infoHash,
			Name:            t.Name(),
			BytesCompleted:  t.BytesCompleted(),
			Length:          t.Length(),
			Seeders:         stats.ConnectedSeeders,
			Leechers:        stats.ActivePeers - stats.ConnectedSeeders,
			UploadRate:      calculateRate(totalUploaded, addedAt),
			DownloadRate:    calculateRate(totalDownloaded, addedAt),
			TotalUploaded:   totalUploaded,
			TotalDownloaded: totalDownloaded,
			IsSeeding:       t.Seeding(),
			IsComplete:      t.BytesCompleted() == t.Length(),
		}

		statuses = append(statuses, status)
	}

	return statuses, nil
}

// SetPrioritizer sets the prioritization strategy
func (s *Seeder) SetPrioritizer(p PrioritizationStrategy) {
	s.prioritizer = p
	s.applyPrioritization()
}

// applyPrioritization applies the current prioritization strategy
func (s *Seeder) applyPrioritization() {
	if s.prioritizer == nil {
		return
	}

	// Get all torrent statuses
	var torrents []TorrentPriority
	for infoHash, t := range s.torrents {
		stats := t.Stats()
		torrents = append(torrents, TorrentPriority{
			InfoHash:   infoHash,
			Seeders:    stats.ConnectedSeeders,
			Leechers:   stats.ActivePeers - stats.ConnectedSeeders,
			Uploaded:   stats.BytesWrittenData.Int64(),
			Downloaded: stats.BytesReadData.Int64(),
		})
	}

	// Calculate priorities
	priorities := s.prioritizer.CalculatePriorities(torrents)

	// Apply priorities (simplified - in production would adjust bandwidth allocation)
	for infoHash, priority := range priorities {
		if t, ok := s.torrents[infoHash]; ok {
			// Higher priority torrents get more connections
			maxConns := int(float64(s.config.MaxConnectionsPerTorrent) * priority)
			if maxConns < 1 {
				maxConns = 1
			}
			t.SetMaxEstablishedConns(maxConns)
		}
	}
}

// collectStats periodically collects statistics
func (s *Seeder) collectStats() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			s.updateStats()
		case <-s.ctx.Done():
			return
		}
	}
}

// updateStats updates seeder statistics
func (s *Seeder) updateStats() {
	s.mu.RLock()
	defer s.mu.RUnlock()

	s.stats.mu.Lock()
	defer s.stats.mu.Unlock()

	s.stats.ActiveTorrents = len(s.torrents)
	s.stats.TotalConnections = 0
	s.stats.TotalUploaded = 0
	s.stats.TotalDownloaded = 0

	for _, t := range s.torrents {
		stats := t.Stats()
		s.stats.TotalConnections += stats.ActivePeers
		s.stats.TotalUploaded += stats.BytesWrittenData.Int64()
		s.stats.TotalDownloaded += stats.BytesReadData.Int64()
	}
}

// GetStats returns current seeder statistics
func (s *Seeder) GetStats() SeederStats {
	s.stats.mu.RLock()
	defer s.stats.mu.RUnlock()
	return SeederStats{
		TotalUploaded:    s.stats.TotalUploaded,
		TotalDownloaded:  s.stats.TotalDownloaded,
		ActiveTorrents:   s.stats.ActiveTorrents,
		TotalConnections: s.stats.TotalConnections,
		StartTime:        s.stats.StartTime,
	}
}

// Start begins seeding all torrents
func (s *Seeder) Start() error {
	slog.Info("Starting torrent seeder")

	// Torrents are automatically started when added
	// This method is for any additional startup logic

	return nil
}

// Stop gracefully stops the seeder
func (s *Seeder) Stop() error {
	slog.Info("Stopping torrent seeder")

	// Cancel context
	s.cancel()

	// Stop all torrents
	s.mu.Lock()
	for _, t := range s.torrents {
		t.Drop()
	}
	s.torrents = make(map[string]*torrent.Torrent)
	s.addedAt = make(map[string]time.Time)
	s.mu.Unlock()

	// Close client
	s.client.Close()

	slog.Info("Torrent seeder stopped")

	return nil
}

// TorrentStatus represents the current status of a torrent
type TorrentStatus struct {
	InfoHash        string
	Name            string
	BytesCompleted  int64
	Length          int64
	Seeders         int
	Leechers        int
	UploadRate      float64
	DownloadRate    float64
	TotalUploaded   int64
	TotalDownloaded int64
	IsSeeding       bool
	IsComplete      bool
}

func calculateRate(totalBytes int64, startedAt time.Time) float64 {
	if totalBytes <= 0 {
		return 0
	}

	elapsed := time.Since(startedAt).Seconds()
	if elapsed <= 0 {
		return 0
	}

	return float64(totalBytes) / elapsed
}

func newTorrentRateLimiter(bytesPerSecond int64) *rate.Limiter {
	if bytesPerSecond <= 0 {
		return nil
	}

	maxInt := int(^uint(0) >> 1)
	burst := maxInt
	if bytesPerSecond < int64(maxInt) {
		burst = int(bytesPerSecond)
	}

	const minBurst = 16 * 1024
	if burst < minBurst {
		burst = minBurst
	}

	return rate.NewLimiter(rate.Limit(bytesPerSecond), burst)
}

// GetHealthRatio returns the health ratio (seeders/leechers)
func (ts *TorrentStatus) GetHealthRatio() float64 {
	if ts.Leechers == 0 {
		if ts.Seeders > 0 {
			return 999.0 // Very healthy
		}
		return 0
	}
	return float64(ts.Seeders) / float64(ts.Leechers)
}

// GetCompletionPercent returns the completion percentage
func (ts *TorrentStatus) GetCompletionPercent() float64 {
	if ts.Length == 0 {
		return 0
	}
	return float64(ts.BytesCompleted) / float64(ts.Length) * 100
}

// GetSeedRatio returns the upload/download ratio
func (ts *TorrentStatus) GetSeedRatio() float64 {
	if ts.TotalDownloaded == 0 {
		if ts.TotalUploaded > 0 {
			return 999.0
		}
		return 0
	}
	return float64(ts.TotalUploaded) / float64(ts.TotalDownloaded)
}

// PrioritizationStrategy defines how torrents are prioritized
type PrioritizationStrategy interface {
	CalculatePriorities(torrents []TorrentPriority) map[string]float64
}

// TorrentPriority holds data for prioritization
type TorrentPriority struct {
	InfoHash   string
	Seeders    int
	Leechers   int
	Uploaded   int64
	Downloaded int64
}

// PopularityPrioritizer prioritizes popular torrents
type PopularityPrioritizer struct{}

// CalculatePriorities calculates priorities based on popularity
func (p *PopularityPrioritizer) CalculatePriorities(torrents []TorrentPriority) map[string]float64 {
	priorities := make(map[string]float64)

	for _, t := range torrents {
		// Higher priority for more leechers and fewer seeders
		needScore := float64(t.Leechers) / float64(t.Seeders+1)

		// Factor in upload contribution
		uploadScore := float64(t.Uploaded) / 1024 / 1024 / 1024 // GB uploaded

		// Combined priority (0.0 to 1.0)
		priority := (needScore*0.7 + uploadScore*0.3) / 10
		if priority > 1.0 {
			priority = 1.0
		}
		if priority < 0.1 {
			priority = 0.1
		}

		priorities[t.InfoHash] = priority
	}

	return priorities
}

// FIFOPrioritizer gives equal priority (first in, first out)
type FIFOPrioritizer struct{}

// CalculatePriorities returns equal priorities
func (p *FIFOPrioritizer) CalculatePriorities(torrents []TorrentPriority) map[string]float64 {
	priorities := make(map[string]float64)

	for _, t := range torrents {
		priorities[t.InfoHash] = 0.5 // Equal priority
	}

	return priorities
}

// RateLimiter implements rate limiting for bandwidth
type RateLimiter struct {
	rate   int64
	bucket chan struct{}
	ticker *time.Ticker
}

// NewRateLimiter creates a new rate limiter
func NewRateLimiter(bytesPerSecond int64) *RateLimiter {
	rl := &RateLimiter{
		rate:   bytesPerSecond,
		bucket: make(chan struct{}, bytesPerSecond),
		ticker: time.NewTicker(time.Second),
	}

	go rl.refill()

	return rl
}

// refill refills the token bucket
func (rl *RateLimiter) refill() {
	for range rl.ticker.C {
		// Try to fill the bucket
	fillLoop:
		for i := int64(0); i < rl.rate; i++ {
			select {
			case rl.bucket <- struct{}{}:
			default:
				// Bucket is full
				break fillLoop
			}
		}
	}
}

// Wait waits for available bandwidth
func (rl *RateLimiter) Wait(ctx context.Context) error {
	select {
	case <-rl.bucket:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// SeedManager manages seeding for multiple videos
type SeedManager struct {
	seeder    *Seeder
	generator *Generator
	videos    map[uuid.UUID]*domain.VideoTorrent
	mu        sync.RWMutex
	logger    *slog.Logger
}

// NewSeedManager creates a new seed manager
func NewSeedManager(seeder *Seeder, generator *Generator, logger *slog.Logger) *SeedManager {
	return &SeedManager{
		seeder:    seeder,
		generator: generator,
		videos:    make(map[uuid.UUID]*domain.VideoTorrent),
		logger:    logger,
	}
}

// AddVideo adds a video for seeding
func (sm *SeedManager) AddVideo(videoID uuid.UUID, files []VideoFile) error {
	// Generate torrent
	info, err := sm.generator.GenerateFromVideo(context.Background(), videoID, files)
	if err != nil {
		return fmt.Errorf("failed to generate torrent: %w", err)
	}

	// Add to seeder
	err = sm.seeder.AddTorrent(info.TorrentFile, videoID)
	if err != nil {
		return fmt.Errorf("failed to add torrent to seeder: %w", err)
	}

	// Store video torrent info
	sm.mu.Lock()
	sm.videos[videoID] = &domain.VideoTorrent{
		ID:             uuid.New(),
		VideoID:        videoID,
		InfoHash:       info.InfoHash,
		MagnetURI:      info.MagnetURI,
		TotalSizeBytes: info.TotalSize,
		IsSeeding:      true,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}
	sm.mu.Unlock()

	slog.Info("Added video for seeding", "video_id", videoID)

	return nil
}

// RemoveVideo removes a video from seeding
func (sm *SeedManager) RemoveVideo(videoID uuid.UUID) error {
	sm.mu.Lock()
	vt, ok := sm.videos[videoID]
	if !ok {
		sm.mu.Unlock()
		return fmt.Errorf("video not found: %s", videoID)
	}
	delete(sm.videos, videoID)
	sm.mu.Unlock()

	// Remove from seeder
	err := sm.seeder.RemoveTorrent(vt.InfoHash)
	if err != nil {
		return fmt.Errorf("failed to remove torrent: %w", err)
	}

	slog.Info("Removed video from seeding", "video_id", videoID)

	return nil
}

// GetVideoStatus returns the status of a video's torrent
func (sm *SeedManager) GetVideoStatus(videoID uuid.UUID) (*TorrentStatus, error) {
	sm.mu.RLock()
	vt, ok := sm.videos[videoID]
	sm.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("video not found: %s", videoID)
	}

	return sm.seeder.GetTorrentStatus(vt.InfoHash)
}
