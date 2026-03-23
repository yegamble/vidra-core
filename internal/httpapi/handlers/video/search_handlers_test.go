package video

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
)

// MockChannelRepo for search tests
type MockChannelRepoForSearch struct {
	mock.Mock
}

func (m *MockChannelRepoForSearch) Create(ctx context.Context, channel *domain.Channel) error {
	return m.Called(ctx, channel).Error(0)
}
func (m *MockChannelRepoForSearch) GetByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *MockChannelRepoForSearch) GetByHandle(ctx context.Context, handle string) (*domain.Channel, error) {
	args := m.Called(ctx, handle)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *MockChannelRepoForSearch) List(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error) {
	args := m.Called(ctx, params)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ChannelListResponse), args.Error(1)
}
func (m *MockChannelRepoForSearch) Update(ctx context.Context, id uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error) {
	args := m.Called(ctx, id, updates)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *MockChannelRepoForSearch) Delete(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}
func (m *MockChannelRepoForSearch) GetChannelsByAccountID(ctx context.Context, accountID uuid.UUID) ([]domain.Channel, error) {
	args := m.Called(ctx, accountID)
	return args.Get(0).([]domain.Channel), args.Error(1)
}
func (m *MockChannelRepoForSearch) GetDefaultChannelForAccount(ctx context.Context, accountID uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}
func (m *MockChannelRepoForSearch) CheckOwnership(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, channelID, userID)
	return args.Bool(0), args.Error(1)
}

var _ port.ChannelRepository = (*MockChannelRepoForSearch)(nil)

// MockPlaylistRepo for search tests
type MockPlaylistRepoForSearch struct {
	mock.Mock
}

func (m *MockPlaylistRepoForSearch) Create(ctx context.Context, playlist *domain.Playlist) error {
	return m.Called(ctx, playlist).Error(0)
}
func (m *MockPlaylistRepoForSearch) GetByID(ctx context.Context, id uuid.UUID) (*domain.Playlist, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Playlist), args.Error(1)
}
func (m *MockPlaylistRepoForSearch) Update(ctx context.Context, id uuid.UUID, updates domain.UpdatePlaylistRequest) error {
	return m.Called(ctx, id, updates).Error(0)
}
func (m *MockPlaylistRepoForSearch) Delete(ctx context.Context, id uuid.UUID) error {
	return m.Called(ctx, id).Error(0)
}
func (m *MockPlaylistRepoForSearch) List(ctx context.Context, opts domain.PlaylistListOptions) ([]*domain.Playlist, int, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]*domain.Playlist), args.Int(1), args.Error(2)
}
func (m *MockPlaylistRepoForSearch) AddItem(ctx context.Context, playlistID, videoID uuid.UUID, position *int) error {
	return m.Called(ctx, playlistID, videoID, position).Error(0)
}
func (m *MockPlaylistRepoForSearch) RemoveItem(ctx context.Context, playlistID, itemID uuid.UUID) error {
	return m.Called(ctx, playlistID, itemID).Error(0)
}
func (m *MockPlaylistRepoForSearch) GetItems(ctx context.Context, playlistID uuid.UUID, limit, offset int) ([]*domain.PlaylistItem, error) {
	args := m.Called(ctx, playlistID, limit, offset)
	return args.Get(0).([]*domain.PlaylistItem), args.Error(1)
}
func (m *MockPlaylistRepoForSearch) ReorderItem(ctx context.Context, playlistID, itemID uuid.UUID, newPosition int) error {
	return m.Called(ctx, playlistID, itemID, newPosition).Error(0)
}
func (m *MockPlaylistRepoForSearch) IsOwner(ctx context.Context, playlistID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, playlistID, userID)
	return args.Bool(0), args.Error(1)
}
func (m *MockPlaylistRepoForSearch) GetOrCreateWatchLater(ctx context.Context, userID uuid.UUID) (*domain.Playlist, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Playlist), args.Error(1)
}

var _ port.PlaylistRepository = (*MockPlaylistRepoForSearch)(nil)

func TestSearchChannelsHandler_ValidQuery(t *testing.T) {
	repo := &MockChannelRepoForSearch{}
	response := &domain.ChannelListResponse{
		Data:  []domain.Channel{{Name: "Test Channel"}},
		Total: 1,
	}
	repo.On("List", mock.Anything, mock.MatchedBy(func(p domain.ChannelListParams) bool {
		return p.Search == "test"
	})).Return(response, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/video-channels?search=test", nil)
	w := httptest.NewRecorder()

	SearchChannelsHandler(repo)(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	repo.AssertExpectations(t)
}

func TestSearchChannelsHandler_MissingQuery(t *testing.T) {
	repo := &MockChannelRepoForSearch{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/video-channels", nil)
	w := httptest.NewRecorder()

	SearchChannelsHandler(repo)(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestSearchPlaylistsHandler_ValidQuery(t *testing.T) {
	repo := &MockPlaylistRepoForSearch{}
	playlists := []*domain.Playlist{{Name: "Test Playlist"}}
	repo.On("List", mock.Anything, mock.MatchedBy(func(opts domain.PlaylistListOptions) bool {
		return opts.Search == "test"
	})).Return(playlists, 1, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/video-playlists?search=test", nil)
	w := httptest.NewRecorder()

	SearchPlaylistsHandler(repo)(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	repo.AssertExpectations(t)
}

func TestSearchPlaylistsHandler_MissingQuery(t *testing.T) {
	repo := &MockPlaylistRepoForSearch{}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/search/video-playlists", nil)
	w := httptest.NewRecorder()

	SearchPlaylistsHandler(repo)(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
