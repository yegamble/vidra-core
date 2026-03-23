package video

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

type unitVideoRepoStub struct {
	createFn      func(ctx context.Context, video *domain.Video) error
	getByIDFn     func(ctx context.Context, id string) (*domain.Video, error)
	getByUserIDFn func(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error)
	updateFn      func(ctx context.Context, video *domain.Video) error
	deleteFn      func(ctx context.Context, id string, userID string) error
}

func (s *unitVideoRepoStub) Create(ctx context.Context, video *domain.Video) error {
	if s.createFn != nil {
		return s.createFn(ctx, video)
	}
	return nil
}

func (s *unitVideoRepoStub) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	if s.getByIDFn != nil {
		return s.getByIDFn(ctx, id)
	}
	return nil, domain.NewDomainError("VIDEO_NOT_FOUND", "video not found")
}

func (s *unitVideoRepoStub) GetByIDs(ctx context.Context, ids []string) ([]*domain.Video, error) {
	return nil, nil
}

func (s *unitVideoRepoStub) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	if s.getByUserIDFn != nil {
		return s.getByUserIDFn(ctx, userID, limit, offset)
	}
	return nil, 0, nil
}

func (s *unitVideoRepoStub) Update(ctx context.Context, video *domain.Video) error {
	if s.updateFn != nil {
		return s.updateFn(ctx, video)
	}
	return nil
}

func (s *unitVideoRepoStub) Delete(ctx context.Context, id string, userID string) error {
	if s.deleteFn != nil {
		return s.deleteFn(ctx, id, userID)
	}
	return nil
}

