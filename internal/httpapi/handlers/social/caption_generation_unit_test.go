package social

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase/captiongen"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var _ captiongen.Service = (*mockCaptionGenService)(nil)

type mockCaptionGenService struct {
	createJobFn         func(ctx context.Context, videoID uuid.UUID, userID uuid.UUID, req *domain.CreateCaptionGenerationJobRequest) (*domain.CaptionGenerationJob, error)
	getJobStatusFn      func(ctx context.Context, jobID uuid.UUID) (*domain.CaptionGenerationJob, error)
	getJobsByVideoFn    func(ctx context.Context, videoID uuid.UUID) ([]domain.CaptionGenerationJob, error)
	processNextFn       func(ctx context.Context) (bool, error)
	runFn               func(ctx context.Context, workers int) error
	regenerateCaptionFn func(ctx context.Context, videoID uuid.UUID, userID uuid.UUID, targetLanguage *string) (*domain.CaptionGenerationJob, error)
}

func (m *mockCaptionGenService) CreateJob(ctx context.Context, videoID uuid.UUID, userID uuid.UUID, req *domain.CreateCaptionGenerationJobRequest) (*domain.CaptionGenerationJob, error) {
	if m.createJobFn != nil {
		return m.createJobFn(ctx, videoID, userID, req)
	}
	return nil, nil
}

func (m *mockCaptionGenService) GetJobStatus(ctx context.Context, jobID uuid.UUID) (*domain.CaptionGenerationJob, error) {
	if m.getJobStatusFn != nil {
		return m.getJobStatusFn(ctx, jobID)
	}
	return nil, nil
}

func (m *mockCaptionGenService) GetJobsByVideo(ctx context.Context, videoID uuid.UUID) ([]domain.CaptionGenerationJob, error) {
	if m.getJobsByVideoFn != nil {
		return m.getJobsByVideoFn(ctx, videoID)
	}
	return nil, nil
}

func (m *mockCaptionGenService) ProcessNext(ctx context.Context) (bool, error) {
	if m.processNextFn != nil {
		return m.processNextFn(ctx)
	}
	return false, nil
}

func (m *mockCaptionGenService) Run(ctx context.Context, workers int) error {
	if m.runFn != nil {
		return m.runFn(ctx, workers)
	}
	return nil
}

func (m *mockCaptionGenService) RegenerateCaption(ctx context.Context, videoID uuid.UUID, userID uuid.UUID, targetLanguage *string) (*domain.CaptionGenerationJob, error) {
	if m.regenerateCaptionFn != nil {
		return m.regenerateCaptionFn(ctx, videoID, userID, targetLanguage)
	}
	return nil, nil
}

type mockVideoRepository struct {
	getByIDFn func(ctx context.Context, videoID string) (*domain.Video, error)
}

func (m *mockVideoRepository) GetByID(ctx context.Context, videoID string) (*domain.Video, error) {
	if m.getByIDFn != nil {
		return m.getByIDFn(ctx, videoID)
	}
	return nil, nil
}

func TestGenerateCaptions_Success(t *testing.T) {
	userID := uuid.New()
	videoID := uuid.New()
	jobID := uuid.New()

	mockCaptionGen := &mockCaptionGenService{
		createJobFn: func(ctx context.Context, vid uuid.UUID, uid uuid.UUID, req *domain.CreateCaptionGenerationJobRequest) (*domain.CaptionGenerationJob, error) {
			assert.Equal(t, videoID, vid)
			assert.Equal(t, userID, uid)
			return &domain.CaptionGenerationJob{
				ID:       jobID,
				VideoID:  videoID,
				Status:   domain.CaptionGenStatusPending,
				Progress: 0,
			}, nil
		},
	}

	mockVideoRepo := &mockVideoRepository{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			assert.Equal(t, videoID.String(), vid)
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
				Status: domain.StatusCompleted,
			}, nil
		},
	}

	handler := NewCaptionGenerationHandlers(mockCaptionGen, mockVideoRepo)

	body := `{"model_size":"base","output_format":"vtt"}`
	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/captions/generate", bytes.NewBufferString(body))
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GenerateCaptions(rec, req)

	assert.Equal(t, http.StatusAccepted, rec.Code)
}

