package livestream

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/config"
	"athena/internal/domain"
)

// MockLiveStreamRepository implements a mock for testing
type MockLiveStreamRepository struct{}

func (m *MockLiveStreamRepository) Create(ctx context.Context, stream *domain.LiveStream) error {
	return nil
}

func (m *MockLiveStreamRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
	return &domain.LiveStream{
		ID:        id,
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
		Status:    "live",
		Privacy:   "public",
	}, nil
}

func (m *MockLiveStreamRepository) GetByStreamKey(ctx context.Context, streamKey string) (*domain.LiveStream, error) {
	return &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
		Status:    "waiting",
		Privacy:   "public",
	}, nil
}

func (m *MockLiveStreamRepository) Update(ctx context.Context, stream *domain.LiveStream) error {
	return nil
}

func (m *MockLiveStreamRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	return nil
}

func (m *MockLiveStreamRepository) List(ctx context.Context, filters map[string]interface{}, limit, offset int) ([]*domain.LiveStream, int64, error) {
	return nil, 0, nil
}

func (m *MockLiveStreamRepository) Delete(ctx context.Context, id uuid.UUID) error {
	return nil
}

func (m *MockLiveStreamRepository) CountByChannelID(ctx context.Context, channelID uuid.UUID) (int, error) {
	return 0, nil
}

func (m *MockLiveStreamRepository) GetByChannelID(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}

func (m *MockLiveStreamRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}

func (m *MockLiveStreamRepository) GetActiveStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	return nil, nil
}

func (m *MockLiveStreamRepository) UpdateViewerCount(ctx context.Context, id uuid.UUID, count int) error {
	return nil
}

func (m *MockLiveStreamRepository) EndStream(ctx context.Context, id uuid.UUID) error {
	return nil
}

func TestGetQualityVariants(t *testing.T) {
	variants := GetQualityVariants()

	assert.Len(t, variants, 4, "Should have 4 quality variants")

	// Check that variants are in expected order
	assert.Equal(t, "1080p", variants[0].Name)
	assert.Equal(t, "720p", variants[1].Name)
	assert.Equal(t, "480p", variants[2].Name)
	assert.Equal(t, "360p", variants[3].Name)

	// Validate 1080p settings
	assert.Equal(t, 1920, variants[0].Width)
	assert.Equal(t, 1080, variants[0].Height)
	assert.Equal(t, 5000, variants[0].VideoBitrate)
	assert.Equal(t, 128, variants[0].AudioBitrate)

	// Validate 360p settings
	assert.Equal(t, 640, variants[3].Width)
	assert.Equal(t, 360, variants[3].Height)
	assert.Equal(t, 800, variants[3].VideoBitrate)
}

func TestFilterVariantsByConfig(t *testing.T) {
	tests := []struct {
		name          string
		hlsVariants   string
		expectedCount int
		expectedNames []string
	}{
		{
			name:          "All variants",
			hlsVariants:   "all",
			expectedCount: 4,
			expectedNames: []string{"1080p", "720p", "480p", "360p"},
		},
		{
			name:          "Empty config returns all",
			hlsVariants:   "",
			expectedCount: 4,
			expectedNames: []string{"1080p", "720p", "480p", "360p"},
		},
		{
			name:          "Single variant",
			hlsVariants:   "720p",
			expectedCount: 1,
			expectedNames: []string{"720p"},
		},
		{
			name:          "Multiple variants",
			hlsVariants:   "1080p,480p",
			expectedCount: 2,
			expectedNames: []string{"1080p", "480p"},
		},
		{
			name:          "With spaces",
			hlsVariants:   "1080p, 720p, 360p",
			expectedCount: 3,
			expectedNames: []string{"1080p", "720p", "360p"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &config.Config{
				HLSVariants: tt.hlsVariants,
			}

			variants := FilterVariantsByConfig(cfg)

			assert.Len(t, variants, tt.expectedCount)

			// Check that expected variants are present
			variantNames := make([]string, len(variants))
			for i, v := range variants {
				variantNames[i] = v.Name
			}

			for _, expectedName := range tt.expectedNames {
				assert.Contains(t, variantNames, expectedName)
			}
		})
	}
}

