package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// -- Mocks --

type MockUploadRepository struct {
	mock.Mock
}

func (m *MockUploadRepository) CreateSession(ctx context.Context, session *domain.UploadSession) error {
	args := m.Called(ctx, session)
	return args.Error(0)
}
func (m *MockUploadRepository) GetSession(ctx context.Context, sessionID string) (*domain.UploadSession, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.UploadSession), args.Error(1)
}
func (m *MockUploadRepository) UpdateSession(ctx context.Context, session *domain.UploadSession) error {
	args := m.Called(ctx, session)
	return args.Error(0)
}
func (m *MockUploadRepository) DeleteSession(ctx context.Context, sessionID string) error {
	args := m.Called(ctx, sessionID)
	return args.Error(0)
}
func (m *MockUploadRepository) RecordChunk(ctx context.Context, sessionID string, chunkIndex int) error {
	args := m.Called(ctx, sessionID, chunkIndex)
	return args.Error(0)
}
func (m *MockUploadRepository) GetUploadedChunks(ctx context.Context, sessionID string) ([]int, error) {
	args := m.Called(ctx, sessionID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]int), args.Error(1)
}
func (m *MockUploadRepository) IsChunkUploaded(ctx context.Context, sessionID string, chunkIndex int) (bool, error) {
	args := m.Called(ctx, sessionID, chunkIndex)
	return args.Bool(0), args.Error(1)
}
func (m *MockUploadRepository) ExpireOldSessions(ctx context.Context) error {
	args := m.Called(ctx)
	return args.Error(0)
}
func (m *MockUploadRepository) GetExpiredSessions(ctx context.Context) ([]*domain.UploadSession, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.UploadSession), args.Error(1)
}

type MockVideoRepository struct {
	mock.Mock
}

// Implement minimal required methods for the test
func (m *MockVideoRepository) Create(ctx context.Context, video *domain.Video) error {
	args := m.Called(ctx, video)
	return args.Error(0)
}

// Add other methods as stubs if needed, but Create is mostly what we need for InitiateUpload
func (m *MockVideoRepository) GetByID(ctx context.Context, id string) (*domain.Video, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Video), args.Error(1)
}

// Stub out the rest to satisfy interface
func (m *MockVideoRepository) GetByUserID(ctx context.Context, userID string, limit, offset int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) Update(ctx context.Context, video *domain.Video) error      { return nil }
func (m *MockVideoRepository) Delete(ctx context.Context, id string, userID string) error { return nil }
func (m *MockVideoRepository) List(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) Search(ctx context.Context, req *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *MockVideoRepository) UpdateProcessingInfo(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string) error {
	return nil
}
func (m *MockVideoRepository) UpdateProcessingInfoWithCIDs(ctx context.Context, videoID string, status domain.ProcessingStatus, outputPaths map[string]string, thumbnailPath, previewPath string, processedCIDs map[string]string, thumbnailCID, previewCID string) error {
	return nil
}
func (m *MockVideoRepository) Count(ctx context.Context) (int64, error) { return 0, nil }
func (m *MockVideoRepository) GetVideosForMigration(ctx context.Context, limit int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *MockVideoRepository) GetByRemoteURI(ctx context.Context, remoteURI string) (*domain.Video, error) {
	return nil, nil
}
func (m *MockVideoRepository) CreateRemoteVideo(ctx context.Context, video *domain.Video) error {
	return nil
}

type MockEncodingRepository struct {
	mock.Mock
}

// Stubs for encoding repo
func (m *MockEncodingRepository) CreateJob(ctx context.Context, job *domain.EncodingJob) error {
	return nil
}
func (m *MockEncodingRepository) GetJob(ctx context.Context, jobID string) (*domain.EncodingJob, error) {
	return nil, nil
}
func (m *MockEncodingRepository) GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error) {
	return nil, nil
}
func (m *MockEncodingRepository) UpdateJob(ctx context.Context, job *domain.EncodingJob) error {
	return nil
}
func (m *MockEncodingRepository) DeleteJob(ctx context.Context, jobID string) error { return nil }
func (m *MockEncodingRepository) GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error) {
	return nil, nil
}
func (m *MockEncodingRepository) GetNextJob(ctx context.Context) (*domain.EncodingJob, error) {
	return nil, nil
}
func (m *MockEncodingRepository) UpdateJobStatus(ctx context.Context, jobID string, status domain.EncodingStatus) error {
	return nil
}
func (m *MockEncodingRepository) UpdateJobProgress(ctx context.Context, jobID string, progress int) error {
	return nil
}
func (m *MockEncodingRepository) SetJobError(ctx context.Context, jobID string, errorMsg string) error {
	return nil
}
func (m *MockEncodingRepository) GetJobCounts(ctx context.Context) (map[string]int64, error) {
	return nil, nil
}

