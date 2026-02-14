package video

import (
	"context"
	"encoding/json"
	"errors"
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
			encodingRepo := new(MockEncodingRepository)
			videoRepo := new(MockVideoRepository)
			tt.setupMocks(encodingRepo, videoRepo)

			handler := GetEncodingJobHandler(encodingRepo, videoRepo)

			req := httptest.NewRequest("GET", "/api/v1/encoding/jobs/"+tt.jobID, nil)
			if tt.jobID != "" {
				rctx := chi.NewRouteContext()
				rctx.URLParams.Add("jobID", tt.jobID)
				req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
			}

			ctx := req.Context()
			ctx = context.WithValue(ctx, middleware.UserIDKey, tt.userID.String())
			ctx = context.WithValue(ctx, middleware.UserRoleKey, tt.userRole)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			if tt.expectedError != "" {
				var response map[string]interface{}
				err := json.Unmarshal(rr.Body.Bytes(), &response)
				assert.NoError(t, err)
				errorInfo, ok := response["error"].(map[string]interface{})
				assert.True(t, ok, "Expected error field in response")
				assert.Contains(t, errorInfo["message"], tt.expectedError)
			}

			if tt.expectedStatus == http.StatusOK {
				var response map[string]interface{}
				err := json.Unmarshal(rr.Body.Bytes(), &response)
				assert.NoError(t, err)

				data, ok := response["data"].(map[string]interface{})
				assert.True(t, ok, "Expected data field in response")

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

				data, ok := resp["data"].(map[string]interface{})
				assert.True(t, ok, "Expected data field in response")

				assert.Equal(t, float64(3), data["total"])
				assert.Equal(t, float64(2), data["active_count"])
				assert.Equal(t, float64(22), data["overall_progress"])
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

				data, ok := resp["data"].(map[string]interface{})
				assert.True(t, ok, "Expected data field in response")

				assert.Equal(t, float64(2), data["total"])
				assert.Equal(t, float64(2), data["active_count"])
				assert.Equal(t, float64(30), data["overall_progress"])
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
					UserID: "22222222-2222-2222-2222-222222222222",
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

				data, ok := resp["data"].(map[string]interface{})
				assert.True(t, ok, "Expected data field in response")

				assert.Equal(t, float64(1), data["total"])
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encodingRepo := new(MockEncodingRepository)
			videoRepo := new(MockVideoRepository)
			tt.setupMocks(encodingRepo, videoRepo)

			handler := GetEncodingJobsByVideoHandler(encodingRepo, videoRepo)

			req := httptest.NewRequest("GET", "/api/v1/videos/"+tt.videoID+"/encoding-jobs"+tt.queryParams, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("id", tt.videoID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			ctx := req.Context()
			ctx = context.WithValue(ctx, middleware.UserIDKey, tt.userID.String())
			ctx = context.WithValue(ctx, middleware.UserRoleKey, tt.userRole)
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			if tt.validateResp != nil {
				tt.validateResp(t, rr.Body.Bytes())
			}

			encodingRepo.AssertExpectations(t)
			videoRepo.AssertExpectations(t)
		})
	}
}

