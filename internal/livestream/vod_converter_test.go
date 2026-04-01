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

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/port"
)

type MockVideoRepository struct{}

func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	return nil
}

func (m *MockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	return nil, nil
}

func (m *MockVideoRepository) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	return nil, nil
}

func (m *MockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error {
	return nil
}

func (m *MockVideoRepository) Delete(ctx context.Context, id string, userID string) error {
	return nil
}

func (m *MockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (m *MockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (m *MockVideoRepository) UpdateProcessingInfo(ctx context.Context, params port.VideoProcessingParams) error {
	return nil
}

func (m *MockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, params port.VideoProcessingWithCIDsParams) error {
	return nil
}

func (m *MockVideoRepository) Count(ctx context.Context) (int64, error) {
	return 0, nil
}

func (m *MockVideoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	return nil, nil
}

func (m *MockVideoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	return nil
}

func (m *MockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	return []*domain.Video{}, nil
}

func (m *MockVideoRepository) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func TestNewVODConverter(t *testing.T) {
	cfg := &config.Config{
		HLSOutputDir:           "/tmp/test-vod",
		ReplayStorageDir:       "/tmp/test-replays",
		EnableReplayConversion: true,
		ReplayUploadToIPFS:     false,
		ReplayRetentionDays:    30,
		FFmpegPath:             "/usr/bin/ffmpeg",
	}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 3)

	assert.NotNil(t, converter)
	assert.Equal(t, cfg, converter.cfg)
	assert.Equal(t, streamRepo, converter.streamRepo)
	assert.Equal(t, videoRepo, converter.videoRepo)
	assert.Equal(t, 3, converter.workers)
	assert.NotNil(t, converter.jobs)
	assert.NotNil(t, converter.jobQueue)
	assert.Equal(t, 100, cap(converter.jobQueue))
}

func TestNewVODConverter_DefaultWorkers(t *testing.T) {
	cfg := &config.Config{}
	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 0)

	assert.Equal(t, 2, converter.workers)
}

func TestVODConverter_ConvertStreamToVOD_Disabled(t *testing.T) {
	cfg := &config.Config{
		EnableReplayConversion: false,
	}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)

	stream := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
	}

	ctx := context.Background()

	err := converter.ConvertStreamToVOD(ctx, stream)
	assert.NoError(t, err)

	assert.Equal(t, 0, converter.GetActiveJobCount())
}

func TestVODConverter_ConvertStreamToVOD_Success(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test-vod-convert-"+uuid.New().String())
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		HLSOutputDir:           tmpDir,
		ReplayStorageDir:       filepath.Join(tmpDir, "replays"),
		EnableReplayConversion: true,
		FFmpegPath:             "/usr/bin/ffmpeg",
	}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)
	defer converter.Shutdown(context.Background())

	stream := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
	}

	ctx := context.Background()

	err := converter.ConvertStreamToVOD(ctx, stream)
	assert.NoError(t, err)

	assert.Equal(t, 1, converter.GetActiveJobCount())

	job, exists := converter.GetJob(stream.ID)
	assert.True(t, exists)
	assert.NotNil(t, job)
	assert.Equal(t, stream.ID, job.StreamID)
	assert.Equal(t, stream.Title, job.StreamTitle)
}

func TestVODConverter_ConvertStreamToVOD_Duplicate(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test-vod-duplicate-"+uuid.New().String())
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		HLSOutputDir:           tmpDir,
		ReplayStorageDir:       filepath.Join(tmpDir, "replays"),
		EnableReplayConversion: true,
		FFmpegPath:             "/usr/bin/ffmpeg",
	}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)
	defer converter.Shutdown(context.Background())

	stream := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
	}

	ctx := context.Background()

	err := converter.ConvertStreamToVOD(ctx, stream)
	assert.NoError(t, err)

	err = converter.ConvertStreamToVOD(ctx, stream)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "already exists")
}

func TestVODConverter_GetJob(t *testing.T) {
	cfg := &config.Config{
		EnableReplayConversion: true,
	}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)
	defer func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		converter.Shutdown(shutdownCtx)
	}()

	streamID := uuid.New()

	job, exists := converter.GetJob(streamID)
	assert.False(t, exists)
	assert.Nil(t, job)

	converter.jobs[streamID] = &VODConversionJob{
		StreamID: streamID,
		Status:   "pending",
	}

	job, exists = converter.GetJob(streamID)
	assert.True(t, exists)
	assert.NotNil(t, job)
	assert.Equal(t, streamID, job.StreamID)
}

func TestVODConverter_CancelJob(t *testing.T) {
	cfg := &config.Config{
		EnableReplayConversion: true,
	}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)
	defer converter.Shutdown(context.Background())

	streamID := uuid.New()

	err := converter.CancelJob(streamID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not found")

	ctx, cancel := context.WithCancel(context.Background())
	converter.jobs[streamID] = &VODConversionJob{
		StreamID: streamID,
		Status:   "pending",
		Ctx:      ctx,
		Cancel:   cancel,
	}

	err = converter.CancelJob(streamID)
	assert.NoError(t, err)
}

func TestVODConverter_GetActiveJobCount(t *testing.T) {
	cfg := &config.Config{}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)
	defer converter.Shutdown(context.Background())

	assert.Equal(t, 0, converter.GetActiveJobCount())

	converter.jobs[uuid.New()] = &VODConversionJob{Status: "pending"}
	converter.jobs[uuid.New()] = &VODConversionJob{Status: "processing"}

	assert.Equal(t, 2, converter.GetActiveJobCount())
}

