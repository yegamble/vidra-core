package captiongen

import (
	"athena/internal/domain"
	"athena/internal/whisper"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Mock repositories and services

type MockJobRepository struct {
	mock.Mock
}

func (m *MockJobRepository) Create(ctx context.Context, job *domain.CaptionGenerationJob) error {
	args := m.Called(ctx, job)
	return args.Error(0)
}

func (m *MockJobRepository) GetByID(ctx context.Context, jobID uuid.UUID) (*domain.CaptionGenerationJob, error) {
	args := m.Called(ctx, jobID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.CaptionGenerationJob), args.Error(1)
}

func (m *MockJobRepository) GetByVideoID(ctx context.Context, videoID uuid.UUID) ([]domain.CaptionGenerationJob, error) {
	args := m.Called(ctx, videoID)
	return args.Get(0).([]domain.CaptionGenerationJob), args.Error(1)
}

func (m *MockJobRepository) GetNextPendingJob(ctx context.Context) (*domain.CaptionGenerationJob, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.CaptionGenerationJob), args.Error(1)
}

func (m *MockJobRepository) UpdateStatus(ctx context.Context, jobID uuid.UUID, status domain.CaptionGenerationStatus) error {
	args := m.Called(ctx, jobID, status)
	return args.Error(0)
}

func (m *MockJobRepository) UpdateProgress(ctx context.Context, jobID uuid.UUID, progress int) error {
	args := m.Called(ctx, jobID, progress)
	return args.Error(0)
}

func (m *MockJobRepository) MarkCompleted(ctx context.Context, jobID uuid.UUID, captionID uuid.UUID, detectedLanguage string, transcriptionTime int) error {
	args := m.Called(ctx, jobID, captionID, detectedLanguage, transcriptionTime)
	return args.Error(0)
}

func (m *MockJobRepository) MarkFailed(ctx context.Context, jobID uuid.UUID, errorMessage string) error {
	args := m.Called(ctx, jobID, errorMessage)
	return args.Error(0)
}

func (m *MockJobRepository) GetPendingJobs(ctx context.Context, limit int) ([]domain.CaptionGenerationJob, error) {
	args := m.Called(ctx, limit)
	return args.Get(0).([]domain.CaptionGenerationJob), args.Error(1)
}

func (m *MockJobRepository) CountByStatus(ctx context.Context, status domain.CaptionGenerationStatus) (int, error) {
	args := m.Called(ctx, status)
	return args.Int(0), args.Error(1)
}

func (m *MockJobRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit int, offset int) ([]domain.CaptionGenerationJob, error) {
	args := m.Called(ctx, userID, limit, offset)
	return args.Get(0).([]domain.CaptionGenerationJob), args.Error(1)
}

func (m *MockJobRepository) Update(ctx context.Context, job *domain.CaptionGenerationJob) error {
	args := m.Called(ctx, job)
	return args.Error(0)
}

func (m *MockJobRepository) Delete(ctx context.Context, jobID uuid.UUID) error {
	args := m.Called(ctx, jobID)
	return args.Error(0)
}

func (m *MockJobRepository) DeleteOldCompletedJobs(ctx context.Context, daysOld int) (int64, error) {
	args := m.Called(ctx, daysOld)
	return args.Get(0).(int64), args.Error(1)
}

type MockCaptionRepository struct {
	mock.Mock
}

func (m *MockCaptionRepository) Create(ctx context.Context, caption *domain.Caption) error {
	args := m.Called(ctx, caption)
	return args.Error(0)
}

func (m *MockCaptionRepository) GetByID(ctx context.Context, captionID uuid.UUID) (*domain.Caption, error) {
	args := m.Called(ctx, captionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Caption), args.Error(1)
}

func (m *MockCaptionRepository) GetByVideoAndLanguage(ctx context.Context, videoID uuid.UUID, languageCode string) (*domain.Caption, error) {
	args := m.Called(ctx, videoID, languageCode)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Caption), args.Error(1)
}

func (m *MockCaptionRepository) GetByVideoID(ctx context.Context, videoID uuid.UUID) ([]domain.Caption, error) {
	args := m.Called(ctx, videoID)
	return args.Get(0).([]domain.Caption), args.Error(1)
}

func (m *MockCaptionRepository) Update(ctx context.Context, caption *domain.Caption) error {
	args := m.Called(ctx, caption)
	return args.Error(0)
}

func (m *MockCaptionRepository) Delete(ctx context.Context, captionID uuid.UUID) error {
	args := m.Called(ctx, captionID)
	return args.Error(0)
}

func (m *MockCaptionRepository) DeleteByVideoID(ctx context.Context, videoID uuid.UUID) error {
	args := m.Called(ctx, videoID)
	return args.Error(0)
}

func (m *MockCaptionRepository) CountByVideoID(ctx context.Context, videoID uuid.UUID) (int, error) {
	args := m.Called(ctx, videoID)
	return args.Int(0), args.Error(1)
}

type MockVideoRepository struct {
	mock.Mock
}

