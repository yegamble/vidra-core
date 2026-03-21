package importuc

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type MockImportRepository struct {
	mock.Mock
}

func (m *MockImportRepository) Create(ctx context.Context, imp *domain.VideoImport) error {
	args := m.Called(ctx, imp)
	if args.Error(0) == nil {
		imp.ID = "test-import-id"
		imp.CreatedAt = time.Now()
		imp.UpdatedAt = time.Now()
	}
	return args.Error(0)
}

func (m *MockImportRepository) GetByID(ctx context.Context, importID string) (*domain.VideoImport, error) {
	args := m.Called(ctx, importID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoImport), args.Error(1)
}

func (m *MockImportRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.VideoImport, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoImport), args.Error(1)
}

func (m *MockImportRepository) CountByUserID(ctx context.Context, userID string) (int, error) {
	args := m.Called(ctx, userID)
	return args.Int(0), args.Error(1)
}

func (m *MockImportRepository) CountByUserIDAndStatus(ctx context.Context, userID string, status domain.ImportStatus) (int, error) {
	args := m.Called(ctx, userID, status)
	return args.Int(0), args.Error(1)
}

func (m *MockImportRepository) CountByUserIDToday(ctx context.Context, userID string) (int, error) {
	args := m.Called(ctx, userID)
	return args.Int(0), args.Error(1)
}

func (m *MockImportRepository) GetPending(ctx context.Context, limit int) ([]*domain.VideoImport, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoImport), args.Error(1)
}

func (m *MockImportRepository) Update(ctx context.Context, imp *domain.VideoImport) error {
	args := m.Called(ctx, imp)
	return args.Error(0)
}

func (m *MockImportRepository) UpdateProgress(ctx context.Context, importID string, progress int, downloadedBytes int64) error {
	args := m.Called(ctx, importID, progress, downloadedBytes)
	return args.Error(0)
}

func (m *MockImportRepository) MarkFailed(ctx context.Context, importID string, errorMessage string) error {
	args := m.Called(ctx, importID, errorMessage)
	return args.Error(0)
}

func (m *MockImportRepository) MarkCompleted(ctx context.Context, importID string, videoID string) error {
	args := m.Called(ctx, importID, videoID)
	return args.Error(0)
}

func (m *MockImportRepository) Delete(ctx context.Context, importID string) error {
	args := m.Called(ctx, importID)
	return args.Error(0)
}

func (m *MockImportRepository) CleanupOldImports(ctx context.Context, daysOld int) (int64, error) {
	args := m.Called(ctx, daysOld)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockImportRepository) GetStuckImports(ctx context.Context, hoursStuck int) ([]*domain.VideoImport, error) {
	args := m.Called(ctx, hoursStuck)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoImport), args.Error(1)
}

type MockVideoRepository struct {
	mock.Mock
}

func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	if args.Error(0) == nil {
		video.ID = "test-video-id"
	}
	return args.Error(0)
}

func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
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

func (m *MockVideoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	args := m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath)
	return args.Error(0)
}

func (m *MockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	args := m.Called(ctx, videoID, status, outputPaths, thumbnailPath, previewPath, processedCIDs, thumbnailCID, previewCID)
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

func (m *MockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

type MockEncodingRepository struct {
	mock.Mock
}

func (m *MockEncodingRepository) CreateJob(ctx context.Context, job *domain.EncodingJob) error {
	args := m.Called(ctx, job)
	return args.Error(0)
}

func (m *MockEncodingRepository) GetJob(ctx context.Context, jobID string) (*domain.EncodingJob, error) {
	args := m.Called(ctx, jobID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) UpdateJob(ctx context.Context, job *domain.EncodingJob) error {
	args := m.Called(ctx, job)
	return args.Error(0)
}

func (m *MockEncodingRepository) DeleteJob(ctx context.Context, jobID string) error {
	args := m.Called(ctx, jobID)
	return args.Error(0)
}

func (m *MockEncodingRepository) GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) GetNextJob(ctx context.Context) (*domain.EncodingJob, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) UpdateJobStatus(ctx context.Context, jobID string, status domain.EncodingStatus) error {
	args := m.Called(ctx, jobID, status)
	return args.Error(0)
}

func (m *MockEncodingRepository) UpdateJobProgress(ctx context.Context, jobID string, progress int) error {
	args := m.Called(ctx, jobID, progress)
	return args.Error(0)
}

func (m *MockEncodingRepository) SetJobError(ctx context.Context, jobID string, errorMsg string) error {
	args := m.Called(ctx, jobID, errorMsg)
	return args.Error(0)
}

func (m *MockEncodingRepository) GetJobCounts(ctx context.Context) (map[string]int64, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]int64), args.Error(1)
}

