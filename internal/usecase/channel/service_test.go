package channel

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

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
func (m *mockVideoRepo) GetByChannelID(ctx context.Context, channelID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, channelID, limit, offset)
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
func (m *mockVideoRepo) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

type mockChannelRepo struct{ mock.Mock }

func (m *mockChannelRepo) Create(ctx context.Context, channel *domain.Channel) error {
	return m.Called(ctx, channel).Error(0)
}
func (m *mockChannelRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) GetByHandle(ctx context.Context, handle string) (*domain.Channel, error) {
	args := m.Called(ctx, handle)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) List(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ChannelListResponse), args.Error(1)
}
func (m *mockChannelRepo) Update(ctx context.Context, id uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error) {
	args := m.Called(ctx, id, updates)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) Delete(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockChannelRepo) GetChannelsByAccountID(ctx context.Context, accountID uuid.UUID) ([]domain.Channel, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) GetDefaultChannelForAccount(ctx context.Context, accountID uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *mockChannelRepo) CheckOwnership(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, channelID, userID)
	return args.Bool(0), args.Error(1)
}

type mockUserRepo struct{ mock.Mock }

func (m *mockUserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *mockUserRepo) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	return m.Called(ctx, user, passwordHash).Error(0)
}
func (m *mockUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *mockUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *mockUserRepo) Update(ctx context.Context, user *domain.User) error {
	return m.Called(ctx, user).Error(0)
}
func (m *mockUserRepo) Delete(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockUserRepo) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}
func (m *mockUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return m.Called(ctx, userID, passwordHash).Error(0)
}
func (m *mockUserRepo) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.User), args.Error(1)
}
func (m *mockUserRepo) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *mockUserRepo) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	return m.Called(ctx, userID, ipfsCID, webpCID).Error(0)
}
func (m *mockUserRepo) MarkEmailAsVerified(ctx context.Context, userID string) error {
	return m.Called(ctx, userID).Error(0)
}
func (m *mockUserRepo) Anonymize(_ context.Context, _ string) error { return nil }

func TestCreateChannel_Success(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	user := &domain.User{ID: userID.String(), Username: "testuser"}

	userRepo.On("GetByID", mock.Anything, userID.String()).Return(user, nil)
	chRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Channel")).Return(nil)

	desc := "Test channel"
	req := domain.ChannelCreateRequest{
		Handle:      "test_handle",
		DisplayName: "Test Channel",
		Description: &desc,
	}

	ch, err := svc.CreateChannel(context.Background(), userID, req)
	assert.NoError(t, err)
	assert.NotNil(t, ch)
	assert.Equal(t, "test_handle", ch.Handle)
	assert.Equal(t, user, ch.Account)
}

func TestCreateChannel_UserNotFound(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	userRepo.On("GetByID", mock.Anything, userID.String()).Return(nil, domain.ErrNotFound)

	req := domain.ChannelCreateRequest{
		Handle:      "test",
		DisplayName: "Test",
	}

	ch, err := svc.CreateChannel(context.Background(), userID, req)
	assert.Error(t, err)
	assert.Nil(t, ch)
}

func TestGetChannel_Success(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	id := uuid.New()
	expected := &domain.Channel{ID: id, Handle: "test"}
	chRepo.On("GetByID", mock.Anything, id).Return(expected, nil)

	ch, err := svc.GetChannel(context.Background(), id)
	assert.NoError(t, err)
	assert.Equal(t, expected, ch)
}

func TestGetChannelByHandle_Success(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	expected := &domain.Channel{Handle: "test_handle"}
	chRepo.On("GetByHandle", mock.Anything, "test_handle").Return(expected, nil)

	ch, err := svc.GetChannelByHandle(context.Background(), "test_handle")
	assert.NoError(t, err)
	assert.Equal(t, expected, ch)
}

func TestUpdateChannel_Success(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	channelID := uuid.New()
	updated := &domain.Channel{ID: channelID, DisplayName: "Updated"}

	displayName := "Updated"
	updateReq := domain.ChannelUpdateRequest{DisplayName: &displayName}
	chRepo.On("CheckOwnership", mock.Anything, channelID, userID).Return(true, nil)
	chRepo.On("Update", mock.Anything, channelID, mock.Anything).Return(updated, nil)

	ch, err := svc.UpdateChannel(context.Background(), userID, channelID, updateReq)
	assert.NoError(t, err)
	assert.Equal(t, "Updated", ch.DisplayName)
}

