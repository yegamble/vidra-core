package video

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock repositories
type MockEncodingRepository struct {
	mock.Mock
}

func (m *MockEncodingRepository) CreateJob(ctx context.Context, job *domain.EncodingJob) error {
	args := m.Called(ctx, job)
	return args.Error(0)
}

func (m *MockEncodingRepository) GetJob(ctx context.Context, jobID string) (*domain.EncodingJob, error) {
	args := m.Called(ctx, jobID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) UpdateJob(ctx context.Context, job *domain.EncodingJob) error {
	args := m.Called(ctx, job)
	return args.Error(0)
}

func (m *MockEncodingRepository) DeleteJob(ctx context.Context, jobID string) error {
	args := m.Called(ctx, jobID)
	return args.Error(0)
}

func (m *MockEncodingRepository) GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error) {
	args := m.Called(ctx, limit)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) GetNextJob(ctx context.Context) (*domain.EncodingJob, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) ResetStaleJobs(ctx context.Context, staleDuration time.Duration) (int64, error) {
	args := m.Called(ctx, staleDuration)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockEncodingRepository) UpdateJobStatus(ctx context.Context, jobID string, status domain.EncodingStatus) error {
	args := m.Called(ctx, jobID, status)
	return args.Error(0)
}

func (m *MockEncodingRepository) UpdateJobProgress(ctx context.Context, jobID string, progress int) error {
	args := m.Called(ctx, jobID, progress)
	return args.Error(0)
}

func (m *MockEncodingRepository) SetJobError(ctx context.Context, jobID string, errorMsg string) error {
	args := m.Called(ctx, jobID, errorMsg)
	return args.Error(0)
}

func (m *MockEncodingRepository) GetJobCounts(ctx context.Context) (map[string]int64, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(map[string]int64), args.Error(1)
}

func (m *MockEncodingRepository) GetJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.EncodingJob), args.Error(1)
}

func (m *MockEncodingRepository) GetActiveJobsByVideoID(ctx context.Context, videoID string) ([]*domain.EncodingJob, error) {
	args := m.Called(ctx, videoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.EncodingJob), args.Error(1)
}