func (s *unitVideoRepoStub) List(context.Context, *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (s *unitVideoRepoStub) Search(context.Context, *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}

func (s *unitVideoRepoStub) UpdateProcessingInfo(context.Context, string, domain.ProcessingStatus, map[string]string, string, string) error {
	return nil
}

func (s *unitVideoRepoStub) UpdateProcessingInfoWithCIDs(context.Context, string, domain.ProcessingStatus, map[string]string, string, string, map[string]string, string, string) error {
	return nil
}

func (s *unitVideoRepoStub) Count(context.Context) (int64, error) { return 0, nil }

func (s *unitVideoRepoStub) GetVideosForMigration(context.Context, int) ([]*domain.Video, error) {
	return nil, nil
}

func (s *unitVideoRepoStub) GetByRemoteURI(context.Context, string) (*domain.Video, error) {
	return nil, nil
}

func (s *unitVideoRepoStub) CreateRemoteVideo(context.Context, *domain.Video) error { return nil }

type unitUploadServiceStub struct {
	initiateUploadFn func(ctx context.Context, userID string, req *domain.InitiateUploadRequest) (*domain.InitiateUploadResponse, error)
	uploadChunkFn    func(ctx context.Context, sessionID string, chunk *domain.ChunkUpload) (*domain.ChunkUploadResponse, error)
	completeUploadFn func(ctx context.Context, sessionID string) error
	getUploadStatus  func(ctx context.Context, sessionID string) (*domain.UploadSession, error)
}

func (s *unitVideoRepoStub) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (s *unitVideoRepoStub) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

type unitEncodingRepoStub struct {
	getJobCountsFn func(ctx context.Context) (map[string]int64, error)
}

func (s *unitUploadServiceStub) InitiateUpload(ctx context.Context, userID string, req *domain.InitiateUploadRequest) (*domain.InitiateUploadResponse, error) {
	if s.initiateUploadFn != nil {
		return s.initiateUploadFn(ctx, userID, req)
	}
	return nil, nil
}

func (s *unitUploadServiceStub) UploadChunk(ctx context.Context, sessionID string, chunk *domain.ChunkUpload) (*domain.ChunkUploadResponse, error) {
	if s.uploadChunkFn != nil {
		return s.uploadChunkFn(ctx, sessionID, chunk)
	}
	return nil, nil
}

func (s *unitUploadServiceStub) CompleteUpload(ctx context.Context, sessionID string) error {
	if s.completeUploadFn != nil {
		return s.completeUploadFn(ctx, sessionID)
	}
	return nil
}

func (s *unitUploadServiceStub) GetUploadStatus(ctx context.Context, sessionID string) (*domain.UploadSession, error) {
	if s.getUploadStatus != nil {
		return s.getUploadStatus(ctx, sessionID)
	}
	return nil, domain.NewDomainError("NOT_FOUND", "not found")
}

func (s *unitUploadServiceStub) AssembleChunks(context.Context, *domain.UploadSession) error {
	return nil
}
func (s *unitUploadServiceStub) CleanupTempFiles(context.Context, string) error { return nil }

func (s *unitEncodingRepoStub) CreateJob(context.Context, *domain.EncodingJob) error { return nil }
func (s *unitEncodingRepoStub) GetJob(context.Context, string) (*domain.EncodingJob, error) {
	return nil, nil
}
func (s *unitEncodingRepoStub) GetJobByVideoID(context.Context, string) (*domain.EncodingJob, error) {
	return nil, nil
}
func (s *unitEncodingRepoStub) UpdateJob(context.Context, *domain.EncodingJob) error { return nil }
func (s *unitEncodingRepoStub) DeleteJob(context.Context, string) error              { return nil }
func (s *unitEncodingRepoStub) GetPendingJobs(context.Context, int) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (s *unitEncodingRepoStub) GetNextJob(context.Context) (*domain.EncodingJob, error) {
	return nil, nil
}
func (s *unitEncodingRepoStub) UpdateJobStatus(context.Context, string, domain.EncodingStatus) error {
	return nil
}
func (s *unitEncodingRepoStub) UpdateJobProgress(context.Context, string, int) error { return nil }
func (s *unitEncodingRepoStub) SetJobError(context.Context, string, string) error    { return nil }
func (s *unitEncodingRepoStub) GetJobCounts(ctx context.Context) (map[string]int64, error) {
	if s.getJobCountsFn != nil {
		return s.getJobCountsFn(ctx)
	}
	return map[string]int64{"pending": 0, "processing": 0, "completed": 0, "failed": 0}, nil
}
func (s *unitEncodingRepoStub) ResetStaleJobs(context.Context, time.Duration) (int64, error) {
	return 0, nil
}
func (s *unitEncodingRepoStub) GetJobsByVideoID(context.Context, string) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (s *unitEncodingRepoStub) GetActiveJobsByVideoID(context.Context, string) ([]*domain.EncodingJob, error) {
	return nil, nil
}

func (s *unitEncodingRepoStub) ListJobsByStatus(context.Context, string) ([]*domain.EncodingJob, error) {
	return nil, nil
}

func decodeHandlerResponse(t *testing.T, rr *httptest.ResponseRecorder) Response {
	t.Helper()
	var response Response
	require.NoError(t, json.Unmarshal(rr.Body.Bytes(), &response))
	return response
}

func withRouteParams(r *http.Request, params map[string]string) *http.Request {
	rctx := chi.NewRouteContext()
	for k, v := range params {
		rctx.URLParams.Add(k, v)
	}
	return r.WithContext(context.WithValue(r.Context(), chi.RouteCtxKey, rctx))
}

func TestNewVideoHandlers_Unit(t *testing.T) {
	cfg := &config.Config{JWTSecret: "unit-secret"}
	handlers := NewVideoHandlers(
		nil,
		nil,
		nil,
		nil,
		nil,
		&unitUploadServiceStub{},
		nil,
		nil,
		nil,
		nil,
		nil,
		nil,
		"test-jwt-secret",
		cfg,
	)

	require.NotNil(t, handlers)
	assert.Equal(t, "test-jwt-secret", handlers.jwtSecret)
	assert.Equal(t, cfg, handlers.cfg)
	assert.NotNil(t, handlers.uploadService)
}

func TestCreateVideoHandler_UnitBranches(t *testing.T) {
	t.Run("invalid JSON", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos", strings.NewReader("{invalid"))
		CreateVideoHandler(&unitVideoRepoStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "INVALID_JSON", response.Error.Code)
	})

	t.Run("missing title", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos", strings.NewReader(`{"privacy":"public"}`))
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
		CreateVideoHandler(&unitVideoRepoStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "MISSING_TITLE", response.Error.Code)
	})

	t.Run("unauthorized", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos", strings.NewReader(`{"title":"x","privacy":"public"}`))
		CreateVideoHandler(&unitVideoRepoStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusUnauthorized, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "UNAUTHORIZED", response.Error.Code)
	})

	t.Run("create failure", func(t *testing.T) {
		repo := &unitVideoRepoStub{
			createFn: func(context.Context, *domain.Video) error {
				return errors.New("db down")
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos", strings.NewReader(`{"title":"x","privacy":"public"}`))
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
		CreateVideoHandler(repo).ServeHTTP(rr, req)

		require.Equal(t, http.StatusInternalServerError, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "CREATE_FAILED", response.Error.Code)
	})

	t.Run("success defaults tags to empty slice", func(t *testing.T) {
		var created *domain.Video
		repo := &unitVideoRepoStub{
			createFn: func(_ context.Context, video *domain.Video) error {
				created = video
				return nil
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos", strings.NewReader(`{"title":"hello","privacy":"public"}`))
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
		CreateVideoHandler(repo).ServeHTTP(rr, req)

		require.Equal(t, http.StatusCreated, rr.Code)
		assert.Contains(t, rr.Header().Get("Location"), "/api/v1/videos/")
		require.NotNil(t, created)
		assert.NotNil(t, created.Tags)
		assert.Len(t, created.Tags, 0)

		response := decodeHandlerResponse(t, rr)
		require.True(t, response.Success)
		var payload domain.Video
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(raw, &payload))
		assert.Equal(t, "hello", payload.Title)
	})
}

func TestUpdateVideoHandler_UnitBranches(t *testing.T) {
	videoID := uuid.NewString()

	t.Run("invalid UUID", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/invalid", strings.NewReader(`{"title":"x","privacy":"public"}`))
		req = withChiURLParam(req, "id", "not-a-uuid")

		UpdateVideoHandler(&unitVideoRepoStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "INVALID_VIDEO_ID", response.Error.Code)
	})

	t.Run("missing title", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID, strings.NewReader(`{"privacy":"public"}`))
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))

		UpdateVideoHandler(&unitVideoRepoStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "MISSING_TITLE", response.Error.Code)
	})

	t.Run("unauthorized", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID, strings.NewReader(`{"title":"x","privacy":"public"}`))
		req = withChiURLParam(req, "id", videoID)

		UpdateVideoHandler(&unitVideoRepoStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusUnauthorized, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "UNAUTHORIZED", response.Error.Code)
	})

	t.Run("owner mismatch forbidden", func(t *testing.T) {
		repo := &unitVideoRepoStub{
			getByIDFn: func(context.Context, string) (*domain.Video, error) {
				return &domain.Video{ID: videoID, UserID: "owner-1", Status: domain.StatusCompleted}, nil
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID, strings.NewReader(`{"title":"x","privacy":"public"}`))
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "requester-1"))

		UpdateVideoHandler(repo).ServeHTTP(rr, req)

		require.Equal(t, http.StatusForbidden, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "UNAUTHORIZED", response.Error.Code)
	})

	t.Run("update success uses fallback category and empty tags", func(t *testing.T) {
		var updateInput *domain.Video
		getCalls := 0
		repo := &unitVideoRepoStub{
			getByIDFn: func(context.Context, string) (*domain.Video, error) {
				getCalls++
				if getCalls == 1 {
					return &domain.Video{ID: videoID, UserID: "owner-1", Status: domain.StatusProcessing}, nil
				}
				return &domain.Video{ID: videoID, UserID: "owner-1", Status: domain.StatusProcessing}, nil
			},
			updateFn: func(_ context.Context, v *domain.Video) error {
				updateInput = v
				return nil
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPut, "/api/v1/videos/"+videoID, strings.NewReader(`{"title":"updated","description":"desc","privacy":"public","category":"music"}`))
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "owner-1"))

		UpdateVideoHandler(repo).ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		require.NotNil(t, updateInput)
		assert.Equal(t, "updated", updateInput.Title)
		assert.NotNil(t, updateInput.Tags)
		assert.Len(t, updateInput.Tags, 0)

		response := decodeHandlerResponse(t, rr)
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(raw, &payload))
		assert.Equal(t, "music", payload["category"])
	})
}

