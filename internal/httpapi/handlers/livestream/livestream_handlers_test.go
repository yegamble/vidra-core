package livestream

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/livestream"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

type MockLiveStreamRepository struct {
	mock.Mock
}

func (m *MockLiveStreamRepository) Create(ctx context.Context, stream *domain.LiveStream) error {
	args := m.Called(ctx, stream)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.LiveStream, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) GetByStreamKey(ctx context.Context, streamKey string) (*domain.LiveStream, error) {
	args := m.Called(ctx, streamKey)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) GetByChannelID(ctx context.Context, channelID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, channelID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) GetActiveStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) Update(ctx context.Context, stream *domain.LiveStream) error {
	args := m.Called(ctx, stream)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) UpdateViewerCount(ctx context.Context, id uuid.UUID, count int) error {
	args := m.Called(ctx, id, count)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) EndStream(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) CountByChannelID(ctx context.Context, channelID uuid.UUID) (int, error) {
	args := m.Called(ctx, channelID)
	return args.Int(0), args.Error(1)
}

func (m *MockLiveStreamRepository) GetByUserID(ctx context.Context, userID uuid.UUID, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockLiveStreamRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status string) error {
	args := m.Called(ctx, id, status)
	return args.Error(0)
}

func (m *MockLiveStreamRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}
func (m *MockLiveStreamRepository) GetChannelByStreamID(_ context.Context, _ uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}
func (m *MockLiveStreamRepository) UpdateWaitingRoom(_ context.Context, _ uuid.UUID, _ bool, _ string) error {
	return nil
}
func (m *MockLiveStreamRepository) ScheduleStream(_ context.Context, _ uuid.UUID, _ *time.Time, _ *time.Time, _ bool, _ string) error {
	return nil
}
func (m *MockLiveStreamRepository) CancelSchedule(_ context.Context, _ uuid.UUID) error {
	return nil
}
func (m *MockLiveStreamRepository) GetScheduledStreams(_ context.Context, _, _ int) ([]*domain.LiveStream, error) {
	return nil, nil
}
func (m *MockLiveStreamRepository) GetUpcomingStreams(_ context.Context, _ uuid.UUID, _ int) ([]*domain.LiveStream, error) {
	return nil, nil
}

type MockStreamKeyRepository struct {
	mock.Mock
}

func (m *MockStreamKeyRepository) Create(ctx context.Context, channelID uuid.UUID, keyPlaintext string, expiresAt *time.Time) (*domain.StreamKey, error) {
	args := m.Called(ctx, channelID, keyPlaintext, expiresAt)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.StreamKey), args.Error(1)
}

func (m *MockStreamKeyRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.StreamKey, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.StreamKey), args.Error(1)
}

func (m *MockStreamKeyRepository) GetActiveByChannelID(ctx context.Context, channelID uuid.UUID) (*domain.StreamKey, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.StreamKey), args.Error(1)
}

func (m *MockStreamKeyRepository) ValidateKey(ctx context.Context, channelID uuid.UUID, keyPlaintext string) (*domain.StreamKey, error) {
	args := m.Called(ctx, channelID, keyPlaintext)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.StreamKey), args.Error(1)
}

func (m *MockStreamKeyRepository) DeactivateAllForChannel(ctx context.Context, channelID uuid.UUID) error {
	args := m.Called(ctx, channelID)
	return args.Error(0)
}

func (m *MockStreamKeyRepository) GetByChannelID(ctx context.Context, channelID uuid.UUID) (*domain.StreamKey, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.StreamKey), args.Error(1)
}

func (m *MockStreamKeyRepository) Deactivate(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockStreamKeyRepository) MarkUsed(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockStreamKeyRepository) DeleteExpired(ctx context.Context) (int, error) {
	args := m.Called(ctx)
	return args.Int(0), args.Error(1)
}

type MockViewerSessionRepository struct {
	mock.Mock
}

func (m *MockViewerSessionRepository) Create(ctx context.Context, session *domain.ViewerSession) error {
	args := m.Called(ctx, session)
	return args.Error(0)
}

