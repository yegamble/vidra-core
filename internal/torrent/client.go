package torrent

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"time"
	"vidra-core/internal/config"

	"github.com/anacrolix/torrent"
	"github.com/anacrolix/torrent/metainfo"
	"github.com/anacrolix/torrent/storage"
	"github.com/google/uuid"
)

// Client wraps the anacrolix torrent client with additional functionality
type Client struct {
	client      *torrent.Client
	config      *ClientConfig
	downloads   map[string]*Download
	mu          sync.RWMutex
	ctx         context.Context
	cancel      context.CancelFunc
	logger      *slog.Logger
	rateLimiter *BandwidthManager
}

// ClientConfig holds configuration for the torrent client
type ClientConfig struct {
	// Network
	ListenAddr  string
	DisableTCP  bool
	DisableUTP  bool
	DisableIPv6 bool
	NoDHT       bool
	NoUpload    bool

	// Storage
	DataDir        string
	DefaultStorage storage.ClientImpl

	// Performance
	CacheSize          int64 // bytes
	ReadaheadBytes     int64
	MaxUnverifiedBytes int64

	// Limits
	MaxConnections    int
	UploadRateLimit   int64 // bytes per second
	DownloadRateLimit int64 // bytes per second

	// Behavior
	Seed              bool
	DisablePEX        bool
	DisableWebtorrent bool
	DisableTrackers   bool

	// Timeouts
	HandshakeTimeout time.Duration
	RequestTimeout   time.Duration

	// Debug
	Debug bool
}

// DefaultClientConfig returns a default client configuration
func DefaultClientConfig() *ClientConfig {
	return &ClientConfig{
		ListenAddr:         ":6881",
		DisableTCP:         false,
		DisableUTP:         false,
		DisableIPv6:        false,
		NoDHT:              false,
		NoUpload:           false,
		DataDir:            "./downloads",
		CacheSize:          32 * 1024 * 1024, // 32MB
		ReadaheadBytes:     5 * 1024 * 1024,  // 5MB
		MaxUnverifiedBytes: 64 * 1024 * 1024, // 64MB
		MaxConnections:     200,
		UploadRateLimit:    0, // Unlimited
		DownloadRateLimit:  0, // Unlimited
		Seed:               true,
		DisablePEX:         false,
		DisableWebtorrent:  false,
		DisableTrackers:    false,
		HandshakeTimeout:   3 * time.Second,
		RequestTimeout:     5 * time.Second,
		Debug:              false,
	}
}

