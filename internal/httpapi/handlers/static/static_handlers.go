package static

import (
	"fmt"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/storage"
	"athena/internal/usecase"
)

// Handlers serves static video files and HLS streaming playlists.
// It reads from the local storage directory using storage.Paths.
type Handlers struct {
	paths     storage.Paths
	cfg       *config.Config
	videoRepo usecase.VideoRepository
}

// NewHandlers creates static file handlers rooted at the configured storage directory.
func NewHandlers(cfg *config.Config, videoRepo usecase.VideoRepository) *Handlers {
	return &Handlers{
		paths:     storage.NewPaths(cfg.StorageDir),
		cfg:       cfg,
		videoRepo: videoRepo,
	}
}

// ServeWebVideo serves a public web-video file.
// GET /static/web-videos/{filename}
func (h *Handlers) ServeWebVideo(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")
	if filename == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILENAME", "Filename is required"))
		return
	}

	if err := validateFilename(filename); err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}

	filePath := filepath.Join(h.paths.WebVideosDir(), filename)
	serveFile(w, r, filePath)
}

// ServePrivateWebVideo serves a private web-video file (auth required).
// GET /static/web-videos/private/{filename}
func (h *Handlers) ServePrivateWebVideo(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")
	if filename == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILENAME", "Filename is required"))
		return
	}

	if err := validateFilename(filename); err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}

	filePath := filepath.Join(h.paths.WebVideosDir(), "private", filename)
	serveFile(w, r, filePath)
}

// ServeHLSFile serves an HLS streaming playlist or segment file.
// GET /static/streaming-playlists/hls/{filename}
func (h *Handlers) ServeHLSFile(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")
	if filename == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILENAME", "Filename is required"))
		return
	}

	if err := validateFilename(filename); err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}

	filePath := filepath.Join(h.paths.HLSRootDir(), filename)
	serveFile(w, r, filePath)
}

// ServePrivateHLSFile serves a private HLS streaming file (auth required).
// GET /static/streaming-playlists/hls/private/{filename}
func (h *Handlers) ServePrivateHLSFile(w http.ResponseWriter, r *http.Request) {
	filename := chi.URLParam(r, "filename")
	if filename == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILENAME", "Filename is required"))
		return
	}

	if err := validateFilename(filename); err != nil {
		shared.WriteError(w, http.StatusBadRequest, err)
		return
	}

	filePath := filepath.Join(h.paths.HLSRootDir(), "private", filename)
	serveFile(w, r, filePath)
}

// DownloadVideo generates a download response for a video file.
// GET /download/videos/generate/{videoId}
func (h *Handlers) DownloadVideo(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoId")
	if videoIDStr == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	if _, err := uuid.Parse(videoIDStr); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_VIDEO_ID", "Invalid video ID format"))
		return
	}

	video, err := h.videoRepo.GetByID(r.Context(), videoIDStr)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	// Check privacy for non-public videos
	if video.Privacy != domain.PrivacyPublic {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}
		if userID != video.UserID {
			shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
			return
		}
	}

	// Try common video extensions
	extensions := []string{".mp4", ".webm", ".mkv", ".avi", ".mov"}
	for _, ext := range extensions {
		filePath := h.paths.WebVideoFilePath(videoIDStr, ext)
		if _, statErr := os.Stat(filePath); statErr == nil {
			w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q", video.Title+ext))
			w.Header().Set("Content-Type", contentTypeForExt(ext))
			http.ServeFile(w, r, filePath) //nolint:gosec // path constructed from validated videoID + known ext
			return
		}
	}

	shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("FILE_NOT_FOUND", "Video file not found"))
}

// validateFilename rejects filenames containing path traversal or other unsafe characters.
func validateFilename(name string) error {
	if name == "" {
		return domain.NewDomainError("INVALID_FILENAME", "Filename must not be empty")
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) {
		return domain.NewDomainError("INVALID_FILENAME", "Filename contains invalid characters")
	}
	if strings.HasPrefix(name, ".") {
		return domain.NewDomainError("INVALID_FILENAME", "Filename must not start with a dot")
	}
	return nil
}

// serveFile serves a file from disk if it exists, returning 404 otherwise.
func serveFile(w http.ResponseWriter, r *http.Request, filePath string) {
	info, err := os.Stat(filePath)
	if err != nil || info.IsDir() {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("FILE_NOT_FOUND", "Requested file not found"))
		return
	}

	ext := filepath.Ext(filePath)
	ct := contentTypeForExt(ext)
	if ct != "" {
		w.Header().Set("Content-Type", ct)
	}

	w.Header().Set("Cache-Control", "public, max-age=604800, immutable")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	http.ServeFile(w, r, filePath) //nolint:gosec // path validated by caller
}

// contentTypeForExt returns the MIME type for common video/streaming extensions.
func contentTypeForExt(ext string) string {
	switch strings.ToLower(ext) {
	case ".mp4":
		return "video/mp4"
	case ".webm":
		return "video/webm"
	case ".mkv":
		return "video/x-matroska"
	case ".avi":
		return "video/x-msvideo"
	case ".mov":
		return "video/quicktime"
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".ts":
		return "video/mp2t"
	case ".m4s":
		return "video/iso.segment"
	default:
		ct := mime.TypeByExtension(ext)
		if ct != "" {
			return ct
		}
		return "application/octet-stream"
	}
}