func TestDeleteVideoHandler_UnitBranches(t *testing.T) {
	videoID := uuid.NewString()

	t.Run("unauthorized", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/"+videoID, nil)
		req = withChiURLParam(req, "id", videoID)
		DeleteVideoHandler(&unitVideoRepoStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusUnauthorized, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "UNAUTHORIZED", response.Error.Code)
	})

	t.Run("not found domain error", func(t *testing.T) {
		repo := &unitVideoRepoStub{
			deleteFn: func(context.Context, string, string) error {
				return domain.NewDomainError("VIDEO_NOT_FOUND", "video not found")
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/"+videoID, nil)
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "owner-1"))
		DeleteVideoHandler(repo).ServeHTTP(rr, req)

		require.Equal(t, http.StatusNotFound, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "VIDEO_NOT_FOUND", response.Error.Code)
	})

	t.Run("success", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodDelete, "/api/v1/videos/"+videoID, nil)
		req = withChiURLParam(req, "id", videoID)
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "owner-1"))
		DeleteVideoHandler(&unitVideoRepoStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusNoContent, rr.Code)
	})
}

func TestUploadStatusAndResumeHandlers_UnitBranches(t *testing.T) {
	sessionID := uuid.NewString()
	activeSession := &domain.UploadSession{
		ID:             sessionID,
		TotalChunks:    5,
		UploadedChunks: []int{0, 2},
		Status:         domain.UploadStatusActive,
		ExpiresAt:      time.Now().Add(time.Hour),
	}

	t.Run("get status success", func(t *testing.T) {
		service := &unitUploadServiceStub{
			getUploadStatus: func(context.Context, string) (*domain.UploadSession, error) {
				return activeSession, nil
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/"+sessionID+"/status", nil)
		req = withChiURLParam(req, "sessionId", sessionID)

		GetUploadStatusHandler(service).ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.True(t, response.Success)
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		var got domain.UploadSession
		require.NoError(t, json.Unmarshal(raw, &got))
		assert.Equal(t, sessionID, got.ID)
		assert.Equal(t, []int{0, 2}, got.UploadedChunks)
	})

	t.Run("get status not found", func(t *testing.T) {
		service := &unitUploadServiceStub{
			getUploadStatus: func(context.Context, string) (*domain.UploadSession, error) {
				return nil, domain.NewDomainError("SESSION_NOT_FOUND", "not found")
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/"+sessionID+"/status", nil)
		req = withChiURLParam(req, "sessionId", sessionID)

		GetUploadStatusHandler(service).ServeHTTP(rr, req)

		require.Equal(t, http.StatusNotFound, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "SESSION_NOT_FOUND", response.Error.Code)
	})

	t.Run("resume returns remaining chunks and progress", func(t *testing.T) {
		service := &unitUploadServiceStub{
			getUploadStatus: func(context.Context, string) (*domain.UploadSession, error) {
				return activeSession, nil
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/"+sessionID+"/resume", nil)
		req = withChiURLParam(req, "sessionId", sessionID)

		ResumeUploadHandler(service).ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeHandlerResponse(t, rr)
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(raw, &payload))
		assert.Equal(t, sessionID, payload["session_id"])
		require.Contains(t, payload, "remaining_chunks")
		remaining := payload["remaining_chunks"].([]any)
		assert.Len(t, remaining, 3)
		assert.InDelta(t, 40.0, payload["progress_percent"].(float64), 0.0001)
	})
}

func TestCompleteUploadHandler_UnitBranches(t *testing.T) {
	sessionID := uuid.NewString()

	t.Run("missing session ID", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/complete", nil)
		req = withChiURLParam(req, "sessionId", "")
		CompleteUploadHandler(&unitUploadServiceStub{}, nil).ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "MISSING_SESSION_ID", response.Error.Code)
	})

	t.Run("invalid session ID", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/not-a-uuid/complete", nil)
		req = withChiURLParam(req, "sessionId", "not-a-uuid")
		CompleteUploadHandler(&unitUploadServiceStub{}, nil).ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "INVALID_SESSION_ID", response.Error.Code)
	})

	t.Run("domain validation error", func(t *testing.T) {
		service := &unitUploadServiceStub{
			completeUploadFn: func(context.Context, string) error {
				return domain.NewDomainError("INCOMPLETE_UPLOAD", "missing chunks")
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/"+sessionID+"/complete", nil)
		req = withChiURLParam(req, "sessionId", sessionID)

		CompleteUploadHandler(service, nil).ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "INCOMPLETE_UPLOAD", response.Error.Code)
	})

	t.Run("success", func(t *testing.T) {
		service := &unitUploadServiceStub{
			completeUploadFn: func(context.Context, string) error {
				return nil
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/uploads/"+sessionID+"/complete", nil)
		req = withChiURLParam(req, "sessionId", sessionID)

		CompleteUploadHandler(service, nil).ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeHandlerResponse(t, rr)
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(raw, &payload))
		assert.Equal(t, "completed", payload["status"])
		assert.Equal(t, sessionID, payload["session_id"])
	})
}

func TestVideoUploadCompatibilityHandlers_UnitBranches(t *testing.T) {
	videoID := uuid.NewString()
	baseReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID+"/upload/chunks", bytes.NewBufferString("chunk-data"))
		return withChiURLParam(req, "id", videoID)
	}

	t.Run("chunk upload strict mode requires checksum", func(t *testing.T) {
		cfg := &config.Config{
			ValidationStrictMode:        true,
			ValidationAllowedAlgorithms: []string{"sha256"},
		}
		req := baseReq()
		req.Header.Set("X-Chunk-Index", "0")
		req.Header.Set("X-Total-Chunks", "3")

		rr := httptest.NewRecorder()
		VideoUploadChunkHandler(&unitUploadServiceStub{}, cfg).ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "MISSING_CHECKSUM", response.Error.Code)
	})

	t.Run("chunk upload invalid checksum", func(t *testing.T) {
		cfg := &config.Config{
			ValidationStrictMode:        false,
			ValidationAllowedAlgorithms: []string{"sha256"},
		}
		req := baseReq()
		req.Header.Set("X-Chunk-Index", "0")
		req.Header.Set("X-Total-Chunks", "3")
		req.Header.Set("X-Chunk-Checksum", "not-a-valid-checksum")

		rr := httptest.NewRecorder()
		VideoUploadChunkHandler(&unitUploadServiceStub{}, cfg).ServeHTTP(rr, req)

		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
	})

	t.Run("chunk upload success", func(t *testing.T) {
		cfg := &config.Config{
			ValidationStrictMode: false,
		}
		req := baseReq()
		req.Header.Set("X-Chunk-Index", "1")
		req.Header.Set("X-Total-Chunks", "3")

		rr := httptest.NewRecorder()
		VideoUploadChunkHandler(&unitUploadServiceStub{}, cfg).ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeHandlerResponse(t, rr)
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(raw, &payload))
		assert.Equal(t, videoID, payload["video_id"])
		assert.Equal(t, float64(1), payload["chunk_index"])
		assert.Equal(t, true, payload["uploaded"])
	})

	t.Run("complete upload handler invalid UUID", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/not-a-uuid/upload/complete", nil)
		req = withChiURLParam(req, "id", "not-a-uuid")
		rr := httptest.NewRecorder()
		VideoCompleteUploadHandler(&unitUploadServiceStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusNotFound, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "VIDEO_NOT_FOUND", response.Error.Code)
	})

	t.Run("complete upload handler success", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID+"/upload/complete", nil)
		req = withChiURLParam(req, "id", videoID)
		rr := httptest.NewRecorder()
		VideoCompleteUploadHandler(&unitUploadServiceStub{}).ServeHTTP(rr, req)

		require.Equal(t, http.StatusOK, rr.Code)
		response := decodeHandlerResponse(t, rr)
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(raw, &payload))
		assert.Equal(t, videoID, payload["video_id"])
		assert.Equal(t, "completed", payload["status"])
	})
}

