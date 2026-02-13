package upload

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

type mockUploadRepo struct{ mock.Mock }

func (m *mockUploadRepo) CreateSession(ctx context.Context, session *domain.UploadSession) error {
	return m.Called(ctx, session).Error(0)
}
func (m *mockUploadRepo) GetSession(ctx context.Context, sessionID string) (*domain.UploadSession, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.UploadSession), args.Error(1)
}
func (m *mockUploadRepo) UpdateSession(ctx context.Context, session *domain.UploadSession) error {
	return m.Called(ctx, session).Error(0)
}
func (m *mockUploadRepo) DeleteSession(ctx context.Context, sessionID string) error {
	return m.Called(ctx, sessionID).Error(0)
}
func (m *mockUploadRepo) RecordChunk(ctx context.Context, sessionID string, chunkIndex int) error {
	return m.Called(ctx, sessionID, chunkIndex).Error(0)
}
func (m *mockUploadRepo) GetUploadedChunks(ctx context.Context, sessionID string) ([]int, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]int), args.Error(1)
}
func (m *mockUploadRepo) IsChunkUploaded(ctx context.Context, sessionID string, chunkIndex int) (bool, error) {
	args := m.Called(ctx, sessionID, chunkIndex)
	return args.Bool(0), args.Error(1)
}
func (m *mockUploadRepo) ExpireOldSessions(ctx context.Context) error {
	return m.Called(ctx).Error(0)
}
func (m *mockUploadRepo) GetExpiredSessions(ctx context.Context) ([]*domain.UploadSession, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.UploadSession), args.Error(1)
}

type mockEncodingRepo struct{ mock.Mock }

func (m *mockEncodingRepo) CreateJob(ctx context.Context, job *domain.EncodingJob) error {
	return m.Called(ctx, job).Error(0)
}
func (m *mockEncodingRepo) GetJob(ctx context.Context, jobID string) (*domain.EncodingJob, error) {
	args := m.Called(ctx, jobID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EncodingJob), args.Error(1)
}
func (m *mockEncodingRepo) GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EncodingJob), args.Error(1)
}
func (m *mockEncodingRepo) UpdateJob(ctx context.Context, job *domain.EncodingJob) error {
	return m.Called(ctx, job).Error(0)
}
func (m *mockEncodingRepo) DeleteJob(ctx context.Context, jobID string) error {
	return m.Called(ctx, jobID).Error(0)
}
func (m *mockEncodingRepo) GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.EncodingJob), args.Error(1)
}
func (m *mockEncodingRepo) GetNextJob(ctx context.Context) (*domain.EncodingJob, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EncodingJob), args.Error(1)
}
func (m *mockEncodingRepo) UpdateJobStatus(ctx context.Context, jobID string, status domain.EncodingStatus) error {
	return m.Called(ctx, jobID, status).Error(0)
}
func (m *mockEncodingRepo) UpdateJobProgress(ctx context.Context, jobID string, progress int) error {
	return m.Called(ctx, jobID, progress).Error(0)
}
func (m *mockEncodingRepo) SetJobError(ctx context.Context, jobID string, errorMsg string) error {
	return m.Called(ctx, jobID, errorMsg).Error(0)
}
func (m *mockEncodingRepo) GetJobCounts(ctx context.Context) (map[string]int64, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]int64), args.Error(1)
}
func (m *mockEncodingRepo) ResetStaleJobs(ctx context.Context, staleDuration time.Duration) (int64, error) {
	args := m.Called(ctx, staleDuration)
	return args.Get(0).(int64), args.Error(1)
}

type mockVideoRepo struct{ mock.Mock }

func (m *mockVideoRepo) Create(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}
func (m *mockVideoRepo) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	args := m.Called(ctx, ids)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *mockVideoRepo) Update(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}
func (m *mockVideoRepo) Delete(ctx context.Context, id string, userID string) error {
	return m.Called(ctx, id, userID).Error(0)
}
func (m *mockVideoRepo) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *mockVideoRepo) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *mockVideoRepo) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	return m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath).Error(0)
}
func (m *mockVideoRepo) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	return m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath, processedCIDs, thumbnailCID, previewCID).Error(0)
}
func (m *mockVideoRepo) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *mockVideoRepo) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	args := m.Called(ctx, remoteURI)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}

// --- Helper ---

