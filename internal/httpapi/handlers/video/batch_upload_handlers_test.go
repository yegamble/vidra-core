package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	chi "github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
)

// batchUploadServiceStub implements upload.Service for handler tests.
type batchUploadServiceStub struct {
	unitUploadServiceStub
	initiateBatchFn  func(ctx context.Context, userID string, req *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error)
	getBatchStatusFn func(ctx context.Context, batchID, userID string) (*domain.BatchUploadStatus, error)
}

func (s *batchUploadServiceStub) InitiateBatchUpload(ctx context.Context, userID string, req *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error) {
	if s.initiateBatchFn != nil {
		return s.initiateBatchFn(ctx, userID, req)
	}
	return nil, nil
}
func (s *batchUploadServiceStub) GetBatchStatus(ctx context.Context, batchID, userID string) (*domain.BatchUploadStatus, error) {
	if s.getBatchStatusFn != nil {
		return s.getBatchStatusFn(ctx, batchID, userID)
	}
	return nil, nil
}

func TestBatchInitiateUploadHandler(t *testing.T) {
	tests := []struct {
		name       string
		userID     string
		body       interface{}
		maxBatch   int
		serviceFn  func(ctx context.Context, userID string, req *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error)
		wantStatus int
		wantCode   string
	}{
		{
			name:   "happy path - 3 videos",
			userID: "user-1",
			body: domain.BatchUploadRequest{
				Videos: []domain.BatchUploadVideoItem{
					{FileName: "v1.mp4", FileSize: 1024, Title: "V1"},
					{FileName: "v2.mp4", FileSize: 2048, Title: "V2"},
					{FileName: "v3.mp4", FileSize: 512, Title: "V3"},
				},
			},
			maxBatch: 10,
			serviceFn: func(_ context.Context, _ string, _ *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error) {
				return &domain.BatchUploadResponse{
					BatchID: "batch-123",
					Sessions: []domain.InitiateUploadResponse{
						{SessionID: "s1", ChunkSize: 1024, TotalChunks: 1, UploadURL: "/api/v1/uploads/s1/chunks"},
						{SessionID: "s2", ChunkSize: 1024, TotalChunks: 2, UploadURL: "/api/v1/uploads/s2/chunks"},
						{SessionID: "s3", ChunkSize: 512, TotalChunks: 1, UploadURL: "/api/v1/uploads/s3/chunks"},
					},
				}, nil
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:       "unauthorized - no user ID",
			userID:     "",
			body:       domain.BatchUploadRequest{},
			maxBatch:   10,
			wantStatus: http.StatusUnauthorized,
			wantCode:   "UNAUTHORIZED",
		},
		{
			name:   "empty batch",
			userID: "user-1",
			body: domain.BatchUploadRequest{
				Videos: []domain.BatchUploadVideoItem{},
			},
			maxBatch:   10,
			wantStatus: http.StatusBadRequest,
			wantCode:   "EMPTY_BATCH",
		},
		{
			name:   "exceeds batch limit",
			userID: "user-1",
			body: domain.BatchUploadRequest{
				Videos: []domain.BatchUploadVideoItem{
					{FileName: "v1.mp4", FileSize: 1024, Title: "V1"},
					{FileName: "v2.mp4", FileSize: 1024, Title: "V2"},
					{FileName: "v3.mp4", FileSize: 1024, Title: "V3"},
				},
			},
			maxBatch:   2,
			wantStatus: http.StatusBadRequest,
			wantCode:   "BATCH_TOO_LARGE",
		},
		{
			name:   "single video in batch",
			userID: "user-1",
			body: domain.BatchUploadRequest{
				Videos: []domain.BatchUploadVideoItem{
					{FileName: "solo.mp4", FileSize: 1024, Title: "Solo"},
				},
			},
			maxBatch: 10,
			serviceFn: func(_ context.Context, _ string, _ *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error) {
				return &domain.BatchUploadResponse{
					BatchID:  "batch-solo",
					Sessions: []domain.InitiateUploadResponse{{SessionID: "s-solo"}},
				}, nil
			},
			wantStatus: http.StatusCreated,
		},
		{
			name:   "service returns quota exceeded error",
			userID: "user-1",
			body: domain.BatchUploadRequest{
				Videos: []domain.BatchUploadVideoItem{
					{FileName: "v.mp4", FileSize: 1024, Title: "V"},
				},
			},
			maxBatch: 10,
			serviceFn: func(_ context.Context, _ string, _ *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error) {
				return nil, domain.NewDomainError("QUOTA_EXCEEDED", "quota exceeded")
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "QUOTA_EXCEEDED",
		},
		{
			name:   "service returns invalid file extension error",
			userID: "user-1",
			body: domain.BatchUploadRequest{
				Videos: []domain.BatchUploadVideoItem{
					{FileName: "script.exe", FileSize: 1024, Title: "Bad"},
				},
			},
			maxBatch: 10,
			serviceFn: func(_ context.Context, _ string, _ *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error) {
				return nil, domain.NewDomainError("INVALID_FILE_EXTENSION", "Video 1: invalid file extension \".exe\"")
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_FILE_EXTENSION",
		},
		{
			name:   "service returns file too large error",
			userID: "user-1",
			body: domain.BatchUploadRequest{
				Videos: []domain.BatchUploadVideoItem{
					{FileName: "big.mp4", FileSize: 999999999999, Title: "Big"},
				},
			},
			maxBatch: 10,
			serviceFn: func(_ context.Context, _ string, _ *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error) {
				return nil, domain.NewDomainError("INVALID_FILE_SIZE", "Video 1: file size must be between 1 byte and 10GB")
			},
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_FILE_SIZE",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stub := &batchUploadServiceStub{initiateBatchFn: tt.serviceFn}
			cfg := &config.Config{MaxBatchUploadSize: tt.maxBatch}
			handler := BatchInitiateUploadHandler(stub, cfg)

			bodyBytes, _ := json.Marshal(tt.body)
			req := httptest.NewRequest("POST", "/api/v1/uploads/batch", bytes.NewReader(bodyBytes))
			req.Header.Set("Content-Type", "application/json")
			if tt.userID != "" {
				req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, tt.userID))
			}

			w := httptest.NewRecorder()
			handler(w, req)

			assert.Equal(t, tt.wantStatus, w.Code)

			if tt.wantCode != "" {
				var envelope Response
				err := json.Unmarshal(w.Body.Bytes(), &envelope)
				require.NoError(t, err)
				assert.False(t, envelope.Success)
				require.NotNil(t, envelope.Error)
				assert.Equal(t, tt.wantCode, envelope.Error.Code)
			}

			if tt.wantStatus == http.StatusCreated {
				var envelope Response
				err := json.Unmarshal(w.Body.Bytes(), &envelope)
				require.NoError(t, err)
				assert.True(t, envelope.Success)
			}
		})
	}
}