func TestGetMyEncodingJobsHandler(t *testing.T) {
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")
	video1ID := "video1"
	video2ID := "video2"

	tests := []struct {
		name           string
		queryParams    string
		setupMocks     func(*MockEncodingRepository, *MockVideoRepository)
		expectedStatus int
		expectedTotal  int
		expectedJobs   int
	}{
		{
			name:        "success - returns all jobs for user's videos",
			queryParams: "",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				videos := []*domain.Video{
					{ID: video1ID, UserID: userID.String()},
					{ID: video2ID, UserID: userID.String()},
				}
				vr.On("GetByUserID", mock.Anything, userID.String(), 50, 0).
					Return(videos, int64(2), nil)

				job1 := &domain.EncodingJob{
					ID:      "job1",
					VideoID: video1ID,
					Status:  domain.EncodingStatusProcessing,
				}
				job2 := &domain.EncodingJob{
					ID:      "job2",
					VideoID: video2ID,
					Status:  domain.EncodingStatusCompleted,
				}
				er.On("GetJobsByVideoID", mock.Anything, video1ID).
					Return([]*domain.EncodingJob{job1}, nil)
				er.On("GetJobsByVideoID", mock.Anything, video2ID).
					Return([]*domain.EncodingJob{job2}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedTotal:  2,
			expectedJobs:   2,
		},
		{
			name:        "success - filters by status",
			queryParams: "?status=processing",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				videos := []*domain.Video{
					{ID: video1ID, UserID: userID.String()},
					{ID: video2ID, UserID: userID.String()},
				}
				vr.On("GetByUserID", mock.Anything, userID.String(), 50, 0).
					Return(videos, int64(2), nil)

				job1 := &domain.EncodingJob{
					ID:      "job1",
					VideoID: video1ID,
					Status:  domain.EncodingStatusProcessing,
				}
				job2 := &domain.EncodingJob{
					ID:      "job2",
					VideoID: video2ID,
					Status:  domain.EncodingStatusCompleted,
				}
				er.On("GetJobsByVideoID", mock.Anything, video1ID).
					Return([]*domain.EncodingJob{job1}, nil)
				er.On("GetJobsByVideoID", mock.Anything, video2ID).
					Return([]*domain.EncodingJob{job2}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedTotal:  1,
			expectedJobs:   1,
		},
		{
			name:        "success - no videos",
			queryParams: "",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				vr.On("GetByUserID", mock.Anything, userID.String(), 50, 0).
					Return([]*domain.Video{}, int64(0), nil)
			},
			expectedStatus: http.StatusOK,
			expectedTotal:  0,
			expectedJobs:   0,
		},
		{
			name:        "error - get videos fails",
			queryParams: "",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				vr.On("GetByUserID", mock.Anything, userID.String(), 50, 0).
					Return(nil, int64(0), errors.New("database error"))
			},
			expectedStatus: http.StatusInternalServerError,
		},
		{
			name:        "success - skip videos with job errors",
			queryParams: "",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				videos := []*domain.Video{
					{ID: video1ID, UserID: userID.String()},
					{ID: video2ID, UserID: userID.String()},
				}
				vr.On("GetByUserID", mock.Anything, userID.String(), 50, 0).
					Return(videos, int64(2), nil)

				job1 := &domain.EncodingJob{
					ID:      "job1",
					VideoID: video1ID,
					Status:  domain.EncodingStatusProcessing,
				}
				er.On("GetJobsByVideoID", mock.Anything, video1ID).
					Return([]*domain.EncodingJob{job1}, nil)
				er.On("GetJobsByVideoID", mock.Anything, video2ID).
					Return(nil, errors.New("job fetch error"))
			},
			expectedStatus: http.StatusOK,
			expectedTotal:  1,
			expectedJobs:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encodingRepo := new(MockEncodingRepository)
			videoRepo := new(MockVideoRepository)
			tt.setupMocks(encodingRepo, videoRepo)

			handler := GetMyEncodingJobsHandler(encodingRepo, videoRepo)

			req := httptest.NewRequest("GET", "/api/v1/encoding/my-jobs"+tt.queryParams, nil)
			ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code)

			if tt.expectedStatus == http.StatusOK {
				var response map[string]interface{}
				err := json.Unmarshal(rr.Body.Bytes(), &response)
				assert.NoError(t, err)

				data, ok := response["data"].(map[string]interface{})
				assert.True(t, ok, "Expected data field in response")

				assert.Equal(t, float64(tt.expectedTotal), data["total"])
				jobs, ok := data["jobs"].([]interface{})
				assert.True(t, ok)
				assert.Equal(t, tt.expectedJobs, len(jobs))
			}

			encodingRepo.AssertExpectations(t)
			videoRepo.AssertExpectations(t)
		})
	}
}

