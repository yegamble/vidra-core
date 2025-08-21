//go:build integration
// +build integration

package httpapi

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/repository"
	"athena/internal/storage"
	"athena/internal/testutil"
	"athena/internal/usecase"
	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestFullUploadWorkflow_Integration(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

	tempDir := t.TempDir()
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir)

	ctx := context.Background()

	// Create test user
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")

	// Test data - simulate a small video file
	originalData := []byte("This is a test video file content that will be uploaded in chunks for testing purposes. " +
		"The content needs to be long enough to be split into multiple chunks to properly test the chunked upload functionality. " +
		"We'll split this into several chunks and verify that the file is correctly reassembled after all chunks are uploaded.")

	chunkSize := 50 // Small chunks for testing
	totalChunks := (len(originalData) + chunkSize - 1) / chunkSize

	t.Run("Step1_InitiateUpload", func(t *testing.T) {
		req := domain.InitiateUploadRequest{
			FileName:  "test_video.mp4",
			FileSize:  int64(len(originalData)),
			ChunkSize: int64(chunkSize),
		}

		reqBody, _ := json.Marshal(req)
		httpReq := httptest.NewRequest("POST", "/api/v1/uploads/initiate", bytes.NewReader(reqBody))
		httpReq.Header.Set("Content-Type", "application/json")
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), middleware.UserIDKey, user.ID))

		w := httptest.NewRecorder()
		handler := InitiateUploadHandler(uploadService, videoRepo)
		handler(w, httpReq)

		assert.Equal(t, http.StatusCreated, w.Code)

		var response domain.InitiateUploadResponse
		err := json.Unmarshal(w.Body.Bytes(), &response)
		require.NoError(t, err)

		assert.NotEmpty(t, response.SessionID)
		assert.Equal(t, int64(chunkSize), response.ChunkSize)
		assert.Equal(t, totalChunks, response.TotalChunks)

		// Store session ID for next steps
		sessionID := response.SessionID

		t.Run("Step2_UploadChunks", func(t *testing.T) {
			// Upload chunks in non-sequential order to test resumable functionality
			uploadOrder := []int{0, 2, 1, 4, 3} // Upload chunks out of order

			for i, chunkIndex := range uploadOrder {
				if chunkIndex >= totalChunks {
					continue
				}

				start := chunkIndex * chunkSize
				end := start + chunkSize
				if end > len(originalData) {
					end = len(originalData)
				}

				chunkData := originalData[start:end]
				hasher := sha256.New()
				hasher.Write(chunkData)
				checksum := hex.EncodeToString(hasher.Sum(nil))

				httpReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID), bytes.NewReader(chunkData))
				httpReq.Header.Set("X-Chunk-Index", fmt.Sprintf("%d", chunkIndex))
				httpReq.Header.Set("X-Chunk-Checksum", checksum)

				rctx := chi.NewRouteContext()
				rctx.URLParams.Add("sessionId", sessionID)
				httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

				w := httptest.NewRecorder()
				handler := UploadChunkHandler(uploadService)
				handler(w, httpReq)

				assert.Equal(t, http.StatusOK, w.Code, "Failed to upload chunk %d", chunkIndex)

				var chunkResponse domain.ChunkUploadResponse
				err := json.Unmarshal(w.Body.Bytes(), &chunkResponse)
				require.NoError(t, err)

				assert.Equal(t, chunkIndex, chunkResponse.ChunkIndex)
				assert.True(t, chunkResponse.Uploaded)

				expectedRemaining := totalChunks - (i + 1)
				assert.Len(t, chunkResponse.RemainingChunks, expectedRemaining,
					"After uploading chunk %d, should have %d remaining chunks", chunkIndex, expectedRemaining)
			}

			t.Run("Step3_CheckResumeInformation", func(t *testing.T) {
				httpReq := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/uploads/%s/resume", sessionID), nil)

				rctx := chi.NewRouteContext()
				rctx.URLParams.Add("sessionId", sessionID)
				httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

				w := httptest.NewRecorder()
				handler := ResumeUploadHandler(uploadService)
				handler(w, httpReq)

				assert.Equal(t, http.StatusOK, w.Code)

				var resumeResponse map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &resumeResponse)
				require.NoError(t, err)

				assert.Equal(t, sessionID, resumeResponse["session_id"])
				assert.Equal(t, float64(totalChunks), resumeResponse["total_chunks"])

				uploadedChunks := resumeResponse["uploaded_chunks"].([]interface{})
				assert.Len(t, uploadedChunks, totalChunks)

				remainingChunks := resumeResponse["remaining_chunks"].([]interface{})
				assert.Len(t, remainingChunks, 0, "All chunks should be uploaded")

				progressPercent := resumeResponse["progress_percent"].(float64)
				assert.Equal(t, 100.0, progressPercent, "Progress should be 100%")
			})

			t.Run("Step4_CompleteUpload", func(t *testing.T) {
				httpReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/complete", sessionID), nil)

				rctx := chi.NewRouteContext()
				rctx.URLParams.Add("sessionId", sessionID)
				httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

				w := httptest.NewRecorder()
				handler := CompleteUploadHandler(uploadService, encodingRepo)
				handler(w, httpReq)

				assert.Equal(t, http.StatusOK, w.Code)

				var completeResponse map[string]interface{}
				err := json.Unmarshal(w.Body.Bytes(), &completeResponse)
				require.NoError(t, err)

				assert.Equal(t, sessionID, completeResponse["session_id"])
				assert.Equal(t, "completed", completeResponse["status"])

				t.Run("Step5_VerifyFileAssembly", func(t *testing.T) {
					// Get session to find the video ID
					session, err := uploadRepo.GetSession(ctx, sessionID)
					require.NoError(t, err)
					assert.Equal(t, domain.UploadStatusCompleted, session.Status)

					// Verify assembled file exists and content is correct
					sp := storage.NewPaths(tempDir)
					finalPath := sp.WebVideoFilePath(session.VideoID, ".mp4")
					assembledData, err := os.ReadFile(finalPath)
					require.NoError(t, err)
					assert.Equal(t, originalData, assembledData, "Assembled file content should match original")

					t.Run("Step6_VerifyDatabaseState", func(t *testing.T) {
						// Verify video status
						video, err := videoRepo.GetByID(ctx, session.VideoID)
						require.NoError(t, err)
						assert.Equal(t, domain.StatusQueued, video.Status)
						assert.Equal(t, user.ID, video.UserID)
						assert.Equal(t, int64(len(originalData)), video.FileSize)

						// Verify encoding job was created
						job, err := encodingRepo.GetJobByVideoID(ctx, session.VideoID)
						require.NoError(t, err)
						assert.Equal(t, session.VideoID, job.VideoID)
						assert.Equal(t, finalPath, job.SourceFilePath)
						assert.Equal(t, domain.EncodingStatusPending, job.Status)
						assert.Equal(t, 0, job.Progress)
						assert.Contains(t, job.TargetResolutions, "240p")
						assert.NotEmpty(t, job.TargetResolutions)

						t.Run("Step7_TestEncodingQueue", func(t *testing.T) {
							// Test getting pending jobs
							pendingJobs, err := encodingRepo.GetPendingJobs(ctx, 10)
							require.NoError(t, err)
							assert.Len(t, pendingJobs, 1)
							assert.Equal(t, job.ID, pendingJobs[0].ID)

							// Test getting next job (simulates worker picking up job)
							nextJob, err := encodingRepo.GetNextJob(ctx)
							require.NoError(t, err)
							require.NotNil(t, nextJob)
							assert.Equal(t, job.ID, nextJob.ID)
							assert.Equal(t, domain.EncodingStatusProcessing, nextJob.Status)
							assert.NotNil(t, nextJob.StartedAt)

							// Test updating job progress
							err = encodingRepo.UpdateJobProgress(ctx, nextJob.ID, 50)
							require.NoError(t, err)

							updatedJob, err := encodingRepo.GetJob(ctx, nextJob.ID)
							require.NoError(t, err)
							assert.Equal(t, 50, updatedJob.Progress)

							// Test completing job
							now := time.Now()
							updatedJob.Status = domain.EncodingStatusCompleted
							updatedJob.Progress = 100
							updatedJob.CompletedAt = &now
							updatedJob.UpdatedAt = now

							err = encodingRepo.UpdateJob(ctx, updatedJob)
							require.NoError(t, err)

							finalJob, err := encodingRepo.GetJob(ctx, nextJob.ID)
							require.NoError(t, err)
							assert.Equal(t, domain.EncodingStatusCompleted, finalJob.Status)
							assert.Equal(t, 100, finalJob.Progress)
							assert.NotNil(t, finalJob.CompletedAt)

							// Verify no more pending jobs
							pendingJobs, err = encodingRepo.GetPendingJobs(ctx, 10)
							require.NoError(t, err)
							assert.Len(t, pendingJobs, 0)
						})
					})
				})
			})
		})
	})
}