func TestInitiateUpload_Security_FileSizeLimit(t *testing.T) {
	mockUploadRepo := new(MockUploadRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockEncodingRepo := new(MockEncodingRepository)

	cfg := &config.Config{} // Defaults are fine
	tempDir := t.TempDir()

	uploadService := usecase.NewUploadService(mockUploadRepo, mockEncodingRepo, mockVideoRepo, tempDir, cfg)

	userID := "user-123"

	// 10GB + 1 byte
	const tooLargeSize = 10*1024*1024*1024 + 1
	req := domain.InitiateUploadRequest{
		FileName:  "large_video.mp4",
		FileSize:  tooLargeSize,
		ChunkSize: 10 * 1024 * 1024,
	}

	reqBody, _ := json.Marshal(req)
	httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), middleware.UserIDKey, userID))

	w := httptest.NewRecorder()
	handler := InitiateUploadHandler(uploadService, mockVideoRepo)
	handler(w, httpReq)

	assert.Equal(t, http.StatusBadRequest, w.Code)

	var envelope Response
	err := json.Unmarshal(w.Body.Bytes(), &envelope)
	require.NoError(t, err)
	require.False(t, envelope.Success)
	require.NotNil(t, envelope.Error)
	assert.Equal(t, "FILE_TOO_LARGE", envelope.Error.Code)
}

func TestInitiateUpload_Security_InvalidExtension(t *testing.T) {
	mockUploadRepo := new(MockUploadRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockEncodingRepo := new(MockEncodingRepository)

	cfg := &config.Config{}
	tempDir := t.TempDir()

	uploadService := usecase.NewUploadService(mockUploadRepo, mockEncodingRepo, mockVideoRepo, tempDir, cfg)

	userID := "user-123"

	// Mock video creation (should be called if validation passes, but we expect it to fail)
	// If validation is loose (current state), Create will be called.
	// We set up the mock to allow it, so we can assert failure based on return code,
	// OR if the test fails because it *succeeded* (201 Created), that confirms the security gap.
	mockVideoRepo.On("Create", mock.Anything, mock.Anything).Return(nil).Maybe()
	mockUploadRepo.On("CreateSession", mock.Anything, mock.Anything).Return(nil).Maybe()

	invalidExtensions := []string{
		"malware.exe",
		"script.sh",
		"page.php",
		"text.txt",
		"config.json",
		"no_extension",
	}

	for _, filename := range invalidExtensions {
		t.Run(filename, func(t *testing.T) {
			req := domain.InitiateUploadRequest{
				FileName:  filename,
				FileSize:  1024 * 1024,
				ChunkSize: 1024 * 1024,
			}

			reqBody, _ := json.Marshal(req)
			httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
			httpReq.Header.Set("Content-Type", "application/json")
			httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), middleware.UserIDKey, userID))

			w := httptest.NewRecorder()
			handler := InitiateUploadHandler(uploadService, mockVideoRepo)
			handler(w, httpReq)

			// Current behavior: validation is loose, so this likely returns 201 Created.
			// We assert 400 Bad Request to demonstrate the test failure (the "bug").
			assert.Equal(t, http.StatusBadRequest, w.Code, "Should reject %s", filename)

			var envelope Response
			err := json.Unmarshal(w.Body.Bytes(), &envelope)
			require.NoError(t, err)

			if w.Code == http.StatusBadRequest {
				require.False(t, envelope.Success)
				require.NotNil(t, envelope.Error)
				assert.Equal(t, "INVALID_FILE_EXTENSION", envelope.Error.Code)
			}
		})
	}
}

func TestUploadChunk_Security_BoundsCheck(t *testing.T) {
	mockUploadRepo := new(MockUploadRepository)
	mockVideoRepo := new(MockVideoRepository)
	mockEncodingRepo := new(MockEncodingRepository)

	cfg := &config.Config{
		ValidationStrictMode:        false,
		ValidationAllowedAlgorithms: []string{"sha256"},
		ValidationTestMode:          true,
	}
	tempDir := t.TempDir()

	uploadService := usecase.NewUploadService(mockUploadRepo, mockEncodingRepo, mockVideoRepo, tempDir, cfg)

	// Mock session
	sessionID := uuid.NewString()
	session := &domain.UploadSession{
		ID:          sessionID,
		TotalChunks: 10,
		Status:      domain.UploadStatusActive,
		ExpiresAt:   time.Now().Add(time.Hour),
	}

	mockUploadRepo.On("GetSession", mock.Anything, sessionID).Return(session, nil)

	testCases := []struct {
		name       string
		chunkIndex string
		wantStatus int
		wantCode   string
	}{
		{
			name:       "Negative Index",
			chunkIndex: "-1",
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_CHUNK_INDEX",
		},
		{
			name:       "Index Equal To Total",
			chunkIndex: "10",
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_CHUNK_INDEX",
		},
		{
			name:       "Index Greater Than Total",
			chunkIndex: "99",
			wantStatus: http.StatusBadRequest,
			wantCode:   "INVALID_CHUNK_INDEX",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			chunkData := []byte("dummy data")

			httpReq := httptest.NewRequest("POST", "/api/v1/uploads/"+sessionID+"/chunks", bytes.NewReader(chunkData))
			httpReq.Header.Set("X-Chunk-Index", tc.chunkIndex)
			httpReq.Header.Set("X-Chunk-Checksum", "test")

			rctx := chi.NewRouteContext()
			rctx.URLParams.Add("sessionId", sessionID)
			httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

			w := httptest.NewRecorder()
			handler := UploadChunkHandler(uploadService, cfg)
			handler(w, httpReq)

			assert.Equal(t, tc.wantStatus, w.Code)

			var envelope Response
			err := json.Unmarshal(w.Body.Bytes(), &envelope)
			require.NoError(t, err)
			require.NotNil(t, envelope.Error)
			if envelope.Error != nil {
				assert.Contains(t, envelope.Error.Code, tc.wantCode)
			}
		})
	}
}
