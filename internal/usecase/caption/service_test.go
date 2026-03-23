package caption

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockCaptionRepo struct{ mock.Mock }

func (m *mockCaptionRepo) Create(ctx context.Context, caption *domain.Caption) error {
	return m.Called(ctx, caption).Error(0)
}
func (m *mockCaptionRepo) GetByID(ctx context.Context, captionID uuid.UUID) (*domain.Caption, error) {
	args := m.Called(ctx, captionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Caption), args.Error(1)
}
func (m *mockCaptionRepo) GetByVideoID(ctx context.Context, videoID uuid.UUID) ([]domain.Caption, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Caption), args.Error(1)
}
func (m *mockCaptionRepo) GetByVideoAndLanguage(ctx context.Context, videoID uuid.UUID, languageCode string) (*domain.Caption, error) {
	args := m.Called(ctx, videoID, languageCode)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Caption), args.Error(1)
}
func (m *mockCaptionRepo) Update(ctx context.Context, caption *domain.Caption) error {
	return m.Called(ctx, caption).Error(0)
}
func (m *mockCaptionRepo) Delete(ctx context.Context, captionID uuid.UUID) error {
	return m.Called(ctx, captionID).Error(0)
}
func (m *mockCaptionRepo) DeleteByVideoID(ctx context.Context, videoID uuid.UUID) error {
	return m.Called(ctx, videoID).Error(0)
}
func (m *mockCaptionRepo) CountByVideoID(ctx context.Context, videoID uuid.UUID) (int, error) {
	args := m.Called(ctx, videoID)
	return args.Int(0), args.Error(1)
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

func (m *mockVideoRepo) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func newTestService(t *testing.T) (*Service, *mockCaptionRepo, *mockVideoRepo) {
	t.Helper()
	capRepo := new(mockCaptionRepo)
	vidRepo := new(mockVideoRepo)
	cfg := &config.Config{StorageDir: t.TempDir()}
	svc := NewService(capRepo, vidRepo, cfg)
	return svc, capRepo, vidRepo
}

func TestGetCaptionByID_Success(t *testing.T) {
	svc, capRepo, vidRepo := newTestService(t)

	captionID := uuid.New()
	videoID := uuid.New()
	caption := &domain.Caption{
		ID:           captionID,
		VideoID:      videoID,
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
	}
	video := &domain.Video{ID: videoID.String(), Title: "Test Video"}

	capRepo.On("GetByID", mock.Anything, captionID).Return(caption, nil)
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(video, nil)

	result, err := svc.GetCaptionByID(context.Background(), captionID)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "en", result.LanguageCode)
	assert.NotEmpty(t, result.URL)
}

func TestGetCaptionByID_NotFound(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	capRepo.On("GetByID", mock.Anything, captionID).Return(nil, domain.ErrNotFound)

	result, err := svc.GetCaptionByID(context.Background(), captionID)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestGetCaptionsByVideoID_Success(t *testing.T) {
	svc, capRepo, vidRepo := newTestService(t)

	videoID := uuid.New()
	video := &domain.Video{ID: videoID.String(), Title: "Test Video"}
	captions := []domain.Caption{
		{ID: uuid.New(), VideoID: videoID, LanguageCode: "en", Label: "English", FileFormat: domain.CaptionFormatVTT},
		{ID: uuid.New(), VideoID: videoID, LanguageCode: "es", Label: "Spanish", FileFormat: domain.CaptionFormatSRT},
	}

	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(video, nil)
	capRepo.On("GetByVideoID", mock.Anything, videoID).Return(captions, nil)

	result, err := svc.GetCaptionsByVideoID(context.Background(), videoID)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, 2, result.Count)
	assert.Len(t, result.Captions, 2)
}