func (m *MockVideoRepository) GetByID(ctx context.Context, videoID string) (*domain.Video, error) {
	args := m.Called(ctx, videoID)
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

func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

func (m *MockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, userID, limit, offset)
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) Delete(ctx context.Context, id string, userID string) error {
	args := m.Called(ctx, id, userID)
	return args.Error(0)
}

func (m *MockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}

func (m *MockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
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

type MockWhisperClient struct {
	mock.Mock
}

func (m *MockWhisperClient) Transcribe(ctx context.Context, audioPath string, targetLanguage *string) (*whisper.TranscriptionResult, error) {
	args := m.Called(ctx, audioPath, targetLanguage)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*whisper.TranscriptionResult), args.Error(1)
}

func (m *MockWhisperClient) ExtractAudioFromVideo(ctx context.Context, videoPath string, outputPath string) error {
	args := m.Called(ctx, videoPath, outputPath)
	return args.Error(0)
}

func (m *MockWhisperClient) FormatToVTT(result *whisper.TranscriptionResult) (string, error) {
	args := m.Called(result)
	return args.String(0), args.Error(1)
}

func (m *MockWhisperClient) FormatToSRT(result *whisper.TranscriptionResult) (string, error) {
	args := m.Called(result)
	return args.String(0), args.Error(1)
}

func (m *MockWhisperClient) GetProvider() domain.WhisperProvider {
	args := m.Called()
	return args.Get(0).(domain.WhisperProvider)
}

// Tests

func TestRegenerateCaptionWithSpecificLanguage(t *testing.T) {
	// Test that regenerating a caption with a specific language only deletes that language
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	service := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)

	ctx := context.Background()
	videoID := uuid.New()
	userID := uuid.New()
	englishLang := "en"
	captionPath := filepath.Join(tempDir, "caption_en.vtt")

	// Create a fake video file in web-videos directory (required for validation)
	webVideosDir := filepath.Join(tempDir, "web-videos")
	err := os.MkdirAll(webVideosDir, 0750)
	require.NoError(t, err)
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	err = os.WriteFile(videoPath, []byte("fake video content"), 0600)
	require.NoError(t, err)

	// Create a temporary caption file
	err = os.WriteFile(captionPath, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nOld English caption"), 0600)
	require.NoError(t, err)

	// Setup: video exists with English caption
	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	existingEnglishCaption := &domain.Caption{
		ID:           uuid.New(),
		VideoID:      videoID,
		LanguageCode: "en",
		Label:        "English",
		FilePath:     &captionPath,
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(existingEnglishCaption, nil)
	mockCaptionRepo.On("Delete", ctx, existingEnglishCaption.ID).Return(nil)
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)
	mockWhisper.On("GetProvider").Return(domain.WhisperProviderLocal)

	// Execute
	job, err := service.RegenerateCaption(ctx, videoID, userID, &englishLang)

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, job)
	assert.Equal(t, videoID, job.VideoID)
	assert.Equal(t, &englishLang, job.TargetLanguage)

	// Verify old caption file was deleted
	_, err = os.Stat(captionPath)
	assert.True(t, os.IsNotExist(err), "Old caption file should be deleted")

	// Verify mocks were called correctly
	mockCaptionRepo.AssertCalled(t, "GetByVideoAndLanguage", ctx, videoID, "en")
	mockCaptionRepo.AssertCalled(t, "Delete", ctx, existingEnglishCaption.ID)
	mockJobRepo.AssertCalled(t, "Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob"))
}

func TestRegenerateCaptionMultiLanguagePreservation(t *testing.T) {
	// Test that regenerating English caption doesn't affect Spanish caption
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	service := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)

	ctx := context.Background()
	videoID := uuid.New()
	userID := uuid.New()
	englishLang := "en"

	// Create a fake video file in web-videos directory (required for validation)
	webVideosDir := filepath.Join(tempDir, "web-videos")
	err := os.MkdirAll(webVideosDir, 0750)
	require.NoError(t, err)
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	err = os.WriteFile(videoPath, []byte("fake video content"), 0600)
	require.NoError(t, err)

	// Create caption files
	captionPathEN := filepath.Join(tempDir, "caption_en.vtt")
	captionPathES := filepath.Join(tempDir, "caption_es.vtt")

	err = os.WriteFile(captionPathEN, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nOld English"), 0600)
	require.NoError(t, err)
	err = os.WriteFile(captionPathES, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nEspañol antiguo"), 0600)
	require.NoError(t, err)

	// Setup: video has both English and Spanish captions
	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	existingEnglishCaption := &domain.Caption{
		ID:           uuid.New(),
		VideoID:      videoID,
		LanguageCode: "en",
		Label:        "English",
		FilePath:     &captionPathEN,
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(existingEnglishCaption, nil)
	mockCaptionRepo.On("Delete", ctx, existingEnglishCaption.ID).Return(nil)
	// Spanish caption should NOT be queried or deleted
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)
	mockWhisper.On("GetProvider").Return(domain.WhisperProviderLocal)

	// Execute: regenerate English caption
	job, err := service.RegenerateCaption(ctx, videoID, userID, &englishLang)

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, job)

	// Verify English caption file was deleted
	_, err = os.Stat(captionPathEN)
	assert.True(t, os.IsNotExist(err), "English caption file should be deleted")

	// Verify Spanish caption file still exists
	_, err = os.Stat(captionPathES)
	assert.NoError(t, err, "Spanish caption file should still exist")

	// Verify only English caption was affected
	mockCaptionRepo.AssertCalled(t, "GetByVideoAndLanguage", ctx, videoID, "en")
	mockCaptionRepo.AssertCalled(t, "Delete", ctx, existingEnglishCaption.ID)
	mockCaptionRepo.AssertNotCalled(t, "GetByVideoAndLanguage", ctx, videoID, "es")
	mockCaptionRepo.AssertNumberOfCalls(t, "Delete", 1) // Only one delete call
}

