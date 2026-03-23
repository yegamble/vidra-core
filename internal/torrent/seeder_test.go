package torrent

import (
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// calculateRate
// ---------------------------------------------------------------------------

func TestCalculateRate(t *testing.T) {
	tests := []struct {
		name       string
		bytes      int64
		startedAt  time.Time
		wantZero   bool
		wantMinGt  float64 // if not wantZero, rate must be > this value
		wantMaxLt  float64 // if non-zero, rate must be < this value
	}{
		{
			name:      "zero bytes returns zero",
			bytes:     0,
			startedAt: time.Now().Add(-10 * time.Second),
			wantZero:  true,
		},
		{
			name:      "negative bytes returns zero",
			bytes:     -500,
			startedAt: time.Now().Add(-10 * time.Second),
			wantZero:  true,
		},
		{
			name:      "future start time yields zero (negative elapsed)",
			bytes:     1024,
			startedAt: time.Now().Add(10 * time.Second),
			wantZero:  true,
		},
		{
			name:      "positive bytes with elapsed time returns positive rate",
			bytes:     1024,
			startedAt: time.Now().Add(-2 * time.Second),
			wantMinGt: 0.0,
		},
		{
			name:      "large bytes with short elapsed gives high rate",
			bytes:     10 * 1024 * 1024, // 10 MB
			startedAt: time.Now().Add(-1 * time.Second),
			wantMinGt: 1_000_000, // at least ~1 MB/s
		},
		{
			name:      "1 KB over 10 seconds gives roughly 100 B/s",
			bytes:     1000,
			startedAt: time.Now().Add(-10 * time.Second),
			wantMinGt: 50,
			wantMaxLt: 200,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := calculateRate(tt.bytes, tt.startedAt)
			if tt.wantZero {
				assert.Equal(t, 0.0, got)
			} else {
				assert.Greater(t, got, tt.wantMinGt)
				if tt.wantMaxLt > 0 {
					assert.Less(t, got, tt.wantMaxLt)
				}
			}
		})
	}
}

func TestCalculateRate_LargerBytesHigherRate(t *testing.T) {
	startedAt := time.Now().Add(-5 * time.Second)
	rateSmall := calculateRate(1024, startedAt)
	rateLarge := calculateRate(1024*1024, startedAt)
	assert.Greater(t, rateLarge, rateSmall, "more bytes should yield higher rate for same duration")
}

// ---------------------------------------------------------------------------
// FIFOPrioritizer
// ---------------------------------------------------------------------------

func TestFIFOPrioritizer_CalculatePriorities(t *testing.T) {
	tests := []struct {
		name     string
		torrents []TorrentPriority
		wantLen  int
		wantVal  float64
	}{
		{
			name:     "nil slice returns empty map",
			torrents: nil,
			wantLen:  0,
		},
		{
			name:     "empty slice returns empty map",
			torrents: []TorrentPriority{},
			wantLen:  0,
		},
		{
			name:     "single torrent gets 0.5",
			torrents: []TorrentPriority{{InfoHash: "only"}},
			wantLen:  1,
			wantVal:  0.5,
		},
		{
			name: "multiple torrents all get 0.5",
			torrents: []TorrentPriority{
				{InfoHash: "a"},
				{InfoHash: "b"},
				{InfoHash: "c"},
			},
			wantLen: 3,
			wantVal: 0.5,
		},
		{
			name: "ignores seeder and leecher stats",
			torrents: []TorrentPriority{
				{InfoHash: "busy", Seeders: 1000, Leechers: 5000, Uploaded: 100 * 1024 * 1024 * 1024},
				{InfoHash: "empty", Seeders: 0, Leechers: 0, Uploaded: 0},
			},
			wantLen: 2,
			wantVal: 0.5,
		},
	}

	p := &FIFOPrioritizer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priorities := p.CalculatePriorities(tt.torrents)
			assert.Len(t, priorities, tt.wantLen)
			for _, v := range priorities {
				assert.Equal(t, tt.wantVal, v)
			}
		})
	}
}

func TestFIFOPrioritizer_StaggeredTimestampsIgnored(t *testing.T) {
	// FIFO assigns equal priority regardless of any field values.
	// Simulate torrents that would have been added at staggered times by varying stats.
	p := &FIFOPrioritizer{}
	torrents := []TorrentPriority{
		{InfoHash: "first", Seeders: 1, Leechers: 10, Uploaded: 1024},
		{InfoHash: "second", Seeders: 5, Leechers: 50, Uploaded: 10240},
		{InfoHash: "third", Seeders: 20, Leechers: 200, Uploaded: 102400},
	}
	priorities := p.CalculatePriorities(torrents)
	require.Len(t, priorities, 3)
	assert.Equal(t, priorities["first"], priorities["second"])
	assert.Equal(t, priorities["second"], priorities["third"])
}