func (m *MockEncodingRepository) ResetStaleJobs(ctx context.Context, staleDuration time.Duration) (int64, error) {
	args := m.Called(ctx, staleDuration)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockEncodingRepository) GetJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) GetActiveJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.EncodingJob), args.Error(1)
}

type MockYtDlp struct {
	mock.Mock
}

func (m *MockYtDlp) ValidateURL(ctx context.Context, url string) error {
	args := m.Called(ctx, url)
	return args.Error(0)
}

func (m *MockYtDlp) ExtractMetadata(ctx context.Context, url string) (*domain.ImportMetadata, error) {
	args := m.Called(ctx, url)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ImportMetadata), args.Error(1)
}

func (m *MockYtDlp) Download(ctx context.Context, url string, importID string, progressCallback func(progress int, downloadedBytes, totalBytes int64)) (string, error) {
	args := m.Called(ctx, url, importID, progressCallback)
	return args.String(0), args.Error(1)
}

func setupTestService() (Service, *MockImportRepository, *MockVideoRepository, *MockEncodingRepository, *MockYtDlp) {
	importRepo := new(MockImportRepository)
	videoRepo := new(MockVideoRepository)
	encodingRepo := new(MockEncodingRepository)
	mockYtdlp := new(MockYtDlp)

	cfg := &config.Config{
		StorageDir: "/tmp/test-storage",
	}

	svc := NewService(importRepo, videoRepo, encodingRepo, mockYtdlp, cfg, cfg.StorageDir)

	return svc, importRepo, videoRepo, encodingRepo, mockYtdlp
}

func TestImportService_ImportVideo_Success(t *testing.T) {
	svc, importRepo, _, _, ytdlp := setupTestService()
	ctx := context.Background()

	req := &ImportRequest{
		UserID:        "user-123",
		SourceURL:     "https://youtube.com/watch?v=test",
		TargetPrivacy: "private",
	}

	metadata := &domain.ImportMetadata{
		Title:       "Test Video",
		Description: "Test Description",
		Duration:    120,
	}

	importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(5, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusDownloading).Return(2, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusProcessing).Return(1, nil)
	ytdlp.On("ValidateURL", ctx, req.SourceURL).Return(nil)
	ytdlp.On("ExtractMetadata", ctx, req.SourceURL).Return(metadata, nil)
	importRepo.On("Create", ctx, mock.AnythingOfType("*domain.VideoImport")).Return(nil)
	importRepo.On("GetByID", mock.Anything, "test-import-id").Return(nil, errors.New("test")).Maybe()

	imp, err := svc.ImportVideo(ctx, req)

	assert.NoError(t, err)
	assert.NotNil(t, imp)
	assert.Equal(t, "test-import-id", imp.ID)
	assert.Equal(t, req.UserID, imp.UserID)
	assert.Equal(t, req.SourceURL, imp.SourceURL)
	assert.Equal(t, domain.ImportStatusPending, imp.Status)

	importRepo.AssertExpectations(t)
	ytdlp.AssertExpectations(t)
}

func TestImportService_ImportVideo_QuotaExceeded(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	req := &ImportRequest{
		UserID:        "user-123",
		SourceURL:     "https://youtube.com/watch?v=test",
		TargetPrivacy: "private",
	}

	importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(100, nil)

	imp, err := svc.ImportVideo(ctx, req)

	assert.Error(t, err)
	assert.Equal(t, domain.ErrImportQuotaExceeded, err)
	assert.Nil(t, imp)

	importRepo.AssertExpectations(t)
}