func TestVideoValidationHelpers_Unit(t *testing.T) {
	mp4Head := make([]byte, 12)
	copy(mp4Head[4:8], []byte("ftyp"))
	mkvHead := []byte{0x1A, 0x45, 0xDF, 0xA3}
	aviHead := []byte("RIFF0000AVI ")

	assert.True(t, isAllowedVideoExt(".mp4"))
	assert.True(t, isAllowedVideoExt(".webm"))
	assert.False(t, isAllowedVideoExt(".txt"))

	assert.True(t, isAllowedVideoMime("video/mp4"))
	assert.True(t, isAllowedVideoMime("video/quicktime"))
	assert.False(t, isAllowedVideoMime("text/plain"))

	assert.True(t, hasKnownVideoSignature(mp4Head, ".mp4"))
	assert.True(t, hasKnownVideoSignature(mkvHead, ".webm"))
	assert.True(t, hasKnownVideoSignature(aviHead, ".avi"))
	assert.False(t, hasKnownVideoSignature([]byte("hello"), ".mp4"))

	assert.True(t, isAllowedVideo(".mp4", mp4Head, "application/octet-stream"))
	assert.True(t, isAllowedVideo(".txt", []byte("random"), "video/mp4"))
	assert.False(t, isAllowedVideo(".txt", []byte("random"), "text/plain"))

	assert.Equal(t, ".mp4", extFromContentType("video/mp4"))
	assert.Equal(t, ".mov", extFromContentType("video/quicktime"))
	assert.Equal(t, ".mp4", extFromContentType("unknown/type"))
	assert.Equal(t, ".mp4", extFromContentType("application/octet-stream"))

	assert.True(t, isRemoteURL("https://example.com/master.m3u8"))
	assert.True(t, isRemoteURL("http://example.com/master.m3u8"))
	assert.False(t, isRemoteURL("/tmp/master.m3u8"))
}

