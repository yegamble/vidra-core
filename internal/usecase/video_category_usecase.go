package usecase

import (
	"context"
	"fmt"
	"regexp"
	"strings"

	"athena/internal/domain"

	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

// VideoCategoryRepository interface for repository layer
type VideoCategoryRepository interface {
	Create(ctx context.Context, category *domain.VideoCategory) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.VideoCategory, error)
	GetBySlug(ctx context.Context, slug string) (*domain.VideoCategory, error)
	List(ctx context.Context, opts domain.VideoCategoryListOptions) ([]*domain.VideoCategory, error)
	Update(ctx context.Context, id uuid.UUID, updates *domain.UpdateVideoCategoryRequest) error
	Delete(ctx context.Context, id uuid.UUID) error
	GetDefault(ctx context.Context) (*domain.VideoCategory, error)
}

type VideoCategoryUseCase interface {
	CreateCategory(ctx context.Context, userID uuid.UUID, req *domain.CreateVideoCategoryRequest) (*domain.VideoCategory, error)
	GetCategoryByID(ctx context.Context, id uuid.UUID) (*domain.VideoCategory, error)
	GetCategoryBySlug(ctx context.Context, slug string) (*domain.VideoCategory, error)
	ListCategories(ctx context.Context, opts domain.VideoCategoryListOptions) ([]*domain.VideoCategory, error)
	UpdateCategory(ctx context.Context, userID uuid.UUID, categoryID uuid.UUID, req *domain.UpdateVideoCategoryRequest) error
	DeleteCategory(ctx context.Context, userID uuid.UUID, categoryID uuid.UUID) error
	GetDefaultCategory(ctx context.Context) (*domain.VideoCategory, error)
}

type videoCategoryUseCase struct {
	categoryRepo VideoCategoryRepository
	userRepo     UserRepository
	validator    *validator.Validate
}

func NewVideoCategoryUseCase(
	categoryRepo VideoCategoryRepository,
	userRepo UserRepository,
) VideoCategoryUseCase {
	v := validator.New()

	// Register custom validation for slug
	if err := v.RegisterValidation("slug", validateSlug); err != nil {
		// This should never fail with valid validator functions
		panic(fmt.Sprintf("failed to register slug validator: %v", err))
	}

	// Register custom validation for hex color
	if err := v.RegisterValidation("hexcolor", validateHexColor); err != nil {
		// This should never fail with valid validator functions
		panic(fmt.Sprintf("failed to register hexcolor validator: %v", err))
	}

	return &videoCategoryUseCase{
		categoryRepo: categoryRepo,
		userRepo:     userRepo,
		validator:    v,
	}
}

