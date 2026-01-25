package video

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/testutil"
	"athena/internal/usecase"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// createTestConfig creates a config suitable for testing
func createTestConfig() *config.Config {
	return &config.Config{
		ValidationStrictMode:          false, // Allow optional checksums in tests
		ValidationAllowedAlgorithms:   []string{"sha256"},
		ValidationTestMode:            true, // Enable test mode for bypasses
		ValidationEnableIntegrityJobs: false,
		ValidationLogEvents:           false,
	}
}

func TestInitiateUploadHandler(t *testing.T) {
	// Refactored to use mocks - no longer skips in short mode
	mockService := new(MockUploadService)
	mockVideoRepo := new(MockVideoRepository)

	userID := uuid.NewString()

	// Prepare request
	req := domain.InitiateUploadRequest{
		FileName:  "test_video.mp4",
		FileSize:  1048500, // 100 chunks of ~10KB
		ChunkSize: 10485,
	}

	expectedResponse := &domain.InitiateUploadResponse{
		SessionID:   uuid.NewString(),
		ChunkSize:   req.ChunkSize,
		TotalChunks: 100,
		UploadURL:   fmt.Sprintf("/api/v1/uploads/%s/chunks", uuid.NewString()),
	}

	// Expect InitiateUpload to be called
	mockService.On("InitiateUpload", mock.Anything, userID, &req).Return(expectedResponse, nil)

	reqBody, _ := json.Marshal(req)

	// Create HTTP request
	httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), middleware.UserIDKey, userID))

	// Create response recorder
	w := httptest.NewRecorder()

	// Call handler
	handler := InitiateUploadHandler(mockService, mockVideoRepo)
	handler(w, httpReq)

	// Assert response
	assert.Equal(t, http.StatusCreated, w.Code)

	var envelope Response
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	require.NoError(t, err)
	require.True(t, envelope.Success)

	// Decode inner data payload into the expected type
	var response domain.InitiateUploadResponse
	dataBytes, _ := json.Marshal(envelope.Data)
	err = json.Unmarshal(dataBytes, &response)
	require.NoError(t, err)

	assert.Equal(t, expectedResponse.SessionID, response.SessionID)
	assert.Equal(t, req.ChunkSize, response.ChunkSize)
	assert.Equal(t, 100, response.TotalChunks)

	mockService.AssertExpectations(t)
}

func TestInitiateUploadHandler_Unauthorized(t *testing.T) {
	mockService := new(MockUploadService)
	mockVideoRepo := new(MockVideoRepository)

	req := domain.InitiateUploadRequest{
		FileName:  "test_video.mp4",
		FileSize:  1048576,
		ChunkSize: 10485,
	}

	reqBody, _ := json.Marshal(req)

	// Create HTTP request without user context
	httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")

	w := httptest.NewRecorder()

	handler := InitiateUploadHandler(mockService, mockVideoRepo)
	handler(w, httpReq)

	assert.Equal(t, http.StatusUnauthorized, w.Code)
}

func TestUploadChunkHandler(t *testing.T) {
	mockService := new(MockUploadService)
	cfg := createTestConfig()

	sessionID := uuid.NewString()
	chunkIndex := 0
	chunkData := []byte("test chunk data for chunk 0")
	hasher := sha256.New()
	hasher.Write(chunkData)
	checksum := hex.EncodeToString(hasher.Sum(nil))

	// Expect UploadChunk to be called
	mockService.On("UploadChunk", mock.Anything, sessionID, mock.MatchedBy(func(c *domain.ChunkUpload) bool {
		return c.ChunkIndex == chunkIndex && c.Checksum == checksum && bytes.Equal(c.Data, chunkData)
	})).Return(&domain.ChunkUploadResponse{
		ChunkIndex: chunkIndex,
		Uploaded:   true,
		RemainingChunks: []int{1, 2, 3},
	}, nil)

	// Create HTTP request
	httpReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID), bytes.NewReader(chunkData))
	httpReq.Header.Set("X-Chunk-Index", "0")
	httpReq.Header.Set("X-Chunk-Checksum", checksum)

	// Add URL parameters
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", sessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler := UploadChunkHandler(mockService, cfg)
	handler(w, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)

	var envelope Response
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	require.NoError(t, err)
	require.True(t, envelope.Success)

	var chunkResponse domain.ChunkUploadResponse
	dataBytes, _ := json.Marshal(envelope.Data)
	err = json.Unmarshal(dataBytes, &chunkResponse)
	require.NoError(t, err)

	assert.Equal(t, 0, chunkResponse.ChunkIndex)
	assert.True(t, chunkResponse.Uploaded)

	mockService.AssertExpectations(t)
}

