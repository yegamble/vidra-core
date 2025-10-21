package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// TorrentRepository handles database operations for torrents
type TorrentRepository struct {
	db *sqlx.DB
}

// NewTorrentRepository creates a new torrent repository
func NewTorrentRepository(db *sqlx.DB) *TorrentRepository {
	return &TorrentRepository{db: db}
}

// CreateTorrent creates a new torrent record
func (r *TorrentRepository) CreateTorrent(ctx context.Context, torrent *domain.VideoTorrent) error {
	query := `
		INSERT INTO video_torrents (
			id, video_id, info_hash, torrent_file_path, magnet_uri,
			piece_length, total_size_bytes, seeders, leechers,
			completed_downloads, is_seeding, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13
		)`

	_, err := r.db.ExecContext(ctx, query,
		torrent.ID,
		torrent.VideoID,
		torrent.InfoHash,
		torrent.TorrentFilePath,
		torrent.MagnetURI,
		torrent.PieceLength,
		torrent.TotalSizeBytes,
		torrent.Seeders,
		torrent.Leechers,
		torrent.CompletedDownloads,
		torrent.IsSeeding,
		torrent.CreatedAt,
		torrent.UpdatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to create torrent: %w", err)
	}

	return nil
}

// GetTorrentByVideoID retrieves a torrent by video ID
func (r *TorrentRepository) GetTorrentByVideoID(ctx context.Context, videoID uuid.UUID) (*domain.VideoTorrent, error) {
	var torrent domain.VideoTorrent
	query := `
		SELECT
			id, video_id, info_hash, torrent_file_path, magnet_uri,
			piece_length, total_size_bytes, seeders, leechers,
			completed_downloads, is_seeding, created_at, updated_at
		FROM video_torrents
		WHERE video_id = $1`

	err := r.db.GetContext(ctx, &torrent, query, videoID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("torrent not found for video %s", videoID)
		}
		return nil, fmt.Errorf("failed to get torrent: %w", err)
	}

	return &torrent, nil
}

// GetTorrentByInfoHash retrieves a torrent by info hash
func (r *TorrentRepository) GetTorrentByInfoHash(ctx context.Context, infoHash string) (*domain.VideoTorrent, error) {
	var torrent domain.VideoTorrent
	query := `
		SELECT
			id, video_id, info_hash, torrent_file_path, magnet_uri,
			piece_length, total_size_bytes, seeders, leechers,
			completed_downloads, is_seeding, created_at, updated_at
		FROM video_torrents
		WHERE info_hash = $1`

	err := r.db.GetContext(ctx, &torrent, query, infoHash)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("torrent not found for info hash %s", infoHash)
		}
		return nil, fmt.Errorf("failed to get torrent: %w", err)
	}

	return &torrent, nil
}