func newTestService(t *testing.T) (Service, *mockUploadRepo, *mockEncodingRepo, *mockVideoRepo, string) {
	t.Helper()
	uploadRepo := new(mockUploadRepo)
	encodingRepo := new(mockEncodingRepo)
	videoRepo := new(mockVideoRepo)
	tmpDir := t.TempDir()
	cfg := &config.Config{
		ValidationTestMode: true,
		LogLevel:           "info",
	}
	svc := NewService(uploadRepo, encodingRepo, videoRepo, tmpDir, cfg)
	return svc, uploadRepo, encodingRepo, videoRepo, tmpDir
}

// --- Tests for validUploadExt ---

func TestValidUploadExt(t *testing.T) {
	tests := []struct {
		ext   string
		valid bool
	}{
		{".mp4", true},
		{".mov", true},
		{".mkv", true},
		{".webm", true},
		{".avi", true},
		{".MP4", true},
		{".txt", false},
		{".exe", false},
		{".html", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.ext, func(t *testing.T) {
			assert.Equal(t, tt.valid, validUploadExt(tt.ext))
		})
	}
}

// --- Tests for validateFilePath ---

func TestValidateFilePath(t *testing.T) {
	tests := []struct {
		name        string
		path        string
		root        string
		expectError bool
	}{
		{"valid path within root", "/storage/web-videos/abc.mp4", "/storage", false},
		{"path traversal attack", "/storage/../etc/passwd", "/storage", true},
		{"relative path within root", "web-videos/abc.mp4", "/storage", false},
		{"empty root allows any", "/any/path/file.mp4", "", false},
		{"double dot in component", "/storage/web-videos/../../etc/passwd", "/storage", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateFilePath(tt.path, tt.root)
			if tt.expectError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// --- Tests for parseAspectRatio ---

func TestParseAspectRatio(t *testing.T) {
	svc := &service{cfg: &config.Config{LogLevel: "info"}}

	tests := []struct {
		name        string
		input       string
		expectRatio float64
		isDefault   bool
	}{
		{"empty string defaults to 16:9", "", 16.0 / 9.0, true},
		{"16:9 colon format", "16:9", 16.0 / 9.0, false},
		{"4:3 colon format", "4:3", 4.0 / 3.0, false},
		{"16/9 slash format", "16/9", 16.0 / 9.0, false},
		{"decimal format", "1.777", 1.777, false},
		{"invalid string defaults", "invalid", 16.0 / 9.0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.parseAspectRatio(tt.input)
			assert.InDelta(t, tt.expectRatio, result.ratio, 0.01)
			assert.Equal(t, tt.isDefault, result.usedDefault)
		})
	}
}

// --- Tests for detectSourceResolution ---

func TestDetectSourceResolution(t *testing.T) {
	svc := &service{cfg: &config.Config{LogLevel: "info"}}

	tests := []struct {
		name     string
		video    *domain.Video
		expected string
	}{
		{
			"uses height when available",
			&domain.Video{Metadata: domain.VideoMetadata{Height: 1080}},
			"1080p",
		},
		{
			"uses width with default AR when no height",
			&domain.Video{Metadata: domain.VideoMetadata{Width: 1920}},
			"1080p",
		},
		{
			"uses width with custom AR",
			&domain.Video{Metadata: domain.VideoMetadata{Width: 1440, AspectRatio: "4:3"}},
			"1080p",
		},
		{
			"defaults to 720p when no metadata",
			&domain.Video{Metadata: domain.VideoMetadata{}},
			domain.DefaultResolution,
		},
		{
			"480p height",
			&domain.Video{Metadata: domain.VideoMetadata{Height: 480}},
			"480p",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := svc.detectSourceResolution(tt.video)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- Tests for InitiateUpload ---

func TestInitiateUpload_Success(t *testing.T) {
	svc, uploadRepo, _, videoRepo, _ := newTestService(t)

	videoRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Video")).Return(nil)
	uploadRepo.On("CreateSession", mock.Anything, mock.AnythingOfType("*domain.UploadSession")).Return(nil)

	req := &domain.InitiateUploadRequest{
		FileName:  "test.mp4",
		FileSize:  10485760, // 10MB
		ChunkSize: 1048576,  // 1MB
	}

	resp, err := svc.InitiateUpload(context.Background(), "user-1", req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.NotEmpty(t, resp.SessionID)
	assert.Equal(t, int64(1048576), resp.ChunkSize)
	assert.Equal(t, 10, resp.TotalChunks)
	videoRepo.AssertExpectations(t)
	uploadRepo.AssertExpectations(t)
}

func TestInitiateUpload_InvalidExtension(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)

	req := &domain.InitiateUploadRequest{
		FileName:  "test.exe",
		FileSize:  1000,
		ChunkSize: 1000,
	}

	resp, err := svc.InitiateUpload(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "INVALID_FILE_EXTENSION")
}

func TestInitiateUpload_FileTooLarge(t *testing.T) {
	svc, _, _, _, _ := newTestService(t)

	req := &domain.InitiateUploadRequest{
		FileName:  "test.mp4",
		FileSize:  11 * 1024 * 1024 * 1024, // 11GB
		ChunkSize: 1048576,
	}

	resp, err := svc.InitiateUpload(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "FILE_TOO_LARGE")
}

// --- Tests for GetUploadStatus ---

func TestGetUploadStatus_Success(t *testing.T) {
	svc, uploadRepo, _, _, _ := newTestService(t)

	session := &domain.UploadSession{
		ID:          "sess-1",
		TotalChunks: 5,
		Status:      domain.UploadStatusActive,
	}

	uploadRepo.On("GetSession", mock.Anything, "sess-1").Return(session, nil)
	uploadRepo.On("GetUploadedChunks", mock.Anything, "sess-1").Return([]int{0, 1, 2}, nil)

	result, err := svc.GetUploadStatus(context.Background(), "sess-1")
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, []int{0, 1, 2}, result.UploadedChunks)
}

func TestGetUploadStatus_NotFound(t *testing.T) {
	svc, uploadRepo, _, _, _ := newTestService(t)

	uploadRepo.On("GetSession", mock.Anything, "nonexistent").Return(nil, domain.ErrNotFound)

	result, err := svc.GetUploadStatus(context.Background(), "nonexistent")
	assert.Error(t, err)
	assert.Nil(t, result)
}

// --- Tests for UploadChunk ---

func TestUploadChunk_Success(t *testing.T) {
	svc, uploadRepo, _, _, tmpDir := newTestService(t)

	chunksDir := filepath.Join(tmpDir, "cache", "uploads", "sess-1", "chunks")
	_ = os.MkdirAll(chunksDir, 0750)

	session := &domain.UploadSession{
		ID:           "sess-1",
		TotalChunks:  3,
		Status:       domain.UploadStatusActive,
		TempFilePath: chunksDir,
		ExpiresAt:    time.Now().Add(1 * time.Hour),
	}

	uploadRepo.On("GetSession", mock.Anything, "sess-1").Return(session, nil)
	uploadRepo.On("IsChunkUploaded", mock.Anything, "sess-1", 0).Return(false, nil)
	uploadRepo.On("RecordChunk", mock.Anything, "sess-1", 0).Return(nil)
	uploadRepo.On("GetUploadedChunks", mock.Anything, "sess-1").Return([]int{0}, nil)

	chunk := &domain.ChunkUpload{
		SessionID:  "sess-1",
		ChunkIndex: 0,
		Data:       []byte("test data"),
		Checksum:   "test", // test mode bypass
	}

	resp, err := svc.UploadChunk(context.Background(), "sess-1", chunk)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Uploaded)
	assert.Equal(t, 0, resp.ChunkIndex)
	assert.Equal(t, []int{1, 2}, resp.RemainingChunks)
}

func TestUploadChunk_SessionNotActive(t *testing.T) {
	svc, uploadRepo, _, _, _ := newTestService(t)

	session := &domain.UploadSession{
		ID:     "sess-1",
		Status: domain.UploadStatusCompleted,
	}

	uploadRepo.On("GetSession", mock.Anything, "sess-1").Return(session, nil)

	chunk := &domain.ChunkUpload{ChunkIndex: 0, Data: []byte("data"), Checksum: "test"}
	resp, err := svc.UploadChunk(context.Background(), "sess-1", chunk)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "INVALID_SESSION")
}

func TestUploadChunk_SessionExpired(t *testing.T) {
	svc, uploadRepo, _, _, _ := newTestService(t)

	session := &domain.UploadSession{
		ID:        "sess-1",
		Status:    domain.UploadStatusActive,
		ExpiresAt: time.Now().Add(-1 * time.Hour),
	}

	uploadRepo.On("GetSession", mock.Anything, "sess-1").Return(session, nil)

	chunk := &domain.ChunkUpload{ChunkIndex: 0, Data: []byte("data"), Checksum: "test"}
	resp, err := svc.UploadChunk(context.Background(), "sess-1", chunk)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "SESSION_EXPIRED")
}

func TestUploadChunk_InvalidIndex(t *testing.T) {
	svc, uploadRepo, _, _, _ := newTestService(t)

	session := &domain.UploadSession{
		ID:          "sess-1",
		TotalChunks: 3,
		Status:      domain.UploadStatusActive,
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	}

	uploadRepo.On("GetSession", mock.Anything, "sess-1").Return(session, nil)

	chunk := &domain.ChunkUpload{ChunkIndex: 5, Data: []byte("data"), Checksum: "test"}
	resp, err := svc.UploadChunk(context.Background(), "sess-1", chunk)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "INVALID_CHUNK_INDEX")
}

