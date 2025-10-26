package video

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"athena/internal/domain"
	"athena/internal/middleware"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

// Mock VideoCategoryUseCase
type mockVideoCategoryUseCase struct {
	mock.Mock
}

func (m *mockVideoCategoryUseCase) CreateCategory(ctx context.Context, userID uuid.UUID, req *domain.CreateVideoCategoryRequest) (*domain.VideoCategory, error) {
	args := m.Called(ctx, userID, req)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoCategory), args.Error(1)
}

func (m *mockVideoCategoryUseCase) GetCategoryByID(ctx context.Context, id uuid.UUID) (*domain.VideoCategory, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoCategory), args.Error(1)
}

func (m *mockVideoCategoryUseCase) GetCategoryBySlug(ctx context.Context, slug string) (*domain.VideoCategory, error) {
	args := m.Called(ctx, slug)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoCategory), args.Error(1)
}

func (m *mockVideoCategoryUseCase) ListCategories(ctx context.Context, opts domain.VideoCategoryListOptions) ([]*domain.VideoCategory, error) {
	args := m.Called(ctx, opts)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.VideoCategory), args.Error(1)
}

func (m *mockVideoCategoryUseCase) UpdateCategory(ctx context.Context, userID uuid.UUID, categoryID uuid.UUID, req *domain.UpdateVideoCategoryRequest) error {
	args := m.Called(ctx, userID, categoryID, req)
	return args.Error(0)
}

func (m *mockVideoCategoryUseCase) DeleteCategory(ctx context.Context, userID uuid.UUID, categoryID uuid.UUID) error {
	args := m.Called(ctx, userID, categoryID)
	return args.Error(0)
}

func (m *mockVideoCategoryUseCase) GetDefaultCategory(ctx context.Context) (*domain.VideoCategory, error) {
	args := m.Called(ctx)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.VideoCategory), args.Error(1)
}

func TestListCategories_Success(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	categories := []*domain.VideoCategory{
		{
			ID:           uuid.New(),
			Name:         "Music",
			Slug:         "music",
			DisplayOrder: 1,
			IsActive:     true,
		},
		{
			ID:           uuid.New(),
			Name:         "Gaming",
			Slug:         "gaming",
			DisplayOrder: 2,
			IsActive:     true,
		},
	}

	mockUseCase.On("ListCategories", mock.Anything, mock.Anything).Return(categories, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/categories", nil)
	rr := httptest.NewRecorder()

	handler.ListCategories(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response []*domain.VideoCategory
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Len(t, response, 2)
	assert.Equal(t, "Music", response[0].Name)
	assert.Equal(t, "Gaming", response[1].Name)

	mockUseCase.AssertExpectations(t)
}

func TestGetCategory_ByID_Success(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	categoryID := uuid.New()
	category := &domain.VideoCategory{
		ID:           categoryID,
		Name:         "Music",
		Slug:         "music",
		DisplayOrder: 1,
		IsActive:     true,
	}

	mockUseCase.On("GetCategoryByID", mock.Anything, categoryID).Return(category, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/categories/"+categoryID.String(), nil)
	rr := httptest.NewRecorder()

	// Set up chi context with URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", categoryID.String())
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCategory(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response domain.VideoCategory
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "Music", response.Name)

	mockUseCase.AssertExpectations(t)
}

func TestGetCategory_BySlug_Success(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	category := &domain.VideoCategory{
		ID:           uuid.New(),
		Name:         "Music",
		Slug:         "music",
		DisplayOrder: 1,
		IsActive:     true,
	}

	mockUseCase.On("GetCategoryBySlug", mock.Anything, "music").Return(category, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v1/categories/music", nil)
	rr := httptest.NewRecorder()

	// Set up chi context with URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", "music")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	handler.GetCategory(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response domain.VideoCategory
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "Music", response.Name)

	mockUseCase.AssertExpectations(t)
}

func TestCreateCategory_AdminOnly_Success(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	adminUserID := uuid.New()
	createReq := &domain.CreateVideoCategoryRequest{
		Name:         "Podcasts",
		Slug:         "podcasts",
		Description:  stringPtr("Audio podcasts"),
		Icon:         stringPtr("🎙️"),
		Color:        stringPtr("#9933FF"),
		DisplayOrder: 20,
		IsActive:     true,
	}

	createdCategory := &domain.VideoCategory{
		ID:           uuid.New(),
		Name:         createReq.Name,
		Slug:         createReq.Slug,
		Description:  createReq.Description,
		Icon:         createReq.Icon,
		Color:        createReq.Color,
		DisplayOrder: createReq.DisplayOrder,
		IsActive:     createReq.IsActive,
		CreatedBy:    &adminUserID,
	}

	mockUseCase.On("CreateCategory", mock.Anything, adminUserID, mock.MatchedBy(func(req *domain.CreateVideoCategoryRequest) bool {
		return req.Name == "Podcasts" && req.Slug == "podcasts"
	})).Return(createdCategory, nil)

	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/categories", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	// Simulate admin authentication
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, adminUserID.String())
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "admin")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.CreateCategory(rr, req)

	assert.Equal(t, http.StatusCreated, rr.Code)

	var response domain.VideoCategory
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "Podcasts", response.Name)
	assert.Equal(t, "podcasts", response.Slug)

	mockUseCase.AssertExpectations(t)
}