func (m *MockViewerSessionRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.ViewerSession, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ViewerSession), args.Error(1)
}

func (m *MockViewerSessionRepository) GetBySessionID(ctx context.Context, sessionID string) (*domain.ViewerSession, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ViewerSession), args.Error(1)
}

func (m *MockViewerSessionRepository) UpdateHeartbeat(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func (m *MockViewerSessionRepository) EndSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}

func (m *MockViewerSessionRepository) CountActiveViewers(ctx context.Context, streamID uuid.UUID) (int, error) {
	args := m.Called(ctx, streamID)
	return args.Int(0), args.Error(1)
}

func (m *MockViewerSessionRepository) GetActiveByStream(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.ViewerSession, error) {
	args := m.Called(ctx, streamID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.ViewerSession), args.Error(1)
}

func (m *MockViewerSessionRepository) CleanupStale(ctx context.Context) (int, error) {
	args := m.Called(ctx)
	return args.Int(0), args.Error(1)
}

type MockStreamManager struct {
	mock.Mock
}

func (m *MockStreamManager) StartStream(ctx context.Context, streamID uuid.UUID) error {
	args := m.Called(ctx, streamID)
	return args.Error(0)
}

func (m *MockStreamManager) EndStream(ctx context.Context, streamID uuid.UUID) error {
	args := m.Called(ctx, streamID)
	return args.Error(0)
}

func (m *MockStreamManager) GetStreamState(streamID uuid.UUID) (*livestream.StreamState, bool) {
	args := m.Called(streamID)
	if args.Get(0) == nil {
		return nil, args.Bool(1)
	}
	return args.Get(0).(*livestream.StreamState), args.Bool(1)
}

func (m *MockStreamManager) GetActiveStreamCount() int {
	args := m.Called()
	return args.Int(0)
}

type MockChannelRepository struct {
	mock.Mock
}

func (m *MockChannelRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, id)
	if args.Get(0) != nil {
		return args.Get(0).(*domain.Channel), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockChannelRepository) Create(ctx context.Context, channel *domain.Channel) error {
	args := m.Called(ctx, channel)
	return args.Error(0)
}

