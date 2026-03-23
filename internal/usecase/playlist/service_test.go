package playlist

import (
	"context"
	"errors"
	"testing"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockPlaylistRepo struct{ mock.Mock }

func (m *mockPlaylistRepo) Create(ctx context.Context, playlist *domain.Playlist) error {
	return m.Called(ctx, playlist).Error(0)
}
func (m *mockPlaylistRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Playlist, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Playlist), args.Error(1)
}
func (m *mockPlaylistRepo) Update(ctx context.Context, id uuid.UUID, updates domain.UpdatePlaylistRequest) error {
	return m.Called(ctx, id, updates).Error(0)
}
func (m *mockPlaylistRepo) Delete(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockPlaylistRepo) List(ctx context.Context, opts domain.PlaylistListOptions) ([]*domain.Playlist, int, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]*domain.Playlist), args.Int(1), args.Error(2)
}
func (m *mockPlaylistRepo) AddItem(ctx context.Context, playlistID, videoID uuid.UUID, position *int) error {
	return m.Called(ctx, playlistID, videoID, position).Error(0)
}
func (m *mockPlaylistRepo) RemoveItem(ctx context.Context, playlistID, itemID uuid.UUID) error {
	return m.Called(ctx, playlistID, itemID).Error(0)
}
func (m *mockPlaylistRepo) GetItems(ctx context.Context, playlistID uuid.UUID, limit, offset int) ([]*domain.PlaylistItem, error) {
	args := m.Called(ctx, playlistID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.PlaylistItem), args.Error(1)
}
func (m *mockPlaylistRepo) ReorderItem(ctx context.Context, playlistID, itemID uuid.UUID, newPosition int) error {
	return m.Called(ctx, playlistID, itemID, newPosition).Error(0)
}
func (m *mockPlaylistRepo) IsOwner(ctx context.Context, playlistID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, playlistID, userID)
	return args.Bool(0), args.Error(1)
}
func (m *mockPlaylistRepo) GetOrCreateWatchLater(ctx context.Context, userID uuid.UUID) (*domain.Playlist, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Playlist), args.Error(1)
}

type mockVideoRepo struct{ mock.Mock }

func (m *mockVideoRepo) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}
func (m *mockVideoRepo) Create(ctx context.Context, video *domain.Video) error {
	return m.Called(ctx, video).Error(0)
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

func TestCreatePlaylist_Success(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Playlist")).Return(nil)

	desc := "My playlist"
	req := &domain.CreatePlaylistRequest{
		Name:        "Favorites",
		Description: &desc,
		Privacy:     domain.PrivacyPublic,
	}

	pl, err := svc.CreatePlaylist(context.Background(), userID, req)
	assert.NoError(t, err)
	assert.Equal(t, "Favorites", pl.Name)
	assert.False(t, pl.IsWatchLater)
}

func TestGetPlaylist_Public(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	expected := &domain.Playlist{ID: plID, Privacy: domain.PrivacyPublic}
	plRepo.On("GetByID", mock.Anything, plID).Return(expected, nil)

	pl, err := svc.GetPlaylist(context.Background(), plID, nil)
	assert.NoError(t, err)
	assert.Equal(t, expected, pl)
}

func TestGetPlaylist_PrivateUnauthorized(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	ownerID := uuid.New()
	otherID := uuid.New()
	plID := uuid.New()

	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, UserID: ownerID, Privacy: domain.PrivacyPrivate,
	}, nil)

	pl, err := svc.GetPlaylist(context.Background(), plID, &otherID)
	assert.Error(t, err)
	assert.Nil(t, pl)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestGetPlaylist_PrivateOwnerAllowed(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	ownerID := uuid.New()
	plID := uuid.New()

	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, UserID: ownerID, Privacy: domain.PrivacyPrivate,
	}, nil)

	pl, err := svc.GetPlaylist(context.Background(), plID, &ownerID)
	assert.NoError(t, err)
	assert.NotNil(t, pl)
}

func TestUpdatePlaylist_NotOwner(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(false, nil)

	err := svc.UpdatePlaylist(context.Background(), userID, plID, domain.UpdatePlaylistRequest{})
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestUpdatePlaylist_CannotRenameWatchLater(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	newName := "Renamed"

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, IsWatchLater: true,
	}, nil)

	err := svc.UpdatePlaylist(context.Background(), userID, plID, domain.UpdatePlaylistRequest{Name: &newName})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot rename system playlist")
}