// NewClient creates a new torrent client
func NewClient(config *ClientConfig, logger *slog.Logger) (*Client, error) {
	if config == nil {
		config = DefaultClientConfig()
	}

	// Copy config to avoid sharing the caller's pointer
	// This prevents race conditions if the caller modifies the config
	configCopy := *config
	config = &configCopy

	if logger == nil {
		logger = slog.Default()
	}

	// Create torrent client config
	clientConfig := torrent.NewDefaultClientConfig()
	// Set listen address - use SetListenAddr to ensure proper port binding
	// For random port allocation (":0" or "127.0.0.1:0"), we must explicitly set it
	// to avoid the library's default port which causes conflicts in parallel tests
	if config.ListenAddr != "" {
		clientConfig.SetListenAddr(config.ListenAddr)
	}
	clientConfig.DisableTCP = config.DisableTCP
	clientConfig.DisableUTP = config.DisableUTP
	clientConfig.DisableIPv6 = config.DisableIPv6
	clientConfig.NoDHT = config.NoDHT
	clientConfig.NoUpload = config.NoUpload
	clientConfig.Seed = config.Seed
	clientConfig.DisablePEX = config.DisablePEX
	clientConfig.DisableWebtorrent = config.DisableWebtorrent
	clientConfig.DisableTrackers = config.DisableTrackers
	clientConfig.Debug = config.Debug
	clientConfig.HandshakesTimeout = config.HandshakeTimeout

	// Set storage
	if config.DefaultStorage != nil {
		clientConfig.DefaultStorage = config.DefaultStorage
	} else {
		clientConfig.DefaultStorage = storage.NewFileByInfoHash(config.DataDir)
	}

	// Create the client
	torrentClient, err := torrent.NewClient(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create torrent client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create local copies of rate limits to avoid race condition
	// Similar to the fix for ListenAddr at lines 108-110
	uploadLimit := config.UploadRateLimit
	downloadLimit := config.DownloadRateLimit

	client := &Client{
		client:    torrentClient,
		config:    config,
		downloads: make(map[string]*Download),
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
	}

	// Initialize bandwidth manager if rate limits are set
	// Use local copies instead of reading from config pointer
	if uploadLimit > 0 || downloadLimit > 0 {
		client.rateLimiter = NewBandwidthManager(
			uploadLimit,
			downloadLimit,
		)
	}

	return client, nil
}

// NewClientFromAppConfig creates a new torrent client from application config
func NewClientFromAppConfig(cfg *config.Config, logger *slog.Logger) (*Client, error) {
	if logger == nil {
		logger = slog.Default()
	}

	// Create torrent client config
	clientConfig := torrent.NewDefaultClientConfig()
	// Set listen port (the library will bind to 0.0.0.0 by default)
	clientConfig.SetListenAddr(fmt.Sprintf(":%d", cfg.TorrentListenPort))
	clientConfig.DisableTCP = false
	clientConfig.DisableUTP = false
	clientConfig.DisableIPv6 = false
	clientConfig.NoDHT = !cfg.EnableDHT
	clientConfig.NoUpload = false
	clientConfig.Seed = true
	clientConfig.DisablePEX = !cfg.EnablePEX
	clientConfig.DisableWebtorrent = !cfg.EnableWebTorrent
	clientConfig.DisableTrackers = false
	clientConfig.Debug = cfg.LogLevel == "debug"

	// DHT will use the library's default bootstrap nodes if enabled
	// The library includes:
	// - router.bittorrent.com:6881
	// - dht.transmissionbt.com:6881
	// - router.utorrent.com:6881
	// Custom bootstrap nodes can be added via future enhancement if needed
	if cfg.EnableDHT {
		slog.Info("DHT enabled with default bootstrap nodes")
	}

	// Set storage
	clientConfig.DefaultStorage = storage.NewFileByInfoHash(cfg.TorrentDataDir)

	// Set connection limits
	if cfg.TorrentMaxConnections > 0 {
		clientConfig.EstablishedConnsPerTorrent = cfg.TorrentMaxConnections / 10 // per torrent
	}

	// Create the client
	torrentClient, err := torrent.NewClient(clientConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create torrent client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	// Create local copies of rate limits to avoid race condition
	uploadLimit := cfg.TorrentUploadRateLimit
	downloadLimit := cfg.TorrentDownloadRateLimit

	clientCfg := &ClientConfig{
		ListenAddr:        fmt.Sprintf(":%d", cfg.TorrentListenPort),
		NoDHT:             !cfg.EnableDHT,
		DisablePEX:        !cfg.EnablePEX,
		DisableWebtorrent: !cfg.EnableWebTorrent,
		DataDir:           cfg.TorrentDataDir,
		CacheSize:         cfg.TorrentCacheSize,
		MaxConnections:    cfg.TorrentMaxConnections,
		UploadRateLimit:   uploadLimit,
		DownloadRateLimit: downloadLimit,
		Seed:              true,
		HandshakeTimeout:  3 * time.Second,
		RequestTimeout:    5 * time.Second,
		Debug:             cfg.LogLevel == "debug",
	}

	client := &Client{
		client:    torrentClient,
		config:    clientCfg,
		downloads: make(map[string]*Download),
		ctx:       ctx,
		cancel:    cancel,
		logger:    logger,
	}

	// Initialize bandwidth manager if rate limits are set
	// Use local copies instead of reading from cfg pointer
	if uploadLimit > 0 || downloadLimit > 0 {
		client.rateLimiter = NewBandwidthManager(
			uploadLimit,
			downloadLimit,
		)
	}

	slog.Info("Torrent client created with advanced P2P features")

	return client, nil
}

// Download represents an active download
type Download struct {
	ID          uuid.UUID
	Torrent     *torrent.Torrent
	InfoHash    string
	Name        string
	Size        int64
	Progress    float64
	Status      DownloadStatus
	StartedAt   time.Time
	CompletedAt *time.Time
	Error       error
	mu          sync.RWMutex
}

// DownloadStatus represents the status of a download
type DownloadStatus string

const (
	DownloadStatusPending     DownloadStatus = "pending"
	DownloadStatusDownloading DownloadStatus = "downloading"
	DownloadStatusSeeding     DownloadStatus = "seeding"
	DownloadStatusPaused      DownloadStatus = "paused"
	DownloadStatusError       DownloadStatus = "error"
	DownloadStatusCompleted   DownloadStatus = "completed"
)

// AddTorrent adds a torrent from raw bytes
func (c *Client) AddTorrent(data []byte) (*Download, error) {
	// Parse torrent
	mi, err := metainfo.Load(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("failed to parse torrent: %w", err)
	}

	// Add to client
	t, err := c.client.AddTorrent(mi)
	if err != nil {
		return nil, fmt.Errorf("failed to add torrent: %w", err)
	}

	// Wait for info
	select {
	case <-t.GotInfo():
	case <-time.After(30 * time.Second):
		t.Drop()
		return nil, fmt.Errorf("timeout waiting for torrent info")
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}

	// Create download
	download := &Download{
		ID:        uuid.New(),
		Torrent:   t,
		InfoHash:  t.InfoHash().HexString(),
		Name:      t.Name(),
		Size:      t.Length(),
		Status:    DownloadStatusPending,
		StartedAt: time.Now(),
	}

	// Store download
	c.mu.Lock()
	c.downloads[download.InfoHash] = download
	c.mu.Unlock()

	// Start downloading
	t.DownloadAll()
	download.mu.Lock()
	download.Status = DownloadStatusDownloading
	download.mu.Unlock()

	// Monitor progress
	go c.monitorDownload(download)

	slog.Info("Added torrent")

	return download, nil
}

// AddMagnet adds a torrent from a magnet URI
func (c *Client) AddMagnet(magnetURI string) (*Download, error) {
	// Add magnet
	t, err := c.client.AddMagnet(magnetURI)
	if err != nil {
		return nil, fmt.Errorf("failed to add magnet: %w", err)
	}

	// Wait for info
	select {
	case <-t.GotInfo():
	case <-time.After(60 * time.Second):
		t.Drop()
		return nil, fmt.Errorf("timeout waiting for torrent info")
	case <-c.ctx.Done():
		return nil, c.ctx.Err()
	}

	// Create download
	download := &Download{
		ID:        uuid.New(),
		Torrent:   t,
		InfoHash:  t.InfoHash().HexString(),
		Name:      t.Name(),
		Size:      t.Length(),
		Status:    DownloadStatusPending,
		StartedAt: time.Now(),
	}

	// Store download
	c.mu.Lock()
	c.downloads[download.InfoHash] = download
	c.mu.Unlock()

	// Start downloading
	t.DownloadAll()
	download.mu.Lock()
	download.Status = DownloadStatusDownloading
	download.mu.Unlock()

	// Monitor progress
	go c.monitorDownload(download)

	slog.Info("Added magnet")

	return download, nil
}

// GetDownload returns a download by info hash
func (c *Client) GetDownload(infoHash string) (*Download, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	download, ok := c.downloads[infoHash]
	if !ok {
		return nil, fmt.Errorf("download not found: %s", infoHash)
	}

	return download, nil
}

// GetAllDownloads returns all downloads
func (c *Client) GetAllDownloads() []*Download {
	c.mu.RLock()
	defer c.mu.RUnlock()

	downloads := make([]*Download, 0, len(c.downloads))
	for _, d := range c.downloads {
		downloads = append(downloads, d)
	}

	return downloads
}

// PauseDownload pauses a download
func (c *Client) PauseDownload(infoHash string) error {
	download, err := c.GetDownload(infoHash)
	if err != nil {
		return err
	}

	download.mu.Lock()
	defer download.mu.Unlock()

	download.Torrent.DisallowDataDownload()
	download.Torrent.DisallowDataUpload()
	download.Status = DownloadStatusPaused

	slog.Info("Paused download", "info_hash", infoHash)

	return nil
}

// ResumeDownload resumes a download
func (c *Client) ResumeDownload(infoHash string) error {
	download, err := c.GetDownload(infoHash)
	if err != nil {
		return err
	}

	download.mu.Lock()
	defer download.mu.Unlock()

	download.Torrent.AllowDataDownload()
	download.Torrent.AllowDataUpload()

	if download.Torrent.BytesCompleted() == download.Size {
		download.Status = DownloadStatusSeeding
	} else {
		download.Status = DownloadStatusDownloading
	}

	slog.Info("Resumed download", "info_hash", infoHash)

	return nil
}

// RemoveDownload removes a download
func (c *Client) RemoveDownload(infoHash string) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	download, ok := c.downloads[infoHash]
	if !ok {
		return fmt.Errorf("download not found: %s", infoHash)
	}

	// Drop the torrent
	download.Torrent.Drop()

	// Remove from map
	delete(c.downloads, infoHash)

	slog.Info("Removed download", "info_hash", infoHash)

	return nil
}

// monitorDownload monitors the progress of a download
func (c *Client) monitorDownload(download *Download) {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			c.tickDownload(download)
			if !c.downloadExists(download.InfoHash) {
				return
			}
		case <-c.ctx.Done():
			return
		}
	}
}