func (m *MockChannelRepository) GetByHandle(ctx context.Context, handle string) (*domain.Channel, error) {
	args := m.Called(ctx, handle)
	if args.Get(0) != nil {
		return args.Get(0).(*domain.Channel), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockChannelRepository) List(ctx context.Context, params domain.ChannelListParams) (*domain.ChannelListResponse, error) {
	args := m.Called(ctx, params)
	if args.Get(0) != nil {
		return args.Get(0).(*domain.ChannelListResponse), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockChannelRepository) Update(ctx context.Context, id uuid.UUID, updates domain.ChannelUpdateRequest) (*domain.Channel, error) {
	args := m.Called(ctx, id, updates)
	if args.Get(0) != nil {
		return args.Get(0).(*domain.Channel), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockChannelRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockChannelRepository) GetChannelsByAccountID(ctx context.Context, accountID uuid.UUID) ([]domain.Channel, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) != nil {
		return args.Get(0).([]domain.Channel), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockChannelRepository) GetDefaultChannelForAccount(ctx context.Context, accountID uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, accountID)
	if args.Get(0) != nil {
		return args.Get(0).(*domain.Channel), args.Error(1)
	}
	return nil, args.Error(1)
}

func (m *MockChannelRepository) CheckOwnership(ctx context.Context, channelID, userID uuid.UUID) (bool, error) {
	args := m.Called(ctx, channelID, userID)
	return args.Bool(0), args.Error(1)
}

func TestCreateStream(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		streamKeyRepo := new(MockStreamKeyRepository)
		viewerRepo := new(MockViewerSessionRepository)

		handlers := NewLiveStreamHandlers(streamRepo, streamKeyRepo, viewerRepo, nil, nil, nil)

		channelID := uuid.New()
		userID := uuid.New()

		reqBody := CreateStreamRequest{
			ChannelID:   channelID,
			Title:       "Test Stream",
			Description: "Test Description",
			Privacy:     "public",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/"+channelID.String()+"/streams", bytes.NewReader(body))
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
		req = req.WithContext(ctx)

		rr := httptest.NewRecorder()
		router := chi.NewRouter()
		router.Post("/api/v1/channels/{channelId}/streams", handlers.CreateStream)

		streamKeyRepo.On("Create", mock.Anything, channelID, mock.AnythingOfType("string"), (*time.Time)(nil)).
			Return(&domain.StreamKey{
				ID:        uuid.New(),
				ChannelID: channelID,
				IsActive:  true,
				CreatedAt: time.Now(),
			}, nil)

		streamRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.LiveStream")).Return(nil)

		streamKeyRepo.On("MarkUsed", mock.Anything, mock.AnythingOfType("uuid.UUID")).Return(nil)

		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Equal(t, channelID.String(), responseData["channel_id"])
		assert.Equal(t, "Test Stream", responseData["title"])
		assert.Equal(t, "Test Description", responseData["description"])
		assert.Equal(t, domain.StreamStatusWaiting, responseData["status"])
		assert.NotEmpty(t, responseData["stream_key"])

		streamRepo.AssertExpectations(t)
		streamKeyRepo.AssertExpectations(t)
	})

	t.Run("Unauthorized", func(t *testing.T) {
		handlers := NewLiveStreamHandlers(nil, nil, nil, nil, nil, nil)

		channelID := uuid.New()
		reqBody := CreateStreamRequest{
			ChannelID: channelID,
			Title:     "Test Stream",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/"+channelID.String()+"/streams", bytes.NewReader(body))
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Post("/api/v1/channels/{channelId}/streams", handlers.CreateStream)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("Invalid Channel ID", func(t *testing.T) {
		handlers := NewLiveStreamHandlers(nil, nil, nil, nil, nil, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/channels/invalid-id/streams", bytes.NewReader([]byte("{}")))
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New().String())
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Post("/api/v1/channels/{channelId}/streams", handlers.CreateStream)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestGetStream(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

		streamID := uuid.New()
		channelID := uuid.New()
		userID := uuid.New()
		now := time.Now()

		expectedStream := &domain.LiveStream{
			ID:              streamID,
			ChannelID:       channelID,
			UserID:          userID,
			Status:          domain.StreamStatusLive,
			Title:           "Test Stream",
			Description:     "Test Description",
			ViewerCount:     10,
			PeakViewerCount: 15,
			Privacy:         "public",
			SaveReplay:      true,
			StartedAt:       &now,
			CreatedAt:       now,
			UpdatedAt:       now,
		}

		streamRepo.On("GetByID", mock.Anything, streamID).Return(expectedStream, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+streamID.String(), nil)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Get("/api/v1/streams/{id}", handlers.GetStream)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Equal(t, streamID.String(), responseData["id"])
		assert.Equal(t, "Test Stream", responseData["title"])
		assert.Equal(t, domain.StreamStatusLive, responseData["status"])
		assert.Equal(t, float64(10), responseData["viewer_count"])
		assert.Equal(t, float64(15), responseData["peak_viewer_count"])
		assert.NotNil(t, responseData["duration_seconds"])

		streamRepo.AssertExpectations(t)
	})

	t.Run("Not Found", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

		streamID := uuid.New()
		streamRepo.On("GetByID", mock.Anything, streamID).Return(nil, domain.ErrNotFound)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+streamID.String(), nil)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Get("/api/v1/streams/{id}", handlers.GetStream)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusNotFound, rr.Code)

		streamRepo.AssertExpectations(t)
	})
}

func TestUpdateStream(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

		streamID := uuid.New()
		userID := uuid.New()
		now := time.Now()

		existingStream := &domain.LiveStream{
			ID:          streamID,
			ChannelID:   uuid.New(),
			UserID:      userID,
			Status:      domain.StreamStatusWaiting,
			Title:       "Old Title",
			Description: "Old Description",
			Privacy:     "public",
			SaveReplay:  true,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		newTitle := "New Title"
		newDescription := "New Description"
		reqBody := UpdateStreamRequest{
			Title:       &newTitle,
			Description: &newDescription,
		}
		body, _ := json.Marshal(reqBody)

		streamRepo.On("GetByID", mock.Anything, streamID).Return(existingStream, nil)
		streamRepo.On("Update", mock.Anything, mock.AnythingOfType("*domain.LiveStream")).Return(nil)

		req := httptest.NewRequest(http.MethodPut, "/api/v1/streams/"+streamID.String(), bytes.NewReader(body))
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Put("/api/v1/streams/{id}", handlers.UpdateStream)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Equal(t, "New Title", responseData["title"])
		assert.Equal(t, "New Description", responseData["description"])

		streamRepo.AssertExpectations(t)
	})

	t.Run("Forbidden - Not Owner", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

		streamID := uuid.New()
		userID := uuid.New()
		differentUserID := uuid.New()

		existingStream := &domain.LiveStream{
			ID:        streamID,
			ChannelID: uuid.New(),
			UserID:    differentUserID,
			Status:    domain.StreamStatusWaiting,
			Title:     "Old Title",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		streamRepo.On("GetByID", mock.Anything, streamID).Return(existingStream, nil)

		newTitle := "New Title"
		reqBody := UpdateStreamRequest{Title: &newTitle}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPut, "/api/v1/streams/"+streamID.String(), bytes.NewReader(body))
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Put("/api/v1/streams/{id}", handlers.UpdateStream)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusForbidden, rr.Code)

		streamRepo.AssertExpectations(t)
	})
}