func TestGenerateCaptions_Unauthorized(t *testing.T) {
	handler := NewCaptionGenerationHandlers(&mockCaptionGenService{}, &mockVideoRepository{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+uuid.New().String()+"/captions/generate", nil)
	rec := httptest.NewRecorder()

	handler.GenerateCaptions(rec, req)

	assert.Equal(t, http.StatusUnauthorized, rec.Code)
}

func TestGenerateCaptions_InvalidVideoID(t *testing.T) {
	userID := uuid.New()
	handler := NewCaptionGenerationHandlers(&mockCaptionGenService{}, &mockVideoRepository{})

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/invalid/captions/generate", nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "invalid-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GenerateCaptions(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGenerateCaptions_VideoNotFound(t *testing.T) {
	userID := uuid.New()
	videoID := uuid.New()

	mockVideoRepo := &mockVideoRepository{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return nil, domain.ErrNotFound
		},
	}

	handler := NewCaptionGenerationHandlers(&mockCaptionGenService{}, mockVideoRepo)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/captions/generate", nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GenerateCaptions(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestGenerateCaptions_NotOwner(t *testing.T) {
	userID := uuid.New()
	otherUserID := uuid.New()
	videoID := uuid.New()

	mockVideoRepo := &mockVideoRepository{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: otherUserID.String(),
				Status: domain.StatusCompleted,
			}, nil
		},
	}

	handler := NewCaptionGenerationHandlers(&mockCaptionGenService{}, mockVideoRepo)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/captions/generate", nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GenerateCaptions(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code)
}

func TestGenerateCaptions_VideoNotProcessed(t *testing.T) {
	userID := uuid.New()
	videoID := uuid.New()

	mockVideoRepo := &mockVideoRepository{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
				Status: domain.StatusProcessing,
			}, nil
		},
	}

	handler := NewCaptionGenerationHandlers(&mockCaptionGenService{}, mockVideoRepo)

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID.String()+"/captions/generate", nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GenerateCaptions(rec, req)

	assert.Equal(t, http.StatusBadRequest, rec.Code)
}

func TestGetCaptionGenerationJob_Success(t *testing.T) {
	userID := uuid.New()
	videoID := uuid.New()
	jobID := uuid.New()

	expectedJob := &domain.CaptionGenerationJob{
		ID:      jobID,
		VideoID: videoID,
		Status:  domain.CaptionGenStatusCompleted,
	}

	mockCaptionGen := &mockCaptionGenService{
		getJobStatusFn: func(ctx context.Context, jid uuid.UUID) (*domain.CaptionGenerationJob, error) {
			assert.Equal(t, jobID, jid)
			return expectedJob, nil
		},
	}

	mockVideoRepo := &mockVideoRepository{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
				Status: domain.StatusCompleted,
			}, nil
		},
	}

	handler := NewCaptionGenerationHandlers(mockCaptionGen, mockVideoRepo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/captions/jobs/"+jobID.String(), nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("jobId", jobID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GetCaptionGenerationJob(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
}

func TestGetCaptionGenerationJob_JobNotFound(t *testing.T) {
	userID := uuid.New()
	videoID := uuid.New()
	jobID := uuid.New()

	mockCaptionGen := &mockCaptionGenService{
		getJobStatusFn: func(ctx context.Context, jid uuid.UUID) (*domain.CaptionGenerationJob, error) {
			return nil, domain.ErrNotFound
		},
	}

	mockVideoRepo := &mockVideoRepository{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
				Status: domain.StatusCompleted,
			}, nil
		},
	}

	handler := NewCaptionGenerationHandlers(mockCaptionGen, mockVideoRepo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/captions/jobs/"+jobID.String(), nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	rctx.URLParams.Add("jobId", jobID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.GetCaptionGenerationJob(rec, req)

	assert.Equal(t, http.StatusNotFound, rec.Code)
}

func TestListCaptionGenerationJobs_Success(t *testing.T) {
	userID := uuid.New()
	videoID := uuid.New()

	expectedJobs := []domain.CaptionGenerationJob{
		{ID: uuid.New(), VideoID: videoID, Status: domain.CaptionGenStatusCompleted},
		{ID: uuid.New(), VideoID: videoID, Status: domain.CaptionGenStatusPending},
	}

	mockCaptionGen := &mockCaptionGenService{
		getJobsByVideoFn: func(ctx context.Context, vid uuid.UUID) ([]domain.CaptionGenerationJob, error) {
			assert.Equal(t, videoID, vid)
			return expectedJobs, nil
		},
	}

	mockVideoRepo := &mockVideoRepository{
		getByIDFn: func(ctx context.Context, vid string) (*domain.Video, error) {
			return &domain.Video{
				ID:     videoID.String(),
				UserID: userID.String(),
				Status: domain.StatusCompleted,
			}, nil
		},
	}

	handler := NewCaptionGenerationHandlers(mockCaptionGen, mockVideoRepo)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID.String()+"/captions/jobs", nil)
	req = req.WithContext(middleware.WithUserID(req.Context(), userID))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", videoID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	rec := httptest.NewRecorder()

	handler.ListCaptionGenerationJobs(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)

	var wrapper struct {
		Data    ListCaptionGenerationJobsResponse `json:"data"`
		Success bool                              `json:"success"`
	}
	err := json.NewDecoder(rec.Body).Decode(&wrapper)
	require.NoError(t, err)
	assert.True(t, wrapper.Success)
	assert.Equal(t, 2, wrapper.Data.Count)
}
