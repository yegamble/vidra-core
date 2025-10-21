package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewVideoTorrent(t *testing.T) {
	tests := []struct {
		name        string
		videoID     uuid.UUID
		infoHash    string
		torrentPath string
		magnetURI   string
		pieceLength int
		totalSize   int64
		wantErr     bool
		errMsg      string
	}{
		{
			name:        "Valid torrent creation",
			videoID:     uuid.New(),
			infoHash:    "1234567890abcdef1234567890abcdef12345678",
			torrentPath: "/torrents/video.torrent",
			magnetURI:   "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678&dn=video.mp4",
			pieceLength: 262144, // 256KB
			totalSize:   1024 * 1024 * 100, // 100MB
			wantErr:     false,
		},
		{
			name:        "Invalid video ID",
			videoID:     uuid.Nil,
			infoHash:    "1234567890abcdef1234567890abcdef12345678",
			torrentPath: "/torrents/video.torrent",
			magnetURI:   "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678",
			pieceLength: 262144,
			totalSize:   1024 * 1024 * 100,
			wantErr:     true,
			errMsg:      "invalid video ID",
		},
		{
			name:        "Invalid info hash - too short",
			videoID:     uuid.New(),
			infoHash:    "12345",
			torrentPath: "/torrents/video.torrent",
			magnetURI:   "magnet:?xt=urn:btih:12345",
			pieceLength: 262144,
			totalSize:   1024 * 1024 * 100,
			wantErr:     true,
			errMsg:      "invalid info hash",
		},
		{
			name:        "Invalid info hash - non-hex",
			videoID:     uuid.New(),
			infoHash:    "gggggggggggggggggggggggggggggggggggggggg",
			torrentPath: "/torrents/video.torrent",
			magnetURI:   "magnet:?xt=urn:btih:gggggggggggggggggggggggggggggggggggggggg",
			pieceLength: 262144,
			totalSize:   1024 * 1024 * 100,
			wantErr:     true,
			errMsg:      "invalid info hash",
		},
		{
			name:        "Invalid magnet URI - wrong prefix",
			videoID:     uuid.New(),
			infoHash:    "1234567890abcdef1234567890abcdef12345678",
			torrentPath: "/torrents/video.torrent",
			magnetURI:   "http://example.com",
			pieceLength: 262144,
			totalSize:   1024 * 1024 * 100,
			wantErr:     true,
			errMsg:      "invalid magnet URI",
		},
		{
			name:        "Invalid magnet URI - missing xt",
			videoID:     uuid.New(),
			infoHash:    "1234567890abcdef1234567890abcdef12345678",
			torrentPath: "/torrents/video.torrent",
			magnetURI:   "magnet:?dn=video.mp4",
			pieceLength: 262144,
			totalSize:   1024 * 1024 * 100,
			wantErr:     true,
			errMsg:      "missing xt parameter",
		},
		{
			name:        "Invalid piece length - too small",
			videoID:     uuid.New(),
			infoHash:    "1234567890abcdef1234567890abcdef12345678",
			torrentPath: "/torrents/video.torrent",
			magnetURI:   "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678",
			pieceLength: 8192, // 8KB - too small
			totalSize:   1024 * 1024 * 100,
			wantErr:     true,
			errMsg:      "invalid piece length",
		},
		{
			name:        "Invalid piece length - not power of 2",
			videoID:     uuid.New(),
			infoHash:    "1234567890abcdef1234567890abcdef12345678",
			torrentPath: "/torrents/video.torrent",
			magnetURI:   "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678",
			pieceLength: 300000, // Not a power of 2
			totalSize:   1024 * 1024 * 100,
			wantErr:     true,
			errMsg:      "must be a power of 2",
		},
		{
			name:        "Empty torrent path",
			videoID:     uuid.New(),
			infoHash:    "1234567890abcdef1234567890abcdef12345678",
			torrentPath: "",
			magnetURI:   "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678",
			pieceLength: 262144,
			totalSize:   1024 * 1024 * 100,
			wantErr:     true,
			errMsg:      "torrent file path cannot be empty",
		},
		{
			name:        "Invalid total size",
			videoID:     uuid.New(),
			infoHash:    "1234567890abcdef1234567890abcdef12345678",
			torrentPath: "/torrents/video.torrent",
			magnetURI:   "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678",
			pieceLength: 262144,
			totalSize:   0,
			wantErr:     true,
			errMsg:      "total size must be positive",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			torrent, err := NewVideoTorrent(
				tt.videoID,
				tt.infoHash,
				tt.torrentPath,
				tt.magnetURI,
				tt.pieceLength,
				tt.totalSize,
			)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, torrent)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, torrent)
				assert.Equal(t, tt.videoID, torrent.VideoID)
				assert.Equal(t, strings.ToLower(tt.infoHash), torrent.InfoHash)
				assert.Equal(t, tt.torrentPath, torrent.TorrentFilePath)
				assert.Equal(t, tt.magnetURI, torrent.MagnetURI)
				assert.Equal(t, tt.pieceLength, torrent.PieceLength)
				assert.Equal(t, tt.totalSize, torrent.TotalSizeBytes)
				assert.Equal(t, 0, torrent.Seeders)
				assert.Equal(t, 0, torrent.Leechers)
				assert.False(t, torrent.IsSeeding)
				assert.NotZero(t, torrent.ID)
				assert.WithinDuration(t, time.Now(), torrent.CreatedAt, time.Second)
				assert.WithinDuration(t, time.Now(), torrent.UpdatedAt, time.Second)
			}
		})
	}
}

