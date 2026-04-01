package livestream

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"vidra-core/internal/repository"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// MockStreamRepository is a mock implementation of StreamRepository
type MockStreamRepository struct {
	mock.Mock
}

func (m *MockStreamRepository) GetByID(ctx context.Context, id string) (*domain.LiveStream, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.LiveStream), args.Error(1)
}

func (m *MockStreamRepository) GetChannelByID(ctx context.Context, id uuid.UUID) (*domain.Channel, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Channel), args.Error(1)
}

func (m *MockStreamRepository) UpdateWaitingRoom(ctx context.Context, streamID uuid.UUID, enabled bool, message string) error {
	args := m.Called(ctx, streamID, enabled, message)
	return args.Error(0)
}

func (m *MockStreamRepository) ScheduleStream(ctx context.Context, streamID uuid.UUID, params repository.ScheduleStreamParams) error {
	return m.Called(ctx, streamID, params).Error(0)
}

func (m *MockStreamRepository) CancelSchedule(ctx context.Context, streamID uuid.UUID) error {
	args := m.Called(ctx, streamID)
	return args.Error(0)
}

func (m *MockStreamRepository) GetScheduledStreams(ctx context.Context, limit, offset int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

func (m *MockStreamRepository) GetUpcomingStreams(ctx context.Context, userID uuid.UUID, limit int) ([]*domain.LiveStream, error) {
	args := m.Called(ctx, userID, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.LiveStream), args.Error(1)
}

// MockWaitingRoomUserRepository is a mock implementation of UserRepository for waiting room tests
type MockWaitingRoomUserRepository struct {
	mock.Mock
}

func (m *MockWaitingRoomUserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func TestWaitingRoomHandler_GetWaitingRoom(t *testing.T) {
	tests := []struct {
		name           string
		streamID       string
		setupMock      func(*MockStreamRepository)
		expectedStatus int
		expectedBody   *WaitingRoomInfo
		expectError    bool
	}{
		{
			name:     "successful waiting room retrieval",
			streamID: "550e8400-e29b-41d4-a716-446655440000",
			setupMock: func(m *MockStreamRepository) {
				streamID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
				channelID := uuid.New()
				scheduledStart := time.Now().Add(1 * time.Hour)

				m.On("GetByID", mock.Anything, streamID.String()).Return(&domain.LiveStream{
					ID:                 streamID,
					Title:              "Test Stream",
					ChannelID:          channelID,
					Status:             "waiting_room",
					ScheduledStart:     &scheduledStart,
					WaitingRoomEnabled: true,
					WaitingRoomMessage: "Starting soon!",
				}, nil)

				m.On("GetChannelByID", mock.Anything, channelID).Return(&domain.Channel{
					ID:   channelID,
					Name: "Test Channel",
				}, nil)
			},
			expectedStatus: http.StatusOK,
			expectError:    false,
		},
		{
			name:     "invalid stream ID",
			streamID: "invalid-uuid",
			setupMock: func(m *MockStreamRepository) {
				// No mock needed - validation fails before repo call
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
		{
			name:     "stream not found",
			streamID: uuid.New().String(),
			setupMock: func(m *MockStreamRepository) {
				m.On("GetByID", mock.Anything, mock.Anything).Return(nil, sql.ErrNoRows)
			},
			expectedStatus: http.StatusNotFound,
			expectError:    true,
		},
		{
			name:     "stream not in waiting room or scheduled status",
			streamID: uuid.New().String(),
			setupMock: func(m *MockStreamRepository) {
				streamID := uuid.New()
				m.On("GetByID", mock.Anything, mock.Anything).Return(&domain.LiveStream{
					ID:     streamID,
					Title:  "Test Stream",
					Status: "live",
				}, nil)
			},
			expectedStatus: http.StatusBadRequest,
			expectError:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStreamRepo := new(MockStreamRepository)
			mockUserRepo := new(MockWaitingRoomUserRepository)

			if tt.setupMock != nil {
				tt.setupMock(mockStreamRepo)
			}

			handler := NewWaitingRoomHandler(mockStreamRepo, mockUserRepo)

			r := chi.NewRouter()
			r.Get("/api/v1/streams/{streamId}/waiting-room", handler.GetWaitingRoom)

			req := httptest.NewRequest("GET", "/api/v1/streams/"+tt.streamID+"/waiting-room", nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if !tt.expectError && w.Code == http.StatusOK {
				var response WaitingRoomInfo
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.NotNil(t, response.StreamID)
			}

			mockStreamRepo.AssertExpectations(t)
			mockUserRepo.AssertExpectations(t)
		})
	}
}

func TestWaitingRoomHandler_UpdateWaitingRoom(t *testing.T) {
	tests := []struct {
		name           string
		streamID       string
		userID         uuid.UUID
		requestBody    interface{}
		setupMock      func(*MockStreamRepository, uuid.UUID)
		expectedStatus int
	}{
		{
			name:     "successful update",
			streamID: "550e8400-e29b-41d4-a716-446655440000",
			userID:   uuid.New(),
			requestBody: UpdateWaitingRoomRequest{
				WaitingRoomEnabled: true,
				WaitingRoomMessage: "Please wait",
			},
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				streamID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
				channelID := uuid.New()

				m.On("GetByID", mock.Anything, streamID.String()).Return(&domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil)

				m.On("GetChannelByID", mock.Anything, channelID).Return(&domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil)

				m.On("UpdateWaitingRoom", mock.Anything, streamID, true, "Please wait").Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:           "unauthorized - no user in context",
			streamID:       uuid.New().String(),
			userID:         uuid.Nil,
			requestBody:    UpdateWaitingRoomRequest{},
			setupMock:      func(m *MockStreamRepository, userID uuid.UUID) {},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:     "forbidden - user doesn't own channel",
			streamID: "550e8400-e29b-41d4-a716-446655440000",
			userID:   uuid.New(),
			requestBody: UpdateWaitingRoomRequest{
				WaitingRoomEnabled: false,
			},
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				streamID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
				channelID := uuid.New()
				differentUser := uuid.New()

				m.On("GetByID", mock.Anything, streamID.String()).Return(&domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil)

				m.On("GetChannelByID", mock.Anything, channelID).Return(&domain.Channel{
					ID:     channelID,
					UserID: differentUser,
				}, nil)
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:        "invalid request body",
			streamID:    "550e8400-e29b-41d4-a716-446655440000",
			userID:      uuid.New(),
			requestBody: "invalid json",
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				streamID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
				channelID := uuid.New()

				m.On("GetByID", mock.Anything, streamID.String()).Return(&domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil)

				m.On("GetChannelByID", mock.Anything, channelID).Return(&domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil)
				// No call to UpdateWaitingRoom since JSON parsing fails
			},
			expectedStatus: http.StatusBadRequest,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStreamRepo := new(MockStreamRepository)
			mockUserRepo := new(MockWaitingRoomUserRepository)

			if tt.setupMock != nil {
				tt.setupMock(mockStreamRepo, tt.userID)
			}

			handler := NewWaitingRoomHandler(mockStreamRepo, mockUserRepo)

			r := chi.NewRouter()
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tt.userID != uuid.Nil {
						ctx := context.WithValue(r.Context(), middleware.UserIDKey, tt.userID.String())
						next.ServeHTTP(w, r.WithContext(ctx))
					} else {
						next.ServeHTTP(w, r)
					}
				})
			})
			r.Put("/api/v1/streams/{streamId}/waiting-room", handler.UpdateWaitingRoom)

			var body []byte
			if str, ok := tt.requestBody.(string); ok {
				body = []byte(str)
			} else {
				body, _ = json.Marshal(tt.requestBody)
			}

			req := httptest.NewRequest("PUT", "/api/v1/streams/"+tt.streamID+"/waiting-room", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			mockStreamRepo.AssertExpectations(t)
			mockUserRepo.AssertExpectations(t)
		})
	}
}