func TestUpdateChannel_NotOwner(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	channelID := uuid.New()
	displayName := "Test"

	chRepo.On("CheckOwnership", mock.Anything, channelID, userID).Return(false, nil)

	ch, err := svc.UpdateChannel(context.Background(), userID, channelID, domain.ChannelUpdateRequest{DisplayName: &displayName})
	assert.Error(t, err)
	assert.Nil(t, ch)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestDeleteChannel_Success(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	channelID := uuid.New()

	chRepo.On("CheckOwnership", mock.Anything, channelID, userID).Return(true, nil)
	chRepo.On("GetChannelsByAccountID", mock.Anything, userID).Return([]domain.Channel{
		{ID: channelID},
		{ID: uuid.New()},
	}, nil)
	chRepo.On("Delete", mock.Anything, channelID).Return(nil)

	err := svc.DeleteChannel(context.Background(), userID, channelID)
	assert.NoError(t, err)
}

func TestDeleteChannel_LastChannel(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	channelID := uuid.New()

	chRepo.On("CheckOwnership", mock.Anything, channelID, userID).Return(true, nil)
	chRepo.On("GetChannelsByAccountID", mock.Anything, userID).Return([]domain.Channel{
		{ID: channelID},
	}, nil)

	err := svc.DeleteChannel(context.Background(), userID, channelID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot delete the last channel")
}

func TestDeleteChannel_NotOwner(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	channelID := uuid.New()

	chRepo.On("CheckOwnership", mock.Anything, channelID, userID).Return(false, nil)

	err := svc.DeleteChannel(context.Background(), userID, channelID)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestEnsureDefaultChannel_ExistingChannel(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	existing := &domain.Channel{ID: uuid.New(), Handle: "existing"}

	chRepo.On("GetDefaultChannelForAccount", mock.Anything, userID).Return(existing, nil)

	ch, err := svc.EnsureDefaultChannel(context.Background(), userID)
	assert.NoError(t, err)
	assert.Equal(t, existing, ch)
}

func TestEnsureDefaultChannel_CreatesNew(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	user := &domain.User{ID: userID.String(), Username: "testuser", DisplayName: "Test User"}

	chRepo.On("GetDefaultChannelForAccount", mock.Anything, userID).Return(nil, domain.ErrNotFound)
	userRepo.On("GetByID", mock.Anything, userID.String()).Return(user, nil)
	chRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Channel")).Return(nil)

	ch, err := svc.EnsureDefaultChannel(context.Background(), userID)
	assert.NoError(t, err)
	assert.NotNil(t, ch)
	assert.Equal(t, "testuser_channel", ch.Handle)
}

func TestEnsureDefaultChannel_OtherError(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	chRepo.On("GetDefaultChannelForAccount", mock.Anything, userID).Return(nil, errors.New("db error"))

	ch, err := svc.EnsureDefaultChannel(context.Background(), userID)
	assert.Error(t, err)
	assert.Nil(t, ch)
}

func TestGetChannelVideos_NilVideoRepo(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	resp, err := svc.GetChannelVideos(context.Background(), uuid.New(), 1, 20)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 0, resp.Total)
	assert.Empty(t, resp.Data)
}

func TestGetChannelVideos_WithVideoRepo(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	videoRepo := new(mockVideoRepo)
	svc := NewService(chRepo, userRepo, videoRepo)

	channelID := uuid.New()
	expectedVideo := &domain.Video{ID: uuid.New().String(), Title: "Test Video"}
	videoRepo.On("GetByChannelID", mock.Anything, channelID.String(), 20, 0).
		Return([]*domain.Video{expectedVideo}, int64(1), nil)

	resp, err := svc.GetChannelVideos(context.Background(), channelID, 1, 20)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Equal(t, 1, resp.Total)
	assert.Len(t, resp.Data, 1)
	assert.Equal(t, "Test Video", resp.Data[0].Title)
	videoRepo.AssertExpectations(t)
}

func TestListChannels_Success(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	expected := &domain.ChannelListResponse{Total: 2}
	params := domain.ChannelListParams{Page: 1, PageSize: 10}
	chRepo.On("List", mock.Anything, params).Return(expected, nil)

	resp, err := svc.ListChannels(context.Background(), params)
	assert.NoError(t, err)
	assert.Equal(t, expected, resp)
}

func TestListChannels_Error(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	params := domain.ChannelListParams{}
	chRepo.On("List", mock.Anything, params).Return(nil, errors.New("db error"))

	resp, err := svc.ListChannels(context.Background(), params)
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestGetUserChannels_Success(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	expected := []domain.Channel{{ID: uuid.New(), Handle: "ch1"}}
	chRepo.On("GetChannelsByAccountID", mock.Anything, userID).Return(expected, nil)

	channels, err := svc.GetUserChannels(context.Background(), userID)
	assert.NoError(t, err)
	assert.Len(t, channels, 1)
}

func TestGetUserChannels_Error(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	chRepo.On("GetChannelsByAccountID", mock.Anything, userID).Return(nil, errors.New("db error"))

	channels, err := svc.GetUserChannels(context.Background(), userID)
	assert.Error(t, err)
	assert.Nil(t, channels)
}

func TestUpdateChannel_CheckOwnershipError(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	channelID := uuid.New()
	displayName := "Test"

	chRepo.On("CheckOwnership", mock.Anything, channelID, userID).Return(false, errors.New("db error"))

	ch, err := svc.UpdateChannel(context.Background(), userID, channelID, domain.ChannelUpdateRequest{DisplayName: &displayName})
	assert.Error(t, err)
	assert.Nil(t, ch)
	assert.Contains(t, err.Error(), "failed to check channel ownership")
}

func TestDeleteChannel_CheckOwnershipError(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	channelID := uuid.New()

	chRepo.On("CheckOwnership", mock.Anything, channelID, userID).Return(false, errors.New("db error"))

	err := svc.DeleteChannel(context.Background(), userID, channelID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check channel ownership")
}

func TestDeleteChannel_GetChannelsError(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	channelID := uuid.New()

	chRepo.On("CheckOwnership", mock.Anything, channelID, userID).Return(true, nil)
	chRepo.On("GetChannelsByAccountID", mock.Anything, userID).Return(nil, errors.New("db error"))

	err := svc.DeleteChannel(context.Background(), userID, channelID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get user channels")
}

func TestCreateChannel_InvalidRequest(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	req := domain.ChannelCreateRequest{
		Handle:      "",
		DisplayName: "",
	}

	ch, err := svc.CreateChannel(context.Background(), uuid.New(), req)
	assert.Error(t, err)
	assert.Nil(t, ch)
}

func TestUpdateChannel_InvalidRequest(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	emptyDesc := ""
	req := domain.ChannelUpdateRequest{Description: &emptyDesc}

	userID := uuid.New()
	channelID := uuid.New()
	chRepo.On("CheckOwnership", mock.Anything, channelID, userID).Return(true, nil)
	chRepo.On("Update", mock.Anything, channelID, mock.Anything).Return(&domain.Channel{}, nil)

	_, _ = svc.UpdateChannel(context.Background(), userID, channelID, req)
}

func TestEnsureDefaultChannel_EmptyDisplayName(t *testing.T) {
	chRepo := new(mockChannelRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(chRepo, userRepo, nil)

	userID := uuid.New()
	user := &domain.User{ID: userID.String(), Username: "testuser", DisplayName: ""}

	chRepo.On("GetDefaultChannelForAccount", mock.Anything, userID).Return(nil, domain.ErrNotFound)
	userRepo.On("GetByID", mock.Anything, userID.String()).Return(user, nil)
	chRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Channel")).Return(nil)

	ch, err := svc.EnsureDefaultChannel(context.Background(), userID)
	assert.NoError(t, err)
	assert.NotNil(t, ch)
	assert.Contains(t, ch.DisplayName, "testuser")
}