func TestRegenerateCaptionAutoDetect(t *testing.T) {
	// Test that auto-detect doesn't delete captions prematurely
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	service := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)

	ctx := context.Background()
	videoID := uuid.New()
	userID := uuid.New()

	// Create a fake video file in web-videos directory (required for validation)
	webVideosDir := filepath.Join(tempDir, "web-videos")
	err := os.MkdirAll(webVideosDir, 0750)
	require.NoError(t, err)
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	err = os.WriteFile(videoPath, []byte("fake video content"), 0600)
	require.NoError(t, err)

	// Setup: video exists
	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	// When auto-detecting, no caption should be deleted before transcription
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)
	mockWhisper.On("GetProvider").Return(domain.WhisperProviderLocal)

	// Execute: regenerate with auto-detect (nil targetLanguage)
	job, err := service.RegenerateCaption(ctx, videoID, userID, nil)

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, job)
	assert.Nil(t, job.TargetLanguage)

	// Verify no caption was deleted (deletion happens after detection in processJob)
	mockCaptionRepo.AssertNotCalled(t, "GetByVideoAndLanguage", mock.Anything, mock.Anything, mock.Anything)
	mockCaptionRepo.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything)
	mockJobRepo.AssertCalled(t, "Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob"))
}

func TestCreateJob(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()

	ctx := context.Background()
	videoID := uuid.New()
	userID := uuid.New()
	targetLang := "en"

	// Create a fake video file in web-videos directory (actual location)
	webVideosDir := filepath.Join(tempDir, "web-videos")
	err := os.MkdirAll(webVideosDir, 0750)
	require.NoError(t, err)
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	err = os.WriteFile(videoPath, []byte("fake video content"), 0600)
	require.NoError(t, err)

	service := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)
	mockWhisper.On("GetProvider").Return(domain.WhisperProviderLocal)

	req := &domain.CreateCaptionGenerationJobRequest{
		VideoID:        videoID,
		TargetLanguage: &targetLang,
		ModelSize:      domain.WhisperModelBase,
		OutputFormat:   domain.CaptionFormatVTT,
	}

	// Execute
	job, err := service.CreateJob(ctx, videoID, userID, req)

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, job)
	assert.Equal(t, videoID, job.VideoID)
	assert.Equal(t, userID, job.UserID)
	assert.Equal(t, &targetLang, job.TargetLanguage)
	assert.Equal(t, domain.CaptionGenStatusPending, job.Status)
	assert.Equal(t, 0, job.Progress)
	assert.Equal(t, domain.WhisperModelBase, job.ModelSize)
	assert.Equal(t, domain.CaptionFormatVTT, job.OutputFormat)

	mockVideoRepo.AssertCalled(t, "GetByID", ctx, videoID.String())
	mockJobRepo.AssertCalled(t, "Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob"))
}

func TestCreateJobVideoNotProcessed(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	service := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)

	ctx := context.Background()
	videoID := uuid.New()
	userID := uuid.New()
	targetLang := "en"

	// Video is still processing
	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusProcessing,
		MimeType: "video/mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)

	req := &domain.CreateCaptionGenerationJobRequest{
		VideoID:        videoID,
		TargetLanguage: &targetLang,
	}

	// Execute
	job, err := service.CreateJob(ctx, videoID, userID, req)

	// Verify
	assert.Error(t, err)
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "must be fully processed")

	mockVideoRepo.AssertCalled(t, "GetByID", ctx, videoID.String())
	mockJobRepo.AssertNotCalled(t, "Create", mock.Anything, mock.Anything)
}

func TestGetJobStatus(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	service := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)

	ctx := context.Background()
	jobID := uuid.New()

	mockJob := &domain.CaptionGenerationJob{
		ID:       jobID,
		VideoID:  uuid.New(),
		UserID:   uuid.New(),
		Status:   domain.CaptionGenStatusProcessing,
		Progress: 50,
	}

	mockJobRepo.On("GetByID", ctx, jobID).Return(mockJob, nil)

	// Execute
	job, err := service.GetJobStatus(ctx, jobID)

	// Verify
	assert.NoError(t, err)
	assert.NotNil(t, job)
	assert.Equal(t, jobID, job.ID)
	assert.Equal(t, domain.CaptionGenStatusProcessing, job.Status)
	assert.Equal(t, 50, job.Progress)

	mockJobRepo.AssertCalled(t, "GetByID", ctx, jobID)
}

