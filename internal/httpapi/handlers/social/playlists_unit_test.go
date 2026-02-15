package social

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type mockPlaylistService struct {
	createPlaylistFn          func(ctx context.Context, userID uuid.UUID, req *domain.CreatePlaylistRequest) (*domain.Playlist, error)
	getPlaylistFn             func(ctx context.Context, playlistID uuid.UUID, userID *uuid.UUID) (*domain.Playlist, error)
	updatePlaylistFn          func(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, req domain.UpdatePlaylistRequest) error
	deletePlaylistFn          func(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID) error
	listPlaylistsFn           func(ctx context.Context, opts domain.PlaylistListOptions) (*domain.PlaylistListResponse, error)
	addVideoToPlaylistFn      func(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, videoID uuid.UUID, position *int) error
	removeVideoFromPlaylistFn func(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, itemID uuid.UUID) error
	getPlaylistItemsFn        func(ctx context.Context, playlistID uuid.UUID, userID *uuid.UUID, limit, offset int) ([]domain.PlaylistItem, error)
	reorderPlaylistItemFn     func(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, itemID uuid.UUID, newPosition int) error
	getOrCreateWatchLaterFn   func(ctx context.Context, userID uuid.UUID) (*domain.Playlist, error)
	addToWatchLaterFn         func(ctx context.Context, userID uuid.UUID, videoID uuid.UUID) error
}

func (m *mockPlaylistService) CreatePlaylist(ctx context.Context, userID uuid.UUID, req *domain.CreatePlaylistRequest) (*domain.Playlist, error) {
	return m.createPlaylistFn(ctx, userID, req)
}

func (m *mockPlaylistService) GetPlaylist(ctx context.Context, playlistID uuid.UUID, userID *uuid.UUID) (*domain.Playlist, error) {
	return m.getPlaylistFn(ctx, playlistID, userID)
}

func (m *mockPlaylistService) UpdatePlaylist(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, req domain.UpdatePlaylistRequest) error {
	return m.updatePlaylistFn(ctx, userID, playlistID, req)
}

func (m *mockPlaylistService) DeletePlaylist(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID) error {
	return m.deletePlaylistFn(ctx, userID, playlistID)
}

func (m *mockPlaylistService) ListPlaylists(ctx context.Context, opts domain.PlaylistListOptions) (*domain.PlaylistListResponse, error) {
	return m.listPlaylistsFn(ctx, opts)
}

func (m *mockPlaylistService) AddVideoToPlaylist(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, videoID uuid.UUID, position *int) error {
	return m.addVideoToPlaylistFn(ctx, userID, playlistID, videoID, position)
}

func (m *mockPlaylistService) RemoveVideoFromPlaylist(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, itemID uuid.UUID) error {
	return m.removeVideoFromPlaylistFn(ctx, userID, playlistID, itemID)
}

func (m *mockPlaylistService) GetPlaylistItems(ctx context.Context, playlistID uuid.UUID, userID *uuid.UUID, limit, offset int) ([]domain.PlaylistItem, error) {
	return m.getPlaylistItemsFn(ctx, playlistID, userID, limit, offset)
}

func (m *mockPlaylistService) ReorderPlaylistItem(ctx context.Context, userID uuid.UUID, playlistID uuid.UUID, itemID uuid.UUID, newPosition int) error {
	return m.reorderPlaylistItemFn(ctx, userID, playlistID, itemID, newPosition)
}

func (m *mockPlaylistService) GetOrCreateWatchLater(ctx context.Context, userID uuid.UUID) (*domain.Playlist, error) {
	return m.getOrCreateWatchLaterFn(ctx, userID)
}

func (m *mockPlaylistService) AddToWatchLater(ctx context.Context, userID uuid.UUID, videoID uuid.UUID) error {
	return m.addToWatchLaterFn(ctx, userID, videoID)
}

