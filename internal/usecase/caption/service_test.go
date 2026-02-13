package caption

import (
	"context"
	"errors"
	"testing"

	"athena/internal/config"
	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

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

// --- Helper ---

func newTestService(t *testing.T) (*Service, *mockCaptionRepo, *mockVideoRepo) {
	t.Helper()
	capRepo := new(mockCaptionRepo)
	vidRepo := new(mockVideoRepo)
	cfg := &config.Config{StorageDir: t.TempDir()}
	svc := NewService(capRepo, vidRepo, cfg)
	return svc, capRepo, vidRepo
}

// --- Tests ---

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
		FilePath: nil, // No file to delete
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
		LanguageCode: "x", // Too short
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