func TestGetCaptionsByVideoID_VideoNotFound(t *testing.T) {
	svc, _, vidRepo := newTestService(t)

	videoID := uuid.New()
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, domain.ErrNotFound)

	result, err := svc.GetCaptionsByVideoID(context.Background(), videoID)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestUpdateCaption_LabelOnly(t *testing.T) {
	svc, capRepo, vidRepo := newTestService(t)

	captionID := uuid.New()
	videoID := uuid.New()
	existing := &domain.Caption{
		ID:           captionID,
		VideoID:      videoID,
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
	}

	newLabel := "English (US)"
	req := &domain.UpdateCaptionRequest{Label: &newLabel}

	capRepo.On("GetByID", mock.Anything, captionID).Return(existing, nil)
	capRepo.On("Update", mock.Anything, mock.AnythingOfType("*domain.Caption")).Return(nil)
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)

	result, err := svc.UpdateCaption(context.Background(), captionID, req)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "English (US)", result.Label)
}

func TestUpdateCaption_NotFound(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	capRepo.On("GetByID", mock.Anything, captionID).Return(nil, domain.ErrNotFound)

	newLabel := "Test"
	req := &domain.UpdateCaptionRequest{Label: &newLabel}

	result, err := svc.UpdateCaption(context.Background(), captionID, req)
	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestUpdateCaption_LanguageConflict(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	otherID := uuid.New()
	videoID := uuid.New()

	existing := &domain.Caption{
		ID:           captionID,
		VideoID:      videoID,
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
	}
	conflicting := &domain.Caption{
		ID:           otherID,
		VideoID:      videoID,
		LanguageCode: "es",
	}

	newLang := "es"
	req := &domain.UpdateCaptionRequest{LanguageCode: &newLang}

	capRepo.On("GetByID", mock.Anything, captionID).Return(existing, nil)
	capRepo.On("GetByVideoAndLanguage", mock.Anything, videoID, "es").Return(conflicting, nil)

	result, err := svc.UpdateCaption(context.Background(), captionID, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "LANGUAGE_EXISTS")
}

func TestDeleteCaption_Success(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	caption := &domain.Caption{
		ID:       captionID,
		FilePath: nil,
	}

	capRepo.On("GetByID", mock.Anything, captionID).Return(caption, nil)
	capRepo.On("Delete", mock.Anything, captionID).Return(nil)

	err := svc.DeleteCaption(context.Background(), captionID)
	assert.NoError(t, err)
	capRepo.AssertExpectations(t)
}

func TestDeleteCaption_NotFound(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	capRepo.On("GetByID", mock.Anything, captionID).Return(nil, domain.ErrNotFound)

	err := svc.DeleteCaption(context.Background(), captionID)
	assert.Error(t, err)
}

func TestDeleteCaption_DBError(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	caption := &domain.Caption{ID: captionID}

	capRepo.On("GetByID", mock.Anything, captionID).Return(caption, nil)
	capRepo.On("Delete", mock.Anything, captionID).Return(errors.New("db error"))

	err := svc.DeleteCaption(context.Background(), captionID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete caption")
}

func TestCreateCaption_VideoNotFound(t *testing.T) {
	svc, _, vidRepo := newTestService(t)

	videoID := uuid.New()
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, domain.ErrNotFound)

	req := &domain.CreateCaptionRequest{
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
	}

	result, err := svc.CreateCaption(context.Background(), videoID, req, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestCreateCaption_InvalidFormat(t *testing.T) {
	svc, _, vidRepo := newTestService(t)

	videoID := uuid.New()
	video := &domain.Video{ID: videoID.String()}
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(video, nil)

	req := &domain.CreateCaptionRequest{
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormat("invalid"),
	}

	result, err := svc.CreateCaption(context.Background(), videoID, req, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "INVALID_FORMAT")
}

func TestCreateCaption_InvalidLanguageCode(t *testing.T) {
	svc, _, vidRepo := newTestService(t)

	videoID := uuid.New()
	video := &domain.Video{ID: videoID.String()}
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(video, nil)

	req := &domain.CreateCaptionRequest{
		LanguageCode: "x",
		Label:        "Test",
		FileFormat:   domain.CaptionFormatVTT,
	}

	result, err := svc.CreateCaption(context.Background(), videoID, req, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "INVALID_LANGUAGE")
}

func TestGetCaptionContent_NoFile(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	caption := &domain.Caption{
		ID:       captionID,
		FilePath: nil,
	}

	capRepo.On("GetByID", mock.Anything, captionID).Return(caption, nil)

	reader, contentType, err := svc.GetCaptionContent(context.Background(), captionID)
	assert.Error(t, err)
	assert.Nil(t, reader)
	assert.Empty(t, contentType)
	assert.Contains(t, err.Error(), "NO_FILE")
}

func TestGenerateCaptionURL(t *testing.T) {
	svc, _, _ := newTestService(t)

	videoID := uuid.New()
	captionID := uuid.New()

	video := &domain.Video{ID: videoID.String()}
	caption := &domain.Caption{ID: captionID}

	url := svc.generateCaptionURL(video, caption)
	assert.Contains(t, url, videoID.String())
	assert.Contains(t, url, captionID.String())
	assert.Contains(t, url, "/api/v1/videos/")
}

func TestCreateCaption_Success(t *testing.T) {
	svc, capRepo, vidRepo := newTestService(t)

	videoID := uuid.New()
	video := &domain.Video{ID: videoID.String()}
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(video, nil)
	capRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Caption")).Return(nil)

	req := &domain.CreateCaptionRequest{
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
	}

	file := strings.NewReader("WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello world")
	result, err := svc.CreateCaption(context.Background(), videoID, req, file)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "en", result.LanguageCode)
	assert.Equal(t, "English", result.Label)
	assert.NotEmpty(t, result.URL)
	capRepo.AssertExpectations(t)
}

func TestCreateCaption_VideoRepoError(t *testing.T) {
	svc, _, vidRepo := newTestService(t)

	videoID := uuid.New()
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, errors.New("db error"))

	req := &domain.CreateCaptionRequest{
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
	}
	result, err := svc.CreateCaption(context.Background(), videoID, req, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to verify video")
}

func TestCreateCaption_InvalidLanguageTooLong(t *testing.T) {
	svc, _, vidRepo := newTestService(t)

	videoID := uuid.New()
	video := &domain.Video{ID: videoID.String()}
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(video, nil)

	req := &domain.CreateCaptionRequest{
		LanguageCode: "toolonglangu",
		Label:        "Test",
		FileFormat:   domain.CaptionFormatVTT,
	}
	result, err := svc.CreateCaption(context.Background(), videoID, req, nil)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "INVALID_LANGUAGE")
}

func TestCreateCaption_RepoCreateError(t *testing.T) {
	svc, capRepo, vidRepo := newTestService(t)

	videoID := uuid.New()
	video := &domain.Video{ID: videoID.String()}
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(video, nil)
	capRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Caption")).Return(errors.New("db error"))

	req := &domain.CreateCaptionRequest{
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
	}

	file := strings.NewReader("WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello")
	result, err := svc.CreateCaption(context.Background(), videoID, req, file)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to create caption record")
}

func TestCreateCaption_EmptyFile(t *testing.T) {
	svc, _, vidRepo := newTestService(t)

	videoID := uuid.New()
	video := &domain.Video{ID: videoID.String()}
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(video, nil)

	req := &domain.CreateCaptionRequest{
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
	}

	file := strings.NewReader("")
	result, err := svc.CreateCaption(context.Background(), videoID, req, file)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "EMPTY_FILE")
}

