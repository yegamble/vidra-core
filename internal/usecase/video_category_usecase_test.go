package usecase

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Mock: VideoCategoryRepository
// ---------------------------------------------------------------------------

type MockVideoCategoryRepository struct {
	mock.Mock
}

func (m *MockVideoCategoryRepository) Create(ctx context.Context, category *domain.VideoCategory) error {
	args := m.Called(ctx, category)
	return args.Error(0)
}

func (m *MockVideoCategoryRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.VideoCategory, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoCategory), args.Error(1)
}

func (m *MockVideoCategoryRepository) GetBySlug(ctx context.Context, slug string) (*domain.VideoCategory, error) {
	args := m.Called(ctx, slug)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoCategory), args.Error(1)
}

func (m *MockVideoCategoryRepository) List(ctx context.Context, opts domain.VideoCategoryListOptions) ([]*domain.VideoCategory, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoCategory), args.Error(1)
}

func (m *MockVideoCategoryRepository) Update(ctx context.Context, id uuid.UUID, updates *domain.UpdateVideoCategoryRequest) error {
	args := m.Called(ctx, id, updates)
	return args.Error(0)
}

func (m *MockVideoCategoryRepository) Delete(ctx context.Context, id uuid.UUID) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockVideoCategoryRepository) GetDefault(ctx context.Context) (*domain.VideoCategory, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoCategory), args.Error(1)
}

// ---------------------------------------------------------------------------
// Mock: UserRepository (for video category tests)
// Uses the existing UserRepository type alias = port.UserRepository
// MockUserRepository is already defined in message_service_test.go
// We reuse MockVideoCategoryUserRepo to avoid redeclaration.
// ---------------------------------------------------------------------------

type MockVCUserRepo struct {
	mock.Mock
}

func (m *MockVCUserRepo) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	args := m.Called(ctx, user, passwordHash)
	return args.Error(0)
}

func (m *MockVCUserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockVCUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockVCUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockVCUserRepo) Update(ctx context.Context, user *domain.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockVCUserRepo) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockVCUserRepo) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}

func (m *MockVCUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	args := m.Called(ctx, userID, passwordHash)
	return args.Error(0)
}

func (m *MockVCUserRepo) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.User), args.Error(1)
}

func (m *MockVCUserRepo) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockVCUserRepo) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	args := m.Called(ctx, userID, ipfsCID, webpCID)
	return args.Error(0)
}

func (m *MockVCUserRepo) MarkEmailAsVerified(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockVCUserRepo) Anonymize(_ context.Context, _ string) error { return nil }

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func adminUser(id uuid.UUID) *domain.User {
	return &domain.User{
		ID:   id.String(),
		Role: domain.RoleAdmin,
	}
}

func regularUser(id uuid.UUID) *domain.User {
	return &domain.User{
		ID:   id.String(),
		Role: domain.RoleUser,
	}
}

func validCreateRequest() *domain.CreateVideoCategoryRequest {
	return &domain.CreateVideoCategoryRequest{
		Name:         "Music",
		Slug:         "music",
		DisplayOrder: 1,
		IsActive:     true,
	}
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestVideoCategory_NewVideoCategoryUseCase(t *testing.T) {
	catRepo := new(MockVideoCategoryRepository)
	userRepo := new(MockVCUserRepo)

	uc := NewVideoCategoryUseCase(catRepo, userRepo)
	require.NotNil(t, uc)
}

func TestVideoCategory_CreateCategory(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetBySlug", mock.Anything, "music").Return(nil, errors.New("not found"))
		catRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.VideoCategory")).Return(nil)

		req := validCreateRequest()
		cat, err := uc.CreateCategory(ctx, userID, req)
		require.NoError(t, err)
		require.NotNil(t, cat)
		assert.Equal(t, "Music", cat.Name)
		assert.Equal(t, "music", cat.Slug)
	})

	t.Run("validation error - empty name", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		req := &domain.CreateVideoCategoryRequest{
			Name: "",
			Slug: "valid-slug",
		}
		cat, err := uc.CreateCategory(ctx, userID, req)
		require.Error(t, err)
		assert.Nil(t, cat)
		assert.Contains(t, err.Error(), "validation failed")
	})

	t.Run("validation error - invalid slug", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		req := &domain.CreateVideoCategoryRequest{
			Name: "Test",
			Slug: "INVALID SLUG!",
		}
		cat, err := uc.CreateCategory(ctx, userID, req)
		require.Error(t, err)
		assert.Nil(t, cat)
		assert.Contains(t, err.Error(), "validation failed")
	})

	t.Run("non-admin user", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(regularUser(userID), nil)

		req := validCreateRequest()
		cat, err := uc.CreateCategory(ctx, userID, req)
		require.Error(t, err)
		assert.Nil(t, cat)
		assert.Contains(t, err.Error(), "unauthorized")
	})

	t.Run("duplicate slug", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetBySlug", mock.Anything, "music").Return(&domain.VideoCategory{Slug: "music"}, nil)

		req := validCreateRequest()
		cat, err := uc.CreateCategory(ctx, userID, req)
		require.Error(t, err)
		assert.Nil(t, cat)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("repo create error", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetBySlug", mock.Anything, "music").Return(nil, errors.New("not found"))
		catRepo.On("Create", mock.Anything, mock.Anything).Return(errors.New("db error"))

		req := validCreateRequest()
		cat, err := uc.CreateCategory(ctx, userID, req)
		require.Error(t, err)
		assert.Nil(t, cat)
		assert.Contains(t, err.Error(), "failed to create")
	})
}