func TestGetJobsByVideo(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	service := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)

	ctx := context.Background()
	videoID := uuid.New()

	mockJobs := []domain.CaptionGenerationJob{
		{
			ID:      uuid.New(),
			VideoID: videoID,
			Status:  domain.CaptionGenStatusCompleted,
		},
		{
			ID:      uuid.New(),
			VideoID: videoID,
			Status:  domain.CaptionGenStatusProcessing,
		},
	}

	mockJobRepo.On("GetByVideoID", ctx, videoID).Return(mockJobs, nil)

	// Execute
	jobs, err := service.GetJobsByVideo(ctx, videoID)

	// Verify
	assert.NoError(t, err)
	assert.Len(t, jobs, 2)
	assert.Equal(t, domain.CaptionGenStatusCompleted, jobs[0].Status)
	assert.Equal(t, domain.CaptionGenStatusProcessing, jobs[1].Status)

	mockJobRepo.AssertCalled(t, "GetByVideoID", ctx, videoID)
}

func TestGetLanguageLabel(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{"en", "English"},
		{"es", "Spanish"},
		{"fr", "French"},
		{"de", "German"},
		{"ja", "Japanese"},
		{"zh", "Chinese"},
		{"xyz", "XYZ"}, // Unknown code should be capitalized
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			result := getLanguageLabel(tt.code)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- NewService tests ---

func TestNewService(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, "/tmp/uploads")

	assert.NotNil(t, svc, "NewService should return a non-nil Service")

	// Verify it implements the Service interface
	var _ Service = svc
}

// --- ProcessNext tests ---

func TestProcessNext_NoPendingJobs(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())
	ctx := context.Background()

	mockJobRepo.On("GetNextPendingJob", ctx).Return(nil, nil)

	processed, err := svc.ProcessNext(ctx)

	assert.NoError(t, err)
	assert.False(t, processed, "Should return false when no pending jobs exist")
	mockJobRepo.AssertCalled(t, "GetNextPendingJob", ctx)
}

func TestProcessNext_GetNextPendingJobError(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())
	ctx := context.Background()

	mockJobRepo.On("GetNextPendingJob", ctx).Return(nil, errors.New("database connection failed"))

	processed, err := svc.ProcessNext(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get next job")
	assert.False(t, processed)
}

func TestProcessNext_UpdateStatusError(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())
	ctx := context.Background()

	job := &domain.CaptionGenerationJob{
		ID:      uuid.New(),
		VideoID: uuid.New(),
		Status:  domain.CaptionGenStatusPending,
	}

	mockJobRepo.On("GetNextPendingJob", ctx).Return(job, nil)
	mockJobRepo.On("UpdateStatus", ctx, job.ID, domain.CaptionGenStatusProcessing).Return(errors.New("update failed"))

	processed, err := svc.ProcessNext(ctx)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to mark job as processing")
	assert.True(t, processed, "Should return true since a job was found")
}

func TestProcessNext_ProcessJobFailure_MarksFailed(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)
	ctx := context.Background()

	videoID := uuid.New()
	job := &domain.CaptionGenerationJob{
		ID:           uuid.New(),
		VideoID:      videoID,
		Status:       domain.CaptionGenStatusPending,
		OutputFormat: domain.CaptionFormatVTT,
	}

	mockJobRepo.On("GetNextPendingJob", ctx).Return(job, nil)
	mockJobRepo.On("UpdateStatus", ctx, job.ID, domain.CaptionGenStatusProcessing).Return(nil)
	// processJob will fail when getting the video
	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(nil, errors.New("video not found"))
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockJobRepo.On("MarkFailed", ctx, job.ID, mock.AnythingOfType("string")).Return(nil)

	processed, err := svc.ProcessNext(ctx)

	assert.Error(t, err)
	assert.True(t, processed)
	mockJobRepo.AssertCalled(t, "MarkFailed", ctx, job.ID, mock.AnythingOfType("string"))
}

// --- processJob tests ---