func TestNewHLSTranscoder(t *testing.T) {
	cfg := &config.Config{
		HLSOutputDir:            "/tmp/test-hls",
		LiveHLSSegmentLength:    2,
		LiveHLSWindowSize:       10,
		HLSVariants:             "720p,480p",
		FFmpegPath:              "/usr/bin/ffmpeg",
		FFmpegPreset:            "veryfast",
		FFmpegTune:              "zerolatency",
		MaxConcurrentTranscodes: 5,
	}

	repo := &MockLiveStreamRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	transcoder := NewHLSTranscoder(cfg, repo, logger)

	assert.NotNil(t, transcoder)
	assert.Equal(t, cfg, transcoder.cfg)
	assert.Equal(t, repo, transcoder.streamRepo)
	assert.NotNil(t, transcoder.activeStreams)
	assert.NotNil(t, transcoder.shutdownChan)
}

func TestHLSTranscoder_SessionManagement(t *testing.T) {
	cfg := &config.Config{
		HLSOutputDir:         "/tmp/test-hls-sessions",
		LiveHLSSegmentLength: 2,
		LiveHLSWindowSize:    10,
		HLSVariants:          "720p",
		FFmpegPath:           "/usr/bin/ffmpeg",
		FFmpegPreset:         "veryfast",
		FFmpegTune:           "zerolatency",
	}

	repo := &MockLiveStreamRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	transcoder := NewHLSTranscoder(cfg, repo, logger)

	streamID := uuid.New()

	// Initially, no session exists
	assert.False(t, transcoder.IsTranscoding(streamID))

	session, exists := transcoder.GetSession(streamID)
	assert.False(t, exists)
	assert.Nil(t, session)

	// Active stream count should be 0
	assert.Equal(t, 0, transcoder.GetActiveStreamCount())
}

func TestHLSTranscoder_BuildFFmpegCommand(t *testing.T) {
	cfg := &config.Config{
		HLSOutputDir:         "/tmp/test-hls-cmd",
		LiveHLSSegmentLength: 4,
		LiveHLSWindowSize:    5,
		FFmpegPath:           "/usr/bin/ffmpeg",
		FFmpegPreset:         "fast",
		FFmpegTune:           "zerolatency",
	}

	repo := &MockLiveStreamRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	transcoder := NewHLSTranscoder(cfg, repo, logger)

	variants := []QualityVariant{
		{
			Name:         "720p",
			Width:        1280,
			Height:       720,
			VideoBitrate: 2800,
			AudioBitrate: 128,
			MaxBitrate:   2996,
			BufferSize:   4200,
			Framerate:    30,
		},
	}

	rtmpURL := "rtmp://localhost:1935/test-key"
	outputDir := "/tmp/test-output"

	cmd := transcoder.buildFFmpegCommand(rtmpURL, outputDir, variants)

	assert.NotNil(t, cmd)
	assert.Equal(t, cfg.FFmpegPath, cmd.Path)

	args := cmd.Args[1:] // Skip the command name itself

	// Verify key arguments
	assert.Contains(t, args, "-i")
	assert.Contains(t, args, rtmpURL)
	assert.Contains(t, args, "-preset")
	assert.Contains(t, args, "fast")
	assert.Contains(t, args, "-tune")
	assert.Contains(t, args, "zerolatency")
	assert.Contains(t, args, "-hls_time")
	assert.Contains(t, args, "4")
	assert.Contains(t, args, "-hls_list_size")
	assert.Contains(t, args, "5")
	assert.Contains(t, args, "-master_pl_name")
	assert.Contains(t, args, "master.m3u8")
}

func TestHLSTranscoder_OutputDirectoryCreation(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test-hls-output-"+uuid.New().String())
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		HLSOutputDir:         tmpDir,
		LiveHLSSegmentLength: 2,
		LiveHLSWindowSize:    10,
		HLSVariants:          "720p,480p",
		FFmpegPath:           "/bin/false", // Use /bin/false to prevent actual transcoding
		FFmpegPreset:         "veryfast",
		FFmpegTune:           "zerolatency",
	}

	repo := &MockLiveStreamRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	transcoder := NewHLSTranscoder(cfg, repo, logger)

	stream := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
		Status:    "live",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Start transcoding (will fail quickly with /bin/false)
	err := transcoder.StartTranscoding(ctx, stream, "rtmp://localhost:1935/test")

	// Should not return error immediately (starts in background)
	assert.NoError(t, err)

	// Output directory should be created
	outputDir := filepath.Join(tmpDir, stream.ID.String())
	_, err = os.Stat(outputDir)
	assert.NoError(t, err, "Output directory should be created")

	// Variant directories should be created
	_, err = os.Stat(filepath.Join(outputDir, "720p"))
	assert.NoError(t, err, "720p directory should be created")

	_, err = os.Stat(filepath.Join(outputDir, "480p"))
	assert.NoError(t, err, "480p directory should be created")

	// Wait a bit for process to start and fail
	time.Sleep(500 * time.Millisecond)

	// Stop transcoding
	transcoder.StopTranscoding(stream.ID)
}

