package torrent

import (
	"testing"

	"athena/internal/config"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewClientFromAppConfig(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test")
	}

	// Setup test DNS to avoid resolution errors
	SetupTestDNS(t)

	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	t.Run("creates client with DHT enabled", func(t *testing.T) {
		cfg := &config.Config{
			EnableDHT:                true,
			EnablePEX:                true,
			EnableWebTorrent:         true,
			TorrentListenPort:        6881,
			TorrentMaxConnections:    200,
			TorrentDataDir:           "/tmp/torrents",
			TorrentCacheSize:         64 * 1024 * 1024,
			TorrentUploadRateLimit:   0,
			TorrentDownloadRateLimit: 0,
			TorrentSeedRatio:         2.0,
			LogLevel:                 "info",
			DHTBootstrapNodes: []string{
				"router.bittorrent.com:6881",
				"dht.transmissionbt.com:6881",
			},
		}

		client, err := NewClientFromAppConfig(cfg, logger)
		require.NoError(t, err)
		require.NotNil(t, client)

		assert.NotNil(t, client.client)
		assert.NotNil(t, client.config)
		assert.False(t, client.config.NoDHT)
		assert.False(t, client.config.DisablePEX)
		assert.False(t, client.config.DisableWebtorrent)

		// Cleanup
		client.Close()
	})

	t.Run("creates client with DHT disabled", func(t *testing.T) {
		cfg := &config.Config{
			EnableDHT:                false,
			EnablePEX:                false,
			EnableWebTorrent:         false,
			TorrentListenPort:        6882,
			TorrentMaxConnections:    100,
			TorrentDataDir:           "/tmp/torrents2",
			TorrentCacheSize:         32 * 1024 * 1024,
			TorrentUploadRateLimit:   0,
			TorrentDownloadRateLimit: 0,
			TorrentSeedRatio:         1.0,
			LogLevel:                 "info",
		}

		client, err := NewClientFromAppConfig(cfg, logger)
		require.NoError(t, err)
		require.NotNil(t, client)

		assert.NotNil(t, client.client)
		assert.NotNil(t, client.config)
		assert.True(t, client.config.NoDHT)
		assert.True(t, client.config.DisablePEX)
		assert.True(t, client.config.DisableWebtorrent)

		// Cleanup
		client.Close()
	})

	t.Run("creates client with rate limiting", func(t *testing.T) {
		cfg := &config.Config{
			EnableDHT:                true,
			EnablePEX:                true,
			EnableWebTorrent:         true,
			TorrentListenPort:        6883,
			TorrentMaxConnections:    200,
			TorrentDataDir:           "/tmp/torrents3",
			TorrentCacheSize:         64 * 1024 * 1024,
			TorrentUploadRateLimit:   1024 * 1024,     // 1 MB/s
			TorrentDownloadRateLimit: 2 * 1024 * 1024, // 2 MB/s
			TorrentSeedRatio:         2.0,
			LogLevel:                 "info",
		}

		client, err := NewClientFromAppConfig(cfg, logger)
		require.NoError(t, err)
		require.NotNil(t, client)

		assert.NotNil(t, client.rateLimiter)
		assert.Equal(t, int64(1024*1024), client.config.UploadRateLimit)
		assert.Equal(t, int64(2*1024*1024), client.config.DownloadRateLimit)

		// Cleanup
		client.Close()
	})
}

// TestBandwidthManagerNew tests NewBandwidthManager initialization
func TestBandwidthManagerNew(t *testing.T) {
	tests := []struct {
		name          string
		uploadLimit   int64
		downloadLimit int64
	}{
		{
			name:          "both limits set",
			uploadLimit:   1024 * 1024,
			downloadLimit: 2 * 1024 * 1024,
		},
		{
			name:          "zero limits (unlimited)",
			uploadLimit:   0,
			downloadLimit: 0,
		},
		{
			name:          "only upload limit",
			uploadLimit:   512 * 1024,
			downloadLimit: 0,
		},
		{
			name:          "only download limit",
			uploadLimit:   0,
			downloadLimit: 10 * 1024 * 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bm := NewBandwidthManager(tt.uploadLimit, tt.downloadLimit)
			require.NotNil(t, bm)

			assert.Equal(t, tt.uploadLimit, bm.uploadLimit)
			assert.Equal(t, tt.downloadLimit, bm.downloadLimit)
			assert.Equal(t, int64(0), bm.uploadUsed)
			assert.Equal(t, int64(0), bm.downloadUsed)
		})
	}
}