func TestProcessJob_FullSuccess_VTT(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	audioPath := filepath.Join(tempDir, "temp", videoID.String()+"_audio.wav")

	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		UserID:          uuid.New(),
		SourceAudioPath: audioPath,
		OutputFormat:    domain.CaptionFormatVTT,
		ModelSize:       domain.WhisperModelBase,
	}

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
		Language: "",
	}

	transcriptionResult := &whisper.TranscriptionResult{
		Text:             "Hello world",
		DetectedLanguage: "en",
		Confidence:       0.95,
		Duration:         10.0,
		Segments: []whisper.TranscriptionSegment{
			{Index: 0, Start: 0.0, End: 2.0, Text: "Hello world", Confidence: 0.95},
		},
	}

	vttContent := "WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nHello world\n"

	// Create web-videos directory and fake source video
	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake video"), 0600))

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockWhisper.On("ExtractAudioFromVideo", ctx, videoPath, audioPath).Return(nil)
	mockWhisper.On("Transcribe", ctx, audioPath, (*string)(nil)).Return(transcriptionResult, nil)
	mockWhisper.On("FormatToVTT", transcriptionResult).Return(vttContent, nil)
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(nil, errors.New("not found"))
	mockCaptionRepo.On("Create", ctx, mock.AnythingOfType("*domain.Caption")).Return(nil)
	mockVideoRepo.On("Update", ctx, mock.AnythingOfType("*domain.Video")).Return(nil)
	mockJobRepo.On("MarkCompleted", ctx, job.ID, mock.AnythingOfType("uuid.UUID"), "en", mock.AnythingOfType("int")).Return(nil)

	err := svc.processJob(ctx, job)

	assert.NoError(t, err)
	mockWhisper.AssertCalled(t, "ExtractAudioFromVideo", ctx, videoPath, audioPath)
	mockWhisper.AssertCalled(t, "Transcribe", ctx, audioPath, (*string)(nil))
	mockWhisper.AssertCalled(t, "FormatToVTT", transcriptionResult)
	mockCaptionRepo.AssertCalled(t, "Create", ctx, mock.AnythingOfType("*domain.Caption"))
	mockJobRepo.AssertCalled(t, "MarkCompleted", ctx, job.ID, mock.AnythingOfType("uuid.UUID"), "en", mock.AnythingOfType("int"))
	// Verify video language was updated since it was empty
	mockVideoRepo.AssertCalled(t, "Update", ctx, mock.AnythingOfType("*domain.Video"))
}

func TestProcessJob_FullSuccess_SRT(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	audioPath := filepath.Join(tempDir, "temp", videoID.String()+"_audio.wav")

	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		UserID:          uuid.New(),
		SourceAudioPath: audioPath,
		OutputFormat:    domain.CaptionFormatSRT,
		ModelSize:       domain.WhisperModelBase,
	}

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
		Language: "en", // Already set, should not update
	}

	transcriptionResult := &whisper.TranscriptionResult{
		Text:             "Hello world",
		DetectedLanguage: "en",
		Confidence:       0.95,
	}

	srtContent := "1\n00:00:00,000 --> 00:00:02,000\nHello world\n"

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake video"), 0600))

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockWhisper.On("ExtractAudioFromVideo", ctx, videoPath, audioPath).Return(nil)
	mockWhisper.On("Transcribe", ctx, audioPath, (*string)(nil)).Return(transcriptionResult, nil)
	mockWhisper.On("FormatToSRT", transcriptionResult).Return(srtContent, nil)
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(nil, errors.New("not found"))
	mockCaptionRepo.On("Create", ctx, mock.AnythingOfType("*domain.Caption")).Return(nil)
	mockJobRepo.On("MarkCompleted", ctx, job.ID, mock.AnythingOfType("uuid.UUID"), "en", mock.AnythingOfType("int")).Return(nil)

	err := svc.processJob(ctx, job)

	assert.NoError(t, err)
	mockWhisper.AssertCalled(t, "FormatToSRT", transcriptionResult)
	// Video language already set, should NOT update
	mockVideoRepo.AssertNotCalled(t, "Update", mock.Anything, mock.Anything)
}

func TestProcessJob_UnsupportedFormat(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	audioPath := filepath.Join(tempDir, "temp", videoID.String()+"_audio.wav")

	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		SourceAudioPath: audioPath,
		OutputFormat:    domain.CaptionFormat("ass"), // Unsupported
	}

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	transcriptionResult := &whisper.TranscriptionResult{
		Text:             "Hello world",
		DetectedLanguage: "en",
	}

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake video"), 0600))

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockWhisper.On("ExtractAudioFromVideo", ctx, videoPath, audioPath).Return(nil)
	mockWhisper.On("Transcribe", ctx, audioPath, (*string)(nil)).Return(transcriptionResult, nil)

	err := svc.processJob(ctx, job)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "unsupported caption format")
}

func TestProcessJob_ExtractAudioError(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	audioPath := filepath.Join(tempDir, "temp", videoID.String()+"_audio.wav")

	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		SourceAudioPath: audioPath,
		OutputFormat:    domain.CaptionFormatVTT,
	}

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake video"), 0600))

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockWhisper.On("ExtractAudioFromVideo", ctx, videoPath, audioPath).Return(errors.New("ffmpeg not found"))

	err := svc.processJob(ctx, job)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to extract audio")
}

func TestProcessJob_TranscribeError(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	audioPath := filepath.Join(tempDir, "temp", videoID.String()+"_audio.wav")

	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		SourceAudioPath: audioPath,
		OutputFormat:    domain.CaptionFormatVTT,
	}

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake video"), 0600))

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockWhisper.On("ExtractAudioFromVideo", ctx, videoPath, audioPath).Return(nil)
	mockWhisper.On("Transcribe", ctx, audioPath, (*string)(nil)).Return(nil, errors.New("whisper model not loaded"))

	err := svc.processJob(ctx, job)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to transcribe audio")
}