func TestGetEncodingJobHandler_Authorization(t *testing.T) {
	tests := []struct {
		name           string
		jobID          string
		userID         uuid.UUID
		userRole       string
		videoOwnerID   string
		setupMocks     func(*MockEncodingRepository, *MockVideoRepository)
		expectedStatus int
		expectedError  string
	}{
		{
			name:         "video owner can access",
			jobID:        "job-123",
			userID:       uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			userRole:     "user",
			videoOwnerID: "11111111-1111-1111-1111-111111111111",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				job := &domain.EncodingJob{
					ID:       "job-123",
					VideoID:  "video-456",
					Progress: 50,
					Status:   domain.EncodingStatusProcessing,
				}
				er.On("GetJob", mock.Anything, "job-123").Return(job, nil)

				video := &domain.Video{
					ID:     "video-456",
					UserID: "11111111-1111-1111-1111-111111111111",
				}
				vr.On("GetByID", mock.Anything, "video-456").Return(video, nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:         "non-owner regular user denied",
			jobID:        "job-123",
			userID:       uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			userRole:     "user",
			videoOwnerID: "11111111-1111-1111-1111-111111111111",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				job := &domain.EncodingJob{
					ID:      "job-123",
					VideoID: "video-456",
				}
				er.On("GetJob", mock.Anything, "job-123").Return(job, nil)

				video := &domain.Video{
					ID:     "video-456",
					UserID: "11111111-1111-1111-1111-111111111111",
				}
				vr.On("GetByID", mock.Anything, "video-456").Return(video, nil)
			},
			expectedStatus: http.StatusForbidden,
			expectedError:  "forbidden",
		},
		{
			name:         "admin can access any job",
			jobID:        "job-123",
			userID:       uuid.MustParse("33333333-3333-3333-3333-333333333333"),
			userRole:     "admin",
			videoOwnerID: "11111111-1111-1111-1111-111111111111",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				job := &domain.EncodingJob{
					ID:       "job-123",
					VideoID:  "video-456",
					Progress: 75,
					Status:   domain.EncodingStatusProcessing,
				}
				er.On("GetJob", mock.Anything, "job-123").Return(job, nil)

				video := &domain.Video{
					ID:     "video-456",
					UserID: "11111111-1111-1111-1111-111111111111",
				}
				vr.On("GetByID", mock.Anything, "video-456").Return(video, nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:         "moderator can access any job",
			jobID:        "job-123",
			userID:       uuid.MustParse("44444444-4444-4444-4444-444444444444"),
			userRole:     "moderator",
			videoOwnerID: "11111111-1111-1111-1111-111111111111",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				job := &domain.EncodingJob{
					ID:      "job-123",
					VideoID: "video-456",
				}
				er.On("GetJob", mock.Anything, "job-123").Return(job, nil)

				video := &domain.Video{
					ID:     "video-456",
					UserID: "11111111-1111-1111-1111-111111111111",
				}
				vr.On("GetByID", mock.Anything, "video-456").Return(video, nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:     "job not found",
			jobID:    "non-existent",
			userID:   uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			userRole: "user",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				er.On("GetJob", mock.Anything, "non-existent").Return(nil, domain.NewDomainError("JOB_NOT_FOUND", "Job not found"))
			},
			expectedStatus: http.StatusNotFound,
			expectedError:  "Job not found",
		},
		{
			name:           "missing job ID",
			jobID:          "",
			userID:         uuid.MustParse("11111111-1111-1111-1111-111111111111"),
			userRole:       "user",
			setupMocks:     func(er *MockEncodingRepository, vr *MockVideoRepository) {},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "missing job ID",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			encodingRepo := new(MockEncodingRepository)
			videoRepo := new(MockVideoRepository)
			tt.setupMocks(encodingRepo, videoRepo)

			// Create handler
			handler := GetEncodingJobHandler(encodingRepo, videoRepo)

			// Create request
			req := httptest.NewRequest("GET", "/api/v1/encoding/jobs/"+tt.jobID, nil)
			if tt.jobID != "" {
				rctx := chi.NewRouteContext()
				rctx.URLParams.Add("jobID", tt.jobID)
				req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			}

			// Add user context - preserve existing context values
			ctx := req.Context()
			ctx = context.WithValue(ctx, middleware.UserIDKey, tt.userID.String())
			ctx = context.WithValue(ctx, middleware.UserRoleKey, tt.userRole)
			req = req.WithContext(ctx)

			// Execute request
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Assert status
			assert.Equal(t, tt.expectedStatus, rr.Code)

			// Assert error message if expected
			if tt.expectedError != "" {
				var response map[string]interface{}
				err := json.Unmarshal(rr.Body.Bytes(), &response)
				assert.NoError(t, err)
				errorInfo, ok := response["error"].(map[string]interface{})
				assert.True(t, ok, "Expected error field in response")
				assert.Contains(t, errorInfo["message"], tt.expectedError)
			}

			// If success, verify response structure
			if tt.expectedStatus == http.StatusOK {
				var response map[string]interface{}
				err := json.Unmarshal(rr.Body.Bytes(), &response)
				assert.NoError(t, err)

				// Extract data field
				data, ok := response["data"].(map[string]interface{})
				assert.True(t, ok, "Expected data field in response")

				// Verify job ID exists
				assert.NotEmpty(t, data["id"])
			}

			encodingRepo.AssertExpectations(t)
			videoRepo.AssertExpectations(t)
		})
	}
}