func TestImportService_ImportVideo_RateLimited(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	req := &ImportRequest{
		UserID:        "user-123",
		SourceURL:     "https://youtube.com/watch?v=test",
		TargetPrivacy: "private",
	}

	importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(5, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusDownloading).Return(3, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusProcessing).Return(2, nil)

	imp, err := svc.ImportVideo(ctx, req)

	assert.Error(t, err)
	assert.Equal(t, domain.ErrImportRateLimited, err)
	assert.Nil(t, imp)

	importRepo.AssertExpectations(t)
}

func TestImportService_ImportVideo_InvalidURL(t *testing.T) {
	svc, importRepo, _, _, ytdlp := setupTestService()
	ctx := context.Background()

	req := &ImportRequest{
		UserID:        "user-123",
		SourceURL:     "https://youtube.com/watch?v=unsupported123",
		TargetPrivacy: "private",
	}

	importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(5, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusDownloading).Return(2, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusProcessing).Return(1, nil)
	ytdlp.On("ValidateURL", ctx, req.SourceURL).Return(errors.New("unsupported platform"))

	imp, err := svc.ImportVideo(ctx, req)

	assert.Error(t, err)
	assert.Nil(t, imp)

	importRepo.AssertExpectations(t)
	ytdlp.AssertExpectations(t)
}

func TestImportService_GetImport_Success(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	importID := "import-123"
	userID := "user-123"

	expectedImport := &domain.VideoImport{
		ID:        importID,
		UserID:    userID,
		SourceURL: "https://youtube.com/watch?v=test",
		Status:    domain.ImportStatusDownloading,
		Progress:  50,
	}

	importRepo.On("GetByID", ctx, importID).Return(expectedImport, nil)

	imp, err := svc.GetImport(ctx, importID, userID)

	assert.NoError(t, err)
	assert.NotNil(t, imp)
	assert.Equal(t, importID, imp.ID)
	assert.Equal(t, userID, imp.UserID)

	importRepo.AssertExpectations(t)
}

func TestImportService_GetImport_Unauthorized(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	importID := "import-123"
	userID := "user-123"
	otherUserID := "other-user"

	expectedImport := &domain.VideoImport{
		ID:        importID,
		UserID:    otherUserID,
		SourceURL: "https://youtube.com/watch?v=test",
		Status:    domain.ImportStatusDownloading,
	}

	importRepo.On("GetByID", ctx, importID).Return(expectedImport, nil)

	imp, err := svc.GetImport(ctx, importID, userID)

	assert.Error(t, err)
	assert.Nil(t, imp)
	assert.Contains(t, err.Error(), "unauthorized")

	importRepo.AssertExpectations(t)
}

func TestImportService_ListUserImports(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	userID := "user-123"
	limit := 20
	offset := 0

	expectedImports := []*domain.VideoImport{
		{ID: "import-1", UserID: userID, Status: domain.ImportStatusCompleted},
		{ID: "import-2", UserID: userID, Status: domain.ImportStatusDownloading},
	}

	importRepo.On("GetByUserID", ctx, userID, limit, offset).Return(expectedImports, nil)
	importRepo.On("CountByUserID", ctx, userID).Return(42, nil)

	imports, totalCount, err := svc.ListUserImports(ctx, userID, limit, offset)

	assert.NoError(t, err)
	assert.Len(t, imports, 2)
	assert.Equal(t, 42, totalCount)

	importRepo.AssertExpectations(t)
}

func TestImportService_CancelImport_Success(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	importID := "import-123"
	userID := "user-123"

	existingImport := &domain.VideoImport{
		ID:        importID,
		UserID:    userID,
		SourceURL: "https://youtube.com/watch?v=test",
		Status:    domain.ImportStatusDownloading,
	}

	importRepo.On("GetByID", ctx, importID).Return(existingImport, nil)
	importRepo.On("Update", ctx, mock.AnythingOfType("*domain.VideoImport")).Return(nil)

	err := svc.CancelImport(ctx, importID, userID)

	assert.NoError(t, err)

	importRepo.AssertExpectations(t)
}

