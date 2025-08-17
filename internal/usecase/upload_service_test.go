package usecase_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/repository"
	"athena/internal/usecase"
	"athena/internal/testutil"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// createTestConfig creates a config suitable for testing
func createTestConfig() *config.Config {
	return &config.Config{
		ValidationStrictMode:          false, // Allow optional checksums in tests
		ValidationAllowedAlgorithms:   []string{"sha256"},
		ValidationTestMode:           true,  // Enable test mode for bypasses
		ValidationEnableIntegrityJobs: false,
		ValidationLogEvents:          false,
	}
}

func TestUploadService_InitiateUpload(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

    // Create temp directory for uploads within workspace (sandbox-safe)
    tempDir := testTempDir(t)
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Create test user
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")

	req := &domain.InitiateUploadRequest{
		FileName:  "test_video.mp4",
		FileSize:  1048576, // 1MB
		ChunkSize: 10485,   // 10KB
	}

	response, err := uploadService.InitiateUpload(ctx, user.ID, req)
	require.NoError(t, err)
	require.NotNil(t, response)

	assert.NotEmpty(t, response.SessionID)
	assert.Equal(t, req.ChunkSize, response.ChunkSize)
	assert.Equal(t, 101, response.TotalChunks) // 1MB / 10KB = 100.0095... = 101 chunks (ceiling)
	assert.Contains(t, response.UploadURL, response.SessionID)

	// Verify session was created in database
	session, err := uploadRepo.GetSession(ctx, response.SessionID)
	require.NoError(t, err)
	assert.Equal(t, user.ID, session.UserID)
	assert.Equal(t, req.FileName, session.FileName)
	assert.Equal(t, req.FileSize, session.FileSize)
	assert.Equal(t, domain.UploadStatusActive, session.Status)

	// Verify video was created
	video, err := videoRepo.GetByID(ctx, session.VideoID)
	require.NoError(t, err)
	assert.Equal(t, user.ID, video.UserID)
	assert.Equal(t, domain.StatusUploading, video.Status)
	assert.Equal(t, req.FileSize, video.FileSize)

	// Verify temp directory was created
	tempPath := filepath.Join(tempDir, "temp", response.SessionID)
	_, err = os.Stat(tempPath)
	assert.NoError(t, err)
}

func TestUploadService_InitiateUpload_FileTooLarge(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

    tempDir := testTempDir(t)
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")

	req := &domain.InitiateUploadRequest{
		FileName:  "huge_video.mp4",
		FileSize:  11 * 1024 * 1024 * 1024, // 11GB (exceeds 10GB limit)
		ChunkSize: 10485,
	}

	_, err := uploadService.InitiateUpload(ctx, user.ID, req)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "FILE_TOO_LARGE")
}

func TestUploadService_UploadChunk(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

    tempDir := testTempDir(t)
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Setup test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	response := initiateTestUpload(t, uploadService, ctx, user.ID)

	// Create test chunk data
	chunkData := []byte("test chunk data for chunk 0")
	hasher := sha256.New()
	hasher.Write(chunkData)
	checksum := hex.EncodeToString(hasher.Sum(nil))

	chunk := &domain.ChunkUpload{
		SessionID:  response.SessionID,
		ChunkIndex: 0,
		Data:       chunkData,
		Checksum:   checksum,
	}

	// Upload chunk
	chunkResponse, err := uploadService.UploadChunk(ctx, response.SessionID, chunk)
	require.NoError(t, err)
	require.NotNil(t, chunkResponse)

	assert.Equal(t, 0, chunkResponse.ChunkIndex)
	assert.True(t, chunkResponse.Uploaded)
	assert.Len(t, chunkResponse.RemainingChunks, response.TotalChunks-1)
	assert.NotContains(t, chunkResponse.RemainingChunks, 0)

	// Verify chunk was saved to disk
	session, err := uploadRepo.GetSession(ctx, response.SessionID)
	require.NoError(t, err)
	
	chunkPath := filepath.Join(session.TempFilePath, "chunk_0")
	savedData, err := os.ReadFile(chunkPath)
	require.NoError(t, err)
	assert.Equal(t, chunkData, savedData)

	// Verify chunk was recorded in database
	isUploaded, err := uploadRepo.IsChunkUploaded(ctx, response.SessionID, 0)
	require.NoError(t, err)
	assert.True(t, isUploaded)
}

func TestUploadService_UploadChunk_InvalidChecksum(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

    tempDir := testTempDir(t)
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Setup test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	response := initiateTestUpload(t, uploadService, ctx, user.ID)

	chunk := &domain.ChunkUpload{
		SessionID:  response.SessionID,
		ChunkIndex: 0,
		Data:       []byte("test chunk data"),
		Checksum:   "invalid_checksum",
	}

	// Upload chunk with invalid checksum
	_, err := uploadService.UploadChunk(ctx, response.SessionID, chunk)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "CHECKSUM_MISMATCH")
}