func TestValidateInfoHash(t *testing.T) {
	tests := []struct {
		name    string
		hash    string
		wantErr bool
	}{
		{
			name:    "Valid lowercase hash",
			hash:    "1234567890abcdef1234567890abcdef12345678",
			wantErr: false,
		},
		{
			name:    "Valid uppercase hash",
			hash:    "1234567890ABCDEF1234567890ABCDEF12345678",
			wantErr: false,
		},
		{
			name:    "Valid mixed case hash",
			hash:    "1234567890AbCdEf1234567890aBcDeF12345678",
			wantErr: false,
		},
		{
			name:    "Too short",
			hash:    "1234567890abcdef",
			wantErr: true,
		},
		{
			name:    "Too long",
			hash:    "1234567890abcdef1234567890abcdef123456789",
			wantErr: true,
		},
		{
			name:    "Non-hex characters",
			hash:    "xyz4567890abcdef1234567890abcdef12345678",
			wantErr: true,
		},
		{
			name:    "Empty string",
			hash:    "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateInfoHash(tt.hash)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidateMagnetURI(t *testing.T) {
	tests := []struct {
		name    string
		uri     string
		wantErr bool
		errMsg  string
	}{
		{
			name:    "Valid magnet URI",
			uri:     "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678",
			wantErr: false,
		},
		{
			name:    "Valid magnet URI with display name",
			uri:     "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678&dn=video.mp4",
			wantErr: false,
		},
		{
			name:    "Valid magnet URI with trackers",
			uri:     "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678&tr=wss://tracker.openwebtorrent.com",
			wantErr: false,
		},
		{
			name:    "Wrong prefix",
			uri:     "http://example.com",
			wantErr: true,
			errMsg:  "must start with 'magnet:?'",
		},
		{
			name:    "Missing xt parameter",
			uri:     "magnet:?dn=video.mp4",
			wantErr: true,
			errMsg:  "missing xt parameter",
		},
		{
			name:    "Wrong xt format",
			uri:     "magnet:?xt=wrongformat",
			wantErr: true,
			errMsg:  "xt must start with 'urn:btih:'",
		},
		{
			name:    "Empty string",
			uri:     "",
			wantErr: true,
			errMsg:  "must start with 'magnet:?'",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateMagnetURI(tt.uri)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestValidatePieceLength(t *testing.T) {
	tests := []struct {
		name    string
		length  int
		wantErr bool
		errMsg  string
	}{
		{
			name:    "Valid 16KB",
			length:  16 * 1024,
			wantErr: false,
		},
		{
			name:    "Valid 32KB",
			length:  32 * 1024,
			wantErr: false,
		},
		{
			name:    "Valid 256KB (WebTorrent default)",
			length:  256 * 1024,
			wantErr: false,
		},
		{
			name:    "Valid 1MB",
			length:  1024 * 1024,
			wantErr: false,
		},
		{
			name:    "Valid 16MB (max)",
			length:  16 * 1024 * 1024,
			wantErr: false,
		},
		{
			name:    "Too small",
			length:  8 * 1024,
			wantErr: true,
			errMsg:  "must be between",
		},
		{
			name:    "Too large",
			length:  32 * 1024 * 1024,
			wantErr: true,
			errMsg:  "must be between",
		},
		{
			name:    "Not power of 2",
			length:  300000,
			wantErr: true,
			errMsg:  "must be a power of 2",
		},
		{
			name:    "Zero",
			length:  0,
			wantErr: true,
			errMsg:  "must be between",
		},
		{
			name:    "Negative",
			length:  -262144,
			wantErr: true,
			errMsg:  "must be between",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidatePieceLength(tt.length)
			if tt.wantErr {
				assert.Error(t, err)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestVideoTorrent_GetHealthRatio(t *testing.T) {
	tests := []struct {
		name     string
		seeders  int
		leechers int
		expected float64
	}{
		{
			name:     "Normal ratio",
			seeders:  10,
			leechers: 5,
			expected: 2.0,
		},
		{
			name:     "No leechers (very healthy)",
			seeders:  10,
			leechers: 0,
			expected: 999.99,
		},
		{
			name:     "No activity",
			seeders:  0,
			leechers: 0,
			expected: 0,
		},
		{
			name:     "More leechers than seeders",
			seeders:  2,
			leechers: 10,
			expected: 0.2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			torrent := &VideoTorrent{
				Seeders:  tt.seeders,
				Leechers: tt.leechers,
			}
			ratio := torrent.GetHealthRatio()
			assert.Equal(t, tt.expected, ratio)
		})
	}
}

func TestVideoTorrent_IsHealthy(t *testing.T) {
	tests := []struct {
		name     string
		seeders  int
		expected bool
	}{
		{
			name:     "Healthy (3 seeders)",
			seeders:  3,
			expected: true,
		},
		{
			name:     "Very healthy (10 seeders)",
			seeders:  10,
			expected: true,
		},
		{
			name:     "Not healthy (2 seeders)",
			seeders:  2,
			expected: false,
		},
		{
			name:     "Not healthy (0 seeders)",
			seeders:  0,
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			torrent := &VideoTorrent{
				Seeders: tt.seeders,
			}
			isHealthy := torrent.IsHealthy()
			assert.Equal(t, tt.expected, isHealthy)
		})
	}
}

func TestNewTorrentTracker(t *testing.T) {
	tests := []struct {
		name        string
		announceURL string
		isWebSocket bool
		priority    int
		wantErr     bool
		errMsg      string
	}{
		{
			name:        "Valid HTTP tracker",
			announceURL: "http://tracker.example.com/announce",
			isWebSocket: false,
			priority:    1,
			wantErr:     false,
		},
		{
			name:        "Valid HTTPS tracker",
			announceURL: "https://tracker.example.com/announce",
			isWebSocket: false,
			priority:    2,
			wantErr:     false,
		},
		{
			name:        "Valid WebSocket tracker",
			announceURL: "wss://tracker.openwebtorrent.com",
			isWebSocket: true,
			priority:    1,
			wantErr:     false,
		},
		{
			name:        "Valid UDP tracker",
			announceURL: "udp://tracker.example.com:6969",
			isWebSocket: false,
			priority:    3,
			wantErr:     false,
		},
		{
			name:        "Invalid scheme",
			announceURL: "ftp://tracker.example.com",
			isWebSocket: false,
			priority:    1,
			wantErr:     true,
			errMsg:      "unsupported scheme",
		},
		{
			name:        "Missing host",
			announceURL: "http://",
			isWebSocket: false,
			priority:    1,
			wantErr:     true,
			errMsg:      "missing host",
		},
		{
			name:        "Invalid URL",
			announceURL: "not a url",
			isWebSocket: false,
			priority:    1,
			wantErr:     true,
		},
		{
			name:        "Empty URL",
			announceURL: "",
			isWebSocket: false,
			priority:    1,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker, err := NewTorrentTracker(tt.announceURL, tt.isWebSocket, tt.priority)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, tracker)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, tracker)
				assert.Equal(t, tt.announceURL, tracker.AnnounceURL)
				assert.Equal(t, tt.isWebSocket, tracker.IsWebSocket)
				assert.Equal(t, tt.priority, tracker.Priority)
				assert.True(t, tracker.IsActive)
				assert.Zero(t, tracker.SuccessCount)
				assert.Zero(t, tracker.FailureCount)
				assert.Nil(t, tracker.LastChecked)
				assert.NotZero(t, tracker.ID)
				assert.WithinDuration(t, time.Now(), tracker.CreatedAt, time.Second)
			}
		})
	}
}

func TestTorrentTracker_GetReliabilityScore(t *testing.T) {
	tests := []struct {
		name         string
		successCount int
		failureCount int
		expected     float64
	}{
		{
			name:         "Perfect reliability",
			successCount: 100,
			failureCount: 0,
			expected:     100.0,
		},
		{
			name:         "No reliability",
			successCount: 0,
			failureCount: 100,
			expected:     0.0,
		},
		{
			name:         "50% reliability",
			successCount: 50,
			failureCount: 50,
			expected:     50.0,
		},
		{
			name:         "Unknown reliability",
			successCount: 0,
			failureCount: 0,
			expected:     50.0, // Default when unknown
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tracker := &TorrentTracker{
				SuccessCount: tt.successCount,
				FailureCount: tt.failureCount,
			}
			score := tracker.GetReliabilityScore()
			assert.Equal(t, tt.expected, score)
		})
	}
}

func TestNewTorrentPeer(t *testing.T) {
	tests := []struct {
		name      string
		infoHash  string
		peerID    string
		ipAddress string
		port      int
		wantErr   bool
		errMsg    string
	}{
		{
			name:      "Valid peer",
			infoHash:  "1234567890abcdef1234567890abcdef12345678",
			peerID:    "-WT0001-123456789012", // WebTorrent style peer ID
			ipAddress: "192.168.1.100",
			port:      6881,
			wantErr:   false,
		},
		{
			name:      "Invalid info hash",
			infoHash:  "invalid",
			peerID:    "-WT0001-123456789012",
			ipAddress: "192.168.1.100",
			port:      6881,
			wantErr:   true,
			errMsg:    "invalid info hash",
		},
		{
			name:      "Invalid peer ID",
			infoHash:  "1234567890abcdef1234567890abcdef12345678",
			peerID:    "short",
			ipAddress: "192.168.1.100",
			port:      6881,
			wantErr:   true,
			errMsg:    "invalid peer ID",
		},
		{
			name:      "Empty IP address",
			infoHash:  "1234567890abcdef1234567890abcdef12345678",
			peerID:    "-WT0001-123456789012",
			ipAddress: "",
			port:      6881,
			wantErr:   true,
			errMsg:    "IP address cannot be empty",
		},
		{
			name:      "Invalid port (zero)",
			infoHash:  "1234567890abcdef1234567890abcdef12345678",
			peerID:    "-WT0001-123456789012",
			ipAddress: "192.168.1.100",
			port:      0,
			wantErr:   true,
			errMsg:    "invalid port number",
		},
		{
			name:      "Invalid port (too high)",
			infoHash:  "1234567890abcdef1234567890abcdef12345678",
			peerID:    "-WT0001-123456789012",
			ipAddress: "192.168.1.100",
			port:      70000,
			wantErr:   true,
			errMsg:    "invalid port number",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peer, err := NewTorrentPeer(tt.infoHash, tt.peerID, tt.ipAddress, tt.port)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, peer)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, peer)
				assert.Equal(t, strings.ToLower(tt.infoHash), peer.InfoHash)
				assert.Equal(t, tt.peerID, peer.PeerID)
				assert.Equal(t, tt.ipAddress, peer.IPAddress)
				assert.Equal(t, tt.port, peer.Port)
				assert.Equal(t, PeerEventStarted, peer.Event)
				assert.NotZero(t, peer.ID)
				assert.WithinDuration(t, time.Now(), peer.LastAnnounceAt, time.Second)
				assert.WithinDuration(t, time.Now(), peer.CreatedAt, time.Second)
			}
		})
	}
}