func TestImportService_CancelImport_AlreadyCompleted(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	importID := "import-123"
	userID := "user-123"

	existingImport := &domain.VideoImport{
		ID:        importID,
		UserID:    userID,
		SourceURL: "https://youtube.com/watch?v=test",
		Status:    domain.ImportStatusCompleted,
	}

	importRepo.On("GetByID", ctx, importID).Return(existingImport, nil)

	err := svc.CancelImport(ctx, importID, userID)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "terminal state")

	importRepo.AssertExpectations(t)
}

func TestImportService_CleanupOldImports(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	daysOld := 30

	importRepo.On("CleanupOldImports", ctx, daysOld).Return(int64(15), nil)

	deleted, err := svc.CleanupOldImports(ctx, daysOld)

	assert.NoError(t, err)
	assert.Equal(t, int64(15), deleted)

	importRepo.AssertExpectations(t)
}

func TestImportService_ProcessPendingImports(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	pendingImports := []*domain.VideoImport{
		{ID: "import-1", UserID: "user-123", Status: domain.ImportStatusPending},
		{ID: "import-2", UserID: "user-456", Status: domain.ImportStatusPending},
	}

	stuckImports := []*domain.VideoImport{
		{ID: "import-stuck", UserID: "user-789", Status: domain.ImportStatusDownloading},
	}

	importRepo.On("GetPending", ctx, 10).Return(pendingImports, nil)
	importRepo.On("GetStuckImports", ctx, 2).Return(stuckImports, nil)
	importRepo.On("MarkFailed", ctx, "import-stuck", "import timed out after 2 hours").Return(nil)
	importRepo.On("GetByID", mock.Anything, "import-1").Return(nil, errors.New("test")).Maybe()
	importRepo.On("GetByID", mock.Anything, "import-2").Return(nil, errors.New("test")).Maybe()

	err := svc.ProcessPendingImports(ctx)

	assert.NoError(t, err)

	importRepo.AssertExpectations(t)
}

func TestImportService_processImport_HappyPath(t *testing.T) {
	importRepo := new(MockImportRepository)
	videoRepo := new(MockVideoRepository)
	encodingRepo := new(MockEncodingRepository)
	mockYtdlp := new(MockYtDlp)

	storageDir := t.TempDir()
	cfg := &config.Config{StorageDir: storageDir}

	svc := NewService(importRepo, videoRepo, encodingRepo, mockYtdlp, cfg, storageDir).(*service)

	ctx := context.Background()
	importID := "proc-import-1"

	downloadedFile := filepath.Join(storageDir, "downloaded", "video.mp4")
	require.NoError(t, os.MkdirAll(filepath.Dir(downloadedFile), 0750))
	require.NoError(t, os.WriteFile(downloadedFile, []byte("fake video content"), 0600))

	metadata := &domain.ImportMetadata{
		Title:       "Test Video",
		Description: "A test",
		Duration:    60,
	}
	metadataJSON, _ := json.Marshal(metadata)

	pendingImport := &domain.VideoImport{
		ID:            importID,
		UserID:        "user-1",
		SourceURL:     "https://youtube.com/watch?v=abc",
		Status:        domain.ImportStatusPending,
		TargetPrivacy: "private",
		Metadata:      metadataJSON,
	}

	importRepo.On("GetByID", mock.Anything, importID).Return(pendingImport, nil)
	importRepo.On("Update", mock.Anything, mock.AnythingOfType("*domain.VideoImport")).Return(nil)
	mockYtdlp.On("Download", mock.Anything, pendingImport.SourceURL, importID, mock.AnythingOfType("func(int, int64, int64)")).Return(downloadedFile, nil)
	videoRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Video")).Return(nil)
	encodingRepo.On("CreateJob", mock.Anything, mock.AnythingOfType("*domain.EncodingJob")).Return(nil)
	importRepo.On("MarkCompleted", mock.Anything, importID, "test-video-id").Return(nil)

	svc.processImport(ctx, importID)

	importRepo.AssertExpectations(t)
	videoRepo.AssertExpectations(t)
	encodingRepo.AssertExpectations(t)
	mockYtdlp.AssertExpectations(t)
}

