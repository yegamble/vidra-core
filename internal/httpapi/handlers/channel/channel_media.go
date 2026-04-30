package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/storage"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

var allowedImageMIMETypes = map[string]bool{
	"image/png":  true,
	"image/jpeg": true,
	"image/webp": true,
	"image/gif":  true,
}

// validateImageMIME reads the first 512 bytes of r and checks the detected
// content type against the allowed image MIME types. Returns "" on success or
// the detected type string if disallowed.
func validateImageMIME(r io.Reader) (string, bool) {
	buf := make([]byte, 512)
	n, _ := r.Read(buf)
	detected := http.DetectContentType(buf[:n])
	return detected, allowedImageMIMETypes[detected]
}

// ChannelMediaRepository defines data access needed for channel media operations.
type ChannelMediaRepository interface {
	GetOwnerID(ctx context.Context, channelID uuid.UUID) (string, error)
	SetAvatar(ctx context.Context, channelID uuid.UUID, filename, ipfsCID string) error
	ClearAvatar(ctx context.Context, channelID uuid.UUID) error
	SetBanner(ctx context.Context, channelID uuid.UUID, filename, ipfsCID string) error
	ClearBanner(ctx context.Context, channelID uuid.UUID) error
}

// ChannelMediaHandlers handles channel avatar/banner HTTP requests.
type ChannelMediaHandlers struct {
	repo  ChannelMediaRepository
	paths storage.Paths
}

// NewChannelMediaHandlers creates handlers for channel media endpoints.
func NewChannelMediaHandlers(repo ChannelMediaRepository, cfg *config.Config) *ChannelMediaHandlers {
	return &ChannelMediaHandlers{repo: repo, paths: storage.NewPaths(cfg.StorageDir)}
}

// extFromMIME returns the canonical file extension for an allowed image MIME type.
func extFromMIME(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/jpeg":
		return ".jpg"
	case "image/webp":
		return ".webp"
	case "image/gif":
		return ".gif"
	default:
		return ""
	}
}

// randomFileID returns a 16-byte hex string suitable for filenames.
func randomFileID() string {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		// Fallback to UUID-style if rand fails (extremely rare on crypto/rand).
		return uuid.New().String()
	}
	return hex.EncodeToString(b)
}

// uploadMediaConfig holds configuration for the shared upload handler.
type uploadMediaConfig struct {
	formField   string
	setFn       func(ctx context.Context, channelID uuid.UUID, filename, cid string) error
	responseKey string
	staticDir   string
	storageDir  string
	errMessage  string
}

// uploadMedia is the shared implementation for UploadAvatar and UploadBanner.
// Writes the multipart bytes to the configured storage directory and stores the
// generated filename in the channel row. The frontend receives the resolved
// `staticDir + filename` path which the matching ServeAvatar/ServeBanner
// handlers serve back from disk.
func (h *ChannelMediaHandlers) uploadMedia(w http.ResponseWriter, r *http.Request, cfg uploadMediaConfig) {
	channelID, userID, ok := h.extractIDs(w, r)
	if !ok {
		return
	}

	ownerID, err := h.repo.GetOwnerID(r.Context(), channelID)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	if ownerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	if err := r.ParseMultipartForm(5 << 20); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Failed to parse upload"))
		return
	}

	file, _, err := r.FormFile(cfg.formField)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Missing "+cfg.formField+" field"))
		return
	}
	defer file.Close()

	detected, allowed := validateImageMIME(file)
	if !allowed {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_FILE_TYPE", "File type not allowed: "+detected))
		return
	}

	// Rewind after MIME sniff so we can stream the full file to disk.
	if seeker, ok := file.(io.Seeker); ok {
		if _, err := seeker.Seek(0, io.SeekStart); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", cfg.errMessage))
			return
		}
	}

	if err := os.MkdirAll(cfg.storageDir, 0o755); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", cfg.errMessage))
		return
	}

	storedFilename := randomFileID() + extFromMIME(detected)
	dest := filepath.Join(cfg.storageDir, storedFilename)
	out, err := os.Create(dest)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", cfg.errMessage))
		return
	}
	if _, err := io.Copy(out, file); err != nil {
		out.Close()
		_ = os.Remove(dest)
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", cfg.errMessage))
		return
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(dest)
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", cfg.errMessage))
		return
	}

	if err := cfg.setFn(r.Context(), channelID, storedFilename, ""); err != nil {
		_ = os.Remove(dest)
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", cfg.errMessage))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		cfg.responseKey: []map[string]interface{}{
			{"path": cfg.staticDir + storedFilename},
		},
	})
}