func TestDeletePlaylist_CannotDeleteWatchLater(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, IsWatchLater: true,
	}, nil)

	err := svc.DeletePlaylist(context.Background(), userID, plID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "cannot delete system playlist")
}

func TestDeletePlaylist_Success(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{ID: plID, IsWatchLater: false}, nil)
	plRepo.On("Delete", mock.Anything, plID).Return(nil)

	err := svc.DeletePlaylist(context.Background(), userID, plID)
	assert.NoError(t, err)
}

func TestListPlaylists_DefaultLimits(t *testing.T) {
	tests := []struct {
		name         string
		limit        int
		offset       int
		expectLimit  int
		expectOffset int
	}{
		{"zero limit defaults to 20", 0, 0, 20, 0},
		{"negative limit defaults to 20", -1, 0, 20, 0},
		{"over 100 defaults to 20", 150, 0, 20, 0},
		{"valid limit passes through", 50, 5, 50, 5},
		{"negative offset defaults to 0", 10, -5, 10, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			plRepo := new(mockPlaylistRepo)
			vidRepo := new(mockVideoRepo)
			svc := NewService(plRepo, vidRepo)

			plRepo.On("List", mock.Anything, mock.MatchedBy(func(opts domain.PlaylistListOptions) bool {
				return opts.Limit == tt.expectLimit && opts.Offset == tt.expectOffset
			})).Return([]*domain.Playlist{}, 0, nil)

			_, err := svc.ListPlaylists(context.Background(), domain.PlaylistListOptions{
				Limit:  tt.limit,
				Offset: tt.offset,
			})
			assert.NoError(t, err)
		})
	}
}

func TestAddVideoToPlaylist_Success(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	videoID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{}, nil)
	plRepo.On("AddItem", mock.Anything, plID, videoID, (*int)(nil)).Return(nil)

	err := svc.AddVideoToPlaylist(context.Background(), userID, plID, videoID, nil)
	assert.NoError(t, err)
}

func TestAddVideoToPlaylist_VideoNotFound(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	videoID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(nil, domain.ErrNotFound)

	err := svc.AddVideoToPlaylist(context.Background(), userID, plID, videoID, nil)
	assert.Error(t, err)
}

func TestRemoveVideoFromPlaylist_NotOwner(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	itemID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(false, nil)

	err := svc.RemoveVideoFromPlaylist(context.Background(), userID, plID, itemID)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestGetPlaylistItems_PrivateUnauthorized(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	ownerID := uuid.New()

	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, UserID: ownerID, Privacy: domain.PrivacyPrivate,
	}, nil)

	items, err := svc.GetPlaylistItems(context.Background(), plID, nil, 20, 0)
	assert.Error(t, err)
	assert.Nil(t, items)
}

func TestReorderPlaylistItem_NotOwner(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("IsOwner", mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	err := svc.ReorderPlaylistItem(context.Background(), uuid.New(), uuid.New(), uuid.New(), 1)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestGetOrCreateWatchLater_Success(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	expected := &domain.Playlist{ID: uuid.New(), IsWatchLater: true}
	plRepo.On("GetOrCreateWatchLater", mock.Anything, userID).Return(expected, nil)

	pl, err := svc.GetOrCreateWatchLater(context.Background(), userID)
	assert.NoError(t, err)
	assert.Equal(t, expected, pl)
}

func TestAddToWatchLater_Success(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	videoID := uuid.New()
	plID := uuid.New()
	watchLater := &domain.Playlist{ID: plID, UserID: userID, IsWatchLater: true}

	plRepo.On("GetOrCreateWatchLater", mock.Anything, userID).Return(watchLater, nil)
	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{}, nil)
	plRepo.On("AddItem", mock.Anything, plID, videoID, (*int)(nil)).Return(nil)

	err := svc.AddToWatchLater(context.Background(), userID, videoID)
	assert.NoError(t, err)
}

func TestAddToWatchLater_WatchLaterError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("GetOrCreateWatchLater", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))

	err := svc.AddToWatchLater(context.Background(), uuid.New(), uuid.New())
	assert.Error(t, err)
}

func TestCreatePlaylist_RepoError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Playlist")).Return(errors.New("db error"))

	req := &domain.CreatePlaylistRequest{Name: "Test", Privacy: domain.PrivacyPublic}
	pl, err := svc.CreatePlaylist(context.Background(), uuid.New(), req)
	assert.Error(t, err)
	assert.Nil(t, pl)
	assert.Contains(t, err.Error(), "failed to create playlist")
}

