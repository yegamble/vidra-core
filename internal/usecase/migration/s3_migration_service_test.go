package migration

import (
	"log/slog"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
	"vidra-core/internal/storage"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// MockStorageBackend is a mock implementation of storage.StorageBackend
type MockStorageBackend struct {
	mock.Mock
}

func (m *MockStorageBackend) Upload(ctx context.Context, key string, data io.Reader, contentType string) error {
	args := m.Called(ctx, key, data, contentType)
	return args.Error(0)
}

func (m *MockStorageBackend) UploadFile(ctx context.Context, key string, localPath string, contentType string) error {
	args := m.Called(ctx, key, localPath, contentType)
	return args.Error(0)
}

func (m *MockStorageBackend) Download(ctx context.Context, key string) (io.ReadCloser, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(io.ReadCloser), args.Error(1)
}

func (m *MockStorageBackend) GetURL(key string) string {
	args := m.Called(key)
	return args.String(0)
}

func (m *MockStorageBackend) GetSignedURL(ctx context.Context, key string, expiration time.Duration) (string, error) {
	args := m.Called(ctx, key, expiration)
	return args.String(0), args.Error(1)
}

func (m *MockStorageBackend) Delete(ctx context.Context, key string) error {
	args := m.Called(ctx, key)
	return args.Error(0)
}

func (m *MockStorageBackend) Exists(ctx context.Context, key string) (bool, error) {
	args := m.Called(ctx, key)
	return args.Bool(0), args.Error(1)
}

func (m *MockStorageBackend) Copy(ctx context.Context, sourceKey, destKey string) error {
	args := m.Called(ctx, sourceKey, destKey)
	return args.Error(0)
}

func (m *MockStorageBackend) GetMetadata(ctx context.Context, key string) (*storage.FileMetadata, error) {
	args := m.Called(ctx, key)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*storage.FileMetadata), args.Error(1)
}

// MockVideoRepository is a mock implementation of VideoRepository
type MockVideoRepository struct {
	mock.Mock
}

