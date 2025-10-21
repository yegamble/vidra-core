package domain

import (
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
)

// Torrent errors
var (
	ErrInvalidInfoHash      = errors.New("invalid info hash")
	ErrInvalidMagnetURI     = errors.New("invalid magnet URI")
	ErrInvalidPieceLength   = errors.New("invalid piece length")
	ErrInvalidTrackerURL    = errors.New("invalid tracker URL")
	ErrInvalidPeerID        = errors.New("invalid peer ID")
	ErrTorrentNotFound      = errors.New("torrent not found")
	ErrPeerNotFound         = errors.New("peer not found")
	ErrTrackerNotFound      = errors.New("tracker not found")
	ErrTorrentAlreadyExists = errors.New("torrent already exists for this video")
)

// VideoTorrent represents a torrent for a video
type VideoTorrent struct {
	ID                 uuid.UUID `json:"id" db:"id"`
	VideoID            uuid.UUID `json:"video_id" db:"video_id"`
	InfoHash           string    `json:"info_hash" db:"info_hash"`
	TorrentFilePath    string    `json:"torrent_file_path" db:"torrent_file_path"`
	MagnetURI          string    `json:"magnet_uri" db:"magnet_uri"`
	PieceLength        int       `json:"piece_length" db:"piece_length"`
	TotalSizeBytes     int64     `json:"total_size_bytes" db:"total_size_bytes"`
	Seeders            int       `json:"seeders" db:"seeders"`
	Leechers           int       `json:"leechers" db:"leechers"`
	CompletedDownloads int       `json:"completed_downloads" db:"completed_downloads"`
	IsSeeding          bool      `json:"is_seeding" db:"is_seeding"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
	UpdatedAt          time.Time `json:"updated_at" db:"updated_at"`
}

// NewVideoTorrent creates a new video torrent
func NewVideoTorrent(videoID uuid.UUID, infoHash, torrentPath, magnetURI string, pieceLength int, totalSize int64) (*VideoTorrent, error) {
	if videoID == uuid.Nil {
		return nil, ErrInvalidVideoID
	}

	if err := ValidateInfoHash(infoHash); err != nil {
		return nil, err
	}

	if err := ValidateMagnetURI(magnetURI); err != nil {
		return nil, err
	}

	if err := ValidatePieceLength(pieceLength); err != nil {
		return nil, err
	}

	if torrentPath == "" {
		return nil, errors.New("torrent file path cannot be empty")
	}

	if totalSize <= 0 {
		return nil, errors.New("total size must be positive")
	}

	now := time.Now()
	return &VideoTorrent{
		ID:              uuid.New(),
		VideoID:         videoID,
		InfoHash:        strings.ToLower(infoHash),
		TorrentFilePath: torrentPath,
		MagnetURI:       magnetURI,
		PieceLength:     pieceLength,
		TotalSizeBytes:  totalSize,
		Seeders:         0,
		Leechers:        0,
		IsSeeding:       false,
		CreatedAt:       now,
		UpdatedAt:       now,
	}, nil
}

// ValidateInfoHash validates a BitTorrent info hash
func ValidateInfoHash(hash string) error {
	// Info hash should be 40 hex characters (SHA1)
	matched, _ := regexp.MatchString("^[a-fA-F0-9]{40}$", hash)
	if !matched {
		return fmt.Errorf("%w: must be 40 hex characters", ErrInvalidInfoHash)
	}
	return nil
}

// ValidateMagnetURI validates a magnet URI
func ValidateMagnetURI(magnetURI string) error {
	if !strings.HasPrefix(magnetURI, "magnet:?") {
		return fmt.Errorf("%w: must start with 'magnet:?'", ErrInvalidMagnetURI)
	}

	// Parse the magnet URI
	parsed, err := url.Parse(magnetURI)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidMagnetURI, err)
	}

	// Check for required xt (exact topic) parameter
	xt := parsed.Query().Get("xt")
	if xt == "" {
		return fmt.Errorf("%w: missing xt parameter", ErrInvalidMagnetURI)
	}

	// Validate xt format (should be urn:btih:<hash>)
	if !strings.HasPrefix(xt, "urn:btih:") {
		return fmt.Errorf("%w: xt must start with 'urn:btih:'", ErrInvalidMagnetURI)
	}

	return nil
}

// ValidatePieceLength validates the piece length for a torrent
func ValidatePieceLength(length int) error {
	// Piece length must be a power of 2 between 16KB and 16MB
	minPieceLength := 16 * 1024        // 16KB
	maxPieceLength := 16 * 1024 * 1024 // 16MB

	if length < minPieceLength || length > maxPieceLength {
		return fmt.Errorf("%w: must be between %d and %d bytes", ErrInvalidPieceLength, minPieceLength, maxPieceLength)
	}

	// Check if it's a power of 2
	if (length & (length - 1)) != 0 {
		return fmt.Errorf("%w: must be a power of 2", ErrInvalidPieceLength)
	}

	return nil
}

// GetHealthRatio calculates the health ratio (seeders/leechers)
func (vt *VideoTorrent) GetHealthRatio() float64 {
	if vt.Leechers == 0 {
		if vt.Seeders > 0 {
			return 999.99 // Infinite ratio (very healthy)
		}
		return 0 // No activity
	}
	return float64(vt.Seeders) / float64(vt.Leechers)
}

// IsHealthy returns true if the torrent has good availability
func (vt *VideoTorrent) IsHealthy() bool {
	// Consider healthy if we have at least 3 seeders
	return vt.Seeders >= 3
}

// TorrentTracker represents a BitTorrent tracker
type TorrentTracker struct {
	ID           uuid.UUID  `json:"id" db:"id"`
	AnnounceURL  string     `json:"announce_url" db:"announce_url"`
	IsWebSocket  bool       `json:"is_websocket" db:"is_websocket"`
	IsActive     bool       `json:"is_active" db:"is_active"`
	Priority     int        `json:"priority" db:"priority"`
	SuccessCount int        `json:"success_count" db:"success_count"`
	FailureCount int        `json:"failure_count" db:"failure_count"`
	LastChecked  *time.Time `json:"last_checked_at" db:"last_checked_at"`
	CreatedAt    time.Time  `json:"created_at" db:"created_at"`
}

// NewTorrentTracker creates a new torrent tracker
func NewTorrentTracker(announceURL string, isWebSocket bool, priority int) (*TorrentTracker, error) {
	if err := ValidateTrackerURL(announceURL); err != nil {
		return nil, err
	}

	return &TorrentTracker{
		ID:           uuid.New(),
		AnnounceURL:  announceURL,
		IsWebSocket:  isWebSocket,
		IsActive:     true,
		Priority:     priority,
		SuccessCount: 0,
		FailureCount: 0,
		CreatedAt:    time.Now(),
	}, nil
}

// ValidateTrackerURL validates a tracker announce URL
func ValidateTrackerURL(announceURL string) error {
	parsed, err := url.Parse(announceURL)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrInvalidTrackerURL, err)
	}

	// Check for valid schemes
	validSchemes := map[string]bool{
		"http":  true,
		"https": true,
		"udp":   true,
		"ws":    true,
		"wss":   true,
	}

	if !validSchemes[parsed.Scheme] {
		return fmt.Errorf("%w: unsupported scheme %s", ErrInvalidTrackerURL, parsed.Scheme)
	}

	if parsed.Host == "" {
		return fmt.Errorf("%w: missing host", ErrInvalidTrackerURL)
	}

	return nil
}

// GetReliabilityScore calculates tracker reliability (0-100)
func (tt *TorrentTracker) GetReliabilityScore() float64 {
	total := tt.SuccessCount + tt.FailureCount
	if total == 0 {
		return 50.0 // Unknown reliability
	}
	return float64(tt.SuccessCount) / float64(total) * 100
}

// TorrentPeer represents a peer in a torrent swarm
type TorrentPeer struct {
	ID                 uuid.UUID `json:"id" db:"id"`
	InfoHash           string    `json:"info_hash" db:"info_hash"`
	PeerID             string    `json:"peer_id" db:"peer_id"`
	IPAddress          string    `json:"ip_address" db:"ip_address"`
	Port               int       `json:"port" db:"port"`
	UploadedBytes      int64     `json:"uploaded_bytes" db:"uploaded_bytes"`
	DownloadedBytes    int64     `json:"downloaded_bytes" db:"downloaded_bytes"`
	LeftBytes          int64     `json:"left_bytes" db:"left_bytes"`
	Event              string    `json:"event" db:"event"`
	UserAgent          string    `json:"user_agent" db:"user_agent"`
	SupportsWebTorrent bool      `json:"supports_webtorrent" db:"supports_webtorrent"`
	LastAnnounceAt     time.Time `json:"last_announce_at" db:"last_announce_at"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
}