// TestBandwidthManagerReset tests that Reset zeroes out usage counters
func TestBandwidthManagerReset(t *testing.T) {
	bm := NewBandwidthManager(1024*1024, 2*1024*1024)

	// Consume some bandwidth
	ok := bm.RequestUpload(500 * 1024)
	require.True(t, ok)
	ok = bm.RequestDownload(1024 * 1024)
	require.True(t, ok)

	// Verify usage is non-zero
	upUsed, downUsed := bm.GetUsage()
	assert.Equal(t, int64(500*1024), upUsed)
	assert.Equal(t, int64(1024*1024), downUsed)

	// Reset
	bm.Reset()

	// Verify counters are zeroed
	upUsed, downUsed = bm.GetUsage()
	assert.Equal(t, int64(0), upUsed)
	assert.Equal(t, int64(0), downUsed)
}

// TestBandwidthManagerRequestUpload tests upload bandwidth requests
func TestBandwidthManagerRequestUpload(t *testing.T) {
	t.Run("unlimited allows any request", func(t *testing.T) {
		bm := NewBandwidthManager(0, 0)
		assert.True(t, bm.RequestUpload(999999999))
	})

	t.Run("within limit succeeds", func(t *testing.T) {
		bm := NewBandwidthManager(1024, 0)
		assert.True(t, bm.RequestUpload(512))
		assert.True(t, bm.RequestUpload(512))
	})

	t.Run("exceeding limit fails", func(t *testing.T) {
		bm := NewBandwidthManager(1024, 0)
		assert.True(t, bm.RequestUpload(1000))
		assert.False(t, bm.RequestUpload(100)) // Would exceed 1024
	})

	t.Run("exact limit succeeds", func(t *testing.T) {
		bm := NewBandwidthManager(1024, 0)
		assert.True(t, bm.RequestUpload(1024))
		assert.False(t, bm.RequestUpload(1)) // Over limit now
	})
}

// TestBandwidthManagerRequestDownload tests download bandwidth requests
func TestBandwidthManagerRequestDownload(t *testing.T) {
	t.Run("unlimited allows any request", func(t *testing.T) {
		bm := NewBandwidthManager(0, 0)
		assert.True(t, bm.RequestDownload(999999999))
	})

	t.Run("within limit succeeds", func(t *testing.T) {
		bm := NewBandwidthManager(0, 2048)
		assert.True(t, bm.RequestDownload(1024))
		assert.True(t, bm.RequestDownload(1024))
	})

	t.Run("exceeding limit fails", func(t *testing.T) {
		bm := NewBandwidthManager(0, 2048)
		assert.True(t, bm.RequestDownload(2000))
		assert.False(t, bm.RequestDownload(100))
	})
}

// TestBandwidthManagerResetRestoresBandwidth verifies that Reset allows new requests
func TestBandwidthManagerResetRestoresBandwidth(t *testing.T) {
	bm := NewBandwidthManager(1024, 1024)

	// Exhaust bandwidth
	assert.True(t, bm.RequestUpload(1024))
	assert.True(t, bm.RequestDownload(1024))
	assert.False(t, bm.RequestUpload(1))
	assert.False(t, bm.RequestDownload(1))

	// Reset and verify bandwidth is available again
	bm.Reset()
	assert.True(t, bm.RequestUpload(1024))
	assert.True(t, bm.RequestDownload(1024))
}

// TestNewClientWithNilConfig verifies NewClient uses defaults when config is nil
func TestNewClientWithNilConfig(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	client, err := NewClient(nil, logger)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()

	assert.NotNil(t, client.client)
	assert.NotNil(t, client.config)
	assert.Equal(t, ":6881", client.config.ListenAddr)
}

// TestNewClientWithNilLogger verifies NewClient creates a logger when nil
func TestNewClientWithNilLogger(t *testing.T) {
	cfg := MockedDHTConfig()

	client, err := NewClient(cfg, nil)
	require.NoError(t, err)
	require.NotNil(t, client)
	defer client.Close()

	assert.NotNil(t, client.logger)
}