// tickDownload updates progress and completion state for one monitor tick.
func (c *Client) tickDownload(download *Download) {
	download.mu.Lock()
	defer download.mu.Unlock()

	if download.Size > 0 {
		download.Progress = float64(download.Torrent.BytesCompleted()) / float64(download.Size) * 100
	}

	if download.Torrent.BytesCompleted() == download.Size && download.Status == DownloadStatusDownloading {
		c.markDownloadComplete(download)
	}
}

// markDownloadComplete transitions a finished download to seeding or completed
// and logs the event. Must be called with download.mu held.
func (c *Client) markDownloadComplete(download *Download) {
	now := time.Now()
	download.CompletedAt = &now

	if c.config.Seed {
		download.Status = DownloadStatusSeeding
	} else {
		download.Status = DownloadStatusCompleted
	}

	slog.Info("Download completed")
}

// downloadExists reports whether the download with the given info hash is
// still present in the client's download map.
func (c *Client) downloadExists(infoHash string) bool {
	c.mu.RLock()
	_, exists := c.downloads[infoHash]
	c.mu.RUnlock()
	return exists
}

// GetStats returns client statistics
func (c *Client) GetStats() ClientStats {
	c.mu.RLock()
	defer c.mu.RUnlock()

	stats := ClientStats{
		ActiveDownloads: 0,
		TotalDownloads:  len(c.downloads),
	}

	for _, d := range c.downloads {
		d.mu.RLock()
		status := d.Status
		d.mu.RUnlock()

		if status == DownloadStatusDownloading {
			stats.ActiveDownloads++
		}

		torrentStats := d.Torrent.Stats()
		stats.TotalUploaded += torrentStats.BytesWrittenData.Int64()
		stats.TotalDownloaded += torrentStats.BytesReadData.Int64()
		stats.TotalConnections += torrentStats.ActivePeers
	}

	return stats
}