func TestStreamHelpers_UnitBranches(t *testing.T) {
	t.Run("validate stream request", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/stream", nil)

		ctx, ok := validateStreamRequest(rr, req)
		require.False(t, ok)
		require.Nil(t, ctx)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/stream?quality=bogus", nil)
		req = withChiURLParam(req, "id", "video-1")
		ctx, ok = validateStreamRequest(rr, req)
		require.False(t, ok)
		require.Nil(t, ctx)
		assert.Equal(t, http.StatusBadRequest, rr.Code)

		rr = httptest.NewRecorder()
		req = httptest.NewRequest(http.MethodGet, "/api/v1/videos/id/stream?quality=720p", nil)
		req = withChiURLParam(req, "id", "video-1")
		ctx, ok = validateStreamRequest(rr, req)
		require.True(t, ok)
		require.NotNil(t, ctx)
		assert.Equal(t, "video-1", ctx.videoID)
		assert.Equal(t, "720p", ctx.quality)
	})

	t.Run("fetch video error mapping", func(t *testing.T) {
		rr := httptest.NewRecorder()
		video, ok := fetchVideo(rr, httptest.NewRequest(http.MethodGet, "/", nil), nil, "video-id")
		require.False(t, ok)
		require.Nil(t, video)
		assert.Equal(t, http.StatusNotFound, rr.Code)

		repo := &unitVideoRepoStub{
			getByIDFn: func(context.Context, string) (*domain.Video, error) {
				return nil, errors.New("dial timeout")
			},
		}
		rr = httptest.NewRecorder()
		video, ok = fetchVideo(rr, httptest.NewRequest(http.MethodGet, "/", nil), repo, "video-id")
		require.False(t, ok)
		require.Nil(t, video)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)

		repo = &unitVideoRepoStub{
			getByIDFn: func(context.Context, string) (*domain.Video, error) {
				return nil, domain.NewDomainError("VIDEO_NOT_FOUND", "not found")
			},
		}
		rr = httptest.NewRecorder()
		video, ok = fetchVideo(rr, httptest.NewRequest(http.MethodGet, "/", nil), repo, "video-id")
		require.False(t, ok)
		require.Nil(t, video)
		assert.Equal(t, http.StatusNotFound, rr.Code)

		repo = &unitVideoRepoStub{
			getByIDFn: func(context.Context, string) (*domain.Video, error) {
				domainErr := domain.NewDomainError("SOMETHING_ELSE", "other")
				return nil, &domainErr
			},
		}
		rr = httptest.NewRecorder()
		video, ok = fetchVideo(rr, httptest.NewRequest(http.MethodGet, "/", nil), repo, "video-id")
		require.False(t, ok)
		require.Nil(t, video)
		assert.Equal(t, http.StatusInternalServerError, rr.Code)

		repo = &unitVideoRepoStub{
			getByIDFn: func(context.Context, string) (*domain.Video, error) {
				return &domain.Video{ID: "video-id"}, nil
			},
		}
		rr = httptest.NewRecorder()
		video, ok = fetchVideo(rr, httptest.NewRequest(http.MethodGet, "/", nil), repo, "video-id")
		require.True(t, ok)
		require.NotNil(t, video)
		assert.Equal(t, "video-id", video.ID)
	})
}