func TestVODConverter_GetQueueLength(t *testing.T) {
	cfg := &config.Config{}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := &VODConverter{
		cfg:          cfg,
		streamRepo:   streamRepo,
		videoRepo:    videoRepo,
		logger:       logger,
		jobs:         make(map[uuid.UUID]*VODConversionJob),
		jobQueue:     make(chan *VODConversionJob, 100),
		workers:      0,
		shutdownChan: make(chan struct{}),
	}

	assert.Equal(t, 0, converter.GetQueueLength())

	converter.jobQueue <- &VODConversionJob{StreamID: uuid.New()}
	converter.jobQueue <- &VODConversionJob{StreamID: uuid.New()}

	assert.Equal(t, 2, converter.GetQueueLength())
}

func TestVODConverter_FindBestVariant(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test-vod-variant-"+uuid.New().String())
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{}
	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)
	defer converter.Shutdown(context.Background())

	tests := []struct {
		name             string
		variantsToCreate []string
		expectedVariant  string
		shouldError      bool
	}{
		{
			name:             "1080p available",
			variantsToCreate: []string{"1080p", "720p", "480p"},
			expectedVariant:  "1080p",
			shouldError:      false,
		},
		{
			name:             "Only 720p available",
			variantsToCreate: []string{"720p"},
			expectedVariant:  "720p",
			shouldError:      false,
		},
		{
			name:             "Only 360p available",
			variantsToCreate: []string{"360p"},
			expectedVariant:  "360p",
			shouldError:      false,
		},
		{
			name:             "No variants",
			variantsToCreate: []string{},
			expectedVariant:  "",
			shouldError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testDir := filepath.Join(tmpDir, uuid.New().String())
			os.MkdirAll(testDir, 0750)
			defer os.RemoveAll(testDir)

			for _, variant := range tt.variantsToCreate {
				variantDir := filepath.Join(testDir, variant)
				os.MkdirAll(variantDir, 0750)
				os.WriteFile(filepath.Join(variantDir, "segment_000.ts"), []byte("test"), 0644)
			}

			variant, err := converter.findBestVariant(testDir)

			if tt.shouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tt.expectedVariant, variant)
			}
		})
	}
}

func TestVODConverter_Shutdown(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test-vod-shutdown-"+uuid.New().String())
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		HLSOutputDir:           tmpDir,
		ReplayStorageDir:       filepath.Join(tmpDir, "replays"),
		EnableReplayConversion: true,
		FFmpegPath:             "/bin/sleep",
	}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)

	stream1 := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream 1",
	}

	stream2 := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream 2",
	}

	converter.ConvertStreamToVOD(context.Background(), stream1)
	converter.ConvertStreamToVOD(context.Background(), stream2)

	time.Sleep(200 * time.Millisecond)

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	err := converter.Shutdown(shutdownCtx)
	assert.NoError(t, err)
}

func TestVODConverter_JobStateTransitions(t *testing.T) {
	cfg := &config.Config{}
	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)
	defer converter.Shutdown(context.Background())

	streamID := uuid.New()
	ctx, cancel := context.WithCancel(context.Background())

	job := &VODConversionJob{
		StreamID:    streamID,
		StreamTitle: "Test Stream",
		UserID:      uuid.New(),
		SegmentsDir: "/tmp/test",
		OutputPath:  "/tmp/test.mp4",
		Status:      "pending",
		StartedAt:   time.Now(),
		Ctx:         ctx,
		Cancel:      cancel,
	}

	converter.jobs[streamID] = job

	assert.Equal(t, "pending", job.Status)
	assert.Nil(t, job.CompletedAt)
	assert.Empty(t, job.Error)

	job.Status = "processing"
	assert.Equal(t, "processing", job.Status)

	converter.completeJob(job)
	assert.Equal(t, "completed", job.Status)
	assert.NotNil(t, job.CompletedAt)
	assert.Empty(t, job.Error)

	job2 := &VODConversionJob{
		StreamID: uuid.New(),
		Status:   "processing",
	}
	converter.jobs[job2.StreamID] = job2

	testError := assert.AnError
	converter.failJob(job2, testError)
	assert.Equal(t, "failed", job2.Status)
	assert.NotNil(t, job2.CompletedAt)
	assert.NotEmpty(t, job2.Error)
}

func TestVODConverter_CreateOutputDirectory(t *testing.T) {
	tmpDir := filepath.Join(os.TempDir(), "test-vod-output-"+uuid.New().String())
	defer os.RemoveAll(tmpDir)

	cfg := &config.Config{
		HLSOutputDir:           tmpDir,
		ReplayStorageDir:       filepath.Join(tmpDir, "replays"),
		EnableReplayConversion: true,
		FFmpegPath:             "/usr/bin/ffmpeg",
	}

	streamRepo := &MockLiveStreamRepository{}
	videoRepo := &MockVideoRepository{}
	logger := logrus.New()
	logger.SetOutput(os.Stderr)

	converter := NewVODConverter(cfg, streamRepo, videoRepo, logger, 2)
	defer converter.Shutdown(context.Background())

	stream := &domain.LiveStream{
		ID:        uuid.New(),
		UserID:    uuid.New(),
		ChannelID: uuid.New(),
		Title:     "Test Stream",
	}

	ctx := context.Background()

	err := converter.ConvertStreamToVOD(ctx, stream)
	require.NoError(t, err)

	_, err = os.Stat(cfg.ReplayStorageDir)
	assert.NoError(t, err, "Replay storage directory should be created")
}