func TestImportService_processImport_DownloadFails(t *testing.T) {
	importRepo := new(MockImportRepository)
	mockYtdlp := new(MockYtDlp)

	storageDir := t.TempDir()
	cfg := &config.Config{StorageDir: storageDir}

	svc := NewService(importRepo, nil, nil, mockYtdlp, cfg, storageDir).(*service)

	ctx := context.Background()
	importID := "proc-import-2"

	pendingImport := &domain.VideoImport{
		ID:            importID,
		UserID:        "user-1",
		SourceURL:     "https://youtube.com/watch?v=fail",
		Status:        domain.ImportStatusPending,
		TargetPrivacy: "private",
	}

	importRepo.On("GetByID", mock.Anything, importID).Return(pendingImport, nil)
	importRepo.On("Update", mock.Anything, mock.AnythingOfType("*domain.VideoImport")).Return(nil)
	mockYtdlp.On("Download", mock.Anything, pendingImport.SourceURL, importID, mock.AnythingOfType("func(int, int64, int64)")).Return("", errors.New("download error"))
	importRepo.On("MarkFailed", mock.Anything, importID, mock.MatchedBy(func(msg string) bool {
		return len(msg) > 0
	})).Return(nil)

	svc.processImport(ctx, importID)

	importRepo.AssertExpectations(t)
	mockYtdlp.AssertExpectations(t)
}

func TestImportService_processImport_GetByIDFails(t *testing.T) {
	importRepo := new(MockImportRepository)
	mockYtdlp := new(MockYtDlp)

	storageDir := t.TempDir()
	cfg := &config.Config{StorageDir: storageDir}

	svc := NewService(importRepo, nil, nil, mockYtdlp, cfg, storageDir).(*service)

	ctx := context.Background()
	importID := "proc-import-3"

	importRepo.On("GetByID", mock.Anything, importID).Return(nil, errors.New("not found"))

	svc.processImport(ctx, importID)

	importRepo.AssertExpectations(t)
}

func TestImportService_processImport_StartTransitionFails(t *testing.T) {
	importRepo := new(MockImportRepository)
	mockYtdlp := new(MockYtDlp)

	storageDir := t.TempDir()
	cfg := &config.Config{StorageDir: storageDir}

	svc := NewService(importRepo, nil, nil, mockYtdlp, cfg, storageDir).(*service)

	ctx := context.Background()
	importID := "proc-import-4"

	activeImport := &domain.VideoImport{
		ID:            importID,
		UserID:        "user-1",
		SourceURL:     "https://youtube.com/watch?v=test",
		Status:        domain.ImportStatusDownloading,
		TargetPrivacy: "private",
	}

	importRepo.On("GetByID", mock.Anything, importID).Return(activeImport, nil)
	importRepo.On("MarkFailed", mock.Anything, importID, mock.MatchedBy(func(msg string) bool {
		return len(msg) > 0
	})).Return(nil)

	svc.processImport(ctx, importID)

	importRepo.AssertExpectations(t)
}

func TestImportService_downloadVideo_Success(t *testing.T) {
	importRepo := new(MockImportRepository)
	mockYtdlp := new(MockYtDlp)

	storageDir := t.TempDir()
	cfg := &config.Config{StorageDir: storageDir}

	svc := NewService(importRepo, nil, nil, mockYtdlp, cfg, storageDir).(*service)

	ctx := context.Background()
	imp := &domain.VideoImport{
		ID:        "dl-import-1",
		SourceURL: "https://youtube.com/watch?v=test",
	}

	importRepo.On("UpdateProgress", mock.Anything, "dl-import-1", mock.AnythingOfType("int"), mock.AnythingOfType("int64")).Return(nil).Maybe()
	mockYtdlp.On("Download", ctx, imp.SourceURL, imp.ID, mock.AnythingOfType("func(int, int64, int64)")).Return("/tmp/video.mp4", nil)

	path, err := svc.downloadVideo(ctx, imp)

	assert.NoError(t, err)
	assert.Equal(t, "/tmp/video.mp4", path)
	mockYtdlp.AssertExpectations(t)
}

