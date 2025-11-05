package video

import (
	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/storage"
	"athena/internal/usecase"
	"fmt"
	"net/http"
	"path/filepath"
	"strings"
)

// HLSHandlerWithS3 serves HLS playlists and segments with S3 support.
// When S3 is enabled and the video has been migrated to S3, it redirects to S3 URLs.
// Otherwise, it falls back to local file serving.
func HLSHandlerWithS3(videoRepo usecase.VideoRepository, cfg *config.Config, s3Backend storage.StorageBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Path expected: /api/v1/hls/{videoId}/...
		const prefix = "/api/v1/hls/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}

		rel := strings.TrimPrefix(r.URL.Path, prefix)
		parts := strings.SplitN(rel, "/", 2)
		if len(parts) < 1 || parts[0] == "" {
			http.NotFound(w, r)
			return
		}

		videoID := parts[0]
		requestPath := ""
		if len(parts) > 1 {
			requestPath = parts[1]
		}

		// Get video from database
		video, err := videoRepo.GetByID(r.Context(), videoID)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		// Check privacy
		if video.Privacy == domain.PrivacyPrivate {
			userID, _ := r.Context().Value(middleware.UserIDKey).(string)
			if userID == "" || userID != video.UserID {
				shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
				return
			}
		}

		// If S3 is enabled and video is migrated to S3, redirect to S3
		if cfg.EnableS3 && video.StorageTier == "cold" && video.S3MigratedAt != nil {
			s3URL := constructS3URL(videoID, requestPath, s3Backend)

			// For private videos, generate signed URL
			if video.Privacy == domain.PrivacyPrivate && s3Backend != nil {
				// Generate signed URL with 1 hour expiration
				signedURL, err := s3Backend.GetSignedURL(r.Context(), constructS3Key(videoID, requestPath), 3600*1000000000) // 1 hour in nanoseconds
				if err == nil {
					s3URL = signedURL
				}
			}

			// Set appropriate headers before redirect
			if strings.HasSuffix(requestPath, ".m3u8") {
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				w.Header().Set("Cache-Control", "public, max-age=60")
			} else if strings.HasSuffix(requestPath, ".ts") {
				w.Header().Set("Content-Type", "video/MP2T")
				w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
			}

			w.Header().Set("Access-Control-Allow-Origin", "*")
			http.Redirect(w, r, s3URL, http.StatusFound)
			return
		}

		// Fallback to local file serving
		serveLocalHLSFile(w, r, videoID, requestPath)
	}
}

// constructS3URL constructs the S3 URL for an HLS file
func constructS3URL(videoID, requestPath string, s3Backend storage.StorageBackend) string {
	s3Key := constructS3Key(videoID, requestPath)
	if s3Backend != nil {
		return s3Backend.GetURL(s3Key)
	}
	return ""
}

// constructS3Key constructs the S3 key for an HLS file
func constructS3Key(videoID, requestPath string) string {
	return fmt.Sprintf("videos/%s/hls/%s", videoID, requestPath)
}

// serveLocalHLSFile serves an HLS file from local storage
func serveLocalHLSFile(w http.ResponseWriter, r *http.Request, videoID, requestPath string) {
	sp := storage.NewPaths("./storage")
	root := sp.HLSRootDir()
	rel := filepath.Join(videoID, requestPath)
	local := filepath.Clean(filepath.Join(root, rel))

	// Prevent path traversal
	absRoot, _ := filepath.Abs(root)
	absLocal, _ := filepath.Abs(local)
	if relToRoot, err := filepath.Rel(absRoot, absLocal); err != nil || strings.HasPrefix(relToRoot, "..") {
		http.NotFound(w, r)
		return
	}

	// Set headers based on file type
	if strings.HasSuffix(local, ".m3u8") {
		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Cache-Control", "public, max-age=60")
	} else if strings.HasSuffix(local, ".ts") {
		w.Header().Set("Content-Type", "video/MP2T")
		w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
	} else {
		w.Header().Set("Cache-Control", "public, max-age=300")
	}

	w.Header().Set("Access-Control-Allow-Origin", "*")
	http.ServeFile(w, r, local)
}