// PeerEvent types
const (
	PeerEventStarted   = "started"
	PeerEventStopped   = "stopped"
	PeerEventCompleted = "completed"
)

// NewTorrentPeer creates a new torrent peer
func NewTorrentPeer(infoHash, peerID, ipAddress string, port int) (*TorrentPeer, error) {
	if err := ValidateInfoHash(infoHash); err != nil {
		return nil, err
	}

	if err := ValidatePeerID(peerID); err != nil {
		return nil, err
	}

	if ipAddress == "" {
		return nil, errors.New("IP address cannot be empty")
	}

	if port <= 0 || port > 65535 {
		return nil, errors.New("invalid port number")
	}

	now := time.Now()
	return &TorrentPeer{
		ID:             uuid.New(),
		InfoHash:       strings.ToLower(infoHash),
		PeerID:         peerID,
		IPAddress:      ipAddress,
		Port:           port,
		Event:          PeerEventStarted,
		LastAnnounceAt: now,
		CreatedAt:      now,
	}, nil
}

// ValidatePeerID validates a BitTorrent peer ID
func ValidatePeerID(peerID string) error {
	// Peer ID should be exactly 20 bytes (often URL-encoded)
	if len(peerID) < 20 {
		return fmt.Errorf("%w: must be at least 20 characters", ErrInvalidPeerID)
	}
	return nil
}

