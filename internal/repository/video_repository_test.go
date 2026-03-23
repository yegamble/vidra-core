package repository

import (
	"context"
	"strconv"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/testutil"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestVideoRepository_Create(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}
	db := testDB.DB

	repo := NewVideoRepository(db)
	ctx := context.Background()

	// Create a test user first
	userRepo := NewUserRepository(db)
	user := &domain.User{
		ID:          uuid.New().String(),
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
	}
	err := userRepo.Create(ctx, user, "hashedpassword")
	require.NoError(t, err)

	tests := []struct {
		name    string
		video   *domain.Video
		wantErr bool
	}{
		{
			name: "valid video",
			video: &domain.Video{
				Title:       "Test Video",
				Description: "Test Description",
				UserID:      user.ID,
				Privacy:     domain.PrivacyPublic,
				Status:      domain.StatusQueued,
			},
			wantErr: false,
		},
		{
			name: "video with minimal fields",
			video: &domain.Video{
				Title:  "Minimal",
				UserID: user.ID,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := repo.Create(ctx, tt.video)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEmpty(t, tt.video.ID)
			}
		})
	}
}

func TestVideoRepository_GetByID(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}
	db := testDB.DB

	repo := NewVideoRepository(db)
	ctx := context.Background()

	// Create a test user
	userRepo := NewUserRepository(db)
	user := &domain.User{
		ID:          uuid.New().String(),
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
	}
	err := userRepo.Create(ctx, user, "hashedpassword")
	require.NoError(t, err)

	// Create a video
	video := &domain.Video{
		Title:       "Test Video",
		Description: "Test Description",
		UserID:      user.ID,
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
	}
	err = repo.Create(ctx, video)
	require.NoError(t, err)

	t.Run("existing video", func(t *testing.T) {
		retrieved, err := repo.GetByID(ctx, video.ID)
		assert.NoError(t, err)
		assert.Equal(t, video.ID, retrieved.ID)
		assert.Equal(t, video.Title, retrieved.Title)
	})

	t.Run("non-existent video", func(t *testing.T) {
		_, err := repo.GetByID(ctx, uuid.New().String())
		assert.Error(t, err)
		assert.Equal(t, domain.ErrNotFound, err)
	})
}

func TestVideoRepository_Update(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}
	db := testDB.DB

	repo := NewVideoRepository(db)
	ctx := context.Background()

	// Create a test user
	userRepo := NewUserRepository(db)
	user := &domain.User{
		ID:          uuid.New().String(),
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
	}
	err := userRepo.Create(ctx, user, "hashedpassword")
	require.NoError(t, err)

	// Create a video
	video := &domain.Video{
		Title:       "Original Title",
		Description: "Original Description",
		UserID:      user.ID,
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusQueued,
	}
	err = repo.Create(ctx, video)
	require.NoError(t, err)

	// Update the video
	video.Title = "Updated Title"
	video.Description = "Updated Description"
	err = repo.Update(ctx, video)
	assert.NoError(t, err)

	// Verify update
	retrieved, err := repo.GetByID(ctx, video.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated Title", retrieved.Title)
	assert.Equal(t, "Updated Description", retrieved.Description)
}

func TestVideoRepository_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}
	db := testDB.DB

	repo := NewVideoRepository(db)
	ctx := context.Background()

	// Create a test user
	userRepo := NewUserRepository(db)
	user := &domain.User{
		ID:          uuid.New().String(),
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
	}
	err := userRepo.Create(ctx, user, "hashedpassword")
	require.NoError(t, err)

	// Create a video
	video := &domain.Video{
		Title:       "Test Video",
		Description: "Test Description",
		UserID:      user.ID,
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
	}
	err = repo.Create(ctx, video)
	require.NoError(t, err)

	// Delete the video
	err = repo.Delete(ctx, video.ID, user.ID)
	assert.NoError(t, err)

	// Verify deletion
	_, err = repo.GetByID(ctx, video.ID)
	assert.Error(t, err)
}

func TestVideoRepository_Count(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}
	db := testDB.DB

	repo := NewVideoRepository(db)
	ctx := context.Background()

	// Create a test user
	userRepo := NewUserRepository(db)
	user := &domain.User{
		ID:          uuid.New().String(),
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
	}
	err := userRepo.Create(ctx, user, "hashedpassword")
	require.NoError(t, err)

	// Get initial count
	initialCount, err := repo.Count(ctx)
	require.NoError(t, err)

	// Create videos
	for i := 0; i < 3; i++ {
		video := &domain.Video{
			Title:       "Test Video " + strconv.Itoa(i),
			Description: "Test Description",
			UserID:      user.ID,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
		}
		err = repo.Create(ctx, video)
		require.NoError(t, err)
	}

	// Verify count increased
	newCount, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, initialCount+3, newCount)
}

func TestVideoRepository_List(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test")
	}

	testDB := testutil.SetupTestDB(t)
	if testDB == nil {
		return
	}
	db := testDB.DB

	repo := NewVideoRepository(db)
	ctx := context.Background()

	// Create a test user
	userRepo := NewUserRepository(db)
	user := &domain.User{
		ID:          uuid.New().String(),
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Role:        domain.RoleUser,
	}
	err := userRepo.Create(ctx, user, "hashedpassword")
	require.NoError(t, err)

	// Create multiple videos
	for i := 0; i < 5; i++ {
		video := &domain.Video{
			Title:       "Test Video " + strconv.Itoa(i),
			Description: "Test Description",
			UserID:      user.ID,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
		}
		err = repo.Create(ctx, video)
		require.NoError(t, err)
		// Small delay to ensure different timestamps
		time.Sleep(time.Millisecond)
	}

	req := &domain.VideoSearchRequest{
		Limit: 10,
	}

	videos, total, err := repo.List(ctx, req)
	assert.NoError(t, err)
	assert.GreaterOrEqual(t, len(videos), 5)
	assert.GreaterOrEqual(t, total, int64(5))
}
