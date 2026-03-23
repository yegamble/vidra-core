package livestream

import (
	"athena/internal/config"
	"athena/internal/livestream"
	"athena/internal/repository"
)

type LivestreamHandlers struct {
	streamManager     *livestream.StreamManager
	hlsTranscoder     *livestream.HLSTranscoder
	liveStreamRepo    repository.LiveStreamRepository
	streamKeyRepo     repository.StreamKeyRepository
	viewerSessionRepo repository.ViewerSessionRepository
	cfg               *config.Config
}

func NewLivestreamHandlers(
	streamManager *livestream.StreamManager,
	hlsTranscoder *livestream.HLSTranscoder,
	liveStreamRepo repository.LiveStreamRepository,
	streamKeyRepo repository.StreamKeyRepository,
	viewerSessionRepo repository.ViewerSessionRepository,
	cfg *config.Config,
) *LivestreamHandlers {
	return &LivestreamHandlers{
		streamManager:     streamManager,
		hlsTranscoder:     hlsTranscoder,
		liveStreamRepo:    liveStreamRepo,
		streamKeyRepo:     streamKeyRepo,
		viewerSessionRepo: viewerSessionRepo,
		cfg:               cfg,
	}
}