// TestClientPauseDownloadNotFound tests pausing a non-existent download
func TestClientPauseDownloadNotFound(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	cfg := MockedDHTConfig()
	client, err := NewClient(cfg, logger)
	require.NoError(t, err)
	defer client.Close()

	err = client.PauseDownload("nonexistent_hash")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download not found")
}

// TestClientResumeDownloadNotFound tests resuming a non-existent download
func TestClientResumeDownloadNotFound(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	cfg := MockedDHTConfig()
	client, err := NewClient(cfg, logger)
	require.NoError(t, err)
	defer client.Close()

	err = client.ResumeDownload("nonexistent_hash")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download not found")
}

// TestClientGetStatsEmpty tests GetStats on a client with no downloads
func TestClientGetStatsEmpty(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	cfg := MockedDHTConfig()
	client, err := NewClient(cfg, logger)
	require.NoError(t, err)
	defer client.Close()

	stats := client.GetStats()
	assert.Equal(t, 0, stats.ActiveDownloads)
	assert.Equal(t, 0, stats.TotalDownloads)
	assert.Equal(t, int64(0), stats.TotalUploaded)
	assert.Equal(t, int64(0), stats.TotalDownloaded)
	assert.Equal(t, 0, stats.TotalConnections)
}

// TestClientGetAllDownloadsEmpty tests GetAllDownloads on a client with no downloads
func TestClientGetAllDownloadsEmpty(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	cfg := MockedDHTConfig()
	client, err := NewClient(cfg, logger)
	require.NoError(t, err)
	defer client.Close()

	downloads := client.GetAllDownloads()
	assert.Empty(t, downloads)
}

// TestClientCloseIdempotent verifies Close can be called on a fresh client
func TestClientCloseIdempotent(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	cfg := MockedDHTConfig()
	client, err := NewClient(cfg, logger)
	require.NoError(t, err)

	err = client.Close()
	assert.NoError(t, err)
}

func TestSmartSeedingConfiguration(t *testing.T) {
	t.Run("validates smart seeding config", func(t *testing.T) {
		cfg := &config.Config{
			SmartSeedingEnabled:         true,
			SmartSeedingMinSeeders:      3,
			SmartSeedingMaxTorrents:     100,
			SmartSeedingPrioritizeViews: true,
		}

		assert.True(t, cfg.SmartSeedingEnabled)
		assert.Equal(t, 3, cfg.SmartSeedingMinSeeders)
		assert.Equal(t, 100, cfg.SmartSeedingMaxTorrents)
		assert.True(t, cfg.SmartSeedingPrioritizeViews)
	})
}

func TestHybridDistributionConfiguration(t *testing.T) {
	t.Run("validates hybrid distribution config", func(t *testing.T) {
		cfg := &config.Config{
			HybridDistributionEnabled: true,
			HybridPreferIPFS:          false,
			EnableIPFS:                true,
			EnableTorrents:            true,
		}

		assert.True(t, cfg.HybridDistributionEnabled)
		assert.False(t, cfg.HybridPreferIPFS) // Prefer torrent
		assert.True(t, cfg.EnableIPFS)
		assert.True(t, cfg.EnableTorrents)
	})
}

func TestNewClientWithMockedDHT(t *testing.T) {
	logger := logrus.New()
	logger.SetLevel(logrus.FatalLevel)

	t.Run("creates client without external network calls", func(t *testing.T) {
		cfg := MockedDHTConfig()

		client, err := NewClient(cfg, logger)
		require.NoError(t, err)
		require.NotNil(t, client)

		assert.NotNil(t, client.client)
		assert.NotNil(t, client.config)
		assert.True(t, client.config.NoDHT)
		assert.True(t, client.config.DisablePEX)
		assert.True(t, client.config.DisableWebtorrent)

		// Cleanup
		client.Close()
	})

	t.Run("handles rate limiting configuration", func(t *testing.T) {
		cfg := MockedDHTConfig()
		cfg.UploadRateLimit = 1024 * 1024       // 1 MB/s
		cfg.DownloadRateLimit = 2 * 1024 * 1024 // 2 MB/s

		client, err := NewClient(cfg, logger)
		require.NoError(t, err)
		require.NotNil(t, client)

		assert.NotNil(t, client.rateLimiter)
		assert.Equal(t, int64(1024*1024), client.config.UploadRateLimit)
		assert.Equal(t, int64(2*1024*1024), client.config.DownloadRateLimit)

		// Cleanup
		client.Close()
	})
}
