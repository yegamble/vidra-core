package video

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
)

// Mock UploadService
type mockUploadService struct {
	usecase.UploadService
}

func (m *mockUploadService) UploadChunk(ctx context.Context, sessionID string, chunk *domain.ChunkUpload) (*domain.ChunkUploadResponse, error) {
	return &domain.ChunkUploadResponse{Uploaded: true}, nil
}

type dummyReader struct {
	size int64
	read int64
}

func (r *dummyReader) Read(p []byte) (n int, err error) {
	if r.read >= r.size {
		return 0, io.EOF
	}
	remaining := r.size - r.read
	if int64(len(p)) > remaining {
		n = int(remaining)
	} else {
		n = len(p)
	}
	// We don't strictly need to fill p with data for this test, but it's safer
	for i := 0; i < n; i++ {
		p[i] = 'a'
	}
	r.read += int64(n)
	return n, nil
}

func TestUploadChunkHandler_TooLarge(t *testing.T) {
	// No need to skip in short mode as we don't use DB

	// Mock service
	uploadService := &mockUploadService{}

	// Random session ID
	sessionID := uuid.NewString()

	// Prepare large chunk data (105MB + 1 byte)
	size := 105*1024*1024 + 1
	largeReader := &dummyReader{size: int64(size)}

	// Create HTTP request
	httpReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID), largeReader)
	httpReq.Header.Set("X-Chunk-Index", "0")
	// Use a dummy checksum
	httpReq.Header.Set("X-Chunk-Checksum", "dummy")

	// Add URL parameters
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", sessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	cfg := &config.Config{
		ValidationStrictMode: false,
	}

	handler := UploadChunkHandler(uploadService, cfg)
	handler(w, httpReq)

	// Assert response is 400 Bad Request
	assert.Equal(t, http.StatusBadRequest, w.Code)
	// Check that the body indicates read failure
	// The error code in handlers/video/videos.go is "READ_FAILED"
	assert.Contains(t, w.Body.String(), "READ_FAILED")
}

func TestUploadVideoFileHandler_TooLarge(t *testing.T) {
	// 1MB limit for testing + 10MB buffer = 11MB limit
	cfg := &config.Config{
		MaxUploadSize: 1024 * 1024,
	}

	// 12MB payload should trigger failure
	size := 12 * 1024 * 1024
	largeReader := &dummyReader{size: int64(size)}

	req := httptest.NewRequest("POST", "/api/v1/videos/upload", largeReader)
	req.Header.Set("Content-Type", "multipart/form-data; boundary=boundary")
	// Add user context
	req = req.WithContext(context.WithValue(req.Context(), middleware.UserIDKey, "user-id"))

	w := httptest.NewRecorder()

	// Pass nil for repo as it shouldn't be reached
	handler := UploadVideoFileHandler(nil, cfg)
	handler(w, req)

	assert.Equal(t, http.StatusBadRequest, w.Code)
	assert.Contains(t, w.Body.String(), "INVALID_MULTIPART")
}
