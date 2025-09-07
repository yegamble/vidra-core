package repository

import (
	"context"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/testutil"
	"athena/internal/usecase"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUploadRepository_CreateSession(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := NewUploadRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test user and video
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")

	session := &domain.UploadSession{
		ID:             uuid.NewString(),
		VideoID:        video.ID,
		UserID:         user.ID,
		FileName:       "test_video.mp4",
		FileSize:       1048576, // 1MB
		ChunkSize:      10485,   // 10KB
		TotalChunks:    100,
		UploadedChunks: []int{},
		Status:         domain.UploadStatusActive,
		TempFilePath:   "/tmp/test_session",
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(24 * time.Hour),
	}

	err := uploadRepo.CreateSession(ctx, session)
	require.NoError(t, err)

	// Verify session was created
	retrievedSession, err := uploadRepo.GetSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, session.ID, retrievedSession.ID)
	assert.Equal(t, session.VideoID, retrievedSession.VideoID)
	assert.Equal(t, session.UserID, retrievedSession.UserID)
	assert.Equal(t, session.FileName, retrievedSession.FileName)
	assert.Equal(t, session.FileSize, retrievedSession.FileSize)
	assert.Equal(t, session.ChunkSize, retrievedSession.ChunkSize)
	assert.Equal(t, session.TotalChunks, retrievedSession.TotalChunks)
	assert.Equal(t, session.Status, retrievedSession.Status)
	assert.Empty(t, retrievedSession.UploadedChunks)
}

func TestUploadRepository_RecordChunk(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := NewUploadRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	session := createTestUploadSession(t, uploadRepo, ctx, video.ID, user.ID)

	// Record chunks
	err := uploadRepo.RecordChunk(ctx, session.ID, 0)
	require.NoError(t, err)

	err = uploadRepo.RecordChunk(ctx, session.ID, 2)
	require.NoError(t, err)

	err = uploadRepo.RecordChunk(ctx, session.ID, 1)
	require.NoError(t, err)

	// Get uploaded chunks
	chunks, err := uploadRepo.GetUploadedChunks(ctx, session.ID)
	require.NoError(t, err)
	assert.Len(t, chunks, 3)
	assert.Contains(t, chunks, 0)
	assert.Contains(t, chunks, 1)
	assert.Contains(t, chunks, 2)

	// Test duplicate chunk recording (should be idempotent)
	err = uploadRepo.RecordChunk(ctx, session.ID, 1)
	require.NoError(t, err)

	chunks, err = uploadRepo.GetUploadedChunks(ctx, session.ID)
	require.NoError(t, err)
	assert.Len(t, chunks, 3) // Should still be 3, not 4
}

func TestUploadRepository_IsChunkUploaded(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := NewUploadRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	session := createTestUploadSession(t, uploadRepo, ctx, video.ID, user.ID)

	// Initially no chunks uploaded
	isUploaded, err := uploadRepo.IsChunkUploaded(ctx, session.ID, 0)
	require.NoError(t, err)
	assert.False(t, isUploaded)

	// Record chunk 0
	err = uploadRepo.RecordChunk(ctx, session.ID, 0)
	require.NoError(t, err)

	// Now chunk 0 should be uploaded
	isUploaded, err = uploadRepo.IsChunkUploaded(ctx, session.ID, 0)
	require.NoError(t, err)
	assert.True(t, isUploaded)

	// Chunk 1 should still not be uploaded
	isUploaded, err = uploadRepo.IsChunkUploaded(ctx, session.ID, 1)
	require.NoError(t, err)
	assert.False(t, isUploaded)
}

func TestUploadRepository_UpdateSession(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := NewUploadRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	session := createTestUploadSession(t, uploadRepo, ctx, video.ID, user.ID)

	// Update session
	session.Status = domain.UploadStatusCompleted
	session.TempFilePath = "/tmp/updated_path"
	session.UploadedChunks = []int{0, 1, 2, 3, 4}
	session.UpdatedAt = time.Now()

	err := uploadRepo.UpdateSession(ctx, session)
	require.NoError(t, err)

	// Verify update
	retrievedSession, err := uploadRepo.GetSession(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.UploadStatusCompleted, retrievedSession.Status)
	assert.Equal(t, "/tmp/updated_path", retrievedSession.TempFilePath)
	assert.Equal(t, []int{0, 1, 2, 3, 4}, retrievedSession.UploadedChunks)
}

