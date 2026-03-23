package video

import (
	"encoding/json"
	"net/http"
	"strconv"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase"

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

func (h *VideoCategoryHandler) RegisterRoutes(r chi.Router, jwtSecret string) {
	r.Get("/api/v1/categories", h.ListCategories)
	r.Get("/api/v1/categories/{id}", h.GetCategory)

	r.Route("/api/v1/admin/categories", func(r chi.Router) {
		r.Use(middleware.Auth(jwtSecret))
		r.Use(middleware.RequireRole("admin"))

		r.Post("/", h.CreateCategory)
		r.Put("/{id}", h.UpdateCategory)
		r.Delete("/{id}", h.DeleteCategory)
	})
}

func (h *VideoCategoryHandler) ListCategories(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	opts := domain.VideoCategoryListOptions{
		ActiveOnly: true,
		OrderBy:    "display_order",
		OrderDir:   "asc",
	}

	if activeOnly := r.URL.Query().Get("active_only"); activeOnly != "" {
		opts.ActiveOnly = activeOnly == "true"
	}

	if orderBy := r.URL.Query().Get("order_by"); orderBy != "" {
		opts.OrderBy = orderBy
	}

	if orderDir := r.URL.Query().Get("order_dir"); orderDir != "" {
		opts.OrderDir = orderDir
	}

	pageStr := r.URL.Query().Get("page")
	pageSizeStr := r.URL.Query().Get("pageSize")
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")

	page, _ := strconv.Atoi(pageStr)
	pageSize, _ := strconv.Atoi(pageSizeStr)
	limit, _ := strconv.Atoi(limitStr)
	offset, _ := strconv.Atoi(offsetStr)

	if pageSize <= 0 || pageSize > 100 {
		if limit > 0 {
			pageSize = limit
		} else {
			pageSize = 50
		}
	}
	if page <= 0 {
		if offset < 0 {
			offset = 0
		}
		page = (offset / pageSize) + 1
		if page <= 0 {
			page = 1
		}
	}
	opts.Limit = pageSize
	opts.Offset = (page - 1) * pageSize

	categories, err := h.categoryUseCase.ListCategories(ctx, opts)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to list categories", err)
		return
	}

	respondWithJSON(w, http.StatusOK, categories)
}

func (h *VideoCategoryHandler) GetCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	idStr := chi.URLParam(r, "id")

	if id, err := uuid.Parse(idStr); err == nil {
		category, err := h.categoryUseCase.GetCategoryByID(ctx, id)
		if err != nil {
			respondWithError(w, http.StatusNotFound, "Category not found", err)
			return
		}
		respondWithJSON(w, http.StatusOK, category)
		return
	}

	category, err := h.categoryUseCase.GetCategoryBySlug(ctx, idStr)
	if err != nil {
		respondWithError(w, http.StatusNotFound, "Category not found", err)
		return
	}

	respondWithJSON(w, http.StatusOK, category)
}

func (h *VideoCategoryHandler) CreateCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

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

func (h *VideoCategoryHandler) UpdateCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

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

	category, err := h.categoryUseCase.GetCategoryByID(ctx, categoryID)
	if err != nil {
		respondWithError(w, http.StatusInternalServerError, "Failed to fetch updated category", err)
		return
	}

	respondWithJSON(w, http.StatusOK, category)
}

func (h *VideoCategoryHandler) DeleteCategory(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	userID, ok := middleware.GetUserIDFromContext(ctx)
	if !ok {
		respondWithError(w, http.StatusUnauthorized, "Unauthorized", nil)
		return
	}

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

func respondWithJSON(w http.ResponseWriter, code int, payload interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	if payload != nil {
		if err := json.NewEncoder(w).Encode(payload); err != nil {
			_ = err
		}
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