func (uc *videoCategoryUseCase) CreateCategory(ctx context.Context, userID uuid.UUID, req *domain.CreateVideoCategoryRequest) (*domain.VideoCategory, error) {
	// Validate request
	if err := uc.validator.Struct(req); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Check if user is admin
	user, err := uc.userRepo.GetByID(ctx, userID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	if user.Role != "admin" {
		return nil, fmt.Errorf("unauthorized: only admins can create categories")
	}

	// Check if slug already exists
	existingCategory, _ := uc.categoryRepo.GetBySlug(ctx, req.Slug)
	if existingCategory != nil {
		return nil, fmt.Errorf("category with slug '%s' already exists", req.Slug)
	}

	// Create category
	category := &domain.VideoCategory{
		ID:           uuid.New(),
		Name:         req.Name,
		Slug:         req.Slug,
		Description:  req.Description,
		Icon:         req.Icon,
		Color:        req.Color,
		DisplayOrder: req.DisplayOrder,
		IsActive:     req.IsActive,
		CreatedBy:    &userID,
	}

	if err := uc.categoryRepo.Create(ctx, category); err != nil {
		return nil, fmt.Errorf("failed to create category: %w", err)
	}

	return category, nil
}

func (uc *videoCategoryUseCase) GetCategoryByID(ctx context.Context, id uuid.UUID) (*domain.VideoCategory, error) {
	category, err := uc.categoryRepo.GetByID(ctx, id)
	if err != nil {
		return nil, fmt.Errorf("failed to get category: %w", err)
	}
	return category, nil
}

func (uc *videoCategoryUseCase) GetCategoryBySlug(ctx context.Context, slug string) (*domain.VideoCategory, error) {
	category, err := uc.categoryRepo.GetBySlug(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("failed to get category: %w", err)
	}
	return category, nil
}

func (uc *videoCategoryUseCase) ListCategories(ctx context.Context, opts domain.VideoCategoryListOptions) ([]*domain.VideoCategory, error) {
	// Validate options
	if err := uc.validator.Struct(opts); err != nil {
		return nil, fmt.Errorf("validation failed: %w", err)
	}

	// Set defaults
	if opts.Limit == 0 {
		opts.Limit = 50
	}

	categories, err := uc.categoryRepo.List(ctx, opts)
	if err != nil {
		return nil, fmt.Errorf("failed to list categories: %w", err)
	}

	return categories, nil
}

func (uc *videoCategoryUseCase) UpdateCategory(ctx context.Context, userID uuid.UUID, categoryID uuid.UUID, req *domain.UpdateVideoCategoryRequest) error {
	// Validate request
	if err := uc.validator.Struct(req); err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	// Check if user is admin
	user, err := uc.userRepo.GetByID(ctx, userID.String())
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if user.Role != "admin" {
		return fmt.Errorf("unauthorized: only admins can update categories")
	}

	// Check if category exists
	existingCategory, err := uc.categoryRepo.GetByID(ctx, categoryID)
	if err != nil {
		return fmt.Errorf("category not found: %w", err)
	}

	// Prevent modification of the default 'other' category slug
	if existingCategory.Slug == "other" && req.Slug != nil && *req.Slug != "other" {
		return fmt.Errorf("cannot change slug of the default 'other' category")
	}

	// Check if new slug already exists (if changing slug)
	if req.Slug != nil && *req.Slug != existingCategory.Slug {
		duplicateCategory, _ := uc.categoryRepo.GetBySlug(ctx, *req.Slug)
		if duplicateCategory != nil {
			return fmt.Errorf("category with slug '%s' already exists", *req.Slug)
		}
	}

	if err := uc.categoryRepo.Update(ctx, categoryID, req); err != nil {
		return fmt.Errorf("failed to update category: %w", err)
	}

	return nil
}

func (uc *videoCategoryUseCase) DeleteCategory(ctx context.Context, userID uuid.UUID, categoryID uuid.UUID) error {
	// Check if user is admin
	user, err := uc.userRepo.GetByID(ctx, userID.String())
	if err != nil {
		return fmt.Errorf("failed to get user: %w", err)
	}

	if user.Role != "admin" {
		return fmt.Errorf("unauthorized: only admins can delete categories")
	}

	// Check if category exists
	category, err := uc.categoryRepo.GetByID(ctx, categoryID)
	if err != nil {
		return fmt.Errorf("category not found: %w", err)
	}

	// Prevent deletion of the default 'other' category
	if category.Slug == "other" {
		return fmt.Errorf("cannot delete the default 'other' category")
	}

	// Delete the category (videos will have their category_id set to NULL due to ON DELETE SET NULL)
	if err := uc.categoryRepo.Delete(ctx, categoryID); err != nil {
		return fmt.Errorf("failed to delete category: %w", err)
	}

	return nil
}

func (uc *videoCategoryUseCase) GetDefaultCategory(ctx context.Context) (*domain.VideoCategory, error) {
	category, err := uc.categoryRepo.GetDefault(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get default category: %w", err)
	}
	return category, nil
}

// validateSlug checks if a string is a valid URL slug
func validateSlug(fl validator.FieldLevel) bool {
	slug := fl.Field().String()
	if slug == "" {
		return false
	}
	// Slug should only contain lowercase letters, numbers, and hyphens
	match, _ := regexp.MatchString("^[a-z0-9-]+$", slug)
	return match
}

// validateHexColor checks if a string is a valid hex color code
func validateHexColor(fl validator.FieldLevel) bool {
	color := fl.Field().String()
	if color == "" {
		return true // Allow empty
	}
	// Hex color should be in format #RRGGBB or #RGB
	match, _ := regexp.MatchString("^#([A-Fa-f0-9]{6}|[A-Fa-f0-9]{3})$", color)
	return match
}

// Helper function to generate a slug from a name
func GenerateSlug(name string) string {
	// Convert to lowercase
	slug := strings.ToLower(name)

	// Replace spaces and special characters with hyphens
	reg := regexp.MustCompile(`[^a-z0-9]+`)
	slug = reg.ReplaceAllString(slug, "-")

	// Remove leading and trailing hyphens
	slug = strings.Trim(slug, "-")

	return slug
}