func TestProcessJob_FormatToVTTError(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	audioPath := filepath.Join(tempDir, "temp", videoID.String()+"_audio.wav")

	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		SourceAudioPath: audioPath,
		OutputFormat:    domain.CaptionFormatVTT,
	}

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		MimeType: "video/mp4",
	}

	transcriptionResult := &whisper.TranscriptionResult{
		Text:             "test",
		DetectedLanguage: "en",
	}

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake"), 0600))

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockWhisper.On("ExtractAudioFromVideo", ctx, videoPath, audioPath).Return(nil)
	mockWhisper.On("Transcribe", ctx, audioPath, (*string)(nil)).Return(transcriptionResult, nil)
	mockWhisper.On("FormatToVTT", transcriptionResult).Return("", errors.New("formatting failed"))

	err := svc.processJob(ctx, job)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to format caption")
}

func TestProcessJob_CreateCaptionRecordError(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	audioPath := filepath.Join(tempDir, "temp", videoID.String()+"_audio.wav")

	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		SourceAudioPath: audioPath,
		OutputFormat:    domain.CaptionFormatVTT,
	}

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		MimeType: "video/mp4",
		Language: "en",
	}

	transcriptionResult := &whisper.TranscriptionResult{
		Text:             "test",
		DetectedLanguage: "en",
	}

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake"), 0600))

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockWhisper.On("ExtractAudioFromVideo", ctx, videoPath, audioPath).Return(nil)
	mockWhisper.On("Transcribe", ctx, audioPath, (*string)(nil)).Return(transcriptionResult, nil)
	mockWhisper.On("FormatToVTT", transcriptionResult).Return("WEBVTT\n\ntest", nil)
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(nil, errors.New("not found"))
	mockCaptionRepo.On("Create", ctx, mock.AnythingOfType("*domain.Caption")).Return(errors.New("db write error"))

	err := svc.processJob(ctx, job)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create caption record")
}

func TestProcessJob_MarkCompletedError(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	audioPath := filepath.Join(tempDir, "temp", videoID.String()+"_audio.wav")

	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		SourceAudioPath: audioPath,
		OutputFormat:    domain.CaptionFormatVTT,
	}

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		MimeType: "video/mp4",
		Language: "en",
	}

	transcriptionResult := &whisper.TranscriptionResult{
		Text:             "test",
		DetectedLanguage: "en",
	}

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake"), 0600))

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockWhisper.On("ExtractAudioFromVideo", ctx, videoPath, audioPath).Return(nil)
	mockWhisper.On("Transcribe", ctx, audioPath, (*string)(nil)).Return(transcriptionResult, nil)
	mockWhisper.On("FormatToVTT", transcriptionResult).Return("WEBVTT\n\ntest", nil)
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(nil, errors.New("not found"))
	mockCaptionRepo.On("Create", ctx, mock.AnythingOfType("*domain.Caption")).Return(nil)
	mockJobRepo.On("MarkCompleted", ctx, job.ID, mock.AnythingOfType("uuid.UUID"), "en", mock.AnythingOfType("int")).Return(errors.New("db error"))

	err := svc.processJob(ctx, job)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to mark job as completed")
}

func TestProcessJob_GetVideoError(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	job := &domain.CaptionGenerationJob{
		ID:      uuid.New(),
		VideoID: videoID,
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(nil, errors.New("not found"))
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)

	err := svc.processJob(ctx, job)

	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get video")
}

func TestProcessJob_AutoDetectDeletesExistingCaption(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir).(*service)
	ctx := context.Background()

	videoID := uuid.New()
	audioPath := filepath.Join(tempDir, "temp", videoID.String()+"_audio.wav")

	// No target language = auto-detect
	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		SourceAudioPath: audioPath,
		TargetLanguage:  nil,
		OutputFormat:    domain.CaptionFormatVTT,
	}

	// Existing caption for the detected language
	existingCaptionPath := filepath.Join(tempDir, "old_caption.vtt")
	require.NoError(t, os.WriteFile(existingCaptionPath, []byte("old content"), 0600))

	existingCaption := &domain.Caption{
		ID:           uuid.New(),
		VideoID:      videoID,
		LanguageCode: "en",
		FilePath:     &existingCaptionPath,
	}

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		MimeType: "video/mp4",
		Language: "en",
	}

	transcriptionResult := &whisper.TranscriptionResult{
		Text:             "Hello",
		DetectedLanguage: "en",
	}

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake"), 0600))

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("UpdateProgress", ctx, job.ID, mock.AnythingOfType("int")).Return(nil)
	mockWhisper.On("ExtractAudioFromVideo", ctx, videoPath, audioPath).Return(nil)
	mockWhisper.On("Transcribe", ctx, audioPath, (*string)(nil)).Return(transcriptionResult, nil)
	mockWhisper.On("FormatToVTT", transcriptionResult).Return("WEBVTT\n\nHello", nil)
	// Auto-detect: existing caption for detected language should be deleted
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(existingCaption, nil)
	mockCaptionRepo.On("Delete", ctx, existingCaption.ID).Return(nil)
	mockCaptionRepo.On("Create", ctx, mock.AnythingOfType("*domain.Caption")).Return(nil)
	mockJobRepo.On("MarkCompleted", ctx, job.ID, mock.AnythingOfType("uuid.UUID"), "en", mock.AnythingOfType("int")).Return(nil)

	err := svc.processJob(ctx, job)

	assert.NoError(t, err)
	// Verify old caption file was deleted from disk
	_, err = os.Stat(existingCaptionPath)
	assert.True(t, os.IsNotExist(err), "Old caption file should be deleted for auto-detect")
	mockCaptionRepo.AssertCalled(t, "Delete", ctx, existingCaption.ID)
}