func TestGetPlaylist_NotFound(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	plRepo.On("GetByID", mock.Anything, plID).Return(nil, domain.ErrNotFound)

	pl, err := svc.GetPlaylist(context.Background(), plID, nil)
	assert.Error(t, err)
	assert.Nil(t, pl)
}

func TestGetPlaylist_PrivateNoUser(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, UserID: uuid.New(), Privacy: domain.PrivacyPrivate,
	}, nil)

	pl, err := svc.GetPlaylist(context.Background(), plID, nil)
	assert.Error(t, err)
	assert.Nil(t, pl)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestUpdatePlaylist_Success(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	newName := "Updated"

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{ID: plID, IsWatchLater: false}, nil)
	plRepo.On("Update", mock.Anything, plID, mock.Anything).Return(nil)

	err := svc.UpdatePlaylist(context.Background(), userID, plID, domain.UpdatePlaylistRequest{Name: &newName})
	assert.NoError(t, err)
}

func TestUpdatePlaylist_IsOwnerError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("IsOwner", mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("db error"))

	err := svc.UpdatePlaylist(context.Background(), uuid.New(), uuid.New(), domain.UpdatePlaylistRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check ownership")
}

func TestUpdatePlaylist_GetByIDError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("GetByID", mock.Anything, plID).Return(nil, errors.New("db error"))

	err := svc.UpdatePlaylist(context.Background(), userID, plID, domain.UpdatePlaylistRequest{})
	assert.Error(t, err)
}

func TestUpdatePlaylist_RepoUpdateError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{ID: plID, IsWatchLater: false}, nil)
	plRepo.On("Update", mock.Anything, plID, mock.Anything).Return(errors.New("db error"))

	err := svc.UpdatePlaylist(context.Background(), userID, plID, domain.UpdatePlaylistRequest{})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to update playlist")
}

func TestDeletePlaylist_NotOwner(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("IsOwner", mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	err := svc.DeletePlaylist(context.Background(), uuid.New(), uuid.New())
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestDeletePlaylist_IsOwnerError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("IsOwner", mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("db error"))

	err := svc.DeletePlaylist(context.Background(), uuid.New(), uuid.New())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check ownership")
}

func TestDeletePlaylist_GetByIDError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("GetByID", mock.Anything, plID).Return(nil, errors.New("db error"))

	err := svc.DeletePlaylist(context.Background(), userID, plID)
	assert.Error(t, err)
}

func TestDeletePlaylist_DeleteRepoError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{ID: plID, IsWatchLater: false}, nil)
	plRepo.On("Delete", mock.Anything, plID).Return(errors.New("db error"))

	err := svc.DeletePlaylist(context.Background(), userID, plID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete playlist")
}

func TestListPlaylists_RepoError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("List", mock.Anything, mock.Anything).Return(nil, 0, errors.New("db error"))

	resp, err := svc.ListPlaylists(context.Background(), domain.PlaylistListOptions{Limit: 10})
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestAddVideoToPlaylist_IsOwnerError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("IsOwner", mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("db error"))

	err := svc.AddVideoToPlaylist(context.Background(), uuid.New(), uuid.New(), uuid.New(), nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check ownership")
}

func TestAddVideoToPlaylist_NotOwner(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("IsOwner", mock.Anything, mock.Anything, mock.Anything).Return(false, nil)

	err := svc.AddVideoToPlaylist(context.Background(), uuid.New(), uuid.New(), uuid.New(), nil)
	assert.ErrorIs(t, err, domain.ErrUnauthorized)
}

func TestAddVideoToPlaylist_AddItemError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	videoID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	vidRepo.On("GetByID", mock.Anything, videoID.String()).Return(&domain.Video{}, nil)
	plRepo.On("AddItem", mock.Anything, plID, videoID, (*int)(nil)).Return(errors.New("db error"))

	err := svc.AddVideoToPlaylist(context.Background(), userID, plID, videoID, nil)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to add video to playlist")
}

func TestRemoveVideoFromPlaylist_Success(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	itemID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("RemoveItem", mock.Anything, plID, itemID).Return(nil)

	err := svc.RemoveVideoFromPlaylist(context.Background(), userID, plID, itemID)
	assert.NoError(t, err)
}

func TestRemoveVideoFromPlaylist_IsOwnerError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("IsOwner", mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("db error"))

	err := svc.RemoveVideoFromPlaylist(context.Background(), uuid.New(), uuid.New(), uuid.New())
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check ownership")
}