func TestVideoCategory_GetCategoryByID(t *testing.T) {
	ctx := context.Background()
	catID := uuid.New()

	t.Run("success", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		expected := &domain.VideoCategory{ID: catID, Name: "Music", Slug: "music"}
		catRepo.On("GetByID", mock.Anything, catID).Return(expected, nil)

		cat, err := uc.GetCategoryByID(ctx, catID)
		require.NoError(t, err)
		assert.Equal(t, "Music", cat.Name)
	})

	t.Run("not found", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		catRepo.On("GetByID", mock.Anything, catID).Return(nil, errors.New("not found"))

		cat, err := uc.GetCategoryByID(ctx, catID)
		require.Error(t, err)
		assert.Nil(t, cat)
		assert.Contains(t, err.Error(), "failed to get category")
	})
}

func TestVideoCategory_GetCategoryBySlug(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		expected := &domain.VideoCategory{Name: "Gaming", Slug: "gaming"}
		catRepo.On("GetBySlug", mock.Anything, "gaming").Return(expected, nil)

		cat, err := uc.GetCategoryBySlug(ctx, "gaming")
		require.NoError(t, err)
		assert.Equal(t, "Gaming", cat.Name)
	})

	t.Run("not found", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		catRepo.On("GetBySlug", mock.Anything, "nonexistent").Return(nil, errors.New("not found"))

		cat, err := uc.GetCategoryBySlug(ctx, "nonexistent")
		require.Error(t, err)
		assert.Nil(t, cat)
	})
}

func TestVideoCategory_ListCategories(t *testing.T) {
	ctx := context.Background()

	t.Run("success with defaults", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		categories := []*domain.VideoCategory{
			{Name: "Music", Slug: "music"},
			{Name: "Gaming", Slug: "gaming"},
		}
		// When Limit is 0, the usecase sets it to 50 before calling the repo
		catRepo.On("List", mock.Anything, mock.MatchedBy(func(opts domain.VideoCategoryListOptions) bool {
			return opts.Limit == 50
		})).Return(categories, nil)

		opts := domain.VideoCategoryListOptions{}
		result, err := uc.ListCategories(ctx, opts)
		require.NoError(t, err)
		assert.Len(t, result, 2)
	})

	t.Run("validation error - invalid order_by", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		opts := domain.VideoCategoryListOptions{
			OrderBy: "invalid_field",
		}
		result, err := uc.ListCategories(ctx, opts)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "validation failed")
	})

	t.Run("repo error", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		catRepo.On("List", mock.Anything, mock.Anything).Return(nil, errors.New("db error"))

		opts := domain.VideoCategoryListOptions{Limit: 10}
		result, err := uc.ListCategories(ctx, opts)
		require.Error(t, err)
		assert.Nil(t, result)
		assert.Contains(t, err.Error(), "failed to list")
	})
}

func TestVideoCategory_UpdateCategory(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	catID := uuid.New()

	t.Run("success", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		newName := "Updated Music"
		req := &domain.UpdateVideoCategoryRequest{Name: &newName}

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetByID", mock.Anything, catID).Return(&domain.VideoCategory{ID: catID, Slug: "music"}, nil)
		catRepo.On("Update", mock.Anything, catID, req).Return(nil)

		err := uc.UpdateCategory(ctx, userID, catID, req)
		require.NoError(t, err)
	})

	t.Run("non-admin user", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		newName := "test"
		req := &domain.UpdateVideoCategoryRequest{Name: &newName}
		userRepo.On("GetByID", mock.Anything, userID.String()).Return(regularUser(userID), nil)

		err := uc.UpdateCategory(ctx, userID, catID, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unauthorized")
	})

	t.Run("category not found", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		newName := "test"
		req := &domain.UpdateVideoCategoryRequest{Name: &newName}
		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetByID", mock.Anything, catID).Return(nil, errors.New("not found"))

		err := uc.UpdateCategory(ctx, userID, catID, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "category not found")
	})

	t.Run("prevent changing slug of other category", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		newSlug := "renamed"
		req := &domain.UpdateVideoCategoryRequest{Slug: &newSlug}
		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetByID", mock.Anything, catID).Return(&domain.VideoCategory{ID: catID, Slug: "other"}, nil)

		err := uc.UpdateCategory(ctx, userID, catID, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot change slug of the default 'other' category")
	})

	t.Run("duplicate slug on update", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		newSlug := "gaming"
		req := &domain.UpdateVideoCategoryRequest{Slug: &newSlug}
		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetByID", mock.Anything, catID).Return(&domain.VideoCategory{ID: catID, Slug: "music"}, nil)
		catRepo.On("GetBySlug", mock.Anything, "gaming").Return(&domain.VideoCategory{Slug: "gaming"}, nil)

		err := uc.UpdateCategory(ctx, userID, catID, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "already exists")
	})

	t.Run("repo update error", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		newName := "Updated"
		req := &domain.UpdateVideoCategoryRequest{Name: &newName}
		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetByID", mock.Anything, catID).Return(&domain.VideoCategory{ID: catID, Slug: "music"}, nil)
		catRepo.On("Update", mock.Anything, catID, req).Return(errors.New("db error"))

		err := uc.UpdateCategory(ctx, userID, catID, req)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update")
	})
}