func TestUploadService_UploadChunk_Resumable(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

    tempDir := testTempDir(t)
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Setup test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	response := initiateTestUpload(t, uploadService, ctx, user.ID)

	// Upload chunks 0, 2, 4 (skipping 1, 3)
	chunks := []int{0, 2, 4}
	for _, chunkIndex := range chunks {
		chunkData := []byte("test chunk data for chunk " + string(rune(chunkIndex+'0')))
		hasher := sha256.New()
		hasher.Write(chunkData)
		checksum := hex.EncodeToString(hasher.Sum(nil))

		chunk := &domain.ChunkUpload{
			SessionID:  response.SessionID,
			ChunkIndex: chunkIndex,
			Data:       chunkData,
			Checksum:   checksum,
		}

		chunkResponse, err := uploadService.UploadChunk(ctx, response.SessionID, chunk)
		require.NoError(t, err)
		assert.Contains(t, chunkResponse.RemainingChunks, 1) // Should still need chunk 1
		assert.Contains(t, chunkResponse.RemainingChunks, 3) // Should still need chunk 3
		assert.NotContains(t, chunkResponse.RemainingChunks, chunkIndex) // Should not need uploaded chunk
	}

	// Get upload status to verify resumable state
	session, err := uploadService.GetUploadStatus(ctx, response.SessionID)
	require.NoError(t, err)
	assert.Len(t, session.UploadedChunks, 3)
	assert.Contains(t, session.UploadedChunks, 0)
	assert.Contains(t, session.UploadedChunks, 2)
	assert.Contains(t, session.UploadedChunks, 4)
	assert.NotContains(t, session.UploadedChunks, 1)
	assert.NotContains(t, session.UploadedChunks, 3)

	// Upload missing chunks
	missingChunks := []int{1, 3}
	for _, chunkIndex := range missingChunks {
		chunkData := []byte("test chunk data for chunk " + string(rune(chunkIndex+'0')))
		hasher := sha256.New()
		hasher.Write(chunkData)
		checksum := hex.EncodeToString(hasher.Sum(nil))

		chunk := &domain.ChunkUpload{
			SessionID:  response.SessionID,
			ChunkIndex: chunkIndex,
			Data:       chunkData,
			Checksum:   checksum,
		}

		_, err := uploadService.UploadChunk(ctx, response.SessionID, chunk)
		require.NoError(t, err)
	}

	// Verify all chunks are now uploaded
	session, err = uploadService.GetUploadStatus(ctx, response.SessionID)
	require.NoError(t, err)
	assert.Len(t, session.UploadedChunks, 5)
}

func TestUploadService_CompleteUpload(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

    tempDir := testTempDir(t)
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Setup test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	response := initiateTestUpload(t, uploadService, ctx, user.ID)

	// Upload all chunks - use data that matches the expected file size from initiateTestUpload (1000 bytes)
	testData := make([]byte, 1000) // Create 1000-byte test data to match the session
	for i := range testData {
		testData[i] = byte('A' + (i % 26)) // Fill with repeating alphabet
	}
	chunkSize := int(response.ChunkSize) // Use the chunk size from the response (100 bytes)
	totalChunks := response.TotalChunks   // Use the total chunks from the response

	for i := 0; i < totalChunks; i++ {
		start := i * chunkSize
		end := start + chunkSize
		if end > len(testData) {
			end = len(testData)
		}
		
		chunkData := testData[start:end]
		hasher := sha256.New()
		hasher.Write(chunkData)
		checksum := hex.EncodeToString(hasher.Sum(nil))

		chunk := &domain.ChunkUpload{
			SessionID:  response.SessionID,
			ChunkIndex: i,
			Data:       chunkData,
			Checksum:   checksum,
		}

		_, err := uploadService.UploadChunk(ctx, response.SessionID, chunk)
		require.NoError(t, err)
	}

	// Complete upload
	err := uploadService.CompleteUpload(ctx, response.SessionID)
	require.NoError(t, err)

	// Verify session status updated
	session, err := uploadRepo.GetSession(ctx, response.SessionID)
	require.NoError(t, err)
	assert.Equal(t, domain.UploadStatusCompleted, session.Status)

	// Verify video status updated
	video, err := videoRepo.GetByID(ctx, session.VideoID)
	require.NoError(t, err)
	assert.Equal(t, domain.StatusQueued, video.Status)

	// Verify final file was created and content is correct
	finalPath := filepath.Join(tempDir, "completed", session.VideoID+".mp4")
	assembledData, err := os.ReadFile(finalPath)
	require.NoError(t, err)
	assert.Equal(t, testData, assembledData)

	// Verify encoding job was created
	job, err := encodingRepo.GetJobByVideoID(ctx, session.VideoID)
	require.NoError(t, err)
	assert.Equal(t, session.VideoID, job.VideoID)
	assert.Equal(t, finalPath, job.SourceFilePath)
	assert.Equal(t, domain.EncodingStatusPending, job.Status)
	assert.Contains(t, job.TargetResolutions, "720p")
	assert.Contains(t, job.TargetResolutions, "480p")
	assert.Contains(t, job.TargetResolutions, "360p")
	assert.Contains(t, job.TargetResolutions, "240p")
}

