package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTorrentRepository tests torrent repository operations
func TestTorrentRepository(t *testing.T) {
	t.Run("CreateTorrent", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentRepository(sqlxDB)

		torrent := &domain.VideoTorrent{
			ID:                 uuid.New(),
			VideoID:            uuid.New(),
			InfoHash:           "1234567890abcdef1234567890abcdef12345678",
			TorrentFilePath:    "/torrents/video.torrent",
			MagnetURI:          "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678",
			PieceLength:        262144,
			TotalSizeBytes:     1024000,
			Seeders:            5,
			Leechers:           10,
			CompletedDownloads: 100,
			IsSeeding:          true,
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		}

		mock.ExpectExec("INSERT INTO video_torrents").
			WithArgs(
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
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err = repo.CreateTorrent(context.Background(), torrent)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetTorrentByVideoID", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentRepository(sqlxDB)

		videoID := uuid.New()
		expectedTorrent := domain.VideoTorrent{
			ID:                 uuid.New(),
			VideoID:            videoID,
			InfoHash:           "1234567890abcdef1234567890abcdef12345678",
			TorrentFilePath:    "/torrents/video.torrent",
			MagnetURI:          "magnet:?xt=urn:btih:1234567890abcdef1234567890abcdef12345678",
			PieceLength:        262144,
			TotalSizeBytes:     1024000,
			Seeders:            5,
			Leechers:           10,
			CompletedDownloads: 100,
			IsSeeding:          true,
			CreatedAt:          time.Now(),
			UpdatedAt:          time.Now(),
		}

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "info_hash", "torrent_file_path", "magnet_uri",
			"piece_length", "total_size_bytes", "seeders", "leechers",
			"completed_downloads", "is_seeding", "created_at", "updated_at",
		}).AddRow(
			expectedTorrent.ID,
			expectedTorrent.VideoID,
			expectedTorrent.InfoHash,
			expectedTorrent.TorrentFilePath,
			expectedTorrent.MagnetURI,
			expectedTorrent.PieceLength,
			expectedTorrent.TotalSizeBytes,
			expectedTorrent.Seeders,
			expectedTorrent.Leechers,
			expectedTorrent.CompletedDownloads,
			expectedTorrent.IsSeeding,
			expectedTorrent.CreatedAt,
			expectedTorrent.UpdatedAt,
		)

		mock.ExpectQuery("SELECT .+ FROM video_torrents WHERE video_id").
			WithArgs(videoID).
			WillReturnRows(rows)

		torrent, err := repo.GetTorrentByVideoID(context.Background(), videoID)
		assert.NoError(t, err)
		assert.NotNil(t, torrent)
		assert.Equal(t, expectedTorrent.InfoHash, torrent.InfoHash)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetTorrentByInfoHash", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentRepository(sqlxDB)

		infoHash := "1234567890abcdef1234567890abcdef12345678"
		expectedTorrent := domain.VideoTorrent{
			ID:              uuid.New(),
			VideoID:         uuid.New(),
			InfoHash:        infoHash,
			TorrentFilePath: "/torrents/video.torrent",
			MagnetURI:       "magnet:?xt=urn:btih:" + infoHash,
			PieceLength:     262144,
			TotalSizeBytes:  1024000,
			IsSeeding:       true,
			CreatedAt:       time.Now(),
			UpdatedAt:       time.Now(),
		}

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "info_hash", "torrent_file_path", "magnet_uri",
			"piece_length", "total_size_bytes", "seeders", "leechers",
			"completed_downloads", "is_seeding", "created_at", "updated_at",
		}).AddRow(
			expectedTorrent.ID,
			expectedTorrent.VideoID,
			expectedTorrent.InfoHash,
			expectedTorrent.TorrentFilePath,
			expectedTorrent.MagnetURI,
			expectedTorrent.PieceLength,
			expectedTorrent.TotalSizeBytes,
			0, 0, 0,
			expectedTorrent.IsSeeding,
			expectedTorrent.CreatedAt,
			expectedTorrent.UpdatedAt,
		)

		mock.ExpectQuery("SELECT .+ FROM video_torrents WHERE info_hash").
			WithArgs(infoHash).
			WillReturnRows(rows)

		torrent, err := repo.GetTorrentByInfoHash(context.Background(), infoHash)
		assert.NoError(t, err)
		assert.NotNil(t, torrent)
		assert.Equal(t, infoHash, torrent.InfoHash)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("UpdateTorrentStats", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentRepository(sqlxDB)

		infoHash := "1234567890abcdef1234567890abcdef12345678"
		seeders := 10
		leechers := 20

		mock.ExpectExec("UPDATE video_torrents SET seeders").
			WithArgs(infoHash, seeders, leechers, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err = repo.UpdateTorrentStats(context.Background(), infoHash, seeders, leechers)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("IncrementCompletedDownloads", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentRepository(sqlxDB)

		infoHash := "1234567890abcdef1234567890abcdef12345678"

		mock.ExpectExec("UPDATE video_torrents SET completed_downloads").
			WithArgs(infoHash, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err = repo.IncrementCompletedDownloads(context.Background(), infoHash)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("SetSeedingStatus", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentRepository(sqlxDB)

		videoID := uuid.New()

		mock.ExpectExec("UPDATE video_torrents SET is_seeding").
			WithArgs(videoID, true, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err = repo.SetSeedingStatus(context.Background(), videoID, true)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetActiveTorrents", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentRepository(sqlxDB)

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "info_hash", "torrent_file_path", "magnet_uri",
			"piece_length", "total_size_bytes", "seeders", "leechers",
			"completed_downloads", "is_seeding", "created_at", "updated_at",
		}).
			AddRow(uuid.New(), uuid.New(), "hash1", "/path1", "magnet1",
				262144, 1024000, 5, 10, 100, true, time.Now(), time.Now()).
			AddRow(uuid.New(), uuid.New(), "hash2", "/path2", "magnet2",
				262144, 2048000, 3, 7, 50, true, time.Now(), time.Now())

		mock.ExpectQuery("SELECT .+ FROM video_torrents WHERE is_seeding").
			WillReturnRows(rows)

		torrents, err := repo.GetActiveTorrents(context.Background())
		assert.NoError(t, err)
		assert.Len(t, torrents, 2)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("DeleteTorrent", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentRepository(sqlxDB)

		videoID := uuid.New()

		mock.ExpectExec("DELETE FROM video_torrents WHERE video_id").
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err = repo.DeleteTorrent(context.Background(), videoID)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("DeleteTorrent_NotFound", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentRepository(sqlxDB)

		videoID := uuid.New()

		mock.ExpectExec("DELETE FROM video_torrents WHERE video_id").
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err = repo.DeleteTorrent(context.Background(), videoID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "not found")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestTorrentPeerRepository tests peer repository operations
func TestTorrentPeerRepository(t *testing.T) {
	t.Run("UpsertPeer_Insert", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentPeerRepository(sqlxDB)

		peer := &domain.TorrentPeer{
			ID:              uuid.New(),
			InfoHash:        "1234567890abcdef1234567890abcdef12345678",
			PeerID:          "peer123456789012345678",
			IPAddress:       "192.168.1.100",
			Port:            6881,
			UploadedBytes:   1024000,
			DownloadedBytes: 512000,
			LeftBytes:       512000,
			Event:           "started",
			UserAgent:       "WebTorrent/1.0",
			LastAnnounceAt:  time.Now(),
		}

		mock.ExpectExec("INSERT INTO torrent_peers").
			WithArgs(
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
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err = repo.UpsertPeer(context.Background(), peer)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetPeers", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentPeerRepository(sqlxDB)

		infoHash := "1234567890abcdef1234567890abcdef12345678"
		limit := 50

		rows := sqlmock.NewRows([]string{
			"id", "info_hash", "peer_id", "ip_address", "port",
			"uploaded_bytes", "downloaded_bytes", "left_bytes",
			"event", "user_agent", "last_announce_at",
		}).
			AddRow(uuid.New(), infoHash, "peer1", "192.168.1.100", 6881,
				1024000, 512000, 0, "completed", "WebTorrent", time.Now()).
			AddRow(uuid.New(), infoHash, "peer2", "192.168.1.101", 6882,
				512000, 256000, 768000, "started", "qBittorrent", time.Now())

		mock.ExpectQuery("SELECT .+ FROM torrent_peers WHERE info_hash").
			WithArgs(infoHash, limit).
			WillReturnRows(rows)

		peers, err := repo.GetPeers(context.Background(), infoHash, limit)
		assert.NoError(t, err)
		assert.Len(t, peers, 2)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("RemovePeer", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentPeerRepository(sqlxDB)

		infoHash := "1234567890abcdef1234567890abcdef12345678"
		peerID := "peer123456789012345678"

		mock.ExpectExec("DELETE FROM torrent_peers WHERE info_hash").
			WithArgs(infoHash, peerID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err = repo.RemovePeer(context.Background(), infoHash, peerID)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("CleanupOldPeers", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentPeerRepository(sqlxDB)

		mock.ExpectExec("DELETE FROM torrent_peers WHERE last_announce_at").
			WillReturnResult(sqlmock.NewResult(0, 10))

		err = repo.CleanupOldPeers(context.Background())
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetPeerStats", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentPeerRepository(sqlxDB)

		infoHash := "1234567890abcdef1234567890abcdef12345678"

		rows := sqlmock.NewRows([]string{"seeders", "leechers"}).
			AddRow(5, 10)

		mock.ExpectQuery("SELECT .+ FROM torrent_peers WHERE info_hash").
			WithArgs(infoHash).
			WillReturnRows(rows)

		seeders, leechers, err := repo.GetPeerStats(context.Background(), infoHash)
		assert.NoError(t, err)
		assert.Equal(t, 5, seeders)
		assert.Equal(t, 10, leechers)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestTorrentTrackerRepository tests tracker repository operations
func TestTorrentTrackerRepository(t *testing.T) {
	t.Run("GetActiveTrackers", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentTrackerRepository(sqlxDB)

		rows := sqlmock.NewRows([]string{
			"id", "announce_url", "is_websocket", "is_active", "priority", "created_at",
		}).
			AddRow(uuid.New(), "wss://tracker1.com", true, true, 1, time.Now()).
			AddRow(uuid.New(), "wss://tracker2.com", true, true, 2, time.Now())

		mock.ExpectQuery("SELECT .+ FROM torrent_trackers WHERE is_active").
			WillReturnRows(rows)

		trackers, err := repo.GetActiveTrackers(context.Background())
		assert.NoError(t, err)
		assert.Len(t, trackers, 2)
		assert.Equal(t, "wss://tracker1.com", trackers[0].AnnounceURL)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("AddTracker", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentTrackerRepository(sqlxDB)

		tracker := &domain.TorrentTracker{
			ID:          uuid.New(),
			AnnounceURL: "wss://newtracker.com",
			IsWebSocket: true,
			IsActive:    true,
			Priority:    3,
			CreatedAt:   time.Now(),
		}

		mock.ExpectExec("INSERT INTO torrent_trackers").
			WithArgs(
				tracker.ID,
				tracker.AnnounceURL,
				tracker.IsWebSocket,
				tracker.IsActive,
				tracker.Priority,
				tracker.CreatedAt,
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err = repo.AddTracker(context.Background(), tracker)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("SetTrackerActive", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentTrackerRepository(sqlxDB)

		announceURL := "wss://tracker.com"

		mock.ExpectExec("UPDATE torrent_trackers SET is_active").
			WithArgs(announceURL, false).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err = repo.SetTrackerActive(context.Background(), announceURL, false)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestTorrentWebSeedRepository tests web seed repository operations
func TestTorrentWebSeedRepository(t *testing.T) {
	t.Run("AddWebSeed", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentWebSeedRepository(sqlxDB)

		seed := &domain.TorrentWebSeed{
			ID:             uuid.New(),
			VideoTorrentID: uuid.New(),
			URL:            "https://cdn.example.com/videos/",
			Priority:       1,
			IsActive:       true,
			CreatedAt:      time.Now(),
		}

		mock.ExpectExec("INSERT INTO torrent_web_seeds").
			WithArgs(
				seed.ID,
				seed.VideoTorrentID,
				seed.URL,
				seed.Priority,
				seed.IsActive,
				seed.CreatedAt,
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err = repo.AddWebSeed(context.Background(), seed)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetWebSeeds", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentWebSeedRepository(sqlxDB)

		torrentID := uuid.New()

		rows := sqlmock.NewRows([]string{
			"id", "video_torrent_id", "url", "priority", "is_active", "created_at",
		}).
			AddRow(uuid.New(), torrentID, "https://cdn1.com/", 1, true, time.Now()).
			AddRow(uuid.New(), torrentID, "https://cdn2.com/", 2, true, time.Now())

		mock.ExpectQuery("SELECT .+ FROM torrent_web_seeds WHERE torrent_id").
			WithArgs(torrentID).
			WillReturnRows(rows)

		seeds, err := repo.GetWebSeeds(context.Background(), torrentID)
		assert.NoError(t, err)
		assert.Len(t, seeds, 2)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

// TestTorrentStatsRepository tests statistics repository operations
func TestTorrentStatsRepository(t *testing.T) {
	t.Run("RecordStats", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentStatsRepository(sqlxDB)

		stats := &domain.TorrentStats{
			ID:                 uuid.New(),
			VideoTorrentID:     uuid.New(),
			Hour:               time.Now().Truncate(time.Hour),
			TotalPeers:         30,
			TotalSeeds:         10,
			BytesDownloaded:    10240000,
			BytesUploaded:      5120000,
			CompletedDownloads: 5,
		}

		mock.ExpectExec("INSERT INTO torrent_stats").
			WithArgs(
				stats.ID,
				stats.VideoTorrentID,
				stats.Hour,
				stats.TotalSeeds,                  // seeders_avg
				stats.TotalPeers-stats.TotalSeeds, // leechers_avg
				stats.BytesDownloaded,
				stats.BytesUploaded,
				stats.CompletedDownloads,
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err = repo.RecordStats(context.Background(), stats)
		assert.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetStats", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentStatsRepository(sqlxDB)

		torrentID := uuid.New()
		since := time.Now().Add(-24 * time.Hour)

		rows := sqlmock.NewRows([]string{
			"id", "video_torrent_id", "hour", "total_peers", "total_seeds",
			"bytes_downloaded", "bytes_uploaded", "completed_downloads", "created_at",
		}).
			AddRow(uuid.New(), torrentID, time.Now().Add(-2*time.Hour), 30, 10,
				10240000, 5120000, 5, time.Now().Add(-2*time.Hour)).
			AddRow(uuid.New(), torrentID, time.Now().Add(-1*time.Hour), 40, 15,
				20480000, 10240000, 10, time.Now().Add(-1*time.Hour))

		mock.ExpectQuery("SELECT .+ FROM torrent_stats WHERE torrent_id").
			WithArgs(torrentID, since).
			WillReturnRows(rows)

		stats, err := repo.GetStats(context.Background(), torrentID, since)
		assert.NoError(t, err)
		assert.Len(t, stats, 2)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("GetGlobalStats", func(t *testing.T) {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		defer db.Close()

		sqlxDB := sqlx.NewDb(db, "postgres")
		repo := NewTorrentStatsRepository(sqlxDB)

		rows := sqlmock.NewRows([]string{
			"total_torrents", "active_torrents", "total_seeders", "total_leechers",
			"total_downloads", "total_size_bytes", "unique_peers",
		}).AddRow(100, 80, sql.NullInt64{Valid: true, Int64: 500},
			sql.NullInt64{Valid: true, Int64: 1000},
			sql.NullInt64{Valid: true, Int64: 5000},
			sql.NullInt64{Valid: true, Int64: 1024000000},
			50)

		mock.ExpectQuery("SELECT .+ FROM video_torrents").
			WillReturnRows(rows)

		stats, err := repo.GetGlobalStats(context.Background())
		assert.NoError(t, err)
		assert.NotNil(t, stats)
		assert.Equal(t, int64(100), stats["total_torrents"])
		assert.Equal(t, int64(80), stats["active_torrents"])
		assert.Equal(t, int64(500), stats["total_seeders"])
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