func TestTorrentPeer_IsSeeder(t *testing.T) {
	tests := []struct {
		name      string
		leftBytes int64
		event     string
		expected  bool
	}{
		{
			name:      "Is seeder (complete)",
			leftBytes: 0,
			event:     PeerEventCompleted,
			expected:  true,
		},
		{
			name:      "Is seeder (started with complete file)",
			leftBytes: 0,
			event:     PeerEventStarted,
			expected:  true,
		},
		{
			name:      "Not seeder (has bytes left)",
			leftBytes: 1024,
			event:     PeerEventStarted,
			expected:  false,
		},
		{
			name:      "Not seeder (stopped)",
			leftBytes: 0,
			event:     PeerEventStopped,
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peer := &TorrentPeer{
				LeftBytes: tt.leftBytes,
				Event:     tt.event,
			}
			isSeeder := peer.IsSeeder()
			assert.Equal(t, tt.expected, isSeeder)
		})
	}
}

func TestTorrentPeer_IsActive(t *testing.T) {
	tests := []struct {
		name           string
		lastAnnounceAt time.Time
		event          string
		expected       bool
	}{
		{
			name:           "Active (recent announce)",
			lastAnnounceAt: time.Now().Add(-5 * time.Minute),
			event:          PeerEventStarted,
			expected:       true,
		},
		{
			name:           "Active (29 minutes ago)",
			lastAnnounceAt: time.Now().Add(-29 * time.Minute),
			event:          PeerEventStarted,
			expected:       true,
		},
		{
			name:           "Not active (31 minutes ago)",
			lastAnnounceAt: time.Now().Add(-31 * time.Minute),
			event:          PeerEventStarted,
			expected:       false,
		},
		{
			name:           "Not active (stopped recently)",
			lastAnnounceAt: time.Now().Add(-1 * time.Minute),
			event:          PeerEventStopped,
			expected:       false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			peer := &TorrentPeer{
				LastAnnounceAt: tt.lastAnnounceAt,
				Event:          tt.event,
			}
			isActive := peer.IsActive()
			assert.Equal(t, tt.expected, isActive)
		})
	}
}