func TestGetCaptionContent_Success(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "en.vtt")
	err := os.WriteFile(tmpFile, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello"), 0644)
	assert.NoError(t, err)

	captionID := uuid.New()
	caption := &domain.Caption{
		ID:         captionID,
		FilePath:   &tmpFile,
		FileFormat: domain.CaptionFormatVTT,
	}

	capRepo.On("GetByID", mock.Anything, captionID).Return(caption, nil)

	reader, contentType, err := svc.GetCaptionContent(context.Background(), captionID)
	assert.NoError(t, err)
	assert.NotNil(t, reader)
	assert.NotEmpty(t, contentType)
	defer reader.Close()
}

func TestGetCaptionContent_RepoError(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	capRepo.On("GetByID", mock.Anything, captionID).Return(nil, errors.New("db error"))

	reader, contentType, err := svc.GetCaptionContent(context.Background(), captionID)
	assert.Error(t, err)
	assert.Nil(t, reader)
	assert.Empty(t, contentType)
}

func TestGetCaptionContent_EmptyFilePath(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	emptyPath := ""
	caption := &domain.Caption{
		ID:       captionID,
		FilePath: &emptyPath,
	}

	capRepo.On("GetByID", mock.Anything, captionID).Return(caption, nil)

	reader, contentType, err := svc.GetCaptionContent(context.Background(), captionID)
	assert.Error(t, err)
	assert.Nil(t, reader)
	assert.Empty(t, contentType)
	assert.Contains(t, err.Error(), "NO_FILE")
}

