package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
	"athena/internal/middleware"
)

// --- mock studio service ---

type mockStudioService struct {
	createJob *domain.StudioJob
	createErr error
	getJob    *domain.StudioJob
	getErr    error
	listJobs  []*domain.StudioJob
	listErr   error
}

func (m *mockStudioService) CreateEditJob(_ context.Context, _, _ string, _ domain.StudioEditRequest) (*domain.StudioJob, error) {
	return m.createJob, m.createErr
}

func (m *mockStudioService) GetJob(_ context.Context, _ string) (*domain.StudioJob, error) {
	return m.getJob, m.getErr
}

func (m *mockStudioService) ListJobsForVideo(_ context.Context, _ string) ([]*domain.StudioJob, error) {
	return m.listJobs, m.listErr
}

// --- mock video repo ---

type mockStudioVideoRepo struct {
	video *domain.Video
	err   error
}

func (m *mockStudioVideoRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	return m.video, m.err
}

// --- helpers ---

func newStudioRequest(t *testing.T, method, path string, body interface{}, videoID, jobID, userID string) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		require.NoError(t, json.NewEncoder(&buf).Encode(body))
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")

	rctx := chi.NewRouteContext()
	if videoID != "" {
		rctx.URLParams.Add("id", videoID)
	}
	if jobID != "" {
		rctx.URLParams.Add("jobId", jobID)
	}
	ctx := context.WithValue(req.Context(), chi.RouteCtxKey, rctx)
	if userID != "" {
		ctx = context.WithValue(ctx, middleware.UserIDKey, userID)
	}
	return req.WithContext(ctx)
}

func TestStudioHandlers_CreateEditJob(t *testing.T) {
	tests := []struct {
		name       string
		videoID    string
		userID     string
		body       interface{}
		video      *domain.Video
		videoErr   error
		createJob  *domain.StudioJob
		createErr  error
		wantStatus int
	}{
		{
			name:       "missing video ID",
			videoID:    "",
			userID:     "user-1",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing auth",
			videoID:    "vid-1",
			userID:     "",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "video not found",
			videoID:    "vid-1",
			userID:     "user-1",
			videoErr:   domain.ErrVideoNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "not video owner",
			videoID:    "vid-1",
			userID:     "user-2",
			video:      &domain.Video{ID: "vid-1", UserID: "user-1"},
			wantStatus: http.StatusForbidden,
		},
		{
			name:    "invalid JSON body",
			videoID: "vid-1",
			userID:  "user-1",
			video:   &domain.Video{ID: "vid-1", UserID: "user-1"},
			body:    "not json",
			// json.Encoder wraps the string as a JSON string literal; Decode into struct fails
			wantStatus: http.StatusBadRequest,
		},
		{
			name:    "service returns validation error",
			videoID: "vid-1",
			userID:  "user-1",
			video:   &domain.Video{ID: "vid-1", UserID: "user-1"},
			body: domain.StudioEditRequest{Tasks: []domain.StudioTask{
				{Name: "cut", Options: domain.StudioTaskOptions{Start: ptrF64(5), End: ptrF64(30)}},
			}},
			createErr:  domain.ErrInvalidStudioTask,
			wantStatus: http.StatusBadRequest,
		},
		{
			name:    "success",
			videoID: "vid-1",
			userID:  "user-1",
			video:   &domain.Video{ID: "vid-1", UserID: "user-1"},
			body: domain.StudioEditRequest{Tasks: []domain.StudioTask{
				{Name: "cut", Options: domain.StudioTaskOptions{Start: ptrF64(5), End: ptrF64(30)}},
			}},
			createJob:  &domain.StudioJob{ID: "job-1", VideoID: "vid-1", Status: domain.StudioJobStatusPending},
			wantStatus: http.StatusCreated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockStudioService{createJob: tt.createJob, createErr: tt.createErr}
			vRepo := &mockStudioVideoRepo{video: tt.video, err: tt.videoErr}
			h := NewStudioHandlers(svc, vRepo)

			req := newStudioRequest(t, http.MethodPost, "/api/v1/videos/"+tt.videoID+"/studio/edit", tt.body, tt.videoID, "", tt.userID)
			rec := httptest.NewRecorder()

			h.CreateEditJob(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestStudioHandlers_ListJobs(t *testing.T) {
	tests := []struct {
		name       string
		videoID    string
		listJobs   []*domain.StudioJob
		listErr    error
		wantStatus int
	}{
		{
			name:       "missing video ID",
			videoID:    "",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "empty list",
			videoID:    "vid-1",
			listJobs:   nil,
			wantStatus: http.StatusOK,
		},
		{
			name:    "with jobs",
			videoID: "vid-1",
			listJobs: []*domain.StudioJob{
				{ID: "job-1", VideoID: "vid-1"},
				{ID: "job-2", VideoID: "vid-1"},
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockStudioService{listJobs: tt.listJobs, listErr: tt.listErr}
			h := NewStudioHandlers(svc, &mockStudioVideoRepo{})

			req := newStudioRequest(t, http.MethodGet, "/api/v1/videos/"+tt.videoID+"/studio/jobs", nil, tt.videoID, "", "user-1")
			rec := httptest.NewRecorder()

			h.ListJobs(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func TestStudioHandlers_GetJob(t *testing.T) {
	tests := []struct {
		name       string
		jobID      string
		getJob     *domain.StudioJob
		getErr     error
		wantStatus int
	}{
		{
			name:       "missing job ID",
			jobID:      "",
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "job not found",
			jobID:      "nonexistent",
			getErr:     domain.ErrStudioJobNotFound,
			wantStatus: http.StatusNotFound,
		},
		{
			name:       "success",
			jobID:      "job-1",
			getJob:     &domain.StudioJob{ID: "job-1", VideoID: "vid-1"},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := &mockStudioService{getJob: tt.getJob, getErr: tt.getErr}
			h := NewStudioHandlers(svc, &mockStudioVideoRepo{})

			req := newStudioRequest(t, http.MethodGet, "/api/v1/videos/vid-1/studio/jobs/"+tt.jobID, nil, "vid-1", tt.jobID, "user-1")
			rec := httptest.NewRecorder()

			h.GetJob(rec, req)
			assert.Equal(t, tt.wantStatus, rec.Code)
		})
	}
}

func ptrF64(v float64) *float64 { return &v }
