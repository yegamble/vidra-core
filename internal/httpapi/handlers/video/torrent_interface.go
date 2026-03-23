package video

import (
	"context"
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/torrent"

	"github.com/google/uuid"
)

type TorrentManagerInterface interface {
	GetVideoTorrent(ctx context.Context, videoID uuid.UUID) (*domain.VideoTorrent, error)
	GetGlobalStats(ctx context.Context) (map[string]interface{}, error)
}

type TorrentTrackerInterface interface {
	GetStats() torrent.TrackerStats
	GetSwarmInfo(infoHash string) map[string]interface{}
	HandleWebSocket(w http.ResponseWriter, r *http.Request)
}