// --- CreateJob error path tests ---

func TestCreateJob_VideoNotFound(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())
	ctx := context.Background()

	videoID := uuid.New()
	userID := uuid.New()
	lang := "en"

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(nil, errors.New("not found"))

	req := &domain.CreateCaptionGenerationJobRequest{
		VideoID:        videoID,
		TargetLanguage: &lang,
	}

	job, err := svc.CreateJob(ctx, videoID, userID, req)

	assert.Error(t, err)
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "failed to get video")
}

func TestCreateJob_SourceFileNotFound(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	// Do NOT create the web-videos file
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)
	ctx := context.Background()

	videoID := uuid.New()
	userID := uuid.New()
	lang := "en"

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)

	req := &domain.CreateCaptionGenerationJobRequest{
		VideoID:        videoID,
		TargetLanguage: &lang,
	}

	job, err := svc.CreateJob(ctx, videoID, userID, req)

	assert.Error(t, err)
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "source video file not found")
}

func TestCreateJob_DefaultValues(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)
	ctx := context.Background()

	videoID := uuid.New()
	userID := uuid.New()

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake video"), 0600))

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)
	mockWhisper.On("GetProvider").Return(domain.WhisperProviderOpenAI)

	// Request with empty defaults
	req := &domain.CreateCaptionGenerationJobRequest{
		VideoID: videoID,
	}

	job, err := svc.CreateJob(ctx, videoID, userID, req)

	assert.NoError(t, err)
	require.NotNil(t, job)
	// Verify defaults are applied
	assert.Equal(t, domain.WhisperModelBase, job.ModelSize, "Default model should be base")
	assert.Equal(t, domain.CaptionFormatVTT, job.OutputFormat, "Default format should be VTT")
	assert.Equal(t, domain.WhisperProviderOpenAI, job.Provider, "Provider should come from whisper client")
	assert.Equal(t, domain.CaptionGenStatusPending, job.Status)
	assert.Equal(t, 0, job.Progress)
	assert.Equal(t, 3, job.MaxRetries)
	assert.False(t, job.IsAutomatic)
}

func TestCreateJob_RepoCreateError(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)
	ctx := context.Background()

	videoID := uuid.New()
	userID := uuid.New()

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake video"), 0600))

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(errors.New("database full"))
	mockWhisper.On("GetProvider").Return(domain.WhisperProviderLocal)

	req := &domain.CreateCaptionGenerationJobRequest{
		VideoID: videoID,
	}

	job, err := svc.CreateJob(ctx, videoID, userID, req)

	assert.Error(t, err)
	assert.Nil(t, job)
	assert.Contains(t, err.Error(), "failed to create job")
}

// --- RegenerateCaption error path tests ---

func TestRegenerateCaption_VideoNotFound(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())
	ctx := context.Background()

	videoID := uuid.New()
	userID := uuid.New()
	lang := "en"

	// RegenerateCaption queries caption repo first, then delegates to CreateJob
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(nil, errors.New("not found"))
	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(nil, errors.New("not found"))

	job, err := svc.RegenerateCaption(ctx, videoID, userID, &lang)

	assert.Error(t, err)
	assert.Nil(t, job)
}

func TestRegenerateCaption_NoCaptionToDelete(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)
	ctx := context.Background()

	videoID := uuid.New()
	userID := uuid.New()
	lang := "en"

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake video"), 0600))

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	// No existing caption found - should not error, just skip deletion
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(nil, errors.New("not found"))
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)
	mockWhisper.On("GetProvider").Return(domain.WhisperProviderLocal)

	job, err := svc.RegenerateCaption(ctx, videoID, userID, &lang)

	assert.NoError(t, err)
	assert.NotNil(t, job)
	// Delete should not be called since no caption was found
	mockCaptionRepo.AssertNotCalled(t, "Delete", mock.Anything, mock.Anything)
}