func TestWaitingRoomHandler_ScheduleStream(t *testing.T) {
	tests := []struct {
		name           string
		streamID       string
		userID         uuid.UUID
		requestBody    interface{}
		setupMock      func(*MockStreamRepository, uuid.UUID)
		expectedStatus int
	}{
		{
			name:     "successful schedule",
			streamID: "550e8400-e29b-41d4-a716-446655440000",
			userID:   uuid.New(),
			requestBody: ScheduleStreamRequest{
				ScheduledStart:     time.Now().Add(2 * time.Hour),
				WaitingRoomEnabled: true,
				WaitingRoomMessage: "Starting at scheduled time",
			},
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				streamID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
				channelID := uuid.New()

				m.On("GetByID", mock.Anything, streamID.String()).Return(&domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil)

				m.On("GetChannelByID", mock.Anything, channelID).Return(&domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil)

				m.On("ScheduleStream", mock.Anything, streamID,
					mock.AnythingOfType("repository.ScheduleStreamParams")).Return(nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:     "schedule with end time",
			streamID: "550e8400-e29b-41d4-a716-446655440000",
			userID:   uuid.New(),
			requestBody: ScheduleStreamRequest{
				ScheduledStart:     time.Now().Add(2 * time.Hour),
				ScheduledEnd:       func() *time.Time { t := time.Now().Add(4 * time.Hour); return &t }(),
				WaitingRoomEnabled: false,
			},
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				streamID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
				channelID := uuid.New()

				m.On("GetByID", mock.Anything, streamID.String()).Return(&domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil)

				m.On("GetChannelByID", mock.Anything, channelID).Return(&domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil)

				m.On("ScheduleStream", mock.Anything, streamID,
					mock.AnythingOfType("repository.ScheduleStreamParams")).Return(nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:     "schedule in past - error",
			streamID: uuid.New().String(),
			userID:   uuid.New(),
			requestBody: ScheduleStreamRequest{
				ScheduledStart: time.Now().Add(-1 * time.Hour),
			},
			setupMock:      func(m *MockStreamRepository, userID uuid.UUID) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:     "end before start - error",
			streamID: uuid.New().String(),
			userID:   uuid.New(),
			requestBody: ScheduleStreamRequest{
				ScheduledStart: time.Now().Add(4 * time.Hour),
				ScheduledEnd:   func() *time.Time { t := time.Now().Add(2 * time.Hour); return &t }(),
			},
			setupMock:      func(m *MockStreamRepository, userID uuid.UUID) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:           "unauthorized",
			streamID:       uuid.New().String(),
			userID:         uuid.Nil,
			requestBody:    ScheduleStreamRequest{ScheduledStart: time.Now().Add(1 * time.Hour)},
			setupMock:      func(m *MockStreamRepository, userID uuid.UUID) {},
			expectedStatus: http.StatusUnauthorized,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStreamRepo := new(MockStreamRepository)
			mockUserRepo := new(MockWaitingRoomUserRepository)

			if tt.setupMock != nil {
				tt.setupMock(mockStreamRepo, tt.userID)
			}

			handler := NewWaitingRoomHandler(mockStreamRepo, mockUserRepo)

			r := chi.NewRouter()
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tt.userID != uuid.Nil {
						ctx := context.WithValue(r.Context(), middleware.UserIDKey, tt.userID.String())
						next.ServeHTTP(w, r.WithContext(ctx))
					} else {
						next.ServeHTTP(w, r)
					}
				})
			})
			r.Post("/api/v1/streams/{streamId}/schedule", handler.ScheduleStream)

			body, _ := json.Marshal(tt.requestBody)
			req := httptest.NewRequest("POST", "/api/v1/streams/"+tt.streamID+"/schedule", bytes.NewBuffer(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			if w.Code == http.StatusOK {
				var response Response
				err := json.NewDecoder(w.Body).Decode(&response)
				assert.NoError(t, err)
				assert.True(t, response.Success)

				data, ok := response.Data.(map[string]interface{})
				assert.True(t, ok, "response.Data should be a map")
				assert.Equal(t, "scheduled", data["status"])
			}

			mockStreamRepo.AssertExpectations(t)
			mockUserRepo.AssertExpectations(t)
		})
	}
}