func TestTryServeFromOutputPaths_UnitBranches(t *testing.T) {
	t.Run("handles nil video and invalid quality", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/v1/stream", nil)
		rr := httptest.NewRecorder()
		assert.False(t, tryServeFromOutputPaths(rr, req, &streamHandlerContext{}))

		ctx := &streamHandlerContext{
			quality: "not-a-quality",
			video:   &domain.Video{OutputPaths: map[string]string{}},
		}
		rr = httptest.NewRecorder()
		assert.True(t, tryServeFromOutputPaths(rr, req, ctx))
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("remote URL redirects", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/v1/stream", nil)
		ctx := &streamHandlerContext{
			video: &domain.Video{
				OutputPaths: map[string]string{
					"master": "https://example.com/video/master.m3u8",
				},
			},
		}
		rr := httptest.NewRecorder()
		assert.True(t, tryServeFromOutputPaths(rr, req, ctx))
		assert.Equal(t, http.StatusTemporaryRedirect, rr.Code)
		assert.Equal(t, "https://example.com/video/master.m3u8", rr.Header().Get("Location"))
	})

	t.Run("local hls path redirects to static hls endpoint", func(t *testing.T) {
		videoID := "unit-" + uuid.NewString()
		videoDir := filepath.Join("./storage", "streaming-playlists", "hls", videoID)
		masterPath := filepath.Join(videoDir, "master.m3u8")
		require.NoError(t, os.MkdirAll(videoDir, 0o750))
		require.NoError(t, os.WriteFile(masterPath, []byte("#EXTM3U"), 0o600))
		t.Cleanup(func() {
			_ = os.RemoveAll(videoDir)
		})

		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/stream", nil)
		ctx := &streamHandlerContext{
			videoID: videoID,
			video: &domain.Video{
				OutputPaths: map[string]string{
					"master": masterPath,
				},
			},
		}
		rr := httptest.NewRecorder()
		assert.True(t, tryServeFromOutputPaths(rr, req, ctx))
		assert.Equal(t, http.StatusTemporaryRedirect, rr.Code)
		assert.Equal(t, "/api/v1/hls/"+videoID+"/master.m3u8", rr.Header().Get("Location"))
	})

	t.Run("local non-hls path writes file bytes", func(t *testing.T) {
		tmpPath := filepath.Join(t.TempDir(), "master.m3u8")
		require.NoError(t, os.WriteFile(tmpPath, []byte("#EXTM3U\n# unit"), 0o600))
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/v1/stream", nil)
		ctx := &streamHandlerContext{
			video: &domain.Video{
				OutputPaths: map[string]string{
					"master": tmpPath,
				},
			},
		}
		rr := httptest.NewRecorder()
		assert.True(t, tryServeFromOutputPaths(rr, req, ctx))
		assert.Equal(t, "#EXTM3U\n# unit", rr.Body.String())
	})
}

func TestTryServeFromLocalDirectory_UnitBranches(t *testing.T) {
	t.Run("invalid quality handled", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/v1/stream", nil)
		rr := httptest.NewRecorder()
		ctx := &streamHandlerContext{videoID: "v1", quality: "weird"}
		assert.True(t, tryServeFromLocalDirectory(rr, req, ctx))
		assert.Equal(t, http.StatusBadRequest, rr.Code)
	})

	t.Run("missing file returns false", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/v2/stream", nil)
		rr := httptest.NewRecorder()
		ctx := &streamHandlerContext{videoID: "unit-missing-" + uuid.NewString()}
		assert.False(t, tryServeFromLocalDirectory(rr, req, ctx))
	})

	t.Run("existing master redirects", func(t *testing.T) {
		videoID := "unit-local-" + uuid.NewString()
		videoDir := filepath.Join("./storage", "streaming-playlists", "hls", videoID)
		masterPath := filepath.Join(videoDir, "master.m3u8")
		require.NoError(t, os.MkdirAll(videoDir, 0o750))
		require.NoError(t, os.WriteFile(masterPath, []byte("#EXTM3U"), 0o600))
		t.Cleanup(func() {
			_ = os.RemoveAll(videoDir)
		})

		req := httptest.NewRequest(http.MethodGet, "/api/v1/videos/"+videoID+"/stream", nil)
		rr := httptest.NewRecorder()
		ctx := &streamHandlerContext{videoID: videoID}
		assert.True(t, tryServeFromLocalDirectory(rr, req, ctx))
		assert.Equal(t, http.StatusTemporaryRedirect, rr.Code)
		assert.Equal(t, "/api/v1/hls/"+videoID+"/master.m3u8", rr.Header().Get("Location"))
	})
}