// ---------------------------------------------------------------------------
// PopularityPrioritizer
// ---------------------------------------------------------------------------

func TestPopularityPrioritizer_CalculatePriorities(t *testing.T) {
	tests := []struct {
		name    string
		input   []TorrentPriority
		wantLen int
		check   func(t *testing.T, p map[string]float64)
	}{
		{
			name:    "nil slice returns empty map",
			input:   nil,
			wantLen: 0,
		},
		{
			name:    "empty slice returns empty map",
			input:   []TorrentPriority{},
			wantLen: 0,
		},
		{
			name:    "single torrent with leechers",
			input:   []TorrentPriority{{InfoHash: "abc123", Seeders: 0, Leechers: 5}},
			wantLen: 1,
			check: func(t *testing.T, p map[string]float64) {
				assert.GreaterOrEqual(t, p["abc123"], 0.1)
				assert.LessOrEqual(t, p["abc123"], 1.0)
			},
		},
		{
			name: "high need torrent ranks above low need",
			input: []TorrentPriority{
				{InfoHash: "high", Seeders: 0, Leechers: 100},
				{InfoHash: "low", Seeders: 100, Leechers: 1},
			},
			wantLen: 2,
			check: func(t *testing.T, p map[string]float64) {
				assert.Greater(t, p["high"], p["low"])
			},
		},
		{
			name: "extreme values capped at 1.0",
			input: []TorrentPriority{
				{InfoHash: "extreme", Seeders: 0, Leechers: 1_000_000, Uploaded: 100 * 1024 * 1024 * 1024},
			},
			wantLen: 1,
			check: func(t *testing.T, p map[string]float64) {
				assert.LessOrEqual(t, p["extreme"], 1.0)
			},
		},
		{
			name: "very cold torrent gets minimum 0.1",
			input: []TorrentPriority{
				{InfoHash: "cold", Seeders: 1000, Leechers: 0, Uploaded: 0},
			},
			wantLen: 1,
			check: func(t *testing.T, p map[string]float64) {
				assert.Equal(t, 0.1, p["cold"])
			},
		},
		{
			name: "upload contribution increases priority",
			input: []TorrentPriority{
				{InfoHash: "noUpload", Seeders: 5, Leechers: 10, Uploaded: 0},
				{InfoHash: "bigUpload", Seeders: 5, Leechers: 10, Uploaded: 50 * 1024 * 1024 * 1024},
			},
			wantLen: 2,
			check: func(t *testing.T, p map[string]float64) {
				assert.Greater(t, p["bigUpload"], p["noUpload"],
					"torrent with more uploaded data should have higher priority when need is equal")
			},
		},
		{
			name: "all priorities in valid range for mixed inputs",
			input: []TorrentPriority{
				{InfoHash: "t1", Seeders: 5, Leechers: 50, Uploaded: 1024 * 1024 * 1024},
				{InfoHash: "t2", Seeders: 100, Leechers: 10, Uploaded: 0},
				{InfoHash: "t3", Seeders: 1, Leechers: 1, Uploaded: 512 * 1024 * 1024},
			},
			wantLen: 3,
			check: func(t *testing.T, p map[string]float64) {
				for hash, priority := range p {
					assert.GreaterOrEqual(t, priority, 0.1, "priority for %s >= 0.1", hash)
					assert.LessOrEqual(t, priority, 1.0, "priority for %s <= 1.0", hash)
				}
			},
		},
		{
			name: "info hash keys are preserved",
			input: []TorrentPriority{
				{InfoHash: "hash1"},
				{InfoHash: "hash2"},
			},
			wantLen: 2,
			check: func(t *testing.T, p map[string]float64) {
				assert.Contains(t, p, "hash1")
				assert.Contains(t, p, "hash2")
			},
		},
	}

	p := &PopularityPrioritizer{}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priorities := p.CalculatePriorities(tt.input)
			assert.Len(t, priorities, tt.wantLen)
			if tt.check != nil {
				tt.check(t, priorities)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// DefaultSeederConfig
// ---------------------------------------------------------------------------

func TestDefaultSeederConfig(t *testing.T) {
	cfg := DefaultSeederConfig()
	require.NotNil(t, cfg)

	t.Run("network defaults", func(t *testing.T) {
		assert.Equal(t, 6881, cfg.ListenPort)
		assert.False(t, cfg.DisableTCP)
		assert.False(t, cfg.DisableUTP)
	})

	t.Run("bandwidth defaults are unlimited", func(t *testing.T) {
		assert.Equal(t, int64(0), cfg.UploadRateLimit)
		assert.Equal(t, int64(0), cfg.DownloadRateLimit)
	})

	t.Run("connection limits", func(t *testing.T) {
		assert.Equal(t, 200, cfg.MaxConnections)
		assert.Equal(t, 50, cfg.MaxConnectionsPerTorrent)
	})

	t.Run("seeding behavior", func(t *testing.T) {
		assert.Equal(t, 2.0, cfg.SeedRatio)
		assert.Equal(t, 3, cfg.MinSeeders)
		assert.True(t, cfg.PrioritizePopular)
	})

	t.Run("storage defaults", func(t *testing.T) {
		assert.Equal(t, "./torrents", cfg.DataDir)
		assert.Equal(t, int64(64*1024*1024), cfg.CacheSize)
	})

	t.Run("timeout defaults", func(t *testing.T) {
		assert.Equal(t, 3*time.Second, cfg.HandshakeTimeout)
		assert.Equal(t, 5*time.Second, cfg.RequestTimeout)
	})

	t.Run("debug off by default", func(t *testing.T) {
		assert.False(t, cfg.Debug)
	})
}

// ---------------------------------------------------------------------------
// NewSeeder
// ---------------------------------------------------------------------------

func TestNewSeeder_NilConfigUsesDefaults(t *testing.T) {
	seeder, err := NewSeeder(nil, nil)
	require.NoError(t, err)
	require.NotNil(t, seeder)
	defer seeder.Stop() //nolint:errcheck

	assert.NotNil(t, seeder.config)
	assert.Equal(t, 6881, seeder.config.ListenPort)
	assert.NotNil(t, seeder.logger)
	assert.NotNil(t, seeder.torrents)
	assert.NotNil(t, seeder.addedAt)
	assert.NotNil(t, seeder.stats)
	assert.NotNil(t, seeder.client)
}

func TestNewSeeder_CustomConfig(t *testing.T) {
	cfg := &SeederConfig{
		ListenPort:               0, // random port
		DisableTCP:               true,
		DisableUTP:               false,
		MaxConnections:           100,
		MaxConnectionsPerTorrent: 25,
		SeedRatio:                1.5,
		MinSeeders:               2,
		PrioritizePopular:        false,
		DataDir:                  t.TempDir(),
		CacheSize:                32 * 1024 * 1024,
		HandshakeTimeout:         2 * time.Second,
		RequestTimeout:           4 * time.Second,
	}
	logger := logrus.New()

	seeder, err := NewSeeder(cfg, logger)
	require.NoError(t, err)
	require.NotNil(t, seeder)
	defer seeder.Stop() //nolint:errcheck

	assert.Equal(t, cfg, seeder.config)
	assert.Equal(t, logger, seeder.logger)
}

func TestNewSeeder_PrioritizePopularTrue(t *testing.T) {
	cfg := DefaultSeederConfig()
	cfg.ListenPort = 0
	cfg.PrioritizePopular = true
	cfg.DataDir = t.TempDir()

	seeder, err := NewSeeder(cfg, nil)
	require.NoError(t, err)
	defer seeder.Stop() //nolint:errcheck

	_, ok := seeder.prioritizer.(*PopularityPrioritizer)
	assert.True(t, ok, "should use PopularityPrioritizer when PrioritizePopular is true")
}

func TestNewSeeder_PrioritizePopularFalse(t *testing.T) {
	cfg := DefaultSeederConfig()
	cfg.ListenPort = 0
	cfg.PrioritizePopular = false
	cfg.DataDir = t.TempDir()

	seeder, err := NewSeeder(cfg, nil)
	require.NoError(t, err)
	defer seeder.Stop() //nolint:errcheck

	_, ok := seeder.prioritizer.(*FIFOPrioritizer)
	assert.True(t, ok, "should use FIFOPrioritizer when PrioritizePopular is false")
}

func TestNewSeeder_InitializesMaps(t *testing.T) {
	cfg := DefaultSeederConfig()
	cfg.ListenPort = 0
	cfg.DataDir = t.TempDir()

	seeder, err := NewSeeder(cfg, nil)
	require.NoError(t, err)
	defer seeder.Stop() //nolint:errcheck

	assert.NotNil(t, seeder.torrents)
	assert.Empty(t, seeder.torrents)
	assert.NotNil(t, seeder.addedAt)
	assert.Empty(t, seeder.addedAt)
}

func TestNewSeeder_StatsInitialized(t *testing.T) {
	cfg := DefaultSeederConfig()
	cfg.ListenPort = 0
	cfg.DataDir = t.TempDir()

	seeder, err := NewSeeder(cfg, nil)
	require.NoError(t, err)
	defer seeder.Stop() //nolint:errcheck

	stats := seeder.GetStats()
	assert.Equal(t, int64(0), stats.TotalUploaded)
	assert.Equal(t, int64(0), stats.TotalDownloaded)
	assert.Equal(t, 0, stats.ActiveTorrents)
	assert.Equal(t, 0, stats.TotalConnections)
	assert.False(t, stats.StartTime.IsZero(), "start time should be set")
}

// ---------------------------------------------------------------------------
// Start / Stop
// ---------------------------------------------------------------------------

func TestSeeder_StartReturnsNil(t *testing.T) {
	cfg := DefaultSeederConfig()
	cfg.ListenPort = 0
	cfg.DataDir = t.TempDir()

	seeder, err := NewSeeder(cfg, nil)
	require.NoError(t, err)
	defer seeder.Stop() //nolint:errcheck

	err = seeder.Start()
	assert.NoError(t, err)
}

func TestSeeder_StopClearsState(t *testing.T) {
	cfg := DefaultSeederConfig()
	cfg.ListenPort = 0
	cfg.DataDir = t.TempDir()

	seeder, err := NewSeeder(cfg, nil)
	require.NoError(t, err)

	err = seeder.Start()
	require.NoError(t, err)

	err = seeder.Stop()
	assert.NoError(t, err)

	// After stop, torrents and addedAt maps should be empty
	assert.Empty(t, seeder.torrents)
	assert.Empty(t, seeder.addedAt)
}

func TestSeeder_StopCancelsContext(t *testing.T) {
	cfg := DefaultSeederConfig()
	cfg.ListenPort = 0
	cfg.DataDir = t.TempDir()

	seeder, err := NewSeeder(cfg, nil)
	require.NoError(t, err)

	// Context should not be done before stop
	select {
	case <-seeder.ctx.Done():
		t.Fatal("context should not be cancelled before Stop()")
	default:
		// expected
	}

	err = seeder.Stop()
	require.NoError(t, err)

	// Context should be done after stop
	select {
	case <-seeder.ctx.Done():
		// expected
	default:
		t.Fatal("context should be cancelled after Stop()")
	}
}

// ---------------------------------------------------------------------------
// TorrentStatus helpers
// ---------------------------------------------------------------------------

func TestTorrentStatus_GetHealthRatio(t *testing.T) {
	tests := []struct {
		name     string
		seeders  int
		leechers int
		want     float64
	}{
		{"no peers", 0, 0, 0},
		{"seeders only", 5, 0, 999.0},
		{"leechers only", 0, 5, 0},
		{"equal", 5, 5, 1.0},
		{"healthy", 10, 2, 5.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := &TorrentStatus{Seeders: tt.seeders, Leechers: tt.leechers}
			assert.InDelta(t, tt.want, ts.GetHealthRatio(), 0.001)
		})
	}
}

func TestTorrentStatus_GetCompletionPercent(t *testing.T) {
	tests := []struct {
		name      string
		completed int64
		length    int64
		want      float64
	}{
		{"zero length", 0, 0, 0},
		{"fully complete", 100, 100, 100.0},
		{"half complete", 50, 100, 50.0},
		{"nothing completed", 0, 1000, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := &TorrentStatus{BytesCompleted: tt.completed, Length: tt.length}
			assert.InDelta(t, tt.want, ts.GetCompletionPercent(), 0.001)
		})
	}
}

func TestTorrentStatus_GetSeedRatio(t *testing.T) {
	tests := []struct {
		name       string
		uploaded   int64
		downloaded int64
		want       float64
	}{
		{"no traffic", 0, 0, 0},
		{"upload only", 100, 0, 999.0},
		{"equal", 100, 100, 1.0},
		{"2x ratio", 200, 100, 2.0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := &TorrentStatus{TotalUploaded: tt.uploaded, TotalDownloaded: tt.downloaded}
			assert.InDelta(t, tt.want, ts.GetSeedRatio(), 0.001)
		})
	}
}