// ClientStats holds client statistics
type ClientStats struct {
	ActiveDownloads  int
	TotalDownloads   int
	TotalUploaded    int64
	TotalDownloaded  int64
	TotalConnections int
}

// Close closes the client
func (c *Client) Close() error {
	slog.Info("Closing torrent client")

	// Cancel context
	c.cancel()

	// Close all torrents
	c.mu.Lock()
	for _, d := range c.downloads {
		d.Torrent.Drop()
	}
	c.downloads = make(map[string]*Download)
	c.mu.Unlock()

	// Close client
	c.client.Close()

	slog.Info("Torrent client closed")

	return nil
}

// BandwidthManager manages bandwidth allocation
type BandwidthManager struct {
	uploadLimit   int64
	downloadLimit int64
	uploadUsed    int64
	downloadUsed  int64
	mu            sync.RWMutex
}

// NewBandwidthManager creates a new bandwidth manager
func NewBandwidthManager(uploadLimit, downloadLimit int64) *BandwidthManager {
	return &BandwidthManager{
		uploadLimit:   uploadLimit,
		downloadLimit: downloadLimit,
	}
}

// RequestUpload requests upload bandwidth
func (bm *BandwidthManager) RequestUpload(bytes int64) bool {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.uploadLimit == 0 {
		return true // Unlimited
	}

	if bm.uploadUsed+bytes <= bm.uploadLimit {
		bm.uploadUsed += bytes
		return true
	}

	return false
}