func TestNewTorrentWebSeed(t *testing.T) {
	videoTorrentID := uuid.New()

	tests := []struct {
		name           string
		videoTorrentID uuid.UUID
		url            string
		priority       int
		wantErr        bool
		errMsg         string
	}{
		{
			name:           "Valid HTTP web seed",
			videoTorrentID: videoTorrentID,
			url:            "http://cdn.example.com/videos/file.mp4",
			priority:       1,
			wantErr:        false,
		},
		{
			name:           "Valid HTTPS web seed",
			videoTorrentID: videoTorrentID,
			url:            "https://cdn.example.com/videos/file.mp4",
			priority:       1,
			wantErr:        false,
		},
		{
			name:           "Invalid video torrent ID",
			videoTorrentID: uuid.Nil,
			url:            "http://cdn.example.com/videos/file.mp4",
			priority:       1,
			wantErr:        true,
			errMsg:         "video torrent ID cannot be nil",
		},
		{
			name:           "Invalid URL scheme",
			videoTorrentID: videoTorrentID,
			url:            "ftp://cdn.example.com/videos/file.mp4",
			priority:       1,
			wantErr:        true,
			errMsg:         "must use HTTP or HTTPS",
		},
		{
			name:           "Invalid URL format",
			videoTorrentID: videoTorrentID,
			url:            "not a url",
			priority:       1,
			wantErr:        true,
		},
		{
			name:           "Empty URL",
			videoTorrentID: videoTorrentID,
			url:            "",
			priority:       1,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			webSeed, err := NewTorrentWebSeed(tt.videoTorrentID, tt.url, tt.priority)

			if tt.wantErr {
				assert.Error(t, err)
				assert.Nil(t, webSeed)
				if tt.errMsg != "" {
					assert.Contains(t, err.Error(), tt.errMsg)
				}
			} else {
				require.NoError(t, err)
				require.NotNil(t, webSeed)
				assert.Equal(t, tt.videoTorrentID, webSeed.VideoTorrentID)
				assert.Equal(t, tt.url, webSeed.URL)
				assert.Equal(t, tt.priority, webSeed.Priority)
				assert.True(t, webSeed.IsActive)
				assert.NotZero(t, webSeed.ID)
				assert.WithinDuration(t, time.Now(), webSeed.CreatedAt, time.Second)
			}
		})
	}
}