func TestEndStream(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

		streamID := uuid.New()
		userID := uuid.New()
		now := time.Now()

		liveStream := &domain.LiveStream{
			ID:        streamID,
			ChannelID: uuid.New(),
			UserID:    userID,
			Status:    domain.StreamStatusLive,
			Title:     "Test Stream",
			StartedAt: &now,
			CreatedAt: now,
			UpdatedAt: now,
		}

		endedTime := now.Add(10 * time.Minute)
		endedStream := &domain.LiveStream{
			ID:        streamID,
			ChannelID: liveStream.ChannelID,
			UserID:    userID,
			Status:    domain.StreamStatusEnded,
			Title:     "Test Stream",
			StartedAt: &now,
			EndedAt:   &endedTime,
			CreatedAt: now,
			UpdatedAt: endedTime,
		}

		streamRepo.On("GetByID", mock.Anything, streamID).Return(liveStream, nil).Once()
		streamRepo.On("GetByID", mock.Anything, streamID).Return(endedStream, nil).Once()

		req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/"+streamID.String()+"/end", nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Post("/api/v1/streams/{id}/end", handlers.EndStream)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Equal(t, domain.StreamStatusEnded, responseData["status"])
		assert.NotNil(t, responseData["ended_at"])
		assert.NotNil(t, responseData["duration_seconds"])

		streamRepo.AssertExpectations(t)
	})

	t.Run("Stream Not Live", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

		streamID := uuid.New()
		userID := uuid.New()

		waitingStream := &domain.LiveStream{
			ID:        streamID,
			ChannelID: uuid.New(),
			UserID:    userID,
			Status:    domain.StreamStatusWaiting,
			Title:     "Test Stream",
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		}

		streamRepo.On("GetByID", mock.Anything, streamID).Return(waitingStream, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/"+streamID.String()+"/end", nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Post("/api/v1/streams/{id}/end", handlers.EndStream)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusBadRequest, rr.Code)

		streamRepo.AssertExpectations(t)
	})
}

