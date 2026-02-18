package rating

import (
	"context"
	"errors"
	"testing"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// --- Mocks ---

type mockRatingRepo struct{ mock.Mock }

func (m *mockRatingRepo) SetRating(ctx context.Context, userID, videoID uuid.UUID, rating domain.RatingValue) error {
	return m.Called(ctx, userID, videoID, rating).Error(0)
}
func (m *mockRatingRepo) GetRating(ctx context.Context, userID, videoID uuid.UUID) (domain.RatingValue, error) {
	args := m.Called(ctx, userID, videoID)
	return args.Get(0).(domain.RatingValue), args.Error(1)
}
func (m *mockRatingRepo) RemoveRating(ctx context.Context, userID, videoID uuid.UUID) error {
	return m.Called(ctx, userID, videoID).Error(0)
}
func (m *mockRatingRepo) GetVideoRatingStats(ctx context.Context, videoID uuid.UUID, userID *uuid.UUID) (*domain.VideoRatingStats, error) {
	args := m.Called(ctx, videoID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoRatingStats), args.Error(1)
}
func (m *mockRatingRepo) GetUserRatings(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoRating), args.Error(1)
}
func (m *mockRatingRepo) GetVideoRatings(ctx context.Context, videoID uuid.UUID, limit, offset int) ([]*domain.VideoRating, error) {
	args := m.Called(ctx, videoID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoRating), args.Error(1)
}
func (m *mockRatingRepo) BatchGetVideoStats(ctx context.Context, videoIDs []uuid.UUID, userID *uuid.UUID) (map[uuid.UUID]*domain.VideoRatingStats, error) {
	args := m.Called(ctx, videoIDs, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[uuid.UUID]*domain.VideoRatingStats), args.Error(1)
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
	return args.Get(0).([]*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, userID, limit, offset)
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *mockVideoRepo) Update(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
}
func (m *mockVideoRepo) Delete(ctx context.Context, id, userID string) error {
	return m.Called(ctx, id, userID).Error(0)
}
func (m *mockVideoRepo) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *mockVideoRepo) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, req)
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

// --- Tests ---

func TestSetRating_Success(t *testing.T) {
	ratingRepo := new(mockRatingRepo)
	videoRepo := new(mockVideoRepo)
	svc := NewService(ratingRepo, videoRepo)

	userID := uuid.New()
	videoID := uuid.New()

	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)
	ratingRepo.On("SetRating", mock.Anything, userID, videoID, domain.RatingLike).Return(nil)

	err := svc.SetRating(context.Background(), userID, videoID, domain.RatingLike)
	assert.NoError(t, err)
	ratingRepo.AssertExpectations(t)
	videoRepo.AssertExpectations(t)
}

func TestSetRating_VideoNotFound(t *testing.T) {
	ratingRepo := new(mockRatingRepo)
	videoRepo := new(mockVideoRepo)
	svc := NewService(ratingRepo, videoRepo)

	userID := uuid.New()
	videoID := uuid.New()

	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, domain.ErrNotFound)

	err := svc.SetRating(context.Background(), userID, videoID, domain.RatingLike)
	assert.ErrorIs(t, err, domain.ErrNotFound)
}

func TestSetRating_InvalidRating(t *testing.T) {
	ratingRepo := new(mockRatingRepo)
	videoRepo := new(mockVideoRepo)
	svc := NewService(ratingRepo, videoRepo)

	userID := uuid.New()
	videoID := uuid.New()

	videoRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{ID: videoID.String()}, nil)

	err := svc.SetRating(context.Background(), userID, videoID, domain.RatingValue(99))
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid rating value")
}

func TestGetRating_Success(t *testing.T) {
	ratingRepo := new(mockRatingRepo)
	videoRepo := new(mockVideoRepo)
	svc := NewService(ratingRepo, videoRepo)

	userID := uuid.New()
	videoID := uuid.New()

	ratingRepo.On("GetRating", mock.Anything, userID, videoID).Return(domain.RatingLike, nil)

	rating, err := svc.GetRating(context.Background(), userID, videoID)
	assert.NoError(t, err)
	assert.Equal(t, domain.RatingLike, rating)
}

func TestGetRating_Error(t *testing.T) {
	ratingRepo := new(mockRatingRepo)
	videoRepo := new(mockVideoRepo)
	svc := NewService(ratingRepo, videoRepo)

	userID := uuid.New()
	videoID := uuid.New()

	ratingRepo.On("GetRating", mock.Anything, userID, videoID).Return(domain.RatingNone, errors.New("db error"))

	rating, err := svc.GetRating(context.Background(), userID, videoID)
	assert.Error(t, err)
	assert.Equal(t, domain.RatingNone, rating)
}

func TestRemoveRating_Success(t *testing.T) {
	ratingRepo := new(mockRatingRepo)
	videoRepo := new(mockVideoRepo)
	svc := NewService(ratingRepo, videoRepo)

	userID := uuid.New()
	videoID := uuid.New()

	ratingRepo.On("RemoveRating", mock.Anything, userID, videoID).Return(nil)

	err := svc.RemoveRating(context.Background(), userID, videoID)
	assert.NoError(t, err)
}

func TestGetVideoRatingStats_Success(t *testing.T) {
	ratingRepo := new(mockRatingRepo)
	videoRepo := new(mockVideoRepo)
	svc := NewService(ratingRepo, videoRepo)

	videoID := uuid.New()
	expected := &domain.VideoRatingStats{LikesCount: 10, DislikesCount: 2}

	ratingRepo.On("GetVideoRatingStats", mock.Anything, videoID, (*uuid.UUID)(nil)).Return(expected, nil)

	stats, err := svc.GetVideoRatingStats(context.Background(), videoID, nil)
	assert.NoError(t, err)
	assert.Equal(t, expected, stats)
}

func TestGetUserRatings_DefaultLimit(t *testing.T) {
	tests := []struct {
		name           string
		limit          int
		offset         int
		expectedLimit  int
		expectedOffset int
	}{
		{"zero limit defaults to 20", 0, 0, 20, 0},
		{"negative limit defaults to 20", -5, 0, 20, 0},
		{"over 100 defaults to 20", 200, 0, 20, 0},
		{"valid limit passes through", 50, 10, 50, 10},
		{"negative offset defaults to 0", 10, -1, 10, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ratingRepo := new(mockRatingRepo)
			videoRepo := new(mockVideoRepo)
			svc := NewService(ratingRepo, videoRepo)

			userID := uuid.New()
			ratingRepo.On("GetUserRatings", mock.Anything, userID, tt.expectedLimit, tt.expectedOffset).Return([]*domain.VideoRating{}, nil)

			_, err := svc.GetUserRatings(context.Background(), userID, tt.limit, tt.offset)
			assert.NoError(t, err)
			ratingRepo.AssertExpectations(t)
		})
	}
}