func TestWaitingRoomHandler_CancelSchedule(t *testing.T) {
	tests := []struct {
		name           string
		streamID       string
		userID         uuid.UUID
		setupMock      func(*MockStreamRepository, uuid.UUID)
		expectedStatus int
	}{
		{
			name:     "successful cancel",
			streamID: "550e8400-e29b-41d4-a716-446655440000",
			userID:   uuid.New(),
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				streamID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
				channelID := uuid.New()

				m.On("GetByID", mock.Anything, streamID.String()).Return(&domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil)

				m.On("GetChannelByID", mock.Anything, channelID).Return(&domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil)

				m.On("CancelSchedule", mock.Anything, streamID).Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:           "unauthorized",
			streamID:       uuid.New().String(),
			userID:         uuid.Nil,
			setupMock:      func(m *MockStreamRepository, userID uuid.UUID) {},
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:     "stream not found",
			streamID: "550e8400-e29b-41d4-a716-446655440001",
			userID:   uuid.New(),
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				m.On("GetByID", mock.Anything, "550e8400-e29b-41d4-a716-446655440001").Return(nil, sql.ErrNoRows)
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:     "database error on cancel",
			streamID: "550e8400-e29b-41d4-a716-446655440000",
			userID:   uuid.New(),
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				streamID := uuid.MustParse("550e8400-e29b-41d4-a716-446655440000")
				channelID := uuid.New()

				m.On("GetByID", mock.Anything, streamID.String()).Return(&domain.LiveStream{
					ID:        streamID,
					ChannelID: channelID,
				}, nil)

				m.On("GetChannelByID", mock.Anything, channelID).Return(&domain.Channel{
					ID:     channelID,
					UserID: userID,
				}, nil)

				m.On("CancelSchedule", mock.Anything, streamID).Return(errors.New("database error"))
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStreamRepo := new(MockStreamRepository)
			mockUserRepo := new(MockWaitingRoomUserRepository)

			if tt.setupMock != nil {
				tt.setupMock(mockStreamRepo, tt.userID)
			}

			handler := NewWaitingRoomHandler(mockStreamRepo, mockUserRepo)

			r := chi.NewRouter()
			r.Use(func(next http.Handler) http.Handler {
				return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if tt.userID != uuid.Nil {
						ctx := context.WithValue(r.Context(), middleware.UserIDKey, tt.userID.String())
						next.ServeHTTP(w, r.WithContext(ctx))
					} else {
						next.ServeHTTP(w, r)
					}
				})
			})
			r.Delete("/api/v1/streams/{streamId}/schedule", handler.CancelSchedule)

			req := httptest.NewRequest("DELETE", "/api/v1/streams/"+tt.streamID+"/schedule", nil)
			w := httptest.NewRecorder()

			r.ServeHTTP(w, req)

			assert.Equal(t, tt.expectedStatus, w.Code)

			mockStreamRepo.AssertExpectations(t)
			mockUserRepo.AssertExpectations(t)
		})
	}
}

