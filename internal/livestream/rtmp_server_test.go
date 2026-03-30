package livestream

import (
	"testing"

	"vidra-core/internal/config"
	"vidra-core/internal/repository"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
)

// Mock repositories for testing
type rtmpMockLiveStreamRepo struct {
	repository.LiveStreamRepository
}

type rtmpMockStreamKeyRepo struct {
	repository.StreamKeyRepository
}

func TestNewRTMPServer(t *testing.T) {
	cfg := &config.Config{
		RTMPHost: "localhost",
		RTMPPort: 1935,
	}
	logger := logrus.New()
	streamRepo := &rtmpMockLiveStreamRepo{}
	streamKeyRepo := &rtmpMockStreamKeyRepo{}
	streamManager := &StreamManager{}
	hlsTranscoder := &HLSTranscoder{}
	vodConverter := &VODConverter{}

	rtmpServer := NewRTMPServer(
		cfg,
		streamRepo,
		streamKeyRepo,
		streamManager,
		hlsTranscoder,
		vodConverter,
		logger,
	)

	assert.NotNil(t, rtmpServer)
	assert.Equal(t, cfg, rtmpServer.cfg)
	assert.Equal(t, streamRepo, rtmpServer.streamRepo)
	assert.Equal(t, streamKeyRepo, rtmpServer.streamKeyRepo)
	assert.Equal(t, streamManager, rtmpServer.streamManager)
	assert.Equal(t, hlsTranscoder, rtmpServer.hlsTranscoder)
	assert.Equal(t, vodConverter, rtmpServer.vodConverter)
	assert.Equal(t, logger, rtmpServer.logger)
	assert.NotNil(t, rtmpServer.activeStreams)
	assert.NotNil(t, rtmpServer.shutdownChan)
	assert.NotNil(t, rtmpServer.server)
}

func TestRTMPServer_GetActiveStreamCount(t *testing.T) {
	s := &RTMPServer{
		activeStreams: make(map[string]*StreamSession),
	}

	assert.Equal(t, 0, s.GetActiveStreamCount())

	s.activeStreams["test"] = &StreamSession{}
	assert.Equal(t, 1, s.GetActiveStreamCount())
}

func TestRTMPServer_GetActiveStreams(t *testing.T) {
	s := &RTMPServer{
		activeStreams: make(map[string]*StreamSession),
	}

	assert.Empty(t, s.GetActiveStreams())

	session := &StreamSession{StreamKey: "test"}
	s.activeStreams["test"] = session

	active := s.GetActiveStreams()
	assert.Len(t, active, 1)
	assert.Equal(t, session, active[0])
}