func TestGetCaptionContent_FileNotExist(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	nonExistent := "/nonexistent/path/caption.vtt"
	caption := &domain.Caption{
		ID:         captionID,
		FilePath:   &nonExistent,
		FileFormat: domain.CaptionFormatVTT,
	}

	capRepo.On("GetByID", mock.Anything, captionID).Return(caption, nil)

	reader, _, err := svc.GetCaptionContent(context.Background(), captionID)
	assert.Error(t, err)
	assert.Nil(t, reader)
	assert.Contains(t, err.Error(), "failed to open caption file")
}

func TestGetCaptionsByVideoID_RepoError(t *testing.T) {
	svc, capRepo, vidRepo := newTestService(t)

	videoID := uuid.New()
	video := &domain.Video{ID: videoID.String()}
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(video, nil)
	capRepo.On("GetByVideoID", mock.Anything, videoID).Return(nil, errors.New("db error"))

	result, err := svc.GetCaptionsByVideoID(context.Background(), videoID)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to get captions")
}

func TestGetCaptionsByVideoID_VideoRepoGenericError(t *testing.T) {
	svc, _, vidRepo := newTestService(t)

	videoID := uuid.New()
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, errors.New("connection timeout"))

	result, err := svc.GetCaptionsByVideoID(context.Background(), videoID)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to verify video")
}

func TestUpdateCaption_LanguageChangeWithFileRename(t *testing.T) {
	svc, capRepo, vidRepo := newTestService(t)

	tmpDir := t.TempDir()
	oldFile := filepath.Join(tmpDir, "en.vtt")
	err := os.WriteFile(oldFile, []byte("WEBVTT\n\ntest"), 0644)
	assert.NoError(t, err)

	captionID := uuid.New()
	videoID := uuid.New()
	existing := &domain.Caption{
		ID:           captionID,
		VideoID:      videoID,
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
		FilePath:     &oldFile,
	}

	newLang := "fr"
	req := &domain.UpdateCaptionRequest{LanguageCode: &newLang}

	capRepo.On("GetByID", mock.Anything, captionID).Return(existing, nil)
	capRepo.On("GetByVideoAndLanguage", mock.Anything, videoID, "fr").Return(nil, domain.ErrNotFound)
	capRepo.On("Update", mock.Anything, mock.AnythingOfType("*domain.Caption")).Return(nil)
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)

	result, err := svc.UpdateCaption(context.Background(), captionID, req)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Equal(t, "fr", result.LanguageCode)

	expectedNewPath := filepath.Join(tmpDir, "fr.vtt")
	assert.Equal(t, expectedNewPath, *result.FilePath)
	assert.FileExists(t, expectedNewPath)
}

func TestUpdateCaption_UpdateRepoError(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	captionID := uuid.New()
	videoID := uuid.New()
	existing := &domain.Caption{
		ID:           captionID,
		VideoID:      videoID,
		LanguageCode: "en",
		Label:        "English",
		FileFormat:   domain.CaptionFormatVTT,
	}

	newLabel := "Updated"
	req := &domain.UpdateCaptionRequest{Label: &newLabel}

	capRepo.On("GetByID", mock.Anything, captionID).Return(existing, nil)
	capRepo.On("Update", mock.Anything, mock.AnythingOfType("*domain.Caption")).Return(errors.New("db error"))

	result, err := svc.UpdateCaption(context.Background(), captionID, req)
	assert.Error(t, err)
	assert.Nil(t, result)
	assert.Contains(t, err.Error(), "failed to update caption")
}

