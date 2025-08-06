package api

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"gotube/internal/model"
	"gotube/internal/usecase"
)

// VideoHandler manages endpoints related to videos (upload, list, get).
type VideoHandler struct {
	Usecase *usecase.VideoUsecase
}

// RegisterRoutes registers video-related routes under /videos. Some routes
// require authentication (enforced via middleware at router level).
func (h *VideoHandler) RegisterRoutes(r chi.Router) {
	r.Post("/", h.Upload)
	r.Get("/", h.List)
	r.Get("/{id}", h.Get)
}

// Upload handles uploading a new video. It expects a multipart form with
// fields: "title" (string), "description" (string) and file "file".
// The authenticated user ID is taken from context. On success it
// returns the created video object.
func (h *VideoHandler) Upload(w http.ResponseWriter, r *http.Request) {
	// Ensure user is authenticated
	uid := UserID(r.Context())
	if uid == 0 {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return
	}
	// Parse multipart form (limit to 1GB)
	if err := r.ParseMultipartForm(1 << 30); err != nil {
		http.Error(w, "could not parse form", http.StatusBadRequest)
		return
	}
	title := r.FormValue("title")
	description := r.FormValue("description")
	file, header, err := r.FormFile("file")
	if err != nil {
		http.Error(w, "missing file", http.StatusBadRequest)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(file)
	if err != nil {
		http.Error(w, "read file error", http.StatusInternalServerError)
		return
	}
	video, err := h.Usecase.Upload(r.Context(), uid, title, description, header.Filename, data)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(video)
}

// List returns a paginated list of videos. Query parameters `limit` and
// `offset` control pagination and default to 20 and 0 respectively.
func (h *VideoHandler) List(w http.ResponseWriter, r *http.Request) {
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	videos, err := h.Usecase.List(r.Context(), limit, offset)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(videos)
}

// Get returns a video by ID with renditions. The ID is taken from the
// URL path. This endpoint is public; one need not be authenticated to
// fetch video details.
func (h *VideoHandler) Get(w http.ResponseWriter, r *http.Request) {
	idStr := chi.URLParam(r, "id")
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil {
		http.Error(w, "invalid id", http.StatusBadRequest)
		return
	}
	video, renditions, err := h.Usecase.Get(r.Context(), id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	resp := struct {
		*model.Video
		Renditions []*model.VideoRendition `json:"renditions"`
	}{video, renditions}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)
}
