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

func TestUserRepository_Create(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()
	now := time.Now()

	user := &domain.User{
		ID:          uuid.New().String(),
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Bio:         "Test bio",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	err := repo.Create(ctx, user, "hashed_password")
	require.NoError(t, err)

	createdUser, err := repo.GetByID(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, user.Username, createdUser.Username)
	assert.Equal(t, user.Email, createdUser.Email)
	assert.Equal(t, user.DisplayName, createdUser.DisplayName)
	assert.Equal(t, user.Role, createdUser.Role)
	assert.Equal(t, user.IsActive, createdUser.IsActive)
}

func TestUserRepository_GetByEmail(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUser(t, repo, ctx, "testuser", "test@example.com")

	foundUser, err := repo.GetByEmail(ctx, "test@example.com")
	require.NoError(t, err)
	assert.Equal(t, user.ID, foundUser.ID)
	assert.Equal(t, user.Email, foundUser.Email)

	_, err = repo.GetByEmail(ctx, "nonexistent@example.com")
	assert.ErrorIs(t, err, domain.ErrUserNotFound)
}

func TestUserRepository_GetByUsername(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUser(t, repo, ctx, "testuser", "test@example.com")

	foundUser, err := repo.GetByUsername(ctx, "testuser")
	require.NoError(t, err)
	assert.Equal(t, user.ID, foundUser.ID)
	assert.Equal(t, user.Username, foundUser.Username)

	_, err = repo.GetByUsername(ctx, "nonexistent")
	assert.ErrorIs(t, err, domain.ErrUserNotFound)
}

func TestUserRepository_Update(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUser(t, repo, ctx, "testuser", "test@example.com")

	user.DisplayName = "Updated Name"
	user.Bio = "Updated bio"
	user.UpdatedAt = time.Now()

	err := repo.Update(ctx, user)
	require.NoError(t, err)

	updatedUser, err := repo.GetByID(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "Updated Name", updatedUser.DisplayName)
	assert.Equal(t, "Updated bio", updatedUser.Bio)
}

func TestUserRepository_Delete(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUser(t, repo, ctx, "testuser", "test@example.com")

	err := repo.Delete(ctx, user.ID)
	require.NoError(t, err)

	_, err = repo.GetByID(ctx, user.ID)
	assert.ErrorIs(t, err, domain.ErrUserNotFound)

	// Try to delete non-existent user
	err = repo.Delete(ctx, uuid.New().String())
	assert.ErrorIs(t, err, domain.ErrUserNotFound)
}

func TestUserRepository_GetPasswordHash(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUser(t, repo, ctx, "testuser", "test@example.com")

	passwordHash, err := repo.GetPasswordHash(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "hashed_password", passwordHash)

	_, err = repo.GetPasswordHash(ctx, uuid.New().String())
	assert.ErrorIs(t, err, domain.ErrUserNotFound)
}

func TestUserRepository_UpdatePassword(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()
	user := createTestUser(t, repo, ctx, "testuser", "test@example.com")

	err := repo.UpdatePassword(ctx, user.ID, "new_hashed_password")
	require.NoError(t, err)

	passwordHash, err := repo.GetPasswordHash(ctx, user.ID)
	require.NoError(t, err)
	assert.Equal(t, "new_hashed_password", passwordHash)

	err = repo.UpdatePassword(ctx, uuid.New().String(), "some_password")
	assert.ErrorIs(t, err, domain.ErrUserNotFound)
}

func TestUserRepository_List(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	// Create multiple users
	createTestUser(t, repo, ctx, "user1", "user1@example.com")
	createTestUser(t, repo, ctx, "user2", "user2@example.com")
	createTestUser(t, repo, ctx, "user3", "user3@example.com")

	users, err := repo.List(ctx, 2, 0)
	require.NoError(t, err)
	assert.Len(t, users, 2)

	users, err = repo.List(ctx, 2, 1)
	require.NoError(t, err)
	assert.Len(t, users, 2)

	users, err = repo.List(ctx, 10, 0)
	require.NoError(t, err)
	assert.Len(t, users, 3)
}

func TestUserRepository_Count(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()

	count, err := repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(0), count)

	createTestUser(t, repo, ctx, "user1", "user1@example.com")
	createTestUser(t, repo, ctx, "user2", "user2@example.com")

	count, err = repo.Count(ctx)
	require.NoError(t, err)
	assert.Equal(t, int64(2), count)
}

func TestUserRepository_CreateDuplicateEmail(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()
	createTestUser(t, repo, ctx, "user1", "test@example.com")

	user := &domain.User{
		ID:        uuid.New().String(),
		Username:  "user2",
		Email:     "test@example.com",
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := repo.Create(ctx, user, "hashed_password")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func TestUserRepository_CreateDuplicateUsername(t *testing.T) {
	testDB := testutil.SetupTestDB(t)
	repo := NewUserRepository(testDB.DB)

	ctx := context.Background()
	createTestUser(t, repo, ctx, "testuser", "test1@example.com")

	user := &domain.User{
		ID:        uuid.New().String(),
		Username:  "testuser",
		Email:     "test2@example.com",
		Role:      domain.RoleUser,
		IsActive:  true,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	err := repo.Create(ctx, user, "hashed_password")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "duplicate")
}

func createTestUser(t *testing.T, repo usecase.UserRepository, ctx context.Context, username, email string) *domain.User {
	t.Helper()

	user := &domain.User{
		ID:        uuid.New().String(),
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