func TestResumeUploadWorkflow_Integration(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

	tempDir := t.TempDir()
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir)

	ctx := context.Background()

	// Create test user
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")

	// Test data
	originalData := []byte("Test content for resume functionality testing. This will be split into chunks.")
	chunkSize := 20
	totalChunks := (len(originalData) + chunkSize - 1) / chunkSize

	// Step 1: Initiate upload
	response := initiateTestUpload(t, uploadService, ctx, user.ID)
	sessionID := response.SessionID

	// Step 2: Upload some chunks (simulate interrupted upload)
	chunksToUpload := []int{0, 1, 3, 4} // Skip chunk 2
	for _, chunkIndex := range chunksToUpload {
		if chunkIndex >= totalChunks {
			continue
		}

		start := chunkIndex * chunkSize
		end := start + chunkSize
		if end > len(originalData) {
			end = len(originalData)
		}

		chunkData := originalData[start:end]
		hasher := sha256.New()
		hasher.Write(chunkData)
		checksum := hex.EncodeToString(hasher.Sum(nil))

		httpReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID), bytes.NewReader(chunkData))
		httpReq.Header.Set("X-Chunk-Index", fmt.Sprintf("%d", chunkIndex))
		httpReq.Header.Set("X-Chunk-Checksum", checksum)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", sessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler := UploadChunkHandler(uploadService)
		handler(w, httpReq)

		assert.Equal(t, http.StatusOK, w.Code)
	}

	// Step 3: Check resume information
	httpReq := httptest.NewRequest("GET", fmt.Sprintf("/api/v1/uploads/%s/resume", sessionID), nil)
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", sessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	handler := ResumeUploadHandler(uploadService)
	handler(w, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)

	var resumeResponse map[string]interface{}
	err := json.Unmarshal(w.Body.Bytes(), &resumeResponse)
	require.NoError(t, err)

	remainingChunks := resumeResponse["remaining_chunks"].([]interface{})
	require.Len(t, remainingChunks, 1) // Should only be missing chunk 2
	assert.Equal(t, float64(2), remainingChunks[0])

	progressPercent := resumeResponse["progress_percent"].(float64)
	expectedProgress := float64(len(chunksToUpload)) / float64(totalChunks) * 100
	assert.Equal(t, expectedProgress, progressPercent)

	// Step 4: Upload missing chunk
	chunkIndex := 2
	start := chunkIndex * chunkSize
	end := start + chunkSize
	if end > len(originalData) {
		end = len(originalData)
	}

	chunkData := originalData[start:end]
	hasher := sha256.New()
	hasher.Write(chunkData)
	checksum := hex.EncodeToString(hasher.Sum(nil))

	httpReq = httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID), bytes.NewReader(chunkData))
	httpReq.Header.Set("X-Chunk-Index", "2")
	httpReq.Header.Set("X-Chunk-Checksum", checksum)

	rctx = chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", sessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

	w = httptest.NewRecorder()
	handler = UploadChunkHandler(uploadService)
	handler(w, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)

	var chunkResponse domain.ChunkUploadResponse
	err = json.Unmarshal(w.Body.Bytes(), &chunkResponse)
	require.NoError(t, err)
	assert.Len(t, chunkResponse.RemainingChunks, 0) // Should be no remaining chunks

	// Step 5: Complete upload
	httpReq = httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/complete", sessionID), nil)
	rctx = chi.NewRouteContext()
	rctx.URLParams.Add("sessionId", sessionID)
	httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

	w = httptest.NewRecorder()
	handler = CompleteUploadHandler(uploadService, encodingRepo)
	handler(w, httpReq)

	assert.Equal(t, http.StatusOK, w.Code)

	// Step 6: Verify file was correctly assembled
	session, err := uploadRepo.GetSession(ctx, sessionID)
	require.NoError(t, err)

	sp := storage.NewPaths(tempDir)
	finalPath := sp.WebVideoFilePath(session.VideoID, ".mp4")
	assembledData, err := os.ReadFile(finalPath)
	require.NoError(t, err)
	assert.Equal(t, originalData, assembledData, "File should be correctly assembled despite out-of-order upload")
}