func TestImportService_downloadVideo_Error(t *testing.T) {
	importRepo := new(MockImportRepository)
	mockYtdlp := new(MockYtDlp)

	storageDir := t.TempDir()
	cfg := &config.Config{StorageDir: storageDir}

	svc := NewService(importRepo, nil, nil, mockYtdlp, cfg, storageDir).(*service)

	ctx := context.Background()
	imp := &domain.VideoImport{
		ID:        "dl-import-2",
		SourceURL: "https://youtube.com/watch?v=fail",
	}

	mockYtdlp.On("Download", ctx, imp.SourceURL, imp.ID, mock.AnythingOfType("func(int, int64, int64)")).Return("", errors.New("network timeout"))

	path, err := svc.downloadVideo(ctx, imp)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "download failed")
	assert.Empty(t, path)
	mockYtdlp.AssertExpectations(t)
}

func TestImportService_moveToUploads_Success(t *testing.T) {
	storageDir := t.TempDir()
	cfg := &config.Config{StorageDir: storageDir}

	svc := NewService(nil, nil, nil, nil, cfg, storageDir).(*service)

	srcDir := filepath.Join(storageDir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0750))
	srcFile := filepath.Join(srcDir, "video.mp4")
	require.NoError(t, os.WriteFile(srcFile, []byte("video"), 0600))

	destPath, err := svc.moveToUploads("vid-123", srcFile)

	assert.NoError(t, err)
	assert.NotEmpty(t, destPath)
	assert.Contains(t, destPath, "vid-123")
}

func TestImportService_moveToUploads_NoExtension(t *testing.T) {
	storageDir := t.TempDir()
	cfg := &config.Config{StorageDir: storageDir}

	svc := NewService(nil, nil, nil, nil, cfg, storageDir).(*service)

	srcDir := filepath.Join(storageDir, "src")
	require.NoError(t, os.MkdirAll(srcDir, 0750))
	srcFile := filepath.Join(srcDir, "video")
	require.NoError(t, os.WriteFile(srcFile, []byte("video"), 0600))

	destPath, err := svc.moveToUploads("vid-456", srcFile)

	assert.NoError(t, err)
	assert.Contains(t, destPath, ".mp4")
}

func TestServiceLifecycle_HasManagedContext(t *testing.T) {
	svc := NewService(nil, nil, nil, nil, &config.Config{}, t.TempDir())
	concreteSvc := svc.(*service)

	require.NotNil(t, concreteSvc.ctx)
	require.NotNil(t, concreteSvc.cancel)

	select {
	case <-concreteSvc.ctx.Done():
		t.Fatal("service context should not be cancelled at startup")
	default:
	}

	concreteSvc.cancel()
	select {
	case <-concreteSvc.ctx.Done():
	default:
		t.Fatal("service context should be cancelled after calling cancel()")
	}
}

func TestImportService_downloadVideo_PropagatesContext(t *testing.T) {
	importRepo := new(MockImportRepository)
	mockYtdlp := new(MockYtDlp)

	storageDir := t.TempDir()
	cfg := &config.Config{StorageDir: storageDir}

	svc := NewService(importRepo, nil, nil, mockYtdlp, cfg, storageDir).(*service)

	type ctxKey struct{}
	parentCtx := context.WithValue(context.Background(), ctxKey{}, "sentinel")

	imp := &domain.VideoImport{
		ID:        "dl-ctx-test",
		SourceURL: "https://youtube.com/watch?v=ctx",
	}

	var capturedCtx context.Context
	importRepo.On("UpdateProgress", mock.MatchedBy(func(ctx context.Context) bool {
		capturedCtx = ctx
		return true
	}), "dl-ctx-test", mock.AnythingOfType("int"), mock.AnythingOfType("int64")).Return(nil).Maybe()

	// Download mock invokes the progressCallback so UpdateProgress is called
	mockYtdlp.On("Download", parentCtx, imp.SourceURL, imp.ID, mock.AnythingOfType("func(int, int64, int64)")).
		Run(func(args mock.Arguments) {
			cb := args.Get(3).(func(int, int64, int64))
			cb(50, 512, 1024)
		}).
		Return("/tmp/ctx-video.mp4", nil)

	_, err := svc.downloadVideo(parentCtx, imp)
	require.NoError(t, err)

	require.NotNil(t, capturedCtx, "UpdateProgress was not called")
	assert.Equal(t, "sentinel", capturedCtx.Value(ctxKey{}),
		"UpdateProgress must receive parent context, not context.Background()")
}