// UpdateTorrentStats updates torrent statistics
func (r *TorrentRepository) UpdateTorrentStats(ctx context.Context, infoHash string, seeders, leechers int) error {
	query := `
		UPDATE video_torrents
		SET seeders = $2,
		    leechers = $3,
		    updated_at = $4
		WHERE info_hash = $1`

	result, err := r.db.ExecContext(ctx, query, infoHash, seeders, leechers, time.Now())
	if err != nil {
		return fmt.Errorf("failed to update torrent stats: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("torrent not found: %s", infoHash)
	}

	return nil
}

// IncrementCompletedDownloads increments the completed downloads counter
func (r *TorrentRepository) IncrementCompletedDownloads(ctx context.Context, infoHash string) error {
	query := `
		UPDATE video_torrents
		SET completed_downloads = completed_downloads + 1,
		    updated_at = $2
		WHERE info_hash = $1`

	result, err := r.db.ExecContext(ctx, query, infoHash, time.Now())
	if err != nil {
		return fmt.Errorf("failed to increment completed downloads: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("torrent not found: %s", infoHash)
	}

	return nil
}

// SetSeedingStatus updates the seeding status of a torrent
func (r *TorrentRepository) SetSeedingStatus(ctx context.Context, videoID uuid.UUID, isSeeding bool) error {
	query := `
		UPDATE video_torrents
		SET is_seeding = $2,
		    updated_at = $3
		WHERE video_id = $1`

	result, err := r.db.ExecContext(ctx, query, videoID, isSeeding, time.Now())
	if err != nil {
		return fmt.Errorf("failed to update seeding status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("torrent not found for video: %s", videoID)
	}

	return nil
}

// GetActiveTorrents retrieves all torrents that are being seeded
func (r *TorrentRepository) GetActiveTorrents(ctx context.Context) ([]*domain.VideoTorrent, error) {
	var torrents []*domain.VideoTorrent
	query := `
		SELECT
			id, video_id, info_hash, torrent_file_path, magnet_uri,
			piece_length, total_size_bytes, seeders, leechers,
			completed_downloads, is_seeding, created_at, updated_at
		FROM video_torrents
		WHERE is_seeding = true
		ORDER BY created_at DESC`

	err := r.db.SelectContext(ctx, &torrents, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get active torrents: %w", err)
	}

	return torrents, nil
}

// DeleteTorrent deletes a torrent record
func (r *TorrentRepository) DeleteTorrent(ctx context.Context, videoID uuid.UUID) error {
	query := `DELETE FROM video_torrents WHERE video_id = $1`

	result, err := r.db.ExecContext(ctx, query, videoID)
	if err != nil {
		return fmt.Errorf("failed to delete torrent: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("torrent not found for video: %s", videoID)
	}

	return nil
}

// TorrentPeerRepository handles peer operations
type TorrentPeerRepository struct {
	db *sqlx.DB
}

// NewTorrentPeerRepository creates a new peer repository
func NewTorrentPeerRepository(db *sqlx.DB) *TorrentPeerRepository {
	return &TorrentPeerRepository{db: db}
}

// UpsertPeer inserts or updates a peer
func (r *TorrentPeerRepository) UpsertPeer(ctx context.Context, peer *domain.TorrentPeer) error {
	query := `
		INSERT INTO torrent_peers (
			id, info_hash, peer_id, ip_address, port,
			uploaded_bytes, downloaded_bytes, left_bytes,
			event, user_agent, last_announce_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11
		)
		ON CONFLICT (info_hash, peer_id)
		DO UPDATE SET
			ip_address = EXCLUDED.ip_address,
			port = EXCLUDED.port,
			uploaded_bytes = EXCLUDED.uploaded_bytes,
			downloaded_bytes = EXCLUDED.downloaded_bytes,
			left_bytes = EXCLUDED.left_bytes,
			event = EXCLUDED.event,
			user_agent = EXCLUDED.user_agent,
			last_announce_at = EXCLUDED.last_announce_at`

	_, err := r.db.ExecContext(ctx, query,
		peer.ID,
		peer.InfoHash,
		peer.PeerID,
		peer.IPAddress,
		peer.Port,
		peer.UploadedBytes,
		peer.DownloadedBytes,
		peer.LeftBytes,
		peer.Event,
		peer.UserAgent,
		peer.LastAnnounceAt,
	)

	if err != nil {
		return fmt.Errorf("failed to upsert peer: %w", err)
	}

	return nil
}

// GetPeers retrieves active peers for a torrent
func (r *TorrentPeerRepository) GetPeers(ctx context.Context, infoHash string, limit int) ([]*domain.TorrentPeer, error) {
	var peers []*domain.TorrentPeer

	// Get peers that announced within the last 30 minutes
	query := `
		SELECT
			id, info_hash, peer_id, ip_address, port,
			uploaded_bytes, downloaded_bytes, left_bytes,
			event, user_agent, last_announce_at
		FROM torrent_peers
		WHERE info_hash = $1
		  AND last_announce_at > NOW() - INTERVAL '30 minutes'
		  AND event != 'stopped'
		ORDER BY last_announce_at DESC
		LIMIT $2`

	err := r.db.SelectContext(ctx, &peers, query, infoHash, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get peers: %w", err)
	}

	return peers, nil
}

// RemovePeer removes a peer from the swarm
func (r *TorrentPeerRepository) RemovePeer(ctx context.Context, infoHash, peerID string) error {
	query := `DELETE FROM torrent_peers WHERE info_hash = $1 AND peer_id = $2`

	_, err := r.db.ExecContext(ctx, query, infoHash, peerID)
	if err != nil {
		return fmt.Errorf("failed to remove peer: %w", err)
	}

	return nil
}

// CleanupOldPeers removes peers that haven't announced recently
func (r *TorrentPeerRepository) CleanupOldPeers(ctx context.Context) error {
	query := `
		DELETE FROM torrent_peers
		WHERE last_announce_at < NOW() - INTERVAL '30 minutes'`

	_, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return fmt.Errorf("failed to cleanup old peers: %w", err)
	}

	return nil
}

// GetPeerStats retrieves peer statistics for a torrent
func (r *TorrentPeerRepository) GetPeerStats(ctx context.Context, infoHash string) (seeders, leechers int, err error) {
	query := `
		SELECT
			COUNT(*) FILTER (WHERE left_bytes = 0) AS seeders,
			COUNT(*) FILTER (WHERE left_bytes > 0) AS leechers
		FROM torrent_peers
		WHERE info_hash = $1
		  AND last_announce_at > NOW() - INTERVAL '30 minutes'
		  AND event != 'stopped'`

	row := r.db.QueryRowContext(ctx, query, infoHash)
	err = row.Scan(&seeders, &leechers)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to get peer stats: %w", err)
	}

	return seeders, leechers, nil
}

// TorrentTrackerRepository handles tracker operations
type TorrentTrackerRepository struct {
	db *sqlx.DB
}

// NewTorrentTrackerRepository creates a new tracker repository
func NewTorrentTrackerRepository(db *sqlx.DB) *TorrentTrackerRepository {
	return &TorrentTrackerRepository{db: db}
}

// GetActiveTrackers retrieves all active trackers
func (r *TorrentTrackerRepository) GetActiveTrackers(ctx context.Context) ([]*domain.TorrentTracker, error) {
	var trackers []*domain.TorrentTracker
	query := `
		SELECT id, announce_url, is_websocket, is_active, priority, created_at
		FROM torrent_trackers
		WHERE is_active = true
		ORDER BY priority ASC`

	err := r.db.SelectContext(ctx, &trackers, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get active trackers: %w", err)
	}

	return trackers, nil
}

// AddTracker adds a new tracker
func (r *TorrentTrackerRepository) AddTracker(ctx context.Context, tracker *domain.TorrentTracker) error {
	query := `
		INSERT INTO torrent_trackers (
			id, announce_url, is_websocket, is_active, priority, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6
		)`

	_, err := r.db.ExecContext(ctx, query,
		tracker.ID,
		tracker.AnnounceURL,
		tracker.IsWebSocket,
		tracker.IsActive,
		tracker.Priority,
		tracker.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to add tracker: %w", err)
	}

	return nil
}

// SetTrackerActive sets the active status of a tracker
func (r *TorrentTrackerRepository) SetTrackerActive(ctx context.Context, announceURL string, isActive bool) error {
	query := `
		UPDATE torrent_trackers
		SET is_active = $2
		WHERE announce_url = $1`

	result, err := r.db.ExecContext(ctx, query, announceURL, isActive)
	if err != nil {
		return fmt.Errorf("failed to update tracker status: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get affected rows: %w", err)
	}

	if rows == 0 {
		return fmt.Errorf("tracker not found: %s", announceURL)
	}

	return nil
}

// TorrentWebSeedRepository handles web seed operations
type TorrentWebSeedRepository struct {
	db *sqlx.DB
}

// NewTorrentWebSeedRepository creates a new web seed repository
func NewTorrentWebSeedRepository(db *sqlx.DB) *TorrentWebSeedRepository {
	return &TorrentWebSeedRepository{db: db}
}

// AddWebSeed adds a web seed for a torrent
func (r *TorrentWebSeedRepository) AddWebSeed(ctx context.Context, seed *domain.TorrentWebSeed) error {
	query := `
		INSERT INTO torrent_web_seeds (
			id, torrent_id, url, priority, is_active, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6
		)`

	_, err := r.db.ExecContext(ctx, query,
		seed.ID,
		seed.VideoTorrentID,
		seed.URL,
		seed.Priority,
		seed.IsActive,
		seed.CreatedAt,
	)

	if err != nil {
		return fmt.Errorf("failed to add web seed: %w", err)
	}

	return nil
}

// GetWebSeeds retrieves web seeds for a torrent
func (r *TorrentWebSeedRepository) GetWebSeeds(ctx context.Context, torrentID uuid.UUID) ([]*domain.TorrentWebSeed, error) {
	var seeds []*domain.TorrentWebSeed
	query := `
		SELECT id, torrent_id AS video_torrent_id, url, priority, is_active, created_at
		FROM torrent_web_seeds
		WHERE torrent_id = $1 AND is_active = true
		ORDER BY priority ASC`

	err := r.db.SelectContext(ctx, &seeds, query, torrentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get web seeds: %w", err)
	}

	return seeds, nil
}

// TorrentStatsRepository handles torrent statistics
type TorrentStatsRepository struct {
	db *sqlx.DB
}

// NewTorrentStatsRepository creates a new stats repository
func NewTorrentStatsRepository(db *sqlx.DB) *TorrentStatsRepository {
	return &TorrentStatsRepository{db: db}
}

// RecordStats records hourly torrent statistics
func (r *TorrentStatsRepository) RecordStats(ctx context.Context, stats *domain.TorrentStats) error {
	query := `
		INSERT INTO torrent_stats (
			id, torrent_id, hour, seeders_avg, leechers_avg,
			downloaded_bytes, uploaded_bytes, completed_count
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		)`

	_, err := r.db.ExecContext(ctx, query,
		stats.ID,
		stats.VideoTorrentID,
		stats.Hour,
		stats.TotalSeeds,                  // Using TotalSeeds as seeders_avg
		stats.TotalPeers-stats.TotalSeeds, // Calculate leechers
		stats.BytesDownloaded,
		stats.BytesUploaded,
		stats.CompletedDownloads,
	)

	if err != nil {
		return fmt.Errorf("failed to record stats: %w", err)
	}

	return nil
}

// GetStats retrieves statistics for a torrent
func (r *TorrentStatsRepository) GetStats(ctx context.Context, torrentID uuid.UUID, since time.Time) ([]*domain.TorrentStats, error) {
	var stats []*domain.TorrentStats
	query := `
		SELECT
			id,
			torrent_id AS video_torrent_id,
			hour,
			seeders_avg + leechers_avg AS total_peers,
			seeders_avg AS total_seeds,
			downloaded_bytes AS bytes_downloaded,
			uploaded_bytes AS bytes_uploaded,
			completed_count AS completed_downloads,
			hour AS created_at
		FROM torrent_stats
		WHERE torrent_id = $1 AND hour >= $2
		ORDER BY hour DESC`

	err := r.db.SelectContext(ctx, &stats, query, torrentID, since)
	if err != nil {
		return nil, fmt.Errorf("failed to get stats: %w", err)
	}

	return stats, nil
}

// GetGlobalStats retrieves global torrent statistics
func (r *TorrentStatsRepository) GetGlobalStats(ctx context.Context) (map[string]interface{}, error) {
	query := `
		SELECT
			COUNT(DISTINCT vt.id) AS total_torrents,
			COUNT(DISTINCT vt.id) FILTER (WHERE vt.is_seeding) AS active_torrents,
			SUM(vt.seeders) AS total_seeders,
			SUM(vt.leechers) AS total_leechers,
			SUM(vt.completed_downloads) AS total_downloads,
			SUM(vt.total_size_bytes) AS total_size_bytes,
			COUNT(DISTINCT tp.peer_id) AS unique_peers
		FROM video_torrents vt
		LEFT JOIN torrent_peers tp ON vt.info_hash = tp.info_hash
			AND tp.last_announce_at > NOW() - INTERVAL '30 minutes'`

	var stats struct {
		TotalTorrents  int64         `db:"total_torrents"`
		ActiveTorrents int64         `db:"active_torrents"`
		TotalSeeders   sql.NullInt64 `db:"total_seeders"`
		TotalLeechers  sql.NullInt64 `db:"total_leechers"`
		TotalDownloads sql.NullInt64 `db:"total_downloads"`
		TotalSizeBytes sql.NullInt64 `db:"total_size_bytes"`
		UniquePeers    int64         `db:"unique_peers"`
	}

	err := r.db.GetContext(ctx, &stats, query)
	if err != nil {
		return nil, fmt.Errorf("failed to get global stats: %w", err)
	}

	return map[string]interface{}{
		"total_torrents":   stats.TotalTorrents,
		"active_torrents":  stats.ActiveTorrents,
		"total_seeders":    stats.TotalSeeders.Int64,
		"total_leechers":   stats.TotalLeechers.Int64,
		"total_downloads":  stats.TotalDownloads.Int64,
		"total_size_bytes": stats.TotalSizeBytes.Int64,
		"unique_peers":     stats.UniquePeers,
	}, nil
}