// RequestDownload requests download bandwidth
func (bm *BandwidthManager) RequestDownload(bytes int64) bool {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	if bm.downloadLimit == 0 {
		return true // Unlimited
	}

	if bm.downloadUsed+bytes <= bm.downloadLimit {
		bm.downloadUsed += bytes
		return true
	}

	return false
}

// Reset resets the bandwidth usage (called periodically)
func (bm *BandwidthManager) Reset() {
	bm.mu.Lock()
	defer bm.mu.Unlock()

	bm.uploadUsed = 0
	bm.downloadUsed = 0
}

// GetUsage returns current bandwidth usage
func (bm *BandwidthManager) GetUsage() (uploadUsed, downloadUsed int64) {
	bm.mu.RLock()
	defer bm.mu.RUnlock()

	return bm.uploadUsed, bm.downloadUsed
}

// ReadSeeker provides read/seek access to torrent content
type ReadSeeker struct {
	torrent *torrent.Torrent
	reader  torrent.Reader
	offset  int64
	size    int64
}

// NewReadSeeker creates a new read seeker for a torrent
func NewReadSeeker(t *torrent.Torrent) *ReadSeeker {
	return &ReadSeeker{
		torrent: t,
		reader:  t.NewReader(),
		size:    t.Length(),
	}
}

// Read reads data from the torrent
func (rs *ReadSeeker) Read(p []byte) (n int, err error) {
	n, err = rs.reader.Read(p)
	rs.offset += int64(n)
	return n, err
}

// Seek seeks to a position in the torrent
func (rs *ReadSeeker) Seek(offset int64, whence int) (int64, error) {
	var newOffset int64

	switch whence {
	case io.SeekStart:
		newOffset = offset
	case io.SeekCurrent:
		newOffset = rs.offset + offset
	case io.SeekEnd:
		newOffset = rs.size + offset
	default:
		return 0, fmt.Errorf("invalid whence: %d", whence)
	}

	if newOffset < 0 {
		return 0, fmt.Errorf("negative seek position")
	}

	if newOffset > rs.size {
		return 0, fmt.Errorf("seek beyond end of file")
	}

	// Seek the reader
	_, err := rs.reader.Seek(newOffset, io.SeekStart)
	if err != nil {
		return 0, err
	}

	rs.offset = newOffset
	return newOffset, nil
}

// Close closes the read seeker
func (rs *ReadSeeker) Close() error {
	return rs.reader.Close()
}