func TestCreatePlaylist_Success(t *testing.T) {
	userID := uuid.New()
	description := "Test Description"
	expectedPlaylist := &domain.Playlist{
		ID:          uuid.New(),
		Name:        "Test Playlist",
		Description: &description,
		UserID:      userID,
	}

	mockService := &mockPlaylistService{
		createPlaylistFn: func(ctx context.Context, uid uuid.UUID, req *domain.CreatePlaylistRequest) (*domain.Playlist, error) {
			assert.Equal(t, userID, uid)
			assert.Equal(t, "Test Playlist", req.Name)
			return expectedPlaylist, nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	body := `{"name":"Test Playlist","description":"Test Description"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists", bytes.NewBufferString(body))
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))
	rec := httptest.NewRecorder()

	handler.CreatePlaylist(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)

	var wrapper struct {
		Data    domain.Playlist `json:"data"`
		Success bool            `json:"success"`
	}
	err := json.NewDecoder(rec.Body).Decode(&wrapper)
	require.NoError(t, err)
	assert.True(t, wrapper.Success)
	assert.Equal(t, expectedPlaylist.ID, wrapper.Data.ID)
}

func TestCreatePlaylist_Unauthorized(t *testing.T) {
	handler := NewPlaylistHandlers(&mockPlaylistService{})

	body := `{"name":"Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler.CreatePlaylist(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestCreatePlaylist_InvalidJSON(t *testing.T) {
	userID := uuid.New()
	handler := NewPlaylistHandlers(&mockPlaylistService{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists", bytes.NewBufferString("invalid json"))
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))
	rec := httptest.NewRecorder()

	handler.CreatePlaylist(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestCreatePlaylist_ServiceError(t *testing.T) {
	userID := uuid.New()
	mockService := &mockPlaylistService{
		createPlaylistFn: func(ctx context.Context, uid uuid.UUID, req *domain.CreatePlaylistRequest) (*domain.Playlist, error) {
			return nil, errors.New("database error")
		},
	}

	handler := NewPlaylistHandlers(mockService)

	body := `{"name":"Test"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists", bytes.NewBufferString(body))
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))
	rec := httptest.NewRecorder()

	handler.CreatePlaylist(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code)
}

func TestGetPlaylist_Success(t *testing.T) {
	playlistID := uuid.New()
	userID := uuid.New()
	expectedPlaylist := &domain.Playlist{
		ID:     playlistID,
		Name:   "Test Playlist",
		UserID: userID,
	}

	mockService := &mockPlaylistService{
		getPlaylistFn: func(ctx context.Context, pid uuid.UUID, uid *uuid.UUID) (*domain.Playlist, error) {
			assert.Equal(t, playlistID, pid)
			assert.NotNil(t, uid)
			assert.Equal(t, userID, *uid)
			return expectedPlaylist, nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/"+playlistID.String(), nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GetPlaylist(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper struct {
		Data    domain.Playlist `json:"data"`
		Success bool            `json:"success"`
	}
	err := json.NewDecoder(rec.Body).Decode(&wrapper)
	require.NoError(t, err)
	assert.True(t, wrapper.Success)
	assert.Equal(t, expectedPlaylist.ID, wrapper.Data.ID)
}

func TestGetPlaylist_InvalidID(t *testing.T) {
	handler := NewPlaylistHandlers(&mockPlaylistService{})

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/invalid", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GetPlaylist(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetPlaylist_NotFound(t *testing.T) {
	playlistID := uuid.New()
	mockService := &mockPlaylistService{
		getPlaylistFn: func(ctx context.Context, pid uuid.UUID, uid *uuid.UUID) (*domain.Playlist, error) {
			return nil, domain.ErrNotFound
		},
	}

	handler := NewPlaylistHandlers(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/"+playlistID.String(), nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GetPlaylist(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGetPlaylist_Unauthorized(t *testing.T) {
	playlistID := uuid.New()
	mockService := &mockPlaylistService{
		getPlaylistFn: func(ctx context.Context, pid uuid.UUID, uid *uuid.UUID) (*domain.Playlist, error) {
			return nil, domain.ErrUnauthorized
		},
	}

	handler := NewPlaylistHandlers(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/"+playlistID.String(), nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GetPlaylist(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestUpdatePlaylist_Success(t *testing.T) {
	userID := uuid.New()
	playlistID := uuid.New()

	mockService := &mockPlaylistService{
		updatePlaylistFn: func(ctx context.Context, uid uuid.UUID, pid uuid.UUID, req domain.UpdatePlaylistRequest) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, playlistID, pid)
			return nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	body := `{"name":"Updated Name"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/"+playlistID.String(), bytes.NewBufferString(body))
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.UpdatePlaylist(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestUpdatePlaylist_Unauthorized(t *testing.T) {
	playlistID := uuid.New()
	handler := NewPlaylistHandlers(&mockPlaylistService{})

	body := `{"name":"Updated"}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/"+playlistID.String(), bytes.NewBufferString(body))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.UpdatePlaylist(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestDeletePlaylist_Success(t *testing.T) {
	userID := uuid.New()
	playlistID := uuid.New()

	mockService := &mockPlaylistService{
		deletePlaylistFn: func(ctx context.Context, uid uuid.UUID, pid uuid.UUID) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, playlistID, pid)
			return nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/"+playlistID.String(), nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.DeletePlaylist(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestListPlaylists_Success(t *testing.T) {
	expectedResponse := &domain.PlaylistListResponse{
		Playlists: []*domain.Playlist{
			{ID: uuid.New(), Name: "Playlist 1"},
			{ID: uuid.New(), Name: "Playlist 2"},
		},
		Total: 2,
	}

	mockService := &mockPlaylistService{
		listPlaylistsFn: func(ctx context.Context, opts domain.PlaylistListOptions) (*domain.PlaylistListResponse, error) {
			assert.Equal(t, 20, opts.Limit)
			assert.Equal(t, 0, opts.Offset)
			return expectedResponse, nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists", nil)
	rec := httptest.NewRecorder()

	handler.ListPlaylists(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper struct {
		Data    domain.PlaylistListResponse `json:"data"`
		Success bool                        `json:"success"`
	}
	err := json.NewDecoder(rec.Body).Decode(&wrapper)
	require.NoError(t, err)
	assert.True(t, wrapper.Success)
	assert.Equal(t, 2, wrapper.Data.Total)
}

func TestAddVideoToPlaylist_Success(t *testing.T) {
	userID := uuid.New()
	playlistID := uuid.New()
	videoID := uuid.New()

	mockService := &mockPlaylistService{
		addVideoToPlaylistFn: func(ctx context.Context, uid uuid.UUID, pid uuid.UUID, vid uuid.UUID, position *int) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, playlistID, pid)
			assert.Equal(t, videoID, vid)
			return nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	body := `{"video_id":"` + videoID.String() + `"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/playlists/"+playlistID.String()+"/items", bytes.NewBufferString(body))
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.AddVideoToPlaylist(rec, req)

	assert.Equal(t, http.StatusCreated, rec.Code)
}

func TestRemoveVideoFromPlaylist_Success(t *testing.T) {
	userID := uuid.New()
	playlistID := uuid.New()
	itemID := uuid.New()

	mockService := &mockPlaylistService{
		removeVideoFromPlaylistFn: func(ctx context.Context, uid uuid.UUID, pid uuid.UUID, iid uuid.UUID) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, playlistID, pid)
			assert.Equal(t, itemID, iid)
			return nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/playlists/"+playlistID.String()+"/items/"+itemID.String(), nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	rctx.URLParams.Add("itemId", itemID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.RemoveVideoFromPlaylist(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetPlaylistItems_Success(t *testing.T) {
	playlistID := uuid.New()
	expectedItems := []domain.PlaylistItem{
		{ID: uuid.New(), PlaylistID: playlistID},
	}

	mockService := &mockPlaylistService{
		getPlaylistItemsFn: func(ctx context.Context, pid uuid.UUID, uid *uuid.UUID, limit, offset int) ([]domain.PlaylistItem, error) {
			assert.Equal(t, playlistID, pid)
			return expectedItems, nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/playlists/"+playlistID.String()+"/items", nil)

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GetPlaylistItems(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestReorderPlaylistItem_Success(t *testing.T) {
	userID := uuid.New()
	playlistID := uuid.New()
	itemID := uuid.New()

	mockService := &mockPlaylistService{
		reorderPlaylistItemFn: func(ctx context.Context, uid uuid.UUID, pid uuid.UUID, iid uuid.UUID, newPosition int) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, playlistID, pid)
			assert.Equal(t, itemID, iid)
			assert.Equal(t, 5, newPosition)
			return nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	body := `{"new_position":5}`
	req := httptest.NewRequest(http.MethodPut, "/api/v1/playlists/"+playlistID.String()+"/items/"+itemID.String()+"/reorder", bytes.NewBufferString(body))
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", playlistID.String())
	rctx.URLParams.Add("itemId", itemID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.ReorderPlaylistItem(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetWatchLater_Success(t *testing.T) {
	userID := uuid.New()
	expectedPlaylist := &domain.Playlist{
		ID:     uuid.New(),
		Name:   "Watch Later",
		UserID: userID,
	}

	mockService := &mockPlaylistService{
		getOrCreateWatchLaterFn: func(ctx context.Context, uid uuid.UUID) (*domain.Playlist, error) {
			assert.Equal(t, userID, uid)
			return expectedPlaylist, nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/users/me/watch-later", nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))
	rec := httptest.NewRecorder()

	handler.GetWatchLater(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestAddToWatchLater_Success(t *testing.T) {
	userID := uuid.New()
	videoID := uuid.New()

	mockService := &mockPlaylistService{
		addToWatchLaterFn: func(ctx context.Context, uid uuid.UUID, vid uuid.UUID) error {
			assert.Equal(t, userID, uid)
			assert.Equal(t, videoID, vid)
			return nil
		},
	}

	handler := NewPlaylistHandlers(mockService)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/watch-later", nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.AddToWatchLater(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}
