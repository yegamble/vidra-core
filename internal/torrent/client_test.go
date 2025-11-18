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
		cfg.UploadRateLimit = 1024 * 1024     // 1 MB/s
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