func TestUploadRepository_DeleteSession(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := NewUploadRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")
	session := createTestUploadSession(t, uploadRepo, ctx, video.ID, user.ID)

	// Delete session
	err := uploadRepo.DeleteSession(ctx, session.ID)
	require.NoError(t, err)

	// Verify deletion
	_, err = uploadRepo.GetSession(ctx, session.ID)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "SESSION_NOT_FOUND")
}

func TestUploadRepository_ExpireOldSessions(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := NewUploadRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")

	// Create session that is already expired
	expiredSession := &domain.UploadSession{
		ID:             uuid.NewString(),
		VideoID:        video.ID,
		UserID:         user.ID,
		FileName:       "expired_video.mp4",
		FileSize:       1048576,
		ChunkSize:      10485,
		TotalChunks:    100,
		UploadedChunks: []int{},
		Status:         domain.UploadStatusActive,
		TempFilePath:   "/tmp/expired_session",
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		UpdatedAt:      time.Now().Add(-48 * time.Hour),
		ExpiresAt:      time.Now().Add(-24 * time.Hour), // Expired 24 hours ago
	}

	err := uploadRepo.CreateSession(ctx, expiredSession)
	require.NoError(t, err)

	// Create active session
	activeSession := createTestUploadSession(t, uploadRepo, ctx, video.ID, user.ID)

	// Expire old sessions
	err = uploadRepo.ExpireOldSessions(ctx)
	require.NoError(t, err)

	// Check expired session status
	retrievedExpired, err := uploadRepo.GetSession(ctx, expiredSession.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.UploadStatusExpired, retrievedExpired.Status)

	// Check active session is still active
	retrievedActive, err := uploadRepo.GetSession(ctx, activeSession.ID)
	require.NoError(t, err)
	assert.Equal(t, domain.UploadStatusActive, retrievedActive.Status)
}

func TestUploadRepository_GetExpiredSessions(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	uploadRepo := NewUploadRepository(testDB.DB)
	videoRepo := NewVideoRepository(testDB.DB)
	userRepo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create test data
	user := createTestUser(t, userRepo, ctx, "testuser", "test@example.com")
	video := createTestVideo(t, videoRepo, ctx, user.ID, "Test Video")

	// Create expired session
	expiredSession := &domain.UploadSession{
		ID:             uuid.NewString(),
		VideoID:        video.ID,
		UserID:         user.ID,
		FileName:       "expired_video.mp4",
		FileSize:       1048576,
		ChunkSize:      10485,
		TotalChunks:    100,
		UploadedChunks: []int{},
		Status:         domain.UploadStatusExpired,
		TempFilePath:   "/tmp/expired_session",
		CreatedAt:      time.Now().Add(-48 * time.Hour),
		UpdatedAt:      time.Now(),
		ExpiresAt:      time.Now().Add(-24 * time.Hour),
	}

	err := uploadRepo.CreateSession(ctx, expiredSession)
	require.NoError(t, err)

	// Create active session
	createTestUploadSession(t, uploadRepo, ctx, video.ID, user.ID)

	// Get expired sessions
	expiredSessions, err := uploadRepo.GetExpiredSessions(ctx)
	require.NoError(t, err)
	assert.Len(t, expiredSessions, 1)
	assert.Equal(t, expiredSession.ID, expiredSessions[0].ID)
	assert.Equal(t, domain.UploadStatusExpired, expiredSessions[0].Status)
}

// Helper functions
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
		CategoryID:    nil, // Set to nil to avoid foreign key constraint violation
		Metadata:      domain.VideoMetadata{},
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	err := repo.Create(ctx, video)
	require.NoError(t, err)

	return video
}

func createTestUploadSession(t *testing.T, repo usecase.UploadRepository, ctx context.Context, videoID, userID string) *domain.UploadSession {
	t.Helper()

	now := time.Now()
	session := &domain.UploadSession{
		ID:             uuid.NewString(),
		VideoID:        videoID,
		UserID:         userID,
		FileName:       "test_video.mp4",
		FileSize:       1048576, // 1MB
		ChunkSize:      10485,   // 10KB
		TotalChunks:    100,
		UploadedChunks: []int{},
		Status:         domain.UploadStatusActive,
		TempFilePath:   "/tmp/test_session",
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(24 * time.Hour),
	}

	err := repo.CreateSession(ctx, session)
	require.NoError(t, err)

	return session
}