func TestBatchInitiateUploadHandler_InvalidJSON(t *testing.T) {
	stub := &batchUploadServiceStub{}
	cfg := &config.Config{MaxBatchUploadSize: 10}
	handler := BatchInitiateUploadHandler(stub, cfg)

	req := httptest.NewRequest("POST", "/api/v1/uploads/batch", bytes.NewReader([]byte("not json")))
	req.Header.Set("Content-Type", "application/json")
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))

	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestGetBatchStatusHandler_HappyPath(t *testing.T) {
	batchID := "550e8400-e29b-41d4-a716-446655440000"
	stub := &batchUploadServiceStub{
		getBatchStatusFn: func(_ context.Context, id, uid string) (*domain.BatchUploadStatus, error) {
			return &domain.BatchUploadStatus{
				BatchID:          id,
				TotalVideos:      2,
				CompletedUploads: 1,
				ActiveUploads:    1,
			}, nil
		},
	}
	handler := GetBatchStatusHandler(stub)

	req := httptest.NewRequest("GET", "/api/v1/uploads/batch/"+batchID, nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))

	// Set up chi URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("batchId", batchID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusOK, w.Code)

	var envelope Response
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	require.NoError(t, err)
	assert.True(t, envelope.Success)
}

func TestGetBatchStatusHandler_NotFound(t *testing.T) {
	batchID := "550e8400-e29b-41d4-a716-446655440000"
	stub := &batchUploadServiceStub{
		getBatchStatusFn: func(_ context.Context, _, _ string) (*domain.BatchUploadStatus, error) {
			return nil, domain.NewDomainError("BATCH_NOT_FOUND", "Batch upload not found")
		},
	}
	handler := GetBatchStatusHandler(stub)

	req := httptest.NewRequest("GET", "/api/v1/uploads/batch/"+batchID, nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("batchId", batchID)
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestGetBatchStatusHandler_Unauthorized(t *testing.T) {
	handler := GetBatchStatusHandler(&batchUploadServiceStub{})

	req := httptest.NewRequest("GET", "/api/v1/uploads/batch/some-id", nil)

	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestGetBatchStatusHandler_InvalidBatchID(t *testing.T) {
	handler := GetBatchStatusHandler(&batchUploadServiceStub{})

	req := httptest.NewRequest("GET", "/api/v1/uploads/batch/not-a-uuid", nil)
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-1"))

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("batchId", "not-a-uuid")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
}