func TestWaitingRoomHandler_GetScheduledStreams(t *testing.T) {
	mockStreamRepo := new(MockStreamRepository)
	mockUserRepo := new(MockWaitingRoomUserRepository)

	scheduledStart := time.Now().Add(2 * time.Hour)
	streams := []*domain.LiveStream{
		{
			ID:             uuid.New(),
			Title:          "Stream 1",
			Status:         "scheduled",
			ScheduledStart: &scheduledStart,
		},
		{
			ID:             uuid.New(),
			Title:          "Stream 2",
			Status:         "scheduled",
			ScheduledStart: &scheduledStart,
		},
	}

	mockStreamRepo.On("GetScheduledStreams", mock.Anything, 20, 0).Return(streams, nil)

	handler := NewWaitingRoomHandler(mockStreamRepo, mockUserRepo)

	req := httptest.NewRequest("GET", "/api/v1/streams/scheduled", nil)
	w := httptest.NewRecorder()

	handler.GetScheduledStreams(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var response Response
	err := json.NewDecoder(w.Body).Decode(&response)
	assert.NoError(t, err)
	assert.True(t, response.Success)

	// The data should be a slice of streams, but it comes as []interface{} from JSON unmarshaling
	dataSlice, ok := response.Data.([]interface{})
	assert.True(t, ok, "response.Data should be a slice")
	assert.Len(t, dataSlice, 2)

	mockStreamRepo.AssertExpectations(t)
}

func TestWaitingRoomHandler_GetUpcomingStreams(t *testing.T) {
	tests := []struct {
		name      string
		userID    uuid.UUID
		setupMock func(*MockStreamRepository, uuid.UUID)
	}{
		{
			name:   "with authenticated user",
			userID: uuid.New(),
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				streams := []*domain.LiveStream{
					{
						ID:     uuid.New(),
						Title:  "Upcoming Stream",
						Status: "scheduled",
					},
				}
				m.On("GetUpcomingStreams", mock.Anything, userID, 10).Return(streams, nil)
			},
		},
		{
			name:   "without authentication",
			userID: uuid.Nil,
			setupMock: func(m *MockStreamRepository, userID uuid.UUID) {
				streams := []*domain.LiveStream{}
				m.On("GetUpcomingStreams", mock.Anything, uuid.Nil, 10).Return(streams, nil)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockStreamRepo := new(MockStreamRepository)
			mockUserRepo := new(MockWaitingRoomUserRepository)

			if tt.setupMock != nil {
				tt.setupMock(mockStreamRepo, tt.userID)
			}

			handler := NewWaitingRoomHandler(mockStreamRepo, mockUserRepo)

			req := httptest.NewRequest("GET", "/api/v1/streams/upcoming", nil)
			if tt.userID != uuid.Nil {
				ctx := context.WithValue(req.Context(), middleware.UserIDKey, tt.userID.String())
				req = req.WithContext(ctx)
			}
			w := httptest.NewRecorder()

			handler.GetUpcomingStreams(w, req)

			assert.Equal(t, http.StatusOK, w.Code)

			var response Response
			err := json.NewDecoder(w.Body).Decode(&response)
			assert.NoError(t, err)
			assert.True(t, response.Success)

			mockStreamRepo.AssertExpectations(t)
		})
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration time.Duration
		expected string
	}{
		{
			name:     "days hours minutes",
			duration: 26*time.Hour + 30*time.Minute + 45*time.Second,
			expected: "1d 2h 30m",
		},
		{
			name:     "hours minutes",
			duration: 3*time.Hour + 45*time.Minute + 20*time.Second,
			expected: "3h 45m",
		},
		{
			name:     "minutes seconds",
			duration: 15*time.Minute + 30*time.Second,
			expected: "15m 30s",
		},
		{
			name:     "only seconds",
			duration: 45 * time.Second,
			expected: "45s",
		},
		{
			name:     "zero duration",
			duration: 0,
			expected: "0s",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := formatDuration(tt.duration)
			assert.Equal(t, tt.expected, result)
		})
	}
}