func TestTryServeFromS3URLs_UnitBranches(t *testing.T) {
	t.Run("no S3URLs returns false", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := &streamHandlerContext{videoID: "vid-1", video: &domain.Video{S3URLs: nil}}
		assert.False(t, tryServeFromS3URLs(rr, req, ctx))
	})

	t.Run("matching master S3URL redirects 307", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		s3URL := "https://s3.example.com/master.m3u8"
		ctx := &streamHandlerContext{
			videoID: "vid-2",
			quality: "",
			video:   &domain.Video{S3URLs: map[string]string{"master": s3URL}},
		}
		assert.True(t, tryServeFromS3URLs(rr, req, ctx))
		assert.Equal(t, http.StatusTemporaryRedirect, rr.Code)
		assert.Equal(t, s3URL, rr.Header().Get("Location"))
	})

	t.Run("matching quality S3URL redirects 307", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		s3URL := "https://s3.example.com/720p/stream.m3u8"
		ctx := &streamHandlerContext{
			videoID: "vid-3",
			quality: "720p",
			video:   &domain.Video{S3URLs: map[string]string{"720p": s3URL}},
		}
		assert.True(t, tryServeFromS3URLs(rr, req, ctx))
		assert.Equal(t, http.StatusTemporaryRedirect, rr.Code)
		assert.Equal(t, s3URL, rr.Header().Get("Location"))
	})

	t.Run("quality not in S3URLs returns false", func(t *testing.T) {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		ctx := &streamHandlerContext{
			videoID: "vid-4",
			quality: "1080p",
			video:   &domain.Video{S3URLs: map[string]string{"720p": "https://s3.example.com/720p.m3u8"}},
		}
		assert.False(t, tryServeFromS3URLs(rr, req, ctx))
	})
}

func TestHLSRelPath_Unit(t *testing.T) {
	videoID := "unit-rel-" + uuid.NewString()
	localPath := filepath.Join("./storage", "streaming-playlists", "hls", videoID, "master.m3u8")
	rel, ok := hlsRelPath(localPath)
	require.True(t, ok)
	assert.Equal(t, filepath.ToSlash(filepath.Join(videoID, "master.m3u8")), rel)

	_, ok = hlsRelPath(filepath.Join(t.TempDir(), "outside.m3u8"))
	require.False(t, ok)
}

func TestVideoUploadChunkHandler_MissingAndInvalidHeaders(t *testing.T) {
	cfg := &config.Config{ValidationStrictMode: false}
	videoID := uuid.NewString()
	makeReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/"+videoID+"/upload/chunks", io.NopCloser(bytes.NewReader([]byte("data"))))
		return withRouteParams(req, map[string]string{"id": videoID})
	}

	t.Run("missing chunk index", func(t *testing.T) {
		req := makeReq()
		req.Header.Set("X-Total-Chunks", "2")
		rr := httptest.NewRecorder()
		VideoUploadChunkHandler(&unitUploadServiceStub{}, cfg).ServeHTTP(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "MISSING_CHUNK_INDEX", response.Error.Code)
	})

	t.Run("invalid total chunks", func(t *testing.T) {
		req := makeReq()
		req.Header.Set("X-Chunk-Index", "0")
		req.Header.Set("X-Total-Chunks", "0")
		rr := httptest.NewRecorder()
		VideoUploadChunkHandler(&unitUploadServiceStub{}, cfg).ServeHTTP(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "INVALID_TOTAL_CHUNKS", response.Error.Code)
	})
}

func TestResumeUploadHandler_ProgressForCompletedUploads(t *testing.T) {
	sessionID := uuid.NewString()
	service := &unitUploadServiceStub{
		getUploadStatus: func(context.Context, string) (*domain.UploadSession, error) {
			return &domain.UploadSession{
				ID:             sessionID,
				TotalChunks:    4,
				UploadedChunks: []int{0, 1, 2, 3},
				Status:         domain.UploadStatusCompleted,
				ExpiresAt:      time.Now().Add(time.Hour),
			}, nil
		},
	}

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/uploads/"+sessionID+"/resume", nil)
	req = withChiURLParam(req, "sessionId", sessionID)
	ResumeUploadHandler(service).ServeHTTP(rr, req)

	require.Equal(t, http.StatusOK, rr.Code)
	response := decodeHandlerResponse(t, rr)
	raw, err := json.Marshal(response.Data)
	require.NoError(t, err)
	var payload map[string]any
	require.NoError(t, json.Unmarshal(raw, &payload))
	assert.Equal(t, 100.0, payload["progress_percent"])
	remaining, ok := payload["remaining_chunks"]
	if !ok || remaining == nil {
		return
	}
	assert.Equal(t, 0, len(remaining.([]any)))
}

func buildMultipartUploadRequest(t *testing.T, filename string, fileData []byte, fields map[string]string) *http.Request {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	if filename != "" {
		part, err := writer.CreateFormFile("video", filename)
		require.NoError(t, err)
		_, err = part.Write(fileData)
		require.NoError(t, err)
	}

	for k, v := range fields {
		require.NoError(t, writer.WriteField(k, v))
	}
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/api/v1/videos/upload-file", &body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	return req
}

