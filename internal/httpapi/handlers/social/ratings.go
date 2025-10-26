package social

import (
	"athena/internal/domain"
	"athena/internal/middleware"
	ucrt "athena/internal/usecase/rating"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type RatingHandlers struct {
	ratingService *ucrt.Service
}

func NewRatingHandlers(ratingService *ucrt.Service) *RatingHandlers {
	return &RatingHandlers{
		ratingService: ratingService,
	}
}

// SetRating handles PUT /api/v1/videos/{id}/rating
func (h *RatingHandlers) SetRating(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

	var req struct {
		Rating int `json:"rating"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	rating := domain.RatingValue(req.Rating)
	if !rating.IsValid() {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid rating value, must be -1, 0, or 1"))
		return
	}

	if err := h.ratingService.SetRating(r.Context(), userID, videoID, rating); err != nil {
		if err == domain.ErrNotFound {
			WriteError(w, http.StatusNotFound, err)
			return
		}
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// GetRating handles GET /api/v1/videos/{id}/rating
func (h *RatingHandlers) GetRating(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

	// Get current user if authenticated
	var userID *uuid.UUID
	if uid, ok := middleware.GetUserIDFromContext(r.Context()); ok {
		userID = &uid
	}

	stats, err := h.ratingService.GetVideoRatingStats(r.Context(), videoID, userID)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, stats)
}

// RemoveRating handles DELETE /api/v1/videos/{id}/rating
func (h *RatingHandlers) RemoveRating(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

	if err := h.ratingService.RemoveRating(r.Context(), userID, videoID); err != nil {
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]string{
		"status": "ok",
	})
}

// GetUserRatings handles GET /api/v1/users/me/ratings
func (h *RatingHandlers) GetUserRatings(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	limit := 20
	offset := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if val, err := json.Number(l).Int64(); err == nil {
			limit = int(val)
		}
	}
	if o := r.URL.Query().Get("offset"); o != "" {
		if val, err := json.Number(o).Int64(); err == nil {
			offset = int(val)
		}
	}

	ratings, err := h.ratingService.GetUserRatings(r.Context(), userID, limit, offset)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err)
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"ratings": ratings,
		"limit":   limit,
		"offset":  offset,
	})
}