func TestCreateCategory_NonAdmin_Forbidden(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	userID := uuid.New()
	createReq := &domain.CreateVideoCategoryRequest{
		Name: "Podcasts",
		Slug: "podcasts",
	}

	// Simulate that the usecase returns an error for non-admin
	mockUseCase.On("CreateCategory", mock.Anything, userID, mock.Anything).
		Return(nil, domain.NewDomainError("FORBIDDEN", "unauthorized: only admins can create categories"))

	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/categories", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	// Simulate regular user authentication (not admin)
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "user")
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.CreateCategory(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Contains(t, response["details"], "only admins can create categories")

	mockUseCase.AssertExpectations(t)
}

func TestCreateCategory_NoAuth_Unauthorized(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	createReq := &domain.CreateVideoCategoryRequest{
		Name: "Podcasts",
		Slug: "podcasts",
	}

	body, _ := json.Marshal(createReq)
	req := httptest.NewRequest(http.MethodPost, "/api/v1/admin/categories", bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	// No authentication context

	rr := httptest.NewRecorder()
	handler.CreateCategory(rr, req)

	assert.Equal(t, http.StatusUnauthorized, rr.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "Unauthorized", response["error"])

	// No usecase calls should be made
	mockUseCase.AssertNotCalled(t, "CreateCategory")
}

func TestUpdateCategory_AdminOnly_Success(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	adminUserID := uuid.New()
	categoryID := uuid.New()
	updateReq := &domain.UpdateVideoCategoryRequest{
		Name:         stringPtr("Updated Music"),
		DisplayOrder: intPtr(5),
	}

	updatedCategory := &domain.VideoCategory{
		ID:           categoryID,
		Name:         "Updated Music",
		Slug:         "music",
		DisplayOrder: 5,
		IsActive:     true,
	}

	mockUseCase.On("UpdateCategory", mock.Anything, adminUserID, categoryID, mock.Anything).Return(nil)
	mockUseCase.On("GetCategoryByID", mock.Anything, categoryID).Return(updatedCategory, nil)

	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/categories/"+categoryID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	// Simulate admin authentication
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, adminUserID.String())
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "admin")

	// Set up chi context with URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", categoryID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.UpdateCategory(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)

	var response domain.VideoCategory
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Equal(t, "Updated Music", response.Name)
	assert.Equal(t, 5, response.DisplayOrder)

	mockUseCase.AssertExpectations(t)
}

func TestUpdateCategory_NonAdmin_Forbidden(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	userID := uuid.New()
	categoryID := uuid.New()
	updateReq := &domain.UpdateVideoCategoryRequest{
		Name: stringPtr("Updated Music"),
	}

	mockUseCase.On("UpdateCategory", mock.Anything, userID, categoryID, mock.Anything).
		Return(domain.NewDomainError("FORBIDDEN", "unauthorized: only admins can update categories"))

	body, _ := json.Marshal(updateReq)
	req := httptest.NewRequest(http.MethodPut, "/api/v1/admin/categories/"+categoryID.String(), bytes.NewBuffer(body))
	req.Header.Set("Content-Type", "application/json")

	// Simulate regular user authentication
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "user")

	// Set up chi context with URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", categoryID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.UpdateCategory(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Contains(t, response["details"], "only admins can update categories")

	mockUseCase.AssertExpectations(t)
}

func TestDeleteCategory_AdminOnly_Success(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	adminUserID := uuid.New()
	categoryID := uuid.New()

	mockUseCase.On("DeleteCategory", mock.Anything, adminUserID, categoryID).Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/categories/"+categoryID.String(), nil)

	// Simulate admin authentication
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, adminUserID.String())
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "admin")

	// Set up chi context with URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", categoryID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.DeleteCategory(rr, req)

	assert.Equal(t, http.StatusNoContent, rr.Code)

	mockUseCase.AssertExpectations(t)
}

func TestDeleteCategory_NonAdmin_Forbidden(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	userID := uuid.New()
	categoryID := uuid.New()

	mockUseCase.On("DeleteCategory", mock.Anything, userID, categoryID).
		Return(domain.NewDomainError("FORBIDDEN", "unauthorized: only admins can delete categories"))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/categories/"+categoryID.String(), nil)

	// Simulate regular user authentication
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, userID.String())
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "user")

	// Set up chi context with URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", categoryID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.DeleteCategory(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Contains(t, response["details"], "only admins can delete categories")

	mockUseCase.AssertExpectations(t)
}

func TestDeleteCategory_DefaultCategory_Error(t *testing.T) {
	mockUseCase := new(mockVideoCategoryUseCase)
	handler := NewVideoCategoryHandler(mockUseCase)

	adminUserID := uuid.New()
	categoryID := uuid.New()

	mockUseCase.On("DeleteCategory", mock.Anything, adminUserID, categoryID).
		Return(domain.NewDomainError("CANNOT_DELETE", "cannot delete the default 'other' category"))

	req := httptest.NewRequest(http.MethodDelete, "/api/v1/admin/categories/"+categoryID.String(), nil)

	// Simulate admin authentication
	ctx := context.WithValue(req.Context(), middleware.UserIDKey, adminUserID.String())
	ctx = context.WithValue(ctx, middleware.UserRoleKey, "admin")

	// Set up chi context with URL params
	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", categoryID.String())
	ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)

	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()
	handler.DeleteCategory(rr, req)

	assert.Equal(t, http.StatusBadRequest, rr.Code)

	var response map[string]interface{}
	err := json.NewDecoder(rr.Body).Decode(&response)
	assert.NoError(t, err)
	assert.Contains(t, response["details"], "cannot delete the default 'other' category")

	mockUseCase.AssertExpectations(t)
}

// Helper functions
func stringPtr(s string) *string {
	return &s
}

func intPtr(i int) *int {
	return &i
}