func (m *MockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	args := m.Called(ctx, ids)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

func (m *MockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

func (m *MockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) Delete(ctx context.Context, id string, userID string) error {
	args := m.Called(ctx, id, userID)
	return args.Error(0)
}

func (m *MockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, 0, args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) UpdateProcessingInfo(ctx context.Context, params port.VideoProcessingParams) error {
	args := m.Called(ctx, params)
	return args.Error(0)
}

func (m *MockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, params port.VideoProcessingWithCIDsParams) error {
	args := m.Called(ctx, params)
	return args.Error(0)
}

func (m *MockVideoRepository) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockVideoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	args := m.Called(ctx, remoteURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

func TestMigrateVideo_Success(t *testing.T) {
	// Setup
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	// Create temporary test files
	tmpDir := t.TempDir()
	mockPaths := storage.NewPaths(tmpDir)
	testFilePath := filepath.Join(tmpDir, "test-video.mp4")
	err := os.WriteFile(testFilePath, []byte("test video content"), 0644)
	require.NoError(t, err)

	// Setup test video
	video := &domain.Video{
		ID:          videoID,
		Title:       "Test Video",
		StorageTier: "hot",
		OutputPaths: map[string]string{
			"1080p": testFilePath,
		},
	}

	// Mock expectations
	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockS3.On("UploadFile", ctx, mock.Anything, testFilePath, "video/mp4").Return(nil)
	mockS3.On("GetURL", mock.Anything).Return("https://s3.example.com/videos/test-video-123/1080p.mp4")
	mockVideoRepo.On("Update", ctx, mock.MatchedBy(func(v *domain.Video) bool {
		return v.StorageTier == "cold" && v.S3MigratedAt != nil
	})).Return(nil)

	// Create service
	logger := slog.Default()

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      logger,
		DeleteLocal: false,
	})

	// Execute
	err = service.MigrateVideo(ctx, videoID)

	// Assert
	require.NoError(t, err)
	mockVideoRepo.AssertExpectations(t)
	mockS3.AssertExpectations(t)
}

func TestMigrateVideo_AlreadyMigrated(t *testing.T) {
	// Setup
	ctx := context.Background()
	videoID := "test-video-123"
	migrationTime := time.Now()

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)
	mockPaths := storage.NewPaths(t.TempDir())

	video := &domain.Video{
		ID:           videoID,
		StorageTier:  "cold",
		S3MigratedAt: &migrationTime,
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	// Execute
	err := service.MigrateVideo(ctx, videoID)

	// Assert - should return early without uploading
	require.NoError(t, err)
	mockS3.AssertNotCalled(t, "UploadFile")
	mockVideoRepo.AssertExpectations(t)
}

func TestMigrateVideo_FileNotFound(t *testing.T) {
	// Setup
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)
	mockPaths := storage.NewPaths(t.TempDir())

	video := &domain.Video{
		ID:          videoID,
		StorageTier: "hot",
		OutputPaths: map[string]string{
			"1080p": "/non/existent/file.mp4",
		},
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockVideoRepo.On("Update", ctx, mock.Anything).Return(nil)

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	// Execute - should skip missing files but still update video record
	err := service.MigrateVideo(ctx, videoID)

	// Assert
	require.NoError(t, err)
	mockS3.AssertNotCalled(t, "UploadFile")
}

func TestMigrateVideo_S3UploadFailure(t *testing.T) {
	// Setup
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()
	mockPaths := storage.NewPaths(tmpDir)
	testFilePath := filepath.Join(tmpDir, "test-video.mp4")
	err := os.WriteFile(testFilePath, []byte("test"), 0644)
	require.NoError(t, err)

	video := &domain.Video{
		ID:          videoID,
		StorageTier: "hot",
		OutputPaths: map[string]string{
			"1080p": testFilePath,
		},
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockS3.On("UploadFile", ctx, mock.Anything, testFilePath, "video/mp4").
		Return(errors.New("S3 upload failed"))

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	// Execute
	err = service.MigrateVideo(ctx, videoID)

	// Assert
	require.Error(t, err)
	assert.Contains(t, err.Error(), "S3 upload failed")
	mockVideoRepo.AssertNotCalled(t, "Update")
}

func TestMigrateVideo_WithLocalDeletion(t *testing.T) {
	// Setup
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()
	mockPaths := storage.NewPaths(tmpDir)
	testFilePath := filepath.Join(tmpDir, "test-video.mp4")
	err := os.WriteFile(testFilePath, []byte("test video content"), 0644)
	require.NoError(t, err)

	video := &domain.Video{
		ID:          videoID,
		StorageTier: "hot",
		OutputPaths: map[string]string{
			"1080p": testFilePath,
		},
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockS3.On("UploadFile", ctx, mock.Anything, testFilePath, "video/mp4").Return(nil)
	mockS3.On("GetURL", mock.Anything).Return("https://s3.example.com/video.mp4")

	// Expect two Update calls: one after S3 upload, one after local deletion
	mockVideoRepo.On("Update", ctx, mock.MatchedBy(func(v *domain.Video) bool {
		return v.StorageTier == "cold" && v.S3MigratedAt != nil
	})).Return(nil).Twice()

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      slog.Default(),
		DeleteLocal: true,
	})

	// Execute
	err = service.MigrateVideo(ctx, videoID)

	// Assert
	require.NoError(t, err)

	// Verify file was deleted
	_, err = os.Stat(testFilePath)
	assert.True(t, os.IsNotExist(err), "Local file should be deleted")

	mockVideoRepo.AssertExpectations(t)
	mockS3.AssertExpectations(t)
}

func TestMigrateBatch_Success(t *testing.T) {
	// Setup
	ctx := context.Background()

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()
	mockPaths := storage.NewPaths(tmpDir)
	testFile1 := filepath.Join(tmpDir, "video1.mp4")
	testFile2 := filepath.Join(tmpDir, "video2.mp4")

	err := os.WriteFile(testFile1, []byte("video1"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(testFile2, []byte("video2"), 0644)
	require.NoError(t, err)

	videos := []*domain.Video{
		{
			ID:          "video-1",
			StorageTier: "hot",
			OutputPaths: map[string]string{"720p": testFile1},
		},
		{
			ID:          "video-2",
			StorageTier: "hot",
			OutputPaths: map[string]string{"720p": testFile2},
		},
	}

	mockVideoRepo.On("GetVideosForMigration", ctx, 10).Return(videos, nil)

	for _, video := range videos {
		mockVideoRepo.On("GetByID", ctx, video.ID).Return(video, nil)
		mockS3.On("UploadFile", ctx, mock.Anything, mock.Anything, "video/mp4").Return(nil)
		mockS3.On("GetURL", mock.Anything).Return("https://s3.example.com/" + video.ID)
		mockVideoRepo.On("Update", ctx, mock.Anything).Return(nil)
	}

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	// Execute
	count, err := service.MigrateBatch(ctx, 10)

	// Assert
	require.NoError(t, err)
	assert.Equal(t, 2, count)
	mockVideoRepo.AssertExpectations(t)
	mockS3.AssertExpectations(t)
}

func TestMigrateBatch_PartialFailure(t *testing.T) {
	// Setup
	ctx := context.Background()

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()
	mockPaths := storage.NewPaths(tmpDir)
	testFile1 := filepath.Join(tmpDir, "video1.mp4")
	testFile2 := filepath.Join(tmpDir, "video2.mp4")

	err := os.WriteFile(testFile1, []byte("video1"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(testFile2, []byte("video2"), 0644)
	require.NoError(t, err)

	videos := []*domain.Video{
		{ID: "video-1", StorageTier: "hot", OutputPaths: map[string]string{"720p": testFile1}},
		{ID: "video-2", StorageTier: "hot", OutputPaths: map[string]string{"720p": testFile2}},
	}

	mockVideoRepo.On("GetVideosForMigration", ctx, 10).Return(videos, nil)

	// First video succeeds
	mockVideoRepo.On("GetByID", ctx, "video-1").Return(videos[0], nil)
	mockS3.On("UploadFile", ctx, mock.Anything, testFile1, "video/mp4").Return(nil).Once()
	mockS3.On("GetURL", mock.Anything).Return("https://s3.example.com/video-1").Once()
	mockVideoRepo.On("Update", ctx, mock.MatchedBy(func(v *domain.Video) bool {
		return v.ID == "video-1"
	})).Return(nil).Once()

	// Second video fails
	mockVideoRepo.On("GetByID", ctx, "video-2").Return(videos[1], nil)
	mockS3.On("UploadFile", ctx, mock.Anything, testFile2, "video/mp4").
		Return(errors.New("S3 error")).Once()

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	// Execute
	count, err := service.MigrateBatch(ctx, 10)

	// Assert - should complete with 1 success despite 1 failure
	require.NoError(t, err)
	assert.Equal(t, 1, count)
	mockVideoRepo.AssertExpectations(t)
}

func TestGenerateS3Key(t *testing.T) {
	service := NewS3MigrationService(Config{
		Logger: slog.Default(),
	})

	tests := []struct {
		name       string
		videoID    string
		variant    string
		localPath  string
		wantPrefix string
		wantSuffix string
	}{
		{
			name:       "MP4 file",
			videoID:    "video-123",
			variant:    "1080p",
			localPath:  "/path/to/video.mp4",
			wantPrefix: "videos/video-123/1080p",
			wantSuffix: ".mp4",
		},
		{
			name:       "WebM file",
			videoID:    "video-456",
			variant:    "720p",
			localPath:  "/path/to/video.webm",
			wantPrefix: "videos/video-456/720p",
			wantSuffix: ".webm",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			key := service.generateS3Key(tt.videoID, tt.variant, tt.localPath)
			assert.Contains(t, key, tt.wantPrefix)
			assert.True(t, filepath.Ext(key) == tt.wantSuffix)
		})
	}
}

func TestNewS3MigrationService_NilLogger(t *testing.T) {
	// When Logger is nil, NewS3MigrationService should create a default logger
	service := NewS3MigrationService(Config{
		S3Backend:   new(MockStorageBackend),
		VideoRepo:   new(MockVideoRepository),
		StoragePath: storage.NewPaths(t.TempDir()),
		Logger:      nil,
		DeleteLocal: false,
	})

	assert.NotNil(t, service)
	assert.NotNil(t, service.logger)
}

func TestMigrateVideo_GetVideoError(t *testing.T) {
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	mockVideoRepo.On("GetByID", ctx, videoID).Return(nil, errors.New("database error"))

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: storage.NewPaths(t.TempDir()),
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	err := service.MigrateVideo(ctx, videoID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get video")
	mockVideoRepo.AssertExpectations(t)
}

func TestMigrateVideo_EmptyOutputPath(t *testing.T) {
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	video := &domain.Video{
		ID:          videoID,
		StorageTier: "hot",
		OutputPaths: map[string]string{
			"1080p": "", // Empty path should be skipped
		},
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockVideoRepo.On("Update", ctx, mock.MatchedBy(func(v *domain.Video) bool {
		return v.StorageTier == "cold" && v.S3MigratedAt != nil
	})).Return(nil)

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: storage.NewPaths(t.TempDir()),
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	err := service.MigrateVideo(ctx, videoID)
	require.NoError(t, err)
	mockS3.AssertNotCalled(t, "UploadFile")
}

func TestMigrateVideo_UpdateError(t *testing.T) {
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()
	testFilePath := filepath.Join(tmpDir, "test-video.mp4")
	err := os.WriteFile(testFilePath, []byte("test"), 0644)
	require.NoError(t, err)

	video := &domain.Video{
		ID:          videoID,
		StorageTier: "hot",
		OutputPaths: map[string]string{
			"1080p": testFilePath,
		},
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockS3.On("UploadFile", ctx, mock.Anything, testFilePath, "video/mp4").Return(nil)
	mockS3.On("GetURL", mock.Anything).Return("https://s3.example.com/video.mp4")
	mockVideoRepo.On("Update", ctx, mock.Anything).Return(errors.New("update failed"))

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: storage.NewPaths(tmpDir),
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	err = service.MigrateVideo(ctx, videoID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update video record")
}

func TestMigrateVideo_WithThumbnailAndPreview(t *testing.T) {
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()
	testFilePath := filepath.Join(tmpDir, "test-video.mp4")
	thumbPath := filepath.Join(tmpDir, "thumb.jpg")
	previewPath := filepath.Join(tmpDir, "preview.webp")

	err := os.WriteFile(testFilePath, []byte("video"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(thumbPath, []byte("thumb"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(previewPath, []byte("preview"), 0644)
	require.NoError(t, err)

	video := &domain.Video{
		ID:            videoID,
		StorageTier:   "hot",
		OutputPaths:   map[string]string{"720p": testFilePath},
		ThumbnailPath: thumbPath,
		PreviewPath:   previewPath,
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockS3.On("UploadFile", ctx, mock.Anything, testFilePath, "video/mp4").Return(nil)
	mockS3.On("UploadFile", ctx, mock.Anything, thumbPath, "image/jpeg").Return(nil)
	mockS3.On("UploadFile", ctx, mock.Anything, previewPath, "image/webp").Return(nil)
	mockS3.On("GetURL", mock.Anything).Return("https://s3.example.com/video.mp4")
	mockVideoRepo.On("Update", ctx, mock.Anything).Return(nil)

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: storage.NewPaths(tmpDir),
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	err = service.MigrateVideo(ctx, videoID)
	require.NoError(t, err)
	mockS3.AssertExpectations(t)
}

func TestMigrateVideo_ThumbnailNotFound(t *testing.T) {
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()

	video := &domain.Video{
		ID:            videoID,
		StorageTier:   "hot",
		OutputPaths:   map[string]string{},
		ThumbnailPath: "/nonexistent/thumb.jpg",
		PreviewPath:   "/nonexistent/preview.webp",
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockVideoRepo.On("Update", ctx, mock.Anything).Return(nil)

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: storage.NewPaths(tmpDir),
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	err := service.MigrateVideo(ctx, videoID)
	require.NoError(t, err)
	// Thumbnail and preview should be skipped since files don't exist
	mockS3.AssertNotCalled(t, "UploadFile")
}

func TestMigrateVideo_WithHLSFiles(t *testing.T) {
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()
	mockPaths := storage.NewPaths(tmpDir)

	// Create HLS directory with files
	hlsDir := mockPaths.HLSVideoDir(videoID)
	err := os.MkdirAll(hlsDir, 0755)
	require.NoError(t, err)

	// Create HLS files
	err = os.WriteFile(filepath.Join(hlsDir, "master.m3u8"), []byte("hls playlist"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(hlsDir, "segment0.ts"), []byte("segment data"), 0644)
	require.NoError(t, err)

	video := &domain.Video{
		ID:          videoID,
		StorageTier: "hot",
		OutputPaths: map[string]string{},
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	// HLS file uploads
	mockS3.On("UploadFile", ctx, mock.MatchedBy(func(key string) bool {
		return true
	}), mock.Anything, mock.Anything).Return(nil)
	mockVideoRepo.On("Update", ctx, mock.Anything).Return(nil)

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	err = service.MigrateVideo(ctx, videoID)
	require.NoError(t, err)
	mockS3.AssertExpectations(t)
}

func TestMigrateVideo_HLSUploadFailure(t *testing.T) {
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()
	mockPaths := storage.NewPaths(tmpDir)

	// Create HLS directory with a file
	hlsDir := mockPaths.HLSVideoDir(videoID)
	err := os.MkdirAll(hlsDir, 0755)
	require.NoError(t, err)
	err = os.WriteFile(filepath.Join(hlsDir, "master.m3u8"), []byte("hls"), 0644)
	require.NoError(t, err)

	video := &domain.Video{
		ID:          videoID,
		StorageTier: "hot",
		OutputPaths: map[string]string{},
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockS3.On("UploadFile", ctx, mock.Anything, mock.Anything, mock.Anything).Return(errors.New("HLS upload error"))

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	err = service.MigrateVideo(ctx, videoID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to migrate HLS files")
}

func TestDeleteLocalFiles_AllSuccess(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "file1.mp4")
	file2 := filepath.Join(tmpDir, "file2.mp4")
	err := os.WriteFile(file1, []byte("test1"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(file2, []byte("test2"), 0644)
	require.NoError(t, err)

	service := NewS3MigrationService(Config{
		Logger: slog.Default(),
	})

	err = service.deleteLocalFiles([]string{file1, file2})
	assert.NoError(t, err)

	// Verify files are deleted
	_, err = os.Stat(file1)
	assert.True(t, os.IsNotExist(err))
	_, err = os.Stat(file2)
	assert.True(t, os.IsNotExist(err))
}

func TestDeleteLocalFiles_PartialFailure(t *testing.T) {
	tmpDir := t.TempDir()

	file1 := filepath.Join(tmpDir, "file1.mp4")
	err := os.WriteFile(file1, []byte("test1"), 0644)
	require.NoError(t, err)

	service := NewS3MigrationService(Config{
		Logger: slog.Default(),
	})

	// Second file doesn't exist, should cause partial failure
	err = service.deleteLocalFiles([]string{file1, "/nonexistent/file.mp4"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete some files")

	// First file should still be deleted
	_, err = os.Stat(file1)
	assert.True(t, os.IsNotExist(err))
}

func TestMigrateBatch_GetVideosError(t *testing.T) {
	ctx := context.Background()

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	mockVideoRepo.On("GetVideosForMigration", ctx, 10).Return(nil, errors.New("db error"))

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: storage.NewPaths(t.TempDir()),
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	count, err := service.MigrateBatch(ctx, 10)
	require.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "failed to get videos for migration")
}

func TestMigrateBatch_EmptyList(t *testing.T) {
	ctx := context.Background()

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	mockVideoRepo.On("GetVideosForMigration", ctx, 10).Return([]*domain.Video{}, nil)

	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: storage.NewPaths(t.TempDir()),
		Logger:      slog.Default(),
		DeleteLocal: false,
	})

	count, err := service.MigrateBatch(ctx, 10)
	require.NoError(t, err)
	assert.Equal(t, 0, count)
}

func TestMigrateVideo_WithLocalDeletion_DeleteError(t *testing.T) {
	ctx := context.Background()
	videoID := "test-video-123"

	mockS3 := new(MockStorageBackend)
	mockVideoRepo := new(MockVideoRepository)

	tmpDir := t.TempDir()
	mockPaths := storage.NewPaths(tmpDir)
	testFilePath := filepath.Join(tmpDir, "test-video.mp4")
	err := os.WriteFile(testFilePath, []byte("test video content"), 0644)
	require.NoError(t, err)

	video := &domain.Video{
		ID:          videoID,
		StorageTier: "hot",
		OutputPaths: map[string]string{
			"1080p": testFilePath,
		},
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(video, nil)
	mockS3.On("UploadFile", ctx, mock.Anything, testFilePath, "video/mp4").Return(nil)
	mockS3.On("GetURL", mock.Anything).Return("https://s3.example.com/video.mp4")
	mockVideoRepo.On("Update", ctx, mock.MatchedBy(func(v *domain.Video) bool {
		return v.StorageTier == "cold" && v.S3MigratedAt != nil
	})).Return(nil).Once()

	// Remove the file before deleteLocalFiles is called so it fails
	service := NewS3MigrationService(Config{
		S3Backend:   mockS3,
		VideoRepo:   mockVideoRepo,
		StoragePath: mockPaths,
		Logger:      slog.Default(),
		DeleteLocal: true,
	})

	// Delete file early so deleteLocalFiles will fail (but migration succeeds)
	err = os.Remove(testFilePath)
	require.NoError(t, err)

	err = service.MigrateVideo(ctx, videoID)
	// Should still succeed even though local deletion failed
	require.NoError(t, err)
}

func TestGetContentType(t *testing.T) {
	service := NewS3MigrationService(Config{
		Logger: slog.Default(),
	})

	tests := []struct {
		path     string
		wantType string
	}{
		{"/path/to/video.mp4", "video/mp4"},
		{"/path/to/video.webm", "video/webm"},
		{"/path/to/playlist.m3u8", "application/vnd.apple.mpegurl"},
		{"/path/to/segment.ts", "video/mp2t"},
		{"/path/to/thumb.jpg", "image/jpeg"},
		{"/path/to/thumb.jpeg", "image/jpeg"},
		{"/path/to/thumb.png", "image/png"},
		{"/path/to/preview.webp", "image/webp"},
		{"/path/to/unknown.xyz", "application/octet-stream"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			contentType := service.getContentType(tt.path)
			assert.Equal(t, tt.wantType, contentType)
		})
	}
}