func TestConcurrentUpload_Integration(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := repository.NewUploadRepository(testDB.DB)
	encodingRepo := repository.NewEncodingRepository(testDB.DB)
	videoRepo := repository.NewVideoRepository(testDB.DB)
	userRepo := repository.NewUserRepository(testDB.DB)

	tempDir := t.TempDir()
	uploadService := usecase.NewUploadService(uploadRepo, encodingRepo, videoRepo, tempDir)

	ctx := context.Background()

	// Create test user
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")

	// Test concurrent uploads to same session (should handle duplicates gracefully)
	response := initiateTestUpload(t, uploadService, ctx, user.ID)
	sessionID := response.SessionID

	// Prepare same chunk data
	chunkData := []byte("concurrent test chunk data")
	hasher := sha256.New()
	hasher.Write(chunkData)
	checksum := hex.EncodeToString(hasher.Sum(nil))

	// Upload same chunk concurrently
	done := make(chan bool, 2)
	errors := make(chan error, 2)

	uploadChunk := func() {
		httpReq := httptest.NewRequest("POST", fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID), bytes.NewReader(chunkData))
		httpReq.Header.Set("X-Chunk-Index", "0")
		httpReq.Header.Set("X-Chunk-Checksum", checksum)

		rctx := chi.NewRouteContext()
		rctx.URLParams.Add("sessionId", sessionID)
		httpReq = httpReq.WithContext(context.WithValue(httpReq.Context(), chi.RouteCtxKey, rctx))

		w := httptest.NewRecorder()
		handler := UploadChunkHandler(uploadService)
		handler(w, httpReq)

		if w.Code != http.StatusOK {
			errors <- fmt.Errorf("expected status 200, got %d", w.Code)
		} else {
			errors <- nil
		}
		done <- true
	}

	// Start concurrent uploads
	go uploadChunk()
	go uploadChunk()

	// Wait for both to complete
	<-done
	<-done

	// Check errors
	err1 := <-errors
	err2 := <-errors

	// Both should succeed (idempotent)
	assert.NoError(t, err1)
	assert.NoError(t, err2)

	// Verify chunk was recorded only once
	uploadedChunks, err := uploadRepo.GetUploadedChunks(ctx, sessionID)
	require.NoError(t, err)

	chunkCount := 0
	for _, chunk := range uploadedChunks {
		if chunk == 0 {
			chunkCount++
		}
	}
	assert.Equal(t, 1, chunkCount, "Chunk 0 should be recorded exactly once despite concurrent uploads")
}

// Helper functions for integration tests
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
		FileSize:  100, // Small size for testing
		ChunkSize: 20,  // Small chunks for testing
	}

	response, err := service.InitiateUpload(ctx, userID, req)
	require.NoError(t, err)

	return response
}