func TestVideoCategory_DeleteCategory(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	catID := uuid.New()

	t.Run("success", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetByID", mock.Anything, catID).Return(&domain.VideoCategory{ID: catID, Slug: "music"}, nil)
		catRepo.On("Delete", mock.Anything, catID).Return(nil)

		err := uc.DeleteCategory(ctx, userID, catID)
		require.NoError(t, err)
	})

	t.Run("non-admin user", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(regularUser(userID), nil)

		err := uc.DeleteCategory(ctx, userID, catID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unauthorized")
	})

	t.Run("not found", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetByID", mock.Anything, catID).Return(nil, errors.New("not found"))

		err := uc.DeleteCategory(ctx, userID, catID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "category not found")
	})

	t.Run("prevent deletion of other category", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetByID", mock.Anything, catID).Return(&domain.VideoCategory{ID: catID, Slug: "other"}, nil)

		err := uc.DeleteCategory(ctx, userID, catID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot delete the default 'other' category")
	})

	t.Run("repo delete error", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetByID", mock.Anything, catID).Return(&domain.VideoCategory{ID: catID, Slug: "music"}, nil)
		catRepo.On("Delete", mock.Anything, catID).Return(errors.New("db error"))

		err := uc.DeleteCategory(ctx, userID, catID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete")
	})
}

func TestVideoCategory_GetDefaultCategory(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		expected := &domain.VideoCategory{Name: "Other", Slug: "other"}
		catRepo.On("GetDefault", mock.Anything).Return(expected, nil)

		cat, err := uc.GetDefaultCategory(ctx)
		require.NoError(t, err)
		assert.Equal(t, "Other", cat.Name)
	})

	t.Run("error", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		catRepo.On("GetDefault", mock.Anything).Return(nil, errors.New("not found"))

		cat, err := uc.GetDefaultCategory(ctx)
		require.Error(t, err)
		assert.Nil(t, cat)
		assert.Contains(t, err.Error(), "failed to get default category")
	})
}

func TestVideoCategory_CreateWithColor(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("valid hex color", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		color := "#FF5733"
		req := &domain.CreateVideoCategoryRequest{
			Name:     "Colored",
			Slug:     "colored",
			Color:    &color,
			IsActive: true,
		}

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetBySlug", mock.Anything, "colored").Return(nil, errors.New("not found"))
		catRepo.On("Create", mock.Anything, mock.Anything).Return(nil)

		cat, err := uc.CreateCategory(ctx, userID, req)
		require.NoError(t, err)
		require.NotNil(t, cat)
	})

	t.Run("valid short hex color", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		color := "#F00"
		req := &domain.CreateVideoCategoryRequest{
			Name:     "Red",
			Slug:     "red",
			Color:    &color,
			IsActive: true,
		}

		userRepo.On("GetByID", mock.Anything, userID.String()).Return(adminUser(userID), nil)
		catRepo.On("GetBySlug", mock.Anything, "red").Return(nil, errors.New("not found"))
		catRepo.On("Create", mock.Anything, mock.Anything).Return(nil)

		cat, err := uc.CreateCategory(ctx, userID, req)
		require.NoError(t, err)
		require.NotNil(t, cat)
	})

	t.Run("invalid hex color", func(t *testing.T) {
		catRepo := new(MockVideoCategoryRepository)
		userRepo := new(MockVCUserRepo)
		uc := NewVideoCategoryUseCase(catRepo, userRepo)

		color := "not-a-color"
		req := &domain.CreateVideoCategoryRequest{
			Name:     "Bad Color",
			Slug:     "bad-color",
			Color:    &color,
			IsActive: true,
		}

		cat, err := uc.CreateCategory(ctx, userID, req)
		require.Error(t, err)
		assert.Nil(t, cat)
		assert.Contains(t, err.Error(), "validation failed")
	})
}

func TestVideoCategory_GenerateSlug(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple name", "Music", "music"},
		{"spaces", "Live Music", "live-music"},
		{"special chars", "Rock & Roll!", "rock-roll"},
		{"multiple spaces", "Science  and  Technology", "science-and-technology"},
		{"leading trailing special", "---Hello World---", "hello-world"},
		{"unicode chars", "Cafe & Bar", "cafe-bar"},
		{"numbers", "Top 10 Videos", "top-10-videos"},
		{"already lowercase slug", "my-slug", "my-slug"},
		{"uppercase", "GAMING", "gaming"},
		{"empty string", "", ""},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := GenerateSlug(tc.input)
			assert.Equal(t, tc.want, got)
		})
	}
}