func TestUploadChunkHandler_InvalidChecksum(t *testing.T) {
	mockService := new(MockUploadService)
	cfg := createTestConfig()

	sessionID := uuid.NewString()
	chunkData := []byte("test chunk data")

	// Create HTTP request with invalid checksum
	httpReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID), bytes.NewReader(chunkData))
	httpReq.Header.Set("X-Chunk-Index", "0")
	httpReq.Header.Set("X-Chunk-Checksum", "invalid_checksum")

	// Add URL parameters
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", sessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler := UploadChunkHandler(mockService, cfg)
	handler(w, httpReq)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var envelope Response
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	require.NoError(t, err)
	require.False(t, envelope.Success)
	require.NotNil(t, envelope.Error)
	assert.Contains(t, envelope.Error.Code, "CHECKSUM_MISMATCH")

	mockService.AssertNotCalled(t, "UploadChunk")
}

func TestCompleteUploadHandler(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration tests in short mode")
	}

	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

	tempDir := t.TempDir()
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Setup test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	response := initiateTestUpload(t, uploadService, ctx, user.ID)

	// Upload all chunks first
	uploadAllTestChunks(t, uploadService, ctx, response.SessionID, response.TotalChunks)

	// Create HTTP request
	httpReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/complete", response.SessionID), nil)

	// Add URL parameters
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", response.SessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler := CompleteUploadHandler(uploadService, encodingRepo)
	handler(w, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)

	var envelope Response
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	require.NoError(t, err)
	require.True(t, envelope.Success)

	// Response is a map payload
	var completeResponse map[string]interface{}
	dataBytes, _ := json.Marshal(envelope.Data)
	err = json.Unmarshal(dataBytes, &completeResponse)
	require.NoError(t, err)

	assert.Equal(t, response.SessionID, completeResponse["session_id"])
	assert.Equal(t, "completed", completeResponse["status"])
	assert.Contains(t, completeResponse["message"], "processing queued")
}

func TestGetUploadStatusHandler(t *testing.T) {
	mockService := new(MockUploadService)

	sessionID := uuid.NewString()
	expectedSession := &domain.UploadSession{
		ID:             sessionID,
		Status:         domain.UploadStatusActive,
		TotalChunks:    10,
		UploadedChunks: []int{0, 2},
	}

	mockService.On("GetUploadStatus", mock.Anything, sessionID).Return(expectedSession, nil)

	// Create HTTP request
	httpReq := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/uploads/%s/status", sessionID), nil)

	// Add URL parameters
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", sessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler := GetUploadStatusHandler(mockService)
	handler(w, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)

	var envelope Response
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	require.NoError(t, err)
	require.True(t, envelope.Success)

	var session domain.UploadSession
	dataBytes, _ := json.Marshal(envelope.Data)
	err = json.Unmarshal(dataBytes, &session)
	require.NoError(t, err)

	assert.Equal(t, sessionID, session.ID)
	assert.Equal(t, domain.UploadStatusActive, session.Status)
	assert.Len(t, session.UploadedChunks, 2)
	assert.Contains(t, session.UploadedChunks, 0)
	assert.Contains(t, session.UploadedChunks, 2)

	mockService.AssertExpectations(t)
}

func TestResumeUploadHandler(t *testing.T) {
	mockService := new(MockUploadService)

	sessionID := uuid.NewString()
	expectedSession := &domain.UploadSession{
		ID:             sessionID,
		Status:         domain.UploadStatusActive,
		TotalChunks:    10,
		UploadedChunks: []int{0, 1, 3},
		ExpiresAt:      time.Now().Add(1 * time.Hour),
	}

	mockService.On("GetUploadStatus", mock.Anything, sessionID).Return(expectedSession, nil)

	// Create HTTP request
	httpReq := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/uploads/%s/resume", sessionID), nil)

	// Add URL parameters
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", sessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()

	handler := ResumeUploadHandler(mockService)
	handler(w, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)

	var envelope Response
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	require.NoError(t, err)
	require.True(t, envelope.Success)

	var resumeResponse map[string]interface{}
	dataBytes, _ := json.Marshal(envelope.Data)
	err = json.Unmarshal(dataBytes, &resumeResponse)
	require.NoError(t, err)

	assert.Equal(t, sessionID, resumeResponse["session_id"])
	assert.Equal(t, float64(10), resumeResponse["total_chunks"])

	// Check uploaded chunks
	uploadedChunks := resumeResponse["uploaded_chunks"].([]interface{})
	assert.Len(t, uploadedChunks, 3)

	// Check remaining chunks
	remainingChunks := resumeResponse["remaining_chunks"].([]interface{})
	expectedRemaining := 7
	assert.Len(t, remainingChunks, expectedRemaining)

	mockService.AssertExpectations(t)
}