func TestGetStreamStats(t *testing.T) {
	t.Run("Active Stream - From Manager", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

		streamID := uuid.New()
		now := time.Now()

		activeStream := &domain.LiveStream{
			ID:              streamID,
			ChannelID:       uuid.New(),
			UserID:          uuid.New(),
			Status:          domain.StreamStatusLive,
			StartedAt:       &now,
			ViewerCount:     25,
			PeakViewerCount: 30,
			CreatedAt:       now,
			UpdatedAt:       now,
		}

		streamRepo.On("GetByID", mock.Anything, streamID).Return(activeStream, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/stats", nil)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Get("/api/v1/streams/{id}/stats", handlers.GetStreamStats)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Equal(t, streamID.String(), responseData["stream_id"])
		assert.Equal(t, domain.StreamStatusLive, responseData["status"])
		assert.Equal(t, float64(25), responseData["viewer_count"])
		assert.Equal(t, float64(30), responseData["peak_viewer_count"])
		assert.NotNil(t, responseData["duration_seconds"])

		streamRepo.AssertExpectations(t)
	})

	t.Run("Inactive Stream - From Database", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

		streamID := uuid.New()
		now := time.Now()
		endedTime := now.Add(30 * time.Minute)

		endedStream := &domain.LiveStream{
			ID:              streamID,
			ChannelID:       uuid.New(),
			UserID:          uuid.New(),
			Status:          domain.StreamStatusEnded,
			StartedAt:       &now,
			EndedAt:         &endedTime,
			ViewerCount:     10,
			PeakViewerCount: 20,
			CreatedAt:       now,
			UpdatedAt:       endedTime,
		}

		streamRepo.On("GetByID", mock.Anything, streamID).Return(endedStream, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/"+streamID.String()+"/stats", nil)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Get("/api/v1/streams/{id}/stats", handlers.GetStreamStats)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Equal(t, streamID.String(), responseData["stream_id"])
		assert.Equal(t, domain.StreamStatusEnded, responseData["status"])
		assert.Equal(t, float64(10), responseData["viewer_count"])
		assert.Equal(t, float64(20), responseData["peak_viewer_count"])

		streamRepo.AssertExpectations(t)
	})
}

func TestRotateStreamKey(t *testing.T) {
	t.Run("Success - No Existing Key", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		streamKeyRepo := new(MockStreamKeyRepository)
		handlers := NewLiveStreamHandlers(streamRepo, streamKeyRepo, nil, nil, nil, nil)

		streamID := uuid.New()
		channelID := uuid.New()
		userID := uuid.New()

		streamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
			ID:        streamID,
			ChannelID: channelID,
		}, nil)
		streamKeyRepo.On("GetActiveByChannelID", mock.Anything, channelID).Return(nil, domain.ErrNotFound)
		streamKeyRepo.On("Create", mock.Anything, channelID, mock.AnythingOfType("string"), (*time.Time)(nil)).
			Return(&domain.StreamKey{
				ID:        uuid.New(),
				ChannelID: channelID,
				IsActive:  true,
				CreatedAt: time.Now(),
			}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/"+streamID.String()+"/rotate-key", nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Post("/api/v1/streams/{id}/rotate-key", handlers.RotateStreamKey)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Contains(t, responseData, "stream_key")
		assert.NotEmpty(t, responseData["stream_key"])

		streamRepo.AssertExpectations(t)
		streamKeyRepo.AssertExpectations(t)
	})

	t.Run("Success - Deactivates Existing Key", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		streamKeyRepo := new(MockStreamKeyRepository)
		handlers := NewLiveStreamHandlers(streamRepo, streamKeyRepo, nil, nil, nil, nil)

		streamID := uuid.New()
		channelID := uuid.New()
		userID := uuid.New()

		streamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
			ID:        streamID,
			ChannelID: channelID,
		}, nil)

		existingKey := &domain.StreamKey{
			ID:        uuid.New(),
			ChannelID: channelID,
			IsActive:  true,
			CreatedAt: time.Now(),
		}

		streamKeyRepo.On("GetActiveByChannelID", mock.Anything, channelID).Return(existingKey, nil)
		streamKeyRepo.On("DeactivateAllForChannel", mock.Anything, channelID).Return(nil)
		streamKeyRepo.On("Create", mock.Anything, channelID, mock.AnythingOfType("string"), (*time.Time)(nil)).
			Return(&domain.StreamKey{
				ID:        uuid.New(),
				ChannelID: channelID,
				IsActive:  true,
				CreatedAt: time.Now(),
			}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/"+streamID.String()+"/rotate-key", nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Post("/api/v1/streams/{id}/rotate-key", handlers.RotateStreamKey)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		streamRepo.AssertExpectations(t)
		streamKeyRepo.AssertExpectations(t)
	})
}