func TestDeleteCaption_WithFilePath(t *testing.T) {
	svc, capRepo, _ := newTestService(t)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "en.vtt")
	err := os.WriteFile(tmpFile, []byte("test"), 0644)
	assert.NoError(t, err)

	captionID := uuid.New()
	caption := &domain.Caption{
		ID:       captionID,
		FilePath: &tmpFile,
	}

	capRepo.On("GetByID", mock.Anything, captionID).Return(caption, nil)
	capRepo.On("Delete", mock.Anything, captionID).Return(nil)

	err = svc.DeleteCaption(context.Background(), captionID)
	assert.NoError(t, err)
	_, statErr := os.Stat(tmpFile)
	assert.True(t, os.IsNotExist(statErr))
}

func TestValidateCaptionFormat_ValidVTT(t *testing.T) {
	svc, _, _ := newTestService(t)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.vtt")
	err := os.WriteFile(tmpFile, []byte("WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello"), 0644)
	assert.NoError(t, err)

	err = svc.validateCaptionFormat(tmpFile)
	assert.NoError(t, err)
}

func TestValidateCaptionFormat_InvalidVTT(t *testing.T) {
	svc, _, _ := newTestService(t)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.vtt")
	err := os.WriteFile(tmpFile, []byte("not a vtt file content"), 0644)
	assert.NoError(t, err)

	err = svc.validateCaptionFormat(tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID_VTT")
}

func TestValidateCaptionFormat_ValidSRT(t *testing.T) {
	svc, _, _ := newTestService(t)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.srt")
	err := os.WriteFile(tmpFile, []byte("1\n00:00:00,000 --> 00:00:05,000\nHello"), 0644)
	assert.NoError(t, err)

	err = svc.validateCaptionFormat(tmpFile)
	assert.NoError(t, err)
}

func TestValidateCaptionFormat_InvalidSRT(t *testing.T) {
	svc, _, _ := newTestService(t)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.srt")
	err := os.WriteFile(tmpFile, []byte("binary\x00data"), 0644)
	assert.NoError(t, err)

	err = svc.validateCaptionFormat(tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "INVALID_SRT")
}

func TestValidateCaptionFormat_EmptyFile(t *testing.T) {
	svc, _, _ := newTestService(t)

	tmpDir := t.TempDir()
	tmpFile := filepath.Join(tmpDir, "test.vtt")
	err := os.WriteFile(tmpFile, []byte{}, 0644)
	assert.NoError(t, err)

	err = svc.validateCaptionFormat(tmpFile)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "EMPTY_FILE")
}

func TestValidateCaptionFormat_NonexistentFile(t *testing.T) {
	svc, _, _ := newTestService(t)

	err := svc.validateCaptionFormat("/nonexistent/path/test.vtt")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to open file for validation")
}

func TestSaveCaption_Success(t *testing.T) {
	svc, _, _ := newTestService(t)

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.vtt")
	reader := strings.NewReader("WEBVTT\n\n00:00:00.000 --> 00:00:05.000\nHello")

	written, err := svc.saveCaption(reader, filePath)
	assert.NoError(t, err)
	assert.Greater(t, written, int64(0))
	assert.FileExists(t, filePath)
}

func TestSaveCaption_EmptyReader(t *testing.T) {
	svc, _, _ := newTestService(t)

	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "test.vtt")
	reader := strings.NewReader("")

	written, err := svc.saveCaption(reader, filePath)
	assert.Error(t, err)
	assert.Equal(t, int64(0), written)
	assert.Contains(t, err.Error(), "EMPTY_FILE")
}

func TestGetCaptionByID_VideoRepoError(t *testing.T) {
	svc, capRepo, vidRepo := newTestService(t)

	captionID := uuid.New()
	videoID := uuid.New()
	caption := &domain.Caption{
		ID:           captionID,
		VideoID:      videoID,
		LanguageCode: "en",
		FileFormat:   domain.CaptionFormatVTT,
	}

	capRepo.On("GetByID", mock.Anything, captionID).Return(caption, nil)
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, errors.New("db error"))

	result, err := svc.GetCaptionByID(context.Background(), captionID)
	assert.NoError(t, err)
	assert.NotNil(t, result)
	assert.Empty(t, result.URL)
}
