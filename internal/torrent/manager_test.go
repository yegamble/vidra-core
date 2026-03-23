package torrent

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultManagerConfig_Values(t *testing.T) {
	cfg := DefaultManagerConfig()
	require.NotNil(t, cfg)
	assert.NotEmpty(t, cfg.TorrentDir)
	assert.NotEmpty(t, cfg.DataDir)
	assert.Greater(t, cfg.SeedRatio, 0.0)
	assert.Greater(t, cfg.MinSeeders, 0)
	assert.Greater(t, cfg.MaxActiveTorrents, 0)
	assert.Greater(t, cfg.CleanupInterval, time.Duration(0))
	assert.Greater(t, cfg.StatsInterval, time.Duration(0))
}

func TestDefaultManagerConfig_NetworkDefaults(t *testing.T) {
	cfg := DefaultManagerConfig()
	assert.Greater(t, cfg.MaxConnectionsPerTorrent, 0)
}

func TestManagerMetrics_InitialZeroValues(t *testing.T) {
	m := &ManagerMetrics{}
	assert.Equal(t, int64(0), m.TorrentsAdded)
	assert.Equal(t, int64(0), m.TorrentsRemoved)
	assert.Equal(t, int64(0), m.TorrentsActive)
	assert.Equal(t, int64(0), m.BytesUploaded)
	assert.Equal(t, int64(0), m.BytesDownloaded)
	assert.Equal(t, int64(0), m.PeersConnected)
	assert.Equal(t, int64(0), m.ErrorCount)
}

func TestManagedTorrent_FieldAssignment(t *testing.T) {
	videoID := uuid.New()
	addedAt := time.Now()
	mt := &ManagedTorrent{
		VideoID:      videoID,
		InfoHash:     "abc123def456",
		TorrentPath:  "/var/torrents/abc.torrent",
		DataPath:     "/var/data/abc",
		AddedAt:      addedAt,
		LastActiveAt: addedAt,
		Priority:     5,
	}
	assert.Equal(t, videoID, mt.VideoID)
	assert.Equal(t, "abc123def456", mt.InfoHash)
	assert.Equal(t, "/var/torrents/abc.torrent", mt.TorrentPath)
	assert.Equal(t, 5, mt.Priority)
	assert.Equal(t, addedAt, mt.AddedAt)
}

func TestVideoFile_FieldAssignment(t *testing.T) {
	vf := VideoFile{
		Path: "/videos/test.mp4",
		Size: 1024 * 1024 * 500,
	}
	assert.Equal(t, "/videos/test.mp4", vf.Path)
	assert.Equal(t, int64(1024*1024*500), vf.Size)
}