func TestGetActiveStreams(t *testing.T) {
	t.Run("Success", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

		now := time.Now()
		streams := []*domain.LiveStream{
			{
				ID:              uuid.New(),
				ChannelID:       uuid.New(),
				UserID:          uuid.New(),
				Status:          domain.StreamStatusLive,
				Title:           "Stream 1",
				ViewerCount:     10,
				PeakViewerCount: 15,
				StartedAt:       &now,
				CreatedAt:       now,
				UpdatedAt:       now,
			},
			{
				ID:              uuid.New(),
				ChannelID:       uuid.New(),
				UserID:          uuid.New(),
				Status:          domain.StreamStatusLive,
				Title:           "Stream 2",
				ViewerCount:     20,
				PeakViewerCount: 25,
				StartedAt:       &now,
				CreatedAt:       now,
				UpdatedAt:       now,
			},
		}

		streamRepo.On("GetActiveStreams", mock.Anything, 20, 0).Return(streams, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/active", nil)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Get("/api/v1/streams/active", handlers.GetActiveStreams)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Equal(t, float64(2), responseData["total"])
		assert.Equal(t, float64(1), responseData["page"])

		data := responseData["data"].([]interface{})
		assert.Len(t, data, 2)

		streamRepo.AssertExpectations(t)
	})
}

func TestCreateStreamCorrectRoute(t *testing.T) {
	t.Run("Success - channelID from body only", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		streamKeyRepo := new(MockStreamKeyRepository)
		viewerRepo := new(MockViewerSessionRepository)

		handlers := NewLiveStreamHandlers(streamRepo, streamKeyRepo, viewerRepo, nil, nil, nil)

		channelID := uuid.New()
		userID := uuid.New()

		reqBody := CreateStreamRequest{
			ChannelID:   channelID,
			Title:       "Test Stream",
			Description: "Test Description",
			Privacy:     "public",
		}
		body, _ := json.Marshal(reqBody)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/streams", bytes.NewReader(body))
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Post("/api/v1/streams", handlers.CreateStream)

		streamKeyRepo.On("Create", mock.Anything, channelID, mock.AnythingOfType("string"), (*time.Time)(nil)).
			Return(&domain.StreamKey{
				ID:        uuid.New(),
				ChannelID: channelID,
				IsActive:  true,
				CreatedAt: time.Now(),
			}, nil)
		streamRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.LiveStream")).Return(nil)
		streamKeyRepo.On("MarkUsed", mock.Anything, mock.AnythingOfType("uuid.UUID")).Return(nil)

		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusCreated, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Equal(t, channelID.String(), responseData["channel_id"])
		assert.Equal(t, "Test Stream", responseData["title"])

		streamRepo.AssertExpectations(t)
		streamKeyRepo.AssertExpectations(t)
	})
}