// ServeAvatar serves a stored channel avatar from disk.
// GET /lazy-static/avatars/{filename}
func (h *ChannelMediaHandlers) ServeAvatar(w http.ResponseWriter, r *http.Request) {
	h.serveStoredFile(w, r, h.paths.AvatarsDir())
}

// ServeBanner serves a stored channel banner from disk.
// GET /lazy-static/banners/{filename}
func (h *ChannelMediaHandlers) ServeBanner(w http.ResponseWriter, r *http.Request) {
	h.serveStoredFile(w, r, h.paths.BannersDir())
}

func (h *ChannelMediaHandlers) serveStoredFile(w http.ResponseWriter, r *http.Request, dir string) {
	filename := chi.URLParam(r, "filename")
	if filename == "" || strings.ContainsAny(filename, `/\`) || filename == "." || filename == ".." {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_FILENAME", "Invalid filename"))
		return
	}
	http.ServeFile(w, r, filepath.Join(dir, filename))
}

// UploadAvatar handles POST /api/v1/channels/{id}/avatar.
func (h *ChannelMediaHandlers) UploadAvatar(w http.ResponseWriter, r *http.Request) {
	h.uploadMedia(w, r, uploadMediaConfig{
		formField:   "avatarfile",
		setFn:       h.repo.SetAvatar,
		responseKey: "avatars",
		staticDir:   "/lazy-static/avatars/",
		storageDir:  h.paths.AvatarsDir(),
		errMessage:  "Failed to set avatar",
	})
}

// UploadBanner handles POST /api/v1/channels/{id}/banner.
func (h *ChannelMediaHandlers) UploadBanner(w http.ResponseWriter, r *http.Request) {
	h.uploadMedia(w, r, uploadMediaConfig{
		formField:   "bannerfile",
		setFn:       h.repo.SetBanner,
		responseKey: "banners",
		staticDir:   "/lazy-static/banners/",
		storageDir:  h.paths.BannersDir(),
		errMessage:  "Failed to set banner",
	})
}

// DeleteAvatar handles DELETE /api/v1/channels/{id}/avatar.
func (h *ChannelMediaHandlers) DeleteAvatar(w http.ResponseWriter, r *http.Request) {
	channelID, userID, ok := h.extractIDs(w, r)
	if !ok {
		return
	}

	ownerID, err := h.repo.GetOwnerID(r.Context(), channelID)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	if ownerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	if err := h.repo.ClearAvatar(r.Context(), channelID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to clear avatar"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteBanner handles DELETE /api/v1/channels/{id}/banner.
func (h *ChannelMediaHandlers) DeleteBanner(w http.ResponseWriter, r *http.Request) {
	channelID, userID, ok := h.extractIDs(w, r)
	if !ok {
		return
	}

	ownerID, err := h.repo.GetOwnerID(r.Context(), channelID)
	if err != nil {
		shared.WriteError(w, shared.MapDomainErrorToHTTP(err), err)
		return
	}

	if ownerID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
		return
	}

	if err := h.repo.ClearBanner(r.Context(), channelID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to clear banner"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// extractIDs parses channelID from the URL and userID from context.
// Writes an error response and returns false on failure.
func (h *ChannelMediaHandlers) extractIDs(w http.ResponseWriter, r *http.Request) (uuid.UUID, string, bool) {
	idStr := chi.URLParam(r, "id")
	channelID, err := uuid.Parse(idStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("BAD_REQUEST", "Invalid channel ID"))
		return uuid.Nil, "", false
	}

	userID, ok := r.Context().Value(middleware.UserIDKey).(string)
	if !ok || userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
		return uuid.Nil, "", false
	}

	return channelID, userID, true
}
