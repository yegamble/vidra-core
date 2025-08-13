package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/usecase"
)

// UserHandler handles user-related HTTP requests
type UserHandler struct {
	repo usecase.UserRepository
}

// NewUserHandler creates a new UserHandler
func NewUserHandler(repo usecase.UserRepository) *UserHandler {
	return &UserHandler{repo: repo}
}

// GetCurrentUser returns the authenticated user's profile
func (h *UserHandler) GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(middleware.UserIDKey).(string)

	user, err := h.repo.GetByID(r.Context(), userID)
	if err != nil {
		WriteError(w, MapDomainErrorToHTTP(err), err)
		return
	}

	WriteJSON(w, http.StatusOK, user)
}

// UpdateCurrentUser updates the authenticated user's profile
func (h *UserHandler) UpdateCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(middleware.UserIDKey).(string)

	var req struct {
		DisplayName string `json:"display_name"`
		Bio         string `json:"bio"`
		Avatar      string `json:"avatar"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	user, err := h.repo.GetByID(r.Context(), userID)
	if err != nil {
		WriteError(w, MapDomainErrorToHTTP(err), err)
		return
	}

	user.DisplayName = req.DisplayName
	user.Bio = req.Bio
	user.Avatar = req.Avatar
	user.UpdatedAt = time.Now()

	if err := h.repo.Update(r.Context(), user); err != nil {
		WriteError(w, MapDomainErrorToHTTP(err), err)
		return
	}

	WriteJSON(w, http.StatusOK, user)
}

// GetUser returns a public user profile by ID
func (h *UserHandler) GetUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_USER_ID", "User ID is required"))
		return
	}

	user, err := h.repo.GetByID(r.Context(), userID)
	if err != nil {
		WriteError(w, MapDomainErrorToHTTP(err), err)
		return
	}

	WriteJSON(w, http.StatusOK, user)
}

// GetUserVideos returns videos for a specific user. The user existence is verified
// using the repository before returning stubbed video data.
func (h *UserHandler) GetUserVideos(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_USER_ID", "User ID is required"))
		return
	}

	if _, err := h.repo.GetByID(r.Context(), userID); err != nil {
		WriteError(w, MapDomainErrorToHTTP(err), err)
		return
	}

	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if limit == 0 || limit > 100 {
		limit = 20
	}

	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	if offset < 0 {
		offset = 0
	}

	videos := []domain.Video{
		{
			ID:          "1",
			Title:       "User Video 1",
			Description: "This is a video by the user",
			Duration:    300,
			Views:       500,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Now().AddDate(0, 0, -5),
			UserID:      userID,
			Tags:        []string{"user", "content"},
			Category:    "personal",
			Language:    "en",
		},
		{
			ID:          "2",
			Title:       "User Video 2",
			Description: "Another video by the user",
			Duration:    180,
			Views:       250,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Now().AddDate(0, 0, -10),
			UserID:      userID,
			Tags:        []string{"tutorial"},
			Category:    "education",
			Language:    "en",
		},
	}

	meta := &Meta{
		Total:  int64(len(videos)),
		Limit:  limit,
		Offset: offset,
	}

	WriteJSONWithMeta(w, http.StatusOK, videos, meta)
}
