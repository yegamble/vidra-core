package video

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"golang.org/x/crypto/bcrypt"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

// PasswordVideoRepository is the subset of video repo needed for ownership checks.
type PasswordVideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
}

// PasswordRepository defines data operations for video passwords.
type PasswordRepository interface {
	ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoPassword, error)
	Create(ctx context.Context, videoID string, passwordHash string) (*domain.VideoPassword, error)
	ReplaceAll(ctx context.Context, videoID string, passwordHashes []string) ([]domain.VideoPassword, error)
	Delete(ctx context.Context, passwordID int64) error
}

// PasswordHandlers handles video password endpoints.
type PasswordHandlers struct {
	passwordRepo PasswordRepository
	videoRepo    PasswordVideoRepository
}

// NewPasswordHandlers creates a new PasswordHandlers.
func NewPasswordHandlers(passwordRepo PasswordRepository, videoRepo PasswordVideoRepository) *PasswordHandlers {
	return &PasswordHandlers{passwordRepo: passwordRepo, videoRepo: videoRepo}
}

// ListPasswords handles GET /api/v1/videos/{id}/passwords.
func (h *PasswordHandlers) ListPasswords(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	// Verify ownership
	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can view passwords"))
		return
	}

	passwords, err := h.passwordRepo.ListByVideoID(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list passwords"))
		return
	}
	if passwords == nil {
		passwords = []domain.VideoPassword{}
	}

	shared.WriteJSON(w, http.StatusOK, passwords)
}

// ReplacePasswords handles PUT /api/v1/videos/{id}/passwords.
func (h *PasswordHandlers) ReplacePasswords(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can manage passwords"))
		return
	}

	var req struct {
		Passwords []string `json:"passwords"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	hashes := make([]string, 0, len(req.Passwords))
	for _, pw := range req.Passwords {
		if pw == "" {
			continue
		}
		hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("HASH_ERROR", "Failed to hash password"))
			return
		}
		hashes = append(hashes, string(hash))
	}

	passwords, err := h.passwordRepo.ReplaceAll(r.Context(), videoID, hashes)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to replace passwords"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, passwords)
}

// AddPassword handles POST /api/v1/videos/{id}/passwords.
func (h *PasswordHandlers) AddPassword(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can add passwords"))
		return
	}

	var req struct {
		Password string `json:"password"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}
	if req.Password == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_PASSWORD", "Password is required"))
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("HASH_ERROR", "Failed to hash password"))
		return
	}

	pw, err := h.passwordRepo.Create(r.Context(), videoID, string(hash))
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to add password"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, pw)
}

// DeletePassword handles DELETE /api/v1/videos/{id}/passwords/{passwordId}.
func (h *PasswordHandlers) DeletePassword(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	passwordIDStr := chi.URLParam(r, "passwordId")
	passwordID, err := strconv.ParseInt(passwordIDStr, 10, 64)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_PASSWORD_ID", "Invalid password ID"))
		return
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can delete passwords"))
		return
	}

	if err := h.passwordRepo.Delete(r.Context(), passwordID); err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
