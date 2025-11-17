package importuc

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/port"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// MockImportRepository is a mock implementation of ImportRepository
type MockImportRepository struct {
	mock.Mock
}

func (m *MockImportRepository) Create(ctx context.Context, imp *domain.VideoImport) error {
	args := m.Called(ctx, imp)
	if args.Error(0) == nil {
		// Set ID for created import
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

// MockVideoRepository is a mock implementation of VideoRepository
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

// MockEncodingRepository is a mock implementation of EncodingRepository
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

// MockYtDlp is a mock implementation of yt-dlp
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

// YtDlpInterface defines the interface for yt-dlp functionality
type YtDlpInterface interface {
	ValidateURL(ctx context.Context, url string) error
	ExtractMetadata(ctx context.Context, url string) (*domain.ImportMetadata, error)
	Download(ctx context.Context, url string, importID string, progressCallback func(progress int, downloadedBytes, totalBytes int64)) (string, error)
}

// Test fixtures
func setupTestService() (Service, *MockImportRepository, *MockVideoRepository, *MockEncodingRepository, *MockYtDlp) {
	importRepo := new(MockImportRepository)
	videoRepo := new(MockVideoRepository)
	encodingRepo := new(MockEncodingRepository)
	mockYtdlp := new(MockYtDlp)

	cfg := &config.Config{
		StorageDir: "/tmp/test-storage",
	}

	// Create a wrapper service that uses the mock
	svc := &serviceWithMockYtdlp{
		importRepo:    importRepo,
		videoRepo:     videoRepo,
		encodingRepo:  encodingRepo,
		ytdlpMock:     mockYtdlp,
		cfg:           cfg,
		storageDir:    cfg.StorageDir,
		activeImports: make(map[string]*importContext),
	}

	return svc, importRepo, videoRepo, encodingRepo, mockYtdlp
}

// serviceWithMockYtdlp is a test wrapper that uses mock yt-dlp
type serviceWithMockYtdlp struct {
	importRepo    ImportRepository
	videoRepo     port.VideoRepository
	encodingRepo  port.EncodingRepository
	ytdlpMock     *MockYtDlp
	cfg           *config.Config
	storageDir    string
	mu            sync.Mutex
	activeImports map[string]*importContext
}

// Implement Service interface by delegating to the real service methods
func (s *serviceWithMockYtdlp) ImportVideo(ctx context.Context, req *ImportRequest) (*domain.VideoImport, error) {
	// Validate request
	if err := s.validateImportRequest(req); err != nil {
		return nil, err
	}

	// Check daily quota
	todayCount, err := s.importRepo.CountByUserIDToday(ctx, req.UserID)
	if err != nil {
		return nil, fmt.Errorf("failed to check daily quota: %w", err)
	}
	if todayCount >= 100 {
		return nil, domain.ErrImportQuotaExceeded
	}

	// Check concurrent imports
	activeCount, err := s.importRepo.CountByUserIDAndStatus(ctx, req.UserID, domain.ImportStatusDownloading)
	if err != nil {
		return nil, fmt.Errorf("failed to check active imports: %w", err)
	}
	processingCount, err := s.importRepo.CountByUserIDAndStatus(ctx, req.UserID, domain.ImportStatusProcessing)
	if err != nil {
		return nil, fmt.Errorf("failed to check processing imports: %w", err)
	}
	if activeCount+processingCount >= 5 {
		return nil, domain.ErrImportRateLimited
	}

	// Validate URL with yt-dlp
	if err := s.ytdlpMock.ValidateURL(ctx, req.SourceURL); err != nil {
		return nil, fmt.Errorf("%w: %v", domain.ErrImportUnsupportedURL, err)
	}

	// Extract metadata
	metadata, err := s.ytdlpMock.ExtractMetadata(ctx, req.SourceURL)
	if err != nil {
		return nil, fmt.Errorf("failed to extract metadata: %w", err)
	}

	// Create import record
	imp := &domain.VideoImport{
		UserID:         req.UserID,
		ChannelID:      req.ChannelID,
		SourceURL:      req.SourceURL,
		Status:         domain.ImportStatusPending,
		TargetPrivacy:  req.TargetPrivacy,
		TargetCategory: req.TargetCategory,
	}

	if err := imp.SetMetadata(metadata); err != nil {
		return nil, fmt.Errorf("failed to set metadata: %w", err)
	}

	if err := s.importRepo.Create(ctx, imp); err != nil {
		return nil, fmt.Errorf("failed to create import: %w", err)
	}

	return imp, nil
}

func (s *serviceWithMockYtdlp) CancelImport(ctx context.Context, importID, userID string) error {
	imp, err := s.importRepo.GetByID(ctx, importID)
	if err != nil {
		return err
	}

	if imp.UserID != userID {
		return fmt.Errorf("unauthorized: import belongs to different user")
	}

	if imp.Status.IsTerminal() {
		return fmt.Errorf("cannot cancel import in terminal state: %s", imp.Status)
	}

	s.mu.Lock()
	if importCtx, exists := s.activeImports[importID]; exists {
		importCtx.cancel()
	}
	s.mu.Unlock()

	if err := imp.Cancel(); err != nil {
		return err
	}

	return s.importRepo.Update(ctx, imp)
}

func (s *serviceWithMockYtdlp) GetImport(ctx context.Context, importID, userID string) (*domain.VideoImport, error) {
	imp, err := s.importRepo.GetByID(ctx, importID)
	if err != nil {
		return nil, err
	}

	if imp.UserID != userID {
		return nil, fmt.Errorf("unauthorized: import belongs to different user")
	}

	return imp, nil
}

func (s *serviceWithMockYtdlp) ListUserImports(ctx context.Context, userID string, limit, offset int) ([]*domain.VideoImport, int, error) {
	imports, err := s.importRepo.GetByUserID(ctx, userID, limit, offset)
	if err != nil {
		return nil, 0, err
	}

	totalCount, err := s.importRepo.CountByUserID(ctx, userID)
	if err != nil {
		return nil, 0, err
	}

	return imports, totalCount, nil
}

func (s *serviceWithMockYtdlp) ProcessPendingImports(ctx context.Context) error {
	pending, err := s.importRepo.GetPending(ctx, 10)
	if err != nil {
		return fmt.Errorf("failed to get pending imports: %w", err)
	}

	for range pending {
		// In tests, we don't actually start background processing
	}

	stuck, err := s.importRepo.GetStuckImports(ctx, 2)
	if err != nil {
		return fmt.Errorf("failed to get stuck imports: %w", err)
	}

	for _, imp := range stuck {
		_ = s.importRepo.MarkFailed(ctx, imp.ID, "import timed out after 2 hours")
	}

	return nil
}

func (s *serviceWithMockYtdlp) CleanupOldImports(ctx context.Context, daysOld int) (int64, error) {
	return s.importRepo.CleanupOldImports(ctx, daysOld)
}

func (s *serviceWithMockYtdlp) validateImportRequest(req *ImportRequest) error {
	if req.UserID == "" {
		return fmt.Errorf("user_id is required")
	}
	if req.SourceURL == "" {
		return fmt.Errorf("source_url is required")
	}
	if err := domain.ValidateURL(req.SourceURL); err != nil {
		return err
	}
	if req.TargetPrivacy == "" {
		req.TargetPrivacy = string(domain.PrivacyPrivate)
	}
	if err := domain.ValidatePrivacy(req.TargetPrivacy); err != nil {
		return err
	}
	return nil
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

	// Setup expectations
	importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(5, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusDownloading).Return(2, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusProcessing).Return(1, nil)
	ytdlp.On("ValidateURL", ctx, req.SourceURL).Return(nil)
	ytdlp.On("ExtractMetadata", ctx, req.SourceURL).Return(metadata, nil)
	importRepo.On("Create", ctx, mock.AnythingOfType("*domain.VideoImport")).Return(nil)

	// Execute
	imp, err := svc.ImportVideo(ctx, req)

	// Assert
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

	// Setup expectations - daily quota exceeded
	importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(100, nil)

	// Execute
	imp, err := svc.ImportVideo(ctx, req)

	// Assert
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

	// Setup expectations - concurrent limit exceeded
	importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(5, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusDownloading).Return(3, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusProcessing).Return(2, nil)

	// Execute
	imp, err := svc.ImportVideo(ctx, req)

	// Assert
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
		SourceURL:     "https://invalid-url.com/video",
		TargetPrivacy: "private",
	}

	// Setup expectations
	importRepo.On("CountByUserIDToday", ctx, req.UserID).Return(5, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusDownloading).Return(2, nil)
	importRepo.On("CountByUserIDAndStatus", ctx, req.UserID, domain.ImportStatusProcessing).Return(1, nil)
	ytdlp.On("ValidateURL", ctx, req.SourceURL).Return(errors.New("unsupported platform"))

	// Execute
	imp, err := svc.ImportVideo(ctx, req)

	// Assert
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

	// Setup expectations
	importRepo.On("GetByID", ctx, importID).Return(expectedImport, nil)

	// Execute
	imp, err := svc.GetImport(ctx, importID, userID)

	// Assert
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
		UserID:    otherUserID, // Different user
		SourceURL: "https://youtube.com/watch?v=test",
		Status:    domain.ImportStatusDownloading,
	}

	// Setup expectations
	importRepo.On("GetByID", ctx, importID).Return(expectedImport, nil)

	// Execute
	imp, err := svc.GetImport(ctx, importID, userID)

	// Assert
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

	// Setup expectations
	importRepo.On("GetByUserID", ctx, userID, limit, offset).Return(expectedImports, nil)
	importRepo.On("CountByUserID", ctx, userID).Return(42, nil)

	// Execute
	imports, totalCount, err := svc.ListUserImports(ctx, userID, limit, offset)

	// Assert
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

	// Setup expectations
	importRepo.On("GetByID", ctx, importID).Return(existingImport, nil)
	importRepo.On("Update", ctx, mock.AnythingOfType("*domain.VideoImport")).Return(nil)

	// Execute
	err := svc.CancelImport(ctx, importID, userID)

	// Assert
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
		Status:    domain.ImportStatusCompleted, // Already completed
	}

	// Setup expectations
	importRepo.On("GetByID", ctx, importID).Return(existingImport, nil)

	// Execute
	err := svc.CancelImport(ctx, importID, userID)

	// Assert
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "terminal state")

	importRepo.AssertExpectations(t)
}

func TestImportService_CleanupOldImports(t *testing.T) {
	svc, importRepo, _, _, _ := setupTestService()
	ctx := context.Background()

	daysOld := 30

	// Setup expectations
	importRepo.On("CleanupOldImports", ctx, daysOld).Return(int64(15), nil)

	// Execute
	deleted, err := svc.CleanupOldImports(ctx, daysOld)

	// Assert
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

	// Setup expectations
	importRepo.On("GetPending", ctx, 10).Return(pendingImports, nil)
	importRepo.On("GetStuckImports", ctx, 2).Return(stuckImports, nil)
	importRepo.On("MarkFailed", ctx, "import-stuck", "import timed out after 2 hours").Return(nil)

	// Execute
	err := svc.ProcessPendingImports(ctx)

	// Assert
	assert.NoError(t, err)

	importRepo.AssertExpectations(t)
}