func TestNewTorrentStats(t *testing.T) {
	videoTorrentID := uuid.New()
	stats := NewTorrentStats(videoTorrentID)

	assert.NotNil(t, stats)
	assert.NotZero(t, stats.ID)
	assert.Equal(t, videoTorrentID, stats.VideoTorrentID)

	// Check that hour is truncated to the hour
	now := time.Now()
	expectedHour := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())
	assert.Equal(t, expectedHour, stats.Hour)

	assert.Zero(t, stats.TotalPeers)
	assert.Zero(t, stats.TotalSeeds)
	assert.Zero(t, stats.BytesUploaded)
	assert.Zero(t, stats.BytesDownloaded)
	assert.Zero(t, stats.CompletedDownloads)
	assert.WithinDuration(t, time.Now(), stats.CreatedAt, time.Second)
}

func TestTorrentStats_GetTransferRatio(t *testing.T) {
	tests := []struct {
		name            string
		bytesUploaded   int64
		bytesDownloaded int64
		expected        float64
	}{
		{
			name:            "Normal ratio",
			bytesUploaded:   1024 * 1024 * 100, // 100MB
			bytesDownloaded: 1024 * 1024 * 50,  // 50MB
			expected:        2.0,
		},
		{
			name:            "No downloads (infinite ratio)",
			bytesUploaded:   1024 * 1024 * 100,
			bytesDownloaded: 0,
			expected:        999.99,
		},
		{
			name:            "No activity",
			bytesUploaded:   0,
			bytesDownloaded: 0,
			expected:        0,
		},
		{
			name:            "More downloads than uploads",
			bytesUploaded:   1024 * 1024 * 10,  // 10MB
			bytesDownloaded: 1024 * 1024 * 100, // 100MB
			expected:        0.1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stats := &TorrentStats{
				BytesUploaded:   tt.bytesUploaded,
				BytesDownloaded: tt.bytesDownloaded,
			}
			ratio := stats.GetTransferRatio()
			assert.Equal(t, tt.expected, ratio)
		})
	}
}