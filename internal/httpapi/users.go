package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/middleware"
)

func GetCurrentUser(w http.ResponseWriter, r *http.Request) {
	userID := r.Context().Value(middleware.UserIDKey).(string)

	user := domain.User{
		ID:          userID,
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: "Test User",
		Avatar:      "https://example.com/avatar.jpg",
		Bio:         "This is a test user bio",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now().AddDate(0, 0, -30),
		UpdatedAt:   time.Now(),
	}

	WriteJSON(w, http.StatusOK, user)
}

func UpdateCurrentUser(w http.ResponseWriter, r *http.Request) {
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

	user := domain.User{
		ID:          userID,
		Username:    "testuser",
		Email:       "test@example.com",
		DisplayName: req.DisplayName,
		Avatar:      req.Avatar,
		Bio:         req.Bio,
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now().AddDate(0, 0, -30),
		UpdatedAt:   time.Now(),
	}

	WriteJSON(w, http.StatusOK, user)
}

func GetUser(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_USER_ID", "User ID is required"))
		return
	}

	user := domain.User{
		ID:          userID,
		Username:    "publicuser",
		DisplayName: "Public User",
		Avatar:      "https://example.com/avatar.jpg",
		Bio:         "This is a public user profile",
		Role:        domain.RoleUser,
		IsActive:    true,
		CreatedAt:   time.Now().AddDate(0, 0, -60),
		UpdatedAt:   time.Now().AddDate(0, 0, -1),
	}

	WriteJSON(w, http.StatusOK, user)
}

func GetUserVideos(w http.ResponseWriter, r *http.Request) {
	userID := chi.URLParam(r, "id")
	if userID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_USER_ID", "User ID is required"))
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