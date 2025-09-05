package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type VideoCategoryHandler struct {
	categoryUseCase usecase.VideoCategoryUseCase
}

func NewVideoCategoryHandler(categoryUseCase usecase.VideoCategoryUseCase) *VideoCategoryHandler {
	return &VideoCategoryHandler{
		categoryUseCase: categoryUseCase,
	}
}

func (h *VideoCategoryHandler) RegisterRoutes(r chi.Router) {
	// Public routes
	r.Get("/api/v1/categories", h.ListCategories)
	r.Get("/api/v1/categories/{id}", h.GetCategory)

	// Admin routes
	r.Route("/api/v1/admin/categories", func(r chi.Router) {
		r.Use(middleware.RequireAuth)
		r.Use(middleware.RequireRole("admin"))

		r.Post("/", h.CreateCategory)
		r.Put("/{id}", h.UpdateCategory)
		r.Delete("/{id}", h.DeleteCategory)
	})
}

// ListCategories handles GET /api/v1/categories
func (h *VideoCategoryHandler) ListCategories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	opts := domain.VideoCategoryListOptions{
		ActiveOnly: true,
		OrderBy:    "display_order",
		OrderDir:   "asc",
	}

	// Parse query parameters
	if activeOnly := r.URL.Query().Get("active_only"); activeOnly != "" {
		opts.ActiveOnly = activeOnly == "true"
	}

	if orderBy := r.URL.Query().Get("order_by"); orderBy != "" {
		opts.OrderBy = orderBy
	}

	if orderDir := r.URL.Query().Get("order_dir"); orderDir != "" {
		opts.OrderDir = orderDir
	}

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if limit, err := strconv.Atoi(limitStr); err == nil {
			opts.Limit = limit
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if offset, err := strconv.Atoi(offsetStr); err == nil {
			opts.Offset = offset
		}
	}

	categories, err := h.categoryUseCase.ListCategories(ctx, opts)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to list categories", err)
		return
	}

	respondWithJSON(w, http.StatusOK, categories)
}

// GetCategory handles GET /api/v1/categories/{id}
func (h *VideoCategoryHandler) GetCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	// Try to parse as UUID first
	if id, err := uuid.Parse(idStr); err == nil {
		category, err := h.categoryUseCase.GetCategoryByID(ctx, id)
		if err != nil {
			respondWithError(w, http.StatusNotFound, "Category not found", err)
			return
		}
		respondWithJSON(w, http.StatusOK, category)
		return
	}

	// Otherwise, treat as slug
	category, err := h.categoryUseCase.GetCategoryBySlug(ctx, idStr)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Category not found", err)
		return
	}

	respondWithJSON(w, http.StatusOK, category)
}

// CreateCategory handles POST /api/v1/admin/categories
func (h *VideoCategoryHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	var req domain.CreateVideoCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	// Auto-generate slug if not provided
	if req.Slug == "" && req.Name != "" {
		req.Slug = usecase.GenerateSlug(req.Name)
	}

	category, err := h.categoryUseCase.CreateCategory(ctx, userID, &req)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to create category", err)
		return
	}

	respondWithJSON(w, http.StatusCreated, category)
}

// UpdateCategory handles PUT /api/v1/admin/categories/{id}
func (h *VideoCategoryHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	// Parse category ID
	categoryIDStr := chi.URLParam(r, "id")
	categoryID, err := uuid.Parse(categoryIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid category ID", err)
		return
	}

	var req domain.UpdateVideoCategoryRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	if err := h.categoryUseCase.UpdateCategory(ctx, userID, categoryID, &req); err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to update category", err)
		return
	}

	// Fetch updated category
	category, err := h.categoryUseCase.GetCategoryByID(ctx, categoryID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to fetch updated category", err)
		return
	}

	respondWithJSON(w, http.StatusOK, category)
}

// DeleteCategory handles DELETE /api/v1/admin/categories/{id}
func (h *VideoCategoryHandler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

	// Parse category ID
	categoryIDStr := chi.URLParam(r, "id")
	categoryID, err := uuid.Parse(categoryIDStr)
	if err != nil {
		respondWithError(w, http.StatusBadRequest, "Invalid category ID", err)
		return
	}

	if err := h.categoryUseCase.DeleteCategory(ctx, userID, categoryID); err != nil {
		respondWithError(w, http.StatusBadRequest, "Failed to delete category", err)
		return
	}

	respondWithJSON(w, http.StatusNoContent, nil)
}

// Helper functions for response
func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if payload != nil {
		json.NewEncoder(w).Encode(payload)
	}
}

func respondWithError(w http.ResponseWriter, code int, message string, err error) {
	errorResponse := map[string]interface{}{
		"error": message,
	}
	if err != nil {
		errorResponse["details"] = err.Error()
	}
	respondWithJSON(w, code, errorResponse)
}