func TestRotateStreamKeyCorrectRoute(t *testing.T) {
	t.Run("Success - stream ID from URL, lookup channelID", func(t *testing.T) {
		streamRepo := new(MockLiveStreamRepository)
		streamKeyRepo := new(MockStreamKeyRepository)

		handlers := NewLiveStreamHandlers(streamRepo, streamKeyRepo, nil, nil, nil, nil)

		streamID := uuid.New()
		channelID := uuid.New()
		userID := uuid.New()

		streamRepo.On("GetByID", mock.Anything, streamID).Return(&domain.LiveStream{
			ID:        streamID,
			ChannelID: channelID,
		}, nil)

		streamKeyRepo.On("GetActiveByChannelID", mock.Anything, channelID).Return(nil, domain.ErrNotFound)
		streamKeyRepo.On("Create", mock.Anything, channelID, mock.AnythingOfType("string"), (*time.Time)(nil)).
			Return(&domain.StreamKey{
				ID:        uuid.New(),
				ChannelID: channelID,
				IsActive:  true,
				CreatedAt: time.Now(),
			}, nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/"+streamID.String()+"/rotate-key", nil)
		ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
		req = req.WithContext(ctx)
		rr := httptest.NewRecorder()

		router := chi.NewRouter()
		router.Post("/api/v1/streams/{id}/rotate-key", handlers.RotateStreamKey)
		router.ServeHTTP(rr, req)

		assert.Equal(t, http.StatusOK, rr.Code)

		var wrapper map[string]interface{}
		err := json.NewDecoder(rr.Body).Decode(&wrapper)
		require.NoError(t, err)

		responseData := wrapper["data"].(map[string]interface{})
		assert.Contains(t, responseData, "stream_key")
		assert.NotEmpty(t, responseData["stream_key"])

		streamRepo.AssertExpectations(t)
		streamKeyRepo.AssertExpectations(t)
	})
}

func TestGetStream_InvalidID(t *testing.T) {
	handlers := NewLiveStreamHandlers(nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/bad-id", nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/streams/{id}", handlers.GetStream)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUpdateStream_Unauthorized(t *testing.T) {
	handlers := NewLiveStreamHandlers(nil, nil, nil, nil, nil, nil)

	streamID := uuid.New()
	req := httptest.NewRequest(http.MethodPut, "/api/v1/streams/"+streamID.String(), bytes.NewReader([]byte("{}")))
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/streams/{id}", handlers.UpdateStream)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestUpdateStream_InvalidID(t *testing.T) {
	handlers := NewLiveStreamHandlers(nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/streams/bad-id", bytes.NewReader([]byte("{}")))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New().String())
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/streams/{id}", handlers.UpdateStream)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestUpdateStream_NotFound(t *testing.T) {
	streamRepo := new(MockLiveStreamRepository)
	handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

	streamID := uuid.New()
	streamRepo.On("GetByID", mock.Anything, streamID).Return(nil, domain.ErrNotFound)

	req := httptest.NewRequest(http.MethodPut, "/api/v1/streams/"+streamID.String(), bytes.NewReader([]byte("{}")))
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New().String())
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Put("/api/v1/streams/{id}", handlers.UpdateStream)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
	streamRepo.AssertExpectations(t)
}

func TestEndStream_Unauthorized(t *testing.T) {
	handlers := NewLiveStreamHandlers(nil, nil, nil, nil, nil, nil)

	streamID := uuid.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/"+streamID.String()+"/end", nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Post("/api/v1/streams/{id}/end", handlers.EndStream)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestEndStream_NotFound(t *testing.T) {
	streamRepo := new(MockLiveStreamRepository)
	handlers := NewLiveStreamHandlers(streamRepo, nil, nil, nil, nil, nil)

	streamID := uuid.New()
	streamRepo.On("GetByID", mock.Anything, streamID).Return(nil, domain.ErrNotFound)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/streams/"+streamID.String()+"/end", nil)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, uuid.New().String())
	req = req.WithContext(ctx)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Post("/api/v1/streams/{id}/end", handlers.EndStream)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusNotFound, rr.Code)
	streamRepo.AssertExpectations(t)
}

func TestGetStreamStats_InvalidID(t *testing.T) {
	handlers := NewLiveStreamHandlers(nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/streams/bad-id/stats", nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/streams/{id}/stats", handlers.GetStreamStats)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}

func TestListChannelStreams_InvalidChannelID(t *testing.T) {
	handlers := NewLiveStreamHandlers(nil, nil, nil, nil, nil, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/channels/bad-id/streams", nil)
	rr := httptest.NewRecorder()

	router := chi.NewRouter()
	router.Get("/api/v1/channels/{channelId}/streams", handlers.ListChannelStreams)
	router.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)
}