func TestRegenerateCaption_EmptyLanguageString(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	tempDir := t.TempDir()
	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)
	ctx := context.Background()

	videoID := uuid.New()
	userID := uuid.New()
	emptyLang := ""

	webVideosDir := filepath.Join(tempDir, "web-videos")
	require.NoError(t, os.MkdirAll(webVideosDir, 0750))
	videoPath := filepath.Join(webVideosDir, videoID.String()+".mp4")
	require.NoError(t, os.WriteFile(videoPath, []byte("fake video"), 0600))

	mockVideo := &domain.Video{
		ID:       videoID.String(),
		Status:   domain.StatusCompleted,
		MimeType: "video/mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID.String()).Return(mockVideo, nil)
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)
	mockWhisper.On("GetProvider").Return(domain.WhisperProviderLocal)

	// Empty string should be treated like nil (no deletion)
	job, err := svc.RegenerateCaption(ctx, videoID, userID, &emptyLang)

	assert.NoError(t, err)
	assert.NotNil(t, job)
	mockCaptionRepo.AssertNotCalled(t, "GetByVideoAndLanguage", mock.Anything, mock.Anything, mock.Anything)
}

// --- GetJobStatus error tests ---

func TestGetJobStatus_NotFound(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())
	ctx := context.Background()

	jobID := uuid.New()
	mockJobRepo.On("GetByID", ctx, jobID).Return(nil, errors.New("not found"))

	job, err := svc.GetJobStatus(ctx, jobID)

	assert.Error(t, err)
	assert.Nil(t, job)
}

// --- GetJobsByVideo error tests ---

func TestGetJobsByVideo_Error(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())
	ctx := context.Background()

	videoID := uuid.New()
	mockJobRepo.On("GetByVideoID", ctx, videoID).Return([]domain.CaptionGenerationJob(nil), errors.New("db error"))

	jobs, err := svc.GetJobsByVideo(ctx, videoID)

	assert.Error(t, err)
	assert.Nil(t, jobs)
}

// --- Run tests ---

func TestRun_ContextCancellation(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())

	// No pending jobs - workers will back off and eventually exit on cancel
	mockJobRepo.On("GetNextPendingJob", mock.Anything).Return(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()

	err := svc.Run(ctx, 2)

	// Should exit cleanly when context is cancelled
	assert.NoError(t, err)
}

func TestRun_DefaultWorkerCount(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())

	mockJobRepo.On("GetNextPendingJob", mock.Anything).Return(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// workers=0 should use default (NumCPU or 2)
	err := svc.Run(ctx, 0)
	assert.NoError(t, err)
}

func TestRun_NegativeWorkerCount(t *testing.T) {
	mockJobRepo := new(MockJobRepository)
	mockCaptionRepo := new(MockCaptionRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockWhisper := new(MockWhisperClient)

	svc := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, t.TempDir())

	mockJobRepo.On("GetNextPendingJob", mock.Anything).Return(nil, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	// Negative workers should also use default
	err := svc.Run(ctx, -1)
	assert.NoError(t, err)
}

// --- getExtensionFromMimeType tests ---

func TestGetExtensionFromMimeType(t *testing.T) {
	tests := []struct {
		mimeType string
		expected string
	}{
		{"video/mp4", ".mp4"},
		{"video/mpeg", ".mpeg"},
		{"video/quicktime", ".mov"},
		{"video/x-msvideo", ".avi"},
		{"video/x-matroska", ".mkv"},
		{"video/webm", ".webm"},
		{"video/ogg", ".ogv"},
		{"video/3gpp", ".3gp"},
		{"video/x-flv", ".flv"},
		{"unknown/type", ".mp4"},    // Fallback
		{"", ".mp4"},                // Empty string fallback
		{"application/pdf", ".mp4"}, // Non-video fallback
	}

	for _, tt := range tests {
		t.Run(fmt.Sprintf("%s->%s", tt.mimeType, tt.expected), func(t *testing.T) {
			result := getExtensionFromMimeType(tt.mimeType)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// --- getLanguageLabel extended tests ---

func TestGetLanguageLabel_AllLanguages(t *testing.T) {
	tests := []struct {
		code     string
		expected string
	}{
		{"pt", "Portuguese"},
		{"ru", "Russian"},
		{"ko", "Korean"},
		{"ar", "Arabic"},
		{"hi", "Hindi"},
		{"nl", "Dutch"},
		{"pl", "Polish"},
		{"tr", "Turkish"},
		{"sv", "Swedish"},
		{"da", "Danish"},
		{"fi", "Finnish"},
		{"no", "Norwegian"},
		{"cs", "Czech"},
		{"el", "Greek"},
		{"he", "Hebrew"},
		{"id", "Indonesian"},
		{"th", "Thai"},
		{"vi", "Vietnamese"},
		{"it", "Italian"},
	}

	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			result := getLanguageLabel(tt.code)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestGetLanguageLabel_CaseInsensitive(t *testing.T) {
	// The function lowercases the code before lookup
	assert.Equal(t, "English", getLanguageLabel("EN"))
	assert.Equal(t, "French", getLanguageLabel("FR"))
	assert.Equal(t, "Spanish", getLanguageLabel("Es"))
}