func TestHLSTranscoder_DuplicateStart(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test-hls-duplicate-"+uuid.New().String())
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		HLSOutputDir:         tmpDir,
		LiveHLSSegmentLength: 2,
		LiveHLSWindowSize:    10,
		HLSVariants:          "720p",
		FFmpegPath:           "/bin/sleep", // Use sleep to keep process running
		FFmpegPreset:         "veryfast",
		FFmpegTune:           "zerolatency",
	}

	repo := &MockLiveStreamRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	transcoder := NewHLSTranscoder(cfg, repo, logger)

	stream := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
		Status:    "live",
	}

	ctx := context.Background()

	// Start transcoding first time
	err := transcoder.StartTranscoding(ctx, stream, "rtmp://localhost:1935/test")
	assert.NoError(t, err)

	// Try to start again - should fail
	err = transcoder.StartTranscoding(ctx, stream, "rtmp://localhost:1935/test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already being transcoded")

	// Cleanup
	transcoder.StopTranscoding(stream.ID)
	transcoder.Shutdown(context.Background())
}

func TestHLSTranscoder_Shutdown(t *testing.T) {
	t.Skip("Skipping shutdown test - requires platform-specific long-running command")

	tmpDir := filepath.Join(os.TempDir(), "test-hls-shutdown-"+uuid.New().String())
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		HLSOutputDir:         tmpDir,
		LiveHLSSegmentLength: 2,
		LiveHLSWindowSize:    10,
		HLSVariants:          "720p",
		FFmpegPath:           "/bin/cat", // Use cat which will wait for input
		FFmpegPreset:         "veryfast",
		FFmpegTune:           "zerolatency",
	}

	repo := &MockLiveStreamRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	transcoder := NewHLSTranscoder(cfg, repo, logger)

	stream := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
		Status:    "live",
	}

	ctx := context.Background()

	// Start transcoding
	err := transcoder.StartTranscoding(ctx, stream, "rtmp://localhost:1935/test")
	require.NoError(t, err)

	// Wait a bit for process to start
	time.Sleep(200 * time.Millisecond)

	// Should be transcoding
	assert.True(t, transcoder.IsTranscoding(stream.ID))
	assert.Equal(t, 1, transcoder.GetActiveStreamCount())

	// Shutdown with timeout
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err = transcoder.Shutdown(shutdownCtx)
	assert.NoError(t, err)

	// All sessions should be stopped
	assert.Equal(t, 0, transcoder.GetActiveStreamCount())
	assert.False(t, transcoder.IsTranscoding(stream.ID))
}

func TestHLSTranscoder_GetHLSPlaylistURL(t *testing.T) {
	cfg := &config.Config{
		HLSOutputDir: "/tmp/test-hls",
	}

	repo := &MockLiveStreamRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	transcoder := NewHLSTranscoder(cfg, repo, logger)

	streamID := uuid.New()
	url := transcoder.GetHLSPlaylistURL(streamID)

	expectedURL := "/api/v1/streams/" + streamID.String() + "/hls/master.m3u8"
	assert.Equal(t, expectedURL, url)
}

func TestHLSTranscoder_NoVariantsEnabled(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test-hls-novariants-"+uuid.New().String())
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		HLSOutputDir:         tmpDir,
		LiveHLSSegmentLength: 2,
		LiveHLSWindowSize:    10,
		HLSVariants:          "invalid_variant", // No valid variants
		FFmpegPath:           "/usr/bin/ffmpeg",
		FFmpegPreset:         "veryfast",
		FFmpegTune:           "zerolatency",
	}

	repo := &MockLiveStreamRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	transcoder := NewHLSTranscoder(cfg, repo, logger)

	stream := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
		Status:    "live",
	}

	ctx := context.Background()

	// Should fail because no valid variants
	err := transcoder.StartTranscoding(ctx, stream, "rtmp://localhost:1935/test")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "no quality variants enabled")
}