// IsSeeder returns true if the peer is a seeder (has complete file)
func (tp *TorrentPeer) IsSeeder() bool {
	return tp.LeftBytes == 0 && tp.Event != PeerEventStopped
}

// IsActive returns true if the peer is still active
func (tp *TorrentPeer) IsActive() bool {
	// Consider active if announced within last 30 minutes
	return time.Since(tp.LastAnnounceAt) < 30*time.Minute && tp.Event != PeerEventStopped
}

// TorrentWebSeed represents an HTTP/HTTPS web seed
type TorrentWebSeed struct {
	ID             uuid.UUID `json:"id" db:"id"`
	VideoTorrentID uuid.UUID `json:"video_torrent_id" db:"video_torrent_id"`
	URL            string    `json:"url" db:"url"`
	IsActive       bool      `json:"is_active" db:"is_active"`
	Priority       int       `json:"priority" db:"priority"`
	CreatedAt      time.Time `json:"created_at" db:"created_at"`
}

// NewTorrentWebSeed creates a new web seed
func NewTorrentWebSeed(videoTorrentID uuid.UUID, seedURL string, priority int) (*TorrentWebSeed, error) {
	if videoTorrentID == uuid.Nil {
		return nil, errors.New("video torrent ID cannot be nil")
	}

	parsed, err := url.Parse(seedURL)
	if err != nil {
		return nil, fmt.Errorf("invalid web seed URL: %w", err)
	}

	// Only allow HTTP and HTTPS
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return nil, errors.New("web seed must use HTTP or HTTPS")
	}

	return &TorrentWebSeed{
		ID:             uuid.New(),
		VideoTorrentID: videoTorrentID,
		URL:            seedURL,
		IsActive:       true,
		Priority:       priority,
		CreatedAt:      time.Now(),
	}, nil
}

// TorrentStats represents hourly statistics for a torrent
type TorrentStats struct {
	ID                 uuid.UUID `json:"id" db:"id"`
	VideoTorrentID     uuid.UUID `json:"video_torrent_id" db:"video_torrent_id"`
	Hour               time.Time `json:"hour" db:"hour"`
	TotalPeers         int       `json:"total_peers" db:"total_peers"`
	TotalSeeds         int       `json:"total_seeds" db:"total_seeds"`
	BytesUploaded      int64     `json:"bytes_uploaded" db:"bytes_uploaded"`
	BytesDownloaded    int64     `json:"bytes_downloaded" db:"bytes_downloaded"`
	CompletedDownloads int       `json:"completed_downloads" db:"completed_downloads"`
	CreatedAt          time.Time `json:"created_at" db:"created_at"`
}

// NewTorrentStats creates new torrent statistics for the current hour
func NewTorrentStats(videoTorrentID uuid.UUID) *TorrentStats {
	now := time.Now()
	hour := time.Date(now.Year(), now.Month(), now.Day(), now.Hour(), 0, 0, 0, now.Location())

	return &TorrentStats{
		ID:             uuid.New(),
		VideoTorrentID: videoTorrentID,
		Hour:           hour,
		CreatedAt:      now,
	}
}

// GetTransferRatio calculates upload/download ratio
func (ts *TorrentStats) GetTransferRatio() float64 {
	if ts.BytesDownloaded == 0 {
		if ts.BytesUploaded > 0 {
			return 999.99 // Infinite ratio
		}
		return 0
	}
	return float64(ts.BytesUploaded) / float64(ts.BytesDownloaded)
}