func TestUploadService_CompleteUpload_IncompleteChunks(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

    tempDir := testTempDir(t)
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Setup test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	response := initiateTestUpload(t, uploadService, ctx, user.ID)

	// Upload only some chunks (incomplete)
	chunkData := []byte("test chunk data")
	hasher := sha256.New()
	hasher.Write(chunkData)
	checksum := hex.EncodeToString(hasher.Sum(nil))

	chunk := &domain.ChunkUpload{
		SessionID:  response.SessionID,
		ChunkIndex: 0,
		Data:       chunkData,
		Checksum:   checksum,
	}

	_, err := uploadService.UploadChunk(ctx, response.SessionID, chunk)
	require.NoError(t, err)

	// Try to complete upload with missing chunks
	err = uploadService.CompleteUpload(ctx, response.SessionID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "INCOMPLETE_UPLOAD")
}

func TestUploadService_ExpiredSession(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

    tempDir := testTempDir(t)
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir, createTestConfig())

	ctx := context.Background()

	// Create test user and video
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")

	// Create expired session
	expiredSession := &domain.UploadSession{
		ID:             uuid.NewString(),
		VideoID:        video.ID,
		UserID:         user.ID,
		FileName:       "test_video.mp4",
		FileSize:       1048576,
		ChunkSize:      10485,
		TotalChunks:    100,
		UploadedChunks: []int{},
		Status:         domain.UploadStatusActive,
		TempFilePath:   filepath.Join(tempDir, "temp", uuid.NewString(), "chunks"),
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		UpdatedAt:      time.Now().Add(-48 * time.Hour),
		ExpiresAt:      time.Now().Add(-24 * time.Hour), // Expired 24 hours ago
	}

	err := uploadRepo.CreateSession(ctx, expiredSession)
	require.NoError(t, err)

	// Try to upload chunk to expired session
	chunkData := []byte("test chunk data")
	hasher := sha256.New()
	hasher.Write(chunkData)
	checksum := hex.EncodeToString(hasher.Sum(nil))

	chunk := &domain.ChunkUpload{
		SessionID:  expiredSession.ID,
		ChunkIndex: 0,
		Data:       chunkData,
		Checksum:   checksum,
	}

	_, err = uploadService.UploadChunk(ctx, expiredSession.ID, chunk)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SESSION_EXPIRED")
}

func TestUploadService_CleanupTempFiles(t *testing.T) {
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

	// Verify temp directory exists
	session, err := uploadRepo.GetSession(ctx, response.SessionID)
	require.NoError(t, err)
	
	tempSessionDir := filepath.Dir(session.TempFilePath)
	_, err = os.Stat(tempSessionDir)
	require.NoError(t, err)

	// Cleanup temp files
	err = uploadService.CleanupTempFiles(ctx, response.SessionID)
	require.NoError(t, err)

	// Verify temp directory was removed
	_, err = os.Stat(tempSessionDir)
	assert.True(t, os.IsNotExist(err))
}

// Helper functions
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

func createTestVideo(t *testing.T, repo usecase.VideoRepository, ctx context.Context, userID, title string) *domain.Video {
	t.Helper()

	now := time.Now()
	video := &domain.Video{
		ID:            uuid.NewString(),
		ThumbnailID:   uuid.NewString(),
		Title:         title,
		Description:   "Test description",
		Privacy:       domain.PrivacyPrivate,
		Status:        domain.StatusUploading,
		UploadDate:    now,
		UserID:        userID,
		ProcessedCIDs: make(map[string]string),
		Tags:          []string{},
		Metadata:      domain.VideoMetadata{},
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	err := repo.Create(ctx, video)
	require.NoError(t, err)

	return video
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

// testTempDir creates a temporary directory under the repository workspace
// to satisfy sandbox write constraints.
func testTempDir(t *testing.T) string {
    t.Helper()
    base := filepath.Join(".", "tmp", "usecase_tests")
    if err := os.MkdirAll(base, 0o755); err != nil {
        t.Fatalf("failed to create base temp dir: %v", err)
    }
    dir := filepath.Join(base, fmt.Sprintf("%s", uuid.NewString()))
    if err := os.MkdirAll(dir, 0o755); err != nil {
        t.Fatalf("failed to create temp dir: %v", err)
    }
    t.Cleanup(func() { _ = os.RemoveAll(dir) })
    return dir
}