func TestRemoveVideoFromPlaylist_RemoveError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	itemID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("RemoveItem", mock.Anything, plID, itemID).Return(errors.New("db error"))

	err := svc.RemoveVideoFromPlaylist(context.Background(), userID, plID, itemID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to remove video from playlist")
}

func TestGetPlaylistItems_Success(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	ownerID := uuid.New()
	expected := []*domain.PlaylistItem{{ID: uuid.New()}}

	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, UserID: ownerID, Privacy: domain.PrivacyPublic,
	}, nil)
	plRepo.On("GetItems", mock.Anything, plID, 20, 0).Return(expected, nil)

	items, err := svc.GetPlaylistItems(context.Background(), plID, nil, 20, 0)
	assert.NoError(t, err)
	assert.Len(t, items, 1)
}

func TestGetPlaylistItems_DefaultLimits(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	ownerID := uuid.New()

	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, UserID: ownerID, Privacy: domain.PrivacyPublic,
	}, nil)
	plRepo.On("GetItems", mock.Anything, plID, 20, 0).Return([]*domain.PlaylistItem{}, nil)

	items, err := svc.GetPlaylistItems(context.Background(), plID, nil, -1, -5)
	assert.NoError(t, err)
	assert.Empty(t, items)
}

func TestGetPlaylistItems_OverLimit(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	ownerID := uuid.New()

	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, UserID: ownerID, Privacy: domain.PrivacyPublic,
	}, nil)
	plRepo.On("GetItems", mock.Anything, plID, 20, 0).Return([]*domain.PlaylistItem{}, nil)

	items, err := svc.GetPlaylistItems(context.Background(), plID, nil, 200, 0)
	assert.NoError(t, err)
	assert.Empty(t, items)
}

func TestGetPlaylistItems_NotFound(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	plRepo.On("GetByID", mock.Anything, plID).Return(nil, domain.ErrNotFound)

	items, err := svc.GetPlaylistItems(context.Background(), plID, nil, 20, 0)
	assert.Error(t, err)
	assert.Nil(t, items)
}

func TestGetPlaylistItems_GetItemsError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, Privacy: domain.PrivacyPublic,
	}, nil)
	plRepo.On("GetItems", mock.Anything, plID, 20, 0).Return(nil, errors.New("db error"))

	items, err := svc.GetPlaylistItems(context.Background(), plID, nil, 20, 0)
	assert.Error(t, err)
	assert.Nil(t, items)
	assert.Contains(t, err.Error(), "failed to get playlist items")
}

func TestGetPlaylistItems_PrivateOwnerAllowed(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plID := uuid.New()
	ownerID := uuid.New()

	plRepo.On("GetByID", mock.Anything, plID).Return(&domain.Playlist{
		ID: plID, UserID: ownerID, Privacy: domain.PrivacyPrivate,
	}, nil)
	plRepo.On("GetItems", mock.Anything, plID, 20, 0).Return([]*domain.PlaylistItem{}, nil)

	items, err := svc.GetPlaylistItems(context.Background(), plID, &ownerID, 20, 0)
	assert.NoError(t, err)
	assert.Empty(t, items)
}

func TestReorderPlaylistItem_Success(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	itemID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("ReorderItem", mock.Anything, plID, itemID, 3).Return(nil)

	err := svc.ReorderPlaylistItem(context.Background(), userID, plID, itemID, 3)
	assert.NoError(t, err)
}

func TestReorderPlaylistItem_IsOwnerError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("IsOwner", mock.Anything, mock.Anything, mock.Anything).Return(false, errors.New("db error"))

	err := svc.ReorderPlaylistItem(context.Background(), uuid.New(), uuid.New(), uuid.New(), 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to check ownership")
}

func TestReorderPlaylistItem_ReorderError(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	userID := uuid.New()
	plID := uuid.New()
	itemID := uuid.New()

	plRepo.On("IsOwner", mock.Anything, plID, userID).Return(true, nil)
	plRepo.On("ReorderItem", mock.Anything, plID, itemID, 1).Return(errors.New("db error"))

	err := svc.ReorderPlaylistItem(context.Background(), userID, plID, itemID, 1)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to reorder playlist item")
}

func TestGetOrCreateWatchLater_Error(t *testing.T) {
	plRepo := new(mockPlaylistRepo)
	vidRepo := new(mockVideoRepo)
	svc := NewService(plRepo, vidRepo)

	plRepo.On("GetOrCreateWatchLater", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))

	pl, err := svc.GetOrCreateWatchLater(context.Background(), uuid.New())
	assert.Error(t, err)
	assert.Nil(t, pl)
}