func TestUploadHandlers_InvalidSessionID(t *testing.T) {
	mockService := new(MockUploadService)
	// We need a real encoding repo for CompleteUploadHandler signature, or we can just pass nil if it's not used
	// before the validation check.

	// Create handlers
	// For CompleteUploadHandler, we pass nil for encodingRepo because session ID validation happens first

	handlers := []struct {
		name    string
		handler http.HandlerFunc
		method  string
		path    string
	}{
		{"UploadChunk", UploadChunkHandler(mockService, createTestConfig()), "POST", "/chunks"},
		{"CompleteUpload", CompleteUploadHandler(mockService, nil), "POST", "/complete"},
		{"GetUploadStatus", GetUploadStatusHandler(mockService), "GET", "/status"},
		{"ResumeUpload", ResumeUploadHandler(mockService), "GET", "/resume"},
	}

	for _, tc := range handlers {
		t.Run(tc.name+"_InvalidUUID", func(t *testing.T) {
			httpReq := httptest.NewRequest(tc.method, tc.path, nil)

			// Add invalid session ID
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("sessionId", "invalid-uuid")
			httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()
			tc.handler(w, httpReq)

			assert.Equal(t, http.StatusBadRequest, w.Code)

			var envelope Response
			err := json.Unmarshal(w.Body.Bytes(), &envelope)
			require.NoError(t, err)
			require.False(t, envelope.Success)
			require.NotNil(t, envelope.Error)
			assert.Contains(t, envelope.Error.Code, "INVALID_SESSION_ID")
		})

		t.Run(tc.name+"_MissingSessionID", func(t *testing.T) {
			httpReq := httptest.NewRequest(tc.method, tc.path, nil)

			// Add empty session ID
			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("sessionId", "")
			httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()
			tc.handler(w, httpReq)

			assert.Equal(t, http.StatusBadRequest, w.Code)

			var envelope Response
			err := json.Unmarshal(w.Body.Bytes(), &envelope)
			require.NoError(t, err)
			require.False(t, envelope.Success)
			require.NotNil(t, envelope.Error)
			assert.Contains(t, envelope.Error.Code, "MISSING_SESSION_ID")
		})
	}
}

// Helper functions (kept for TestCompleteUploadHandler which is skipped but needs compilation)
func createTestUser(t *testing.T, repo usecase.UserRepository, ctx context.Context, username, email string) *domain.User {
	t.Helper()

	user := &domain.User{
		ID:        uuid.NewString(),
		Username:  username,
		Email:     email,
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := repo.Create(ctx, user, "hashed_password")
	require.NoError(t, err)

	return user
}

func initiateTestUpload(t *testing.T, service usecase.UploadService, ctx context.Context, userID string) *domain.InitiateUploadResponse {
	t.Helper()

	req := &domain.InitiateUploadRequest{
		FileName:  "test_video.mp4",
		FileSize:  1000, // Small size for testing
		ChunkSize: 100,  // Small chunks for testing
	}

	response, err := service.InitiateUpload(ctx, userID, req)
	require.NoError(t, err)

	return response
}

func uploadTestChunk(t *testing.T, service usecase.UploadService, ctx context.Context, sessionID string, chunkIndex int) {
	t.Helper()

	chunkData := []byte(fmt.Sprintf("test chunk data for chunk %d", chunkIndex))
	hasher := sha256.New()
	hasher.Write(chunkData)
	checksum := hex.EncodeToString(hasher.Sum(nil))

	chunk := &domain.ChunkUpload{
		SessionID:  sessionID,
		ChunkIndex: chunkIndex,
		Data:       chunkData,
		Checksum:   checksum,
	}

	_, err := service.UploadChunk(ctx, sessionID, chunk)
	require.NoError(t, err)
}

func uploadAllTestChunks(t *testing.T, service usecase.UploadService, ctx context.Context, sessionID string, totalChunks int) {
	t.Helper()

	for i := 0; i < totalChunks; i++ {
		uploadTestChunk(t, service, ctx, sessionID, i)
	}
}