func TestGetMyEncodingJobsHandler_Unauthorized(t *testing.T) {
	encodingRepo := new(MockEncodingRepository)
	videoRepo := new(MockVideoRepository)

	handler := GetMyEncodingJobsHandler(encodingRepo, videoRepo)

	req := httptest.NewRequest("GET", "/api/v1/encoding/my-jobs", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestGetEncodingJobHandler_ErrorPaths(t *testing.T) {
	userID := uuid.MustParse("11111111-1111-1111-1111-111111111111")

	tests := []struct {
		name           string
		jobID          string
		setupMocks     func(*MockEncodingRepository, *MockVideoRepository)
		expectedStatus int
		description    string
	}{
		{
			name:  "job not found - ErrNotFound",
			jobID: "job-123",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				er.On("GetJob", mock.Anything, "job-123").Return(nil, domain.ErrNotFound)
			},
			expectedStatus: http.StatusNotFound,
			description:    "should return 404 when job not found with ErrNotFound",
		},
		{
			name:  "job fetch error",
			jobID: "job-123",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				er.On("GetJob", mock.Anything, "job-123").Return(nil, errors.New("database error"))
			},
			expectedStatus: http.StatusInternalServerError,
			description:    "should return 500 when job fetch fails",
		},
		{
			name:  "video not found - domain error",
			jobID: "job-123",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				job := &domain.EncodingJob{
					ID:      "job-123",
					VideoID: "video-456",
				}
				er.On("GetJob", mock.Anything, "job-123").Return(job, nil)
				vr.On("GetByID", mock.Anything, "video-456").
					Return(nil, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
			},
			expectedStatus: http.StatusNotFound,
			description:    "should return 404 when video not found with domain error",
		},
		{
			name:  "video not found - ErrVideoNotFound",
			jobID: "job-123",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				job := &domain.EncodingJob{
					ID:      "job-123",
					VideoID: "video-456",
				}
				er.On("GetJob", mock.Anything, "job-123").Return(job, nil)
				vr.On("GetByID", mock.Anything, "video-456").Return(nil, domain.ErrVideoNotFound)
			},
			expectedStatus: http.StatusNotFound,
			description:    "should return 404 when video not found with ErrVideoNotFound",
		},
		{
			name:  "video fetch error",
			jobID: "job-123",
			setupMocks: func(er *MockEncodingRepository, vr *MockVideoRepository) {
				job := &domain.EncodingJob{
					ID:      "job-123",
					VideoID: "video-456",
				}
				er.On("GetJob", mock.Anything, "job-123").Return(job, nil)
				vr.On("GetByID", mock.Anything, "video-456").Return(nil, errors.New("database error"))
			},
			expectedStatus: http.StatusInternalServerError,
			description:    "should return 500 when video fetch fails",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			encodingRepo := new(MockEncodingRepository)
			videoRepo := new(MockVideoRepository)
			tt.setupMocks(encodingRepo, videoRepo)

			handler := GetEncodingJobHandler(encodingRepo, videoRepo)

			req := httptest.NewRequest("GET", "/api/v1/encoding/jobs/"+tt.jobID, nil)
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("jobID", tt.jobID)
			req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

			ctx := req.Context()
			ctx = context.WithValue(ctx, middleware.UserIDKey, userID.String())
			ctx = context.WithValue(ctx, middleware.UserRoleKey, "user")
			req = req.WithContext(ctx)

			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.Equal(t, tt.expectedStatus, rr.Code, tt.description)

			encodingRepo.AssertExpectations(t)
			videoRepo.AssertExpectations(t)
		})
	}
}

func TestGetEncodingJobHandler_Unauthorized(t *testing.T) {
	encodingRepo := new(MockEncodingRepository)
	videoRepo := new(MockVideoRepository)

	job := &domain.EncodingJob{
		ID:      "job-123",
		VideoID: "video-456",
	}
	encodingRepo.On("GetJob", mock.Anything, "job-123").Return(job, nil)

	handler := GetEncodingJobHandler(encodingRepo, videoRepo)

	req := httptest.NewRequest("GET", "/api/v1/encoding/jobs/job-123", nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("jobID", "job-123")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))
	// No user ID in context

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)
	encodingRepo.AssertExpectations(t)
}