func TestUploadChunk_AlreadyUploaded(t *testing.T) {
	svc, uploadRepo, _, _, _ := newTestService(t)

	session := &domain.UploadSession{
		ID:          "sess-1",
		TotalChunks: 3,
		Status:      domain.UploadStatusActive,
		ExpiresAt:   time.Now().Add(1 * time.Hour),
	}

	uploadRepo.On("GetSession", mock.Anything, "sess-1").Return(session, nil)
	uploadRepo.On("IsChunkUploaded", mock.Anything, "sess-1", 0).Return(true, nil)
	uploadRepo.On("GetUploadedChunks", mock.Anything, "sess-1").Return([]int{0}, nil)

	chunk := &domain.ChunkUpload{ChunkIndex: 0, Data: []byte("data"), Checksum: "test"}
	resp, err := svc.UploadChunk(context.Background(), "sess-1", chunk)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.True(t, resp.Uploaded)
}

// --- Tests for CompleteUpload ---

func TestCompleteUpload_IncompleteUpload(t *testing.T) {
	svc, uploadRepo, _, _, _ := newTestService(t)

	session := &domain.UploadSession{
		ID:          "sess-1",
		TotalChunks: 5,
		Status:      domain.UploadStatusActive,
	}

	uploadRepo.On("GetSession", mock.Anything, "sess-1").Return(session, nil)
	uploadRepo.On("GetUploadedChunks", mock.Anything, "sess-1").Return([]int{0, 1, 2}, nil)

	err := svc.CompleteUpload(context.Background(), "sess-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "INCOMPLETE_UPLOAD")
}

func TestCompleteUpload_SessionNotActive(t *testing.T) {
	svc, uploadRepo, _, _, _ := newTestService(t)

	session := &domain.UploadSession{
		ID:     "sess-1",
		Status: domain.UploadStatusCompleted,
	}

	uploadRepo.On("GetSession", mock.Anything, "sess-1").Return(session, nil)

	err := svc.CompleteUpload(context.Background(), "sess-1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID_SESSION")
}

// --- Tests for CleanupTempFiles ---

func TestCleanupTempFiles_Success(t *testing.T) {
	svc, uploadRepo, _, _, tmpDir := newTestService(t)

	// Create temp directory structure
	tempDir := filepath.Join(tmpDir, "cache", "uploads", "sess-1", "chunks")
	_ = os.MkdirAll(tempDir, 0750)
	_ = os.WriteFile(filepath.Join(tempDir, "chunk_0"), []byte("data"), 0600)

	session := &domain.UploadSession{
		ID:           "sess-1",
		TempFilePath: tempDir,
	}

	uploadRepo.On("GetSession", mock.Anything, "sess-1").Return(session, nil)

	err := svc.CleanupTempFiles(context.Background(), "sess-1")
	assert.NoError(t, err)

	// Verify directory was removed
	_, statErr := os.Stat(filepath.Dir(tempDir))
	assert.True(t, os.IsNotExist(statErr))
}

func TestCleanupTempFiles_SessionNotFound(t *testing.T) {
	svc, uploadRepo, _, _, _ := newTestService(t)

	uploadRepo.On("GetSession", mock.Anything, "nonexistent").Return(nil, domain.ErrNotFound)

	err := svc.CleanupTempFiles(context.Background(), "nonexistent")
	assert.Error(t, err)
}