func TestGetEncodingJobsByVideoHandler(t *testing.T) {
	tests := []struct {
		name           string
		videoID        string
		userID         uuid.UUID
		userRole       string
		queryParams    string
		setupMocks     func(*MockEncodingRepository, *MockVideoRepository)
		expectedStatus int
		validateResp   func(*testing.T, []byte)
	}{
		{
			name:     "get all jobs for owned video",
			videoID:  "11111111-1111-1111-1111-111111111111",
			userID:   uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			userRole: "user",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				video := &domain.Video{
					ID:     "11111111-1111-1111-1111-111111111111",
					UserID: "22222222-2222-2222-2222-222222222222",
				}
				vr.On("GetByID", mock.Anything, "11111111-1111-1111-1111-111111111111").Return(video, nil)

				jobs := []*domain.EncodingJob{
					{ID: "job1", VideoID: video.ID, Status: domain.EncodingStatusCompleted, Progress: 100},
					{ID: "job2", VideoID: video.ID, Status: domain.EncodingStatusProcessing, Progress: 45},
					{ID: "job3", VideoID: video.ID, Status: domain.EncodingStatusPending, Progress: 0},
				}
				er.On("GetJobsByVideoID", mock.Anything, video.ID).Return(jobs, nil)
			},
			expectedStatus: http.StatusOK,
			validateResp: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				err := json.Unmarshal(body, &resp)
				assert.NoError(t, err)

				// Extract data field
				data, ok := resp["data"].(map[string]interface{})
				assert.True(t, ok, "Expected data field in response")

				assert.Equal(t, float64(3), data["total"])
				assert.Equal(t, float64(2), data["active_count"])      // pending + processing
				assert.Equal(t, float64(22), data["overall_progress"]) // (45 + 0) / 2
			},
		},
		{
			name:        "get active jobs only",
			videoID:     "11111111-1111-1111-1111-111111111111",
			userID:      uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			userRole:    "user",
			queryParams: "?active=true",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				video := &domain.Video{
					ID:     "11111111-1111-1111-1111-111111111111",
					UserID: "22222222-2222-2222-2222-222222222222",
				}
				vr.On("GetByID", mock.Anything, "11111111-1111-1111-1111-111111111111").Return(video, nil)

				jobs := []*domain.EncodingJob{
					{ID: "job2", VideoID: video.ID, Status: domain.EncodingStatusProcessing, Progress: 60},
					{ID: "job3", VideoID: video.ID, Status: domain.EncodingStatusPending, Progress: 0},
				}
				er.On("GetActiveJobsByVideoID", mock.Anything, video.ID).Return(jobs, nil)
			},
			expectedStatus: http.StatusOK,
			validateResp: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				err := json.Unmarshal(body, &resp)
				assert.NoError(t, err)

				// Extract data field
				data, ok := resp["data"].(map[string]interface{})
				assert.True(t, ok, "Expected data field in response")

				assert.Equal(t, float64(2), data["total"])
				assert.Equal(t, float64(2), data["active_count"])
				assert.Equal(t, float64(30), data["overall_progress"]) // (60 + 0) / 2
			},
		},
		{
			name:           "invalid video ID format",
			videoID:        "not-a-uuid",
			userID:         uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			userRole:       "user",
			setupMocks:     func(er *MockEncodingRepository, vr *MockVideoRepository) {},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:     "video not found",
			videoID:  "11111111-1111-1111-1111-111111111111",
			userID:   uuid.MustParse("22222222-2222-2222-2222-222222222222"),
			userRole: "user",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				vr.On("GetByID", mock.Anything, "11111111-1111-1111-1111-111111111111").Return(nil, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:     "unauthorized access",
			videoID:  "11111111-1111-1111-1111-111111111111",
			userID:   uuid.MustParse("33333333-3333-3333-3333-333333333333"),
			userRole: "user",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				video := &domain.Video{
					ID:     "11111111-1111-1111-1111-111111111111",
					UserID: "22222222-2222-2222-2222-222222222222", // Different owner
				}
				vr.On("GetByID", mock.Anything, "11111111-1111-1111-1111-111111111111").Return(video, nil)
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:     "admin access allowed",
			videoID:  "11111111-1111-1111-1111-111111111111",
			userID:   uuid.MustParse("44444444-4444-4444-4444-444444444444"),
			userRole: "admin",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				video := &domain.Video{
					ID:     "11111111-1111-1111-1111-111111111111",
					UserID: "22222222-2222-2222-2222-222222222222",
				}
				vr.On("GetByID", mock.Anything, "11111111-1111-1111-1111-111111111111").Return(video, nil)

				jobs := []*domain.EncodingJob{
					{ID: "job1", VideoID: video.ID, Status: domain.EncodingStatusCompleted, Progress: 100},
				}
				er.On("GetJobsByVideoID", mock.Anything, video.ID).Return(jobs, nil)
			},
			expectedStatus: http.StatusOK,
			validateResp: func(t *testing.T, body []byte) {
				var resp map[string]interface{}
				err := json.Unmarshal(body, &resp)
				assert.NoError(t, err)

				// Extract data field
				data, ok := resp["data"].(map[string]interface{})
				assert.True(t, ok, "Expected data field in response")

				assert.Equal(t, float64(1), data["total"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup mocks
			encodingRepo := new(MockEncodingRepository)
			videoRepo := new(MockVideoRepository)
			tt.setupMocks(encodingRepo, videoRepo)

			// Create handler
			handler := GetEncodingJobsByVideoHandler(encodingRepo, videoRepo)

			// Create request
			req := httptest.NewRequest("GET", "/api/v1/videos/"+tt.videoID+"/encoding-jobs"+tt.queryParams, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.videoID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			// Add user context - preserve existing context values
			ctx := req.Context()
			ctx = context.WithValue(ctx, middleware.UserIDKey, tt.userID.String())
			ctx = context.WithValue(ctx, middleware.UserRoleKey, tt.userRole)
			req = req.WithContext(ctx)

			// Execute request
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			// Assert status
			assert.Equal(t, tt.expectedStatus, rr.Code)

			// Validate response if provided
			if tt.validateResp != nil {
				tt.validateResp(t, rr.Body.Bytes())
			}

			encodingRepo.AssertExpectations(t)
			videoRepo.AssertExpectations(t)
		})
	}
}
