package captiongen

import (
	"athena/internal/domain"
	"context"
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

func (m *MockVideoRepository) GetByID(ctx context.Context, videoID uuid.UUID) (*domain.Video, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}

func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

type MockWhisperClient struct {
	mock.Mock
}

func (m *MockWhisperClient) Transcribe(ctx context.Context, audioPath string, targetLanguage *string) (*TranscriptionResult, error) {
	args := m.Called(ctx, audioPath, targetLanguage)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*TranscriptionResult), args.Error(1)
}

func (m *MockWhisperClient) ExtractAudioFromVideo(ctx context.Context, videoPath string, outputPath string) error {
	args := m.Called(ctx, videoPath, outputPath)
	return args.Error(0)
}

func (m *MockWhisperClient) FormatToVTT(result *TranscriptionResult) (string, error) {
	args := m.Called(result)
	return args.String(0), args.Error(1)
}

func (m *MockWhisperClient) FormatToSRT(result *TranscriptionResult) (string, error) {
	args := m.Called(result)
	return args.String(0), args.Error(1)
}

func (m *MockWhisperClient) GetProvider() domain.WhisperProvider {
	args := m.Called()
	return args.Get(0).(domain.WhisperProvider)
}

// Helper to create TranscriptionResult
type TranscriptionResult struct {
	Text             string
	DetectedLanguage string
	Confidence       float64
	Duration         float64
	Segments         []TranscriptionSegment
}

type TranscriptionSegment struct {
	Index      int
	Start      float64
	End        float64
	Text       string
	Confidence float64
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

	// Create a temporary caption file
	err := os.WriteFile(captionPath, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nOld English caption"), 0644)
	require.NoError(t, err)

	// Setup: video exists with English caption
	mockVideo := &domain.Video{
		ID:                videoID.String(),
		ProcessingStatus:  domain.ProcessingStatusCompleted,
		OriginalFilename:  "test.mp4",
	}

	existingEnglishCaption := &domain.Caption{
		ID:           uuid.New(),
		VideoID:      videoID,
		LanguageCode: "en",
		Label:        "English",
		FilePath:     &captionPath,
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(mockVideo, nil)
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(existingEnglishCaption, nil)
	mockCaptionRepo.On("Delete", ctx, existingEnglishCaption.ID).Return(nil)
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)

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

	// Create caption files
	captionPathEN := filepath.Join(tempDir, "caption_en.vtt")
	captionPathES := filepath.Join(tempDir, "caption_es.vtt")

	err := os.WriteFile(captionPathEN, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nOld English"), 0644)
	require.NoError(t, err)
	err = os.WriteFile(captionPathES, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:02.000\nEspañol antiguo"), 0644)
	require.NoError(t, err)

	// Setup: video has both English and Spanish captions
	mockVideo := &domain.Video{
		ID:                videoID.String(),
		ProcessingStatus:  domain.ProcessingStatusCompleted,
		OriginalFilename:  "test.mp4",
	}

	existingEnglishCaption := &domain.Caption{
		ID:           uuid.New(),
		VideoID:      videoID,
		LanguageCode: "en",
		Label:        "English",
		FilePath:     &captionPathEN,
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(mockVideo, nil)
	mockCaptionRepo.On("GetByVideoAndLanguage", ctx, videoID, "en").Return(existingEnglishCaption, nil)
	mockCaptionRepo.On("Delete", ctx, existingEnglishCaption.ID).Return(nil)
	// Spanish caption should NOT be queried or deleted
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)

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

	// Setup: video exists
	mockVideo := &domain.Video{
		ID:                videoID.String(),
		ProcessingStatus:  domain.ProcessingStatusCompleted,
		OriginalFilename:  "test.mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(mockVideo, nil)
	// When auto-detecting, no caption should be deleted before transcription
	mockJobRepo.On("Create", ctx, mock.AnythingOfType("*domain.CaptionGenerationJob")).Return(nil)

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

	// Create a fake video file
	videoPath := filepath.Join(tempDir, "test-video-id", "original", "test.mp4")
	err := os.MkdirAll(filepath.Dir(videoPath), 0755)
	require.NoError(t, err)
	err = os.WriteFile(videoPath, []byte("fake video content"), 0644)
	require.NoError(t, err)

	service := NewService(mockJobRepo, mockCaptionRepo, mockVideoRepo, mockWhisper, tempDir)

	ctx := context.Background()
	videoID := uuid.New()
	userID := uuid.New()
	targetLang := "en"

	mockVideo := &domain.Video{
		ID:                videoID.String(),
		ProcessingStatus:  domain.ProcessingStatusCompleted,
		OriginalFilename:  "test.mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(mockVideo, nil)
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

	mockVideoRepo.AssertCalled(t, "GetByID", ctx, videoID)
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
		ID:                videoID.String(),
		ProcessingStatus:  domain.ProcessingStatusProcessing,
		OriginalFilename:  "test.mp4",
	}

	mockVideoRepo.On("GetByID", ctx, videoID).Return(mockVideo, nil)

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

	mockVideoRepo.AssertCalled(t, "GetByID", ctx, videoID)
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