func TestEncodingStatusHandler_Unit(t *testing.T) {
	t.Run("repository error", func(t *testing.T) {
		repo := &unitEncodingRepoStub{
			getJobCountsFn: func(context.Context) (map[string]int64, error) {
				return nil, errors.New("db error")
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/encoding/status", nil)
		EncodingStatusHandler(repo).ServeHTTP(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
	})

	t.Run("success", func(t *testing.T) {
		repo := &unitEncodingRepoStub{
			getJobCountsFn: func(context.Context) (map[string]int64, error) {
				return map[string]int64{
					"pending":    3,
					"processing": 1,
					"completed":  7,
					"failed":     2,
				}, nil
			},
		}
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/encoding/status", nil)
		EncodingStatusHandler(repo).ServeHTTP(rr, req)
		require.Equal(t, http.StatusOK, rr.Code)

		response := decodeHandlerResponse(t, rr)
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(raw, &payload))
		assert.Equal(t, float64(3), payload["pending"])
		assert.Equal(t, float64(1), payload["processing"])
		assert.Equal(t, float64(7), payload["completed"])
		assert.Equal(t, float64(2), payload["failed"])
	})
}

func TestUploadVideoFileHandler_UnitBranches(t *testing.T) {
	validMP4Bytes := make([]byte, 64)
	copy(validMP4Bytes[4:8], []byte("ftyp"))

	t.Run("unauthorized", func(t *testing.T) {
		req := buildMultipartUploadRequest(t, "sample.mp4", validMP4Bytes, map[string]string{"title": "video"})
		rr := httptest.NewRecorder()
		UploadVideoFileHandler(&unitVideoRepoStub{}, &config.Config{StorageDir: t.TempDir()}).ServeHTTP(rr, req)
		require.Equal(t, http.StatusUnauthorized, rr.Code)
	})

	t.Run("missing file", func(t *testing.T) {
		req := buildMultipartUploadRequest(t, "", nil, map[string]string{"title": "video"})
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
		rr := httptest.NewRecorder()
		UploadVideoFileHandler(&unitVideoRepoStub{}, &config.Config{StorageDir: t.TempDir()}).ServeHTTP(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "MISSING_FILE", response.Error.Code)
	})

	t.Run("missing title", func(t *testing.T) {
		req := buildMultipartUploadRequest(t, "sample.mp4", validMP4Bytes, map[string]string{})
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
		rr := httptest.NewRecorder()
		UploadVideoFileHandler(&unitVideoRepoStub{}, &config.Config{StorageDir: t.TempDir()}).ServeHTTP(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "MISSING_TITLE", response.Error.Code)
	})

	t.Run("unsupported media type", func(t *testing.T) {
		req := buildMultipartUploadRequest(t, "sample.txt", []byte("not-a-video"), map[string]string{"title": "bad"})
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
		rr := httptest.NewRecorder()
		UploadVideoFileHandler(&unitVideoRepoStub{}, &config.Config{StorageDir: t.TempDir()}).ServeHTTP(rr, req)
		require.Equal(t, http.StatusBadRequest, rr.Code)
		response := decodeHandlerResponse(t, rr)
		require.NotNil(t, response.Error)
		assert.Equal(t, "UNSUPPORTED_MEDIA", response.Error.Code)
	})

	t.Run("repository failure cleans up written file", func(t *testing.T) {
		var attemptedVideoID string
		repo := &unitVideoRepoStub{
			createFn: func(_ context.Context, v *domain.Video) error {
				attemptedVideoID = v.ID
				return errors.New("insert failed")
			},
		}
		storageDir := t.TempDir()
		req := buildMultipartUploadRequest(t, "sample.mp4", validMP4Bytes, map[string]string{"title": "video"})
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
		rr := httptest.NewRecorder()
		UploadVideoFileHandler(repo, &config.Config{StorageDir: storageDir}).ServeHTTP(rr, req)
		require.Equal(t, http.StatusInternalServerError, rr.Code)
		require.NotEmpty(t, attemptedVideoID)

		expectedPath := filepath.Join(storageDir, "web-videos", attemptedVideoID+".mp4")
		_, err := os.Stat(expectedPath)
		assert.True(t, os.IsNotExist(err), "expected temporary file to be removed after repo failure")
	})

	t.Run("success stores file and returns created payload", func(t *testing.T) {
		var created *domain.Video
		repo := &unitVideoRepoStub{
			createFn: func(_ context.Context, v *domain.Video) error {
				created = v
				return nil
			},
		}
		storageDir := t.TempDir()
		req := buildMultipartUploadRequest(t, "sample.mp4", validMP4Bytes, map[string]string{
			"title":       "video",
			"description": "desc",
			"privacy":     "public",
		})
		req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))
		rr := httptest.NewRecorder()
		UploadVideoFileHandler(repo, &config.Config{StorageDir: storageDir}).ServeHTTP(rr, req)
		require.Equal(t, http.StatusCreated, rr.Code)
		require.NotNil(t, created)

		expectedPath := filepath.Join(storageDir, "web-videos", created.ID+".mp4")
		_, err := os.Stat(expectedPath)
		require.NoError(t, err)

		response := decodeHandlerResponse(t, rr)
		raw, err := json.Marshal(response.Data)
		require.NoError(t, err)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(raw, &payload))
		assert.Equal(t, created.ID, payload["id"])
		assert.Equal(t, "video", payload["title"])
	})
}
