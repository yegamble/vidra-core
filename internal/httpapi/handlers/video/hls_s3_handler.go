package video

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"path/filepath"
	"strings"
	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/storage"
	"vidra-core/internal/usecase"
)

func HLSHandlerWithS3(videoRepo usecase.VideoRepository, cfg *config.Config, s3Backend storage.StorageBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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

		video, err := videoRepo.GetByID(r.Context(), videoID)
		if err != nil {
			http.NotFound(w, r)
			return
		}

		if video.Privacy == domain.PrivacyPrivate {
			userID, _ := r.Context().Value(middleware.UserIDKey).(string)
			if userID == "" || userID != video.UserID {
				shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
				return
			}
		}

		if cfg.EnableS3 && video.StorageTier == "cold" && video.S3MigratedAt != nil {
			if video.Privacy == domain.PrivacyPrivate && cfg.ObjectStorageConfig.ProxifyPrivateFiles && s3Backend != nil {
				s3Key := constructS3Key(videoID, requestPath)
				reader, err := s3Backend.Download(r.Context(), s3Key)
				if err != nil {
					http.Error(w, "Failed to fetch file", http.StatusInternalServerError)
					return
				}
				defer reader.Close()

				if strings.HasSuffix(requestPath, ".m3u8") {
					w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
					w.Header().Set("Cache-Control", "private, no-cache")
				} else if strings.HasSuffix(requestPath, ".ts") {
					w.Header().Set("Content-Type", "video/MP2T")
					w.Header().Set("Cache-Control", "private, max-age=3600")
				} else {
					w.Header().Set("Cache-Control", "private, no-cache")
				}

				w.Header().Set("Access-Control-Allow-Origin", "*")
				if _, err := io.Copy(w, reader); err != nil {
					log.Printf("error streaming S3 object for video %s path %s: %v", videoID, requestPath, err)
				}
				return
			}

			s3URL := constructS3URL(videoID, requestPath, s3Backend)

			if video.Privacy == domain.PrivacyPrivate && s3Backend != nil {
				signedURL, err := s3Backend.GetSignedURL(r.Context(), constructS3Key(videoID, requestPath), 3600*1000000000)
				if err == nil {
					s3URL = signedURL
				}
			}

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

		serveLocalHLSFile(w, r, videoID, requestPath)
	}
}

func constructS3URL(videoID, requestPath string, s3Backend storage.StorageBackend) string {
	s3Key := constructS3Key(videoID, requestPath)
	if s3Backend != nil {
		return s3Backend.GetURL(s3Key)
	}
	return ""
}

func constructS3Key(videoID, requestPath string) string {
	return fmt.Sprintf("videos/%s/hls/%s", videoID, requestPath)
}

func serveLocalHLSFile(w http.ResponseWriter, r *http.Request, videoID, requestPath string) {
	sp := storage.NewPaths("./storage")
	root := sp.HLSRootDir()
	rel := filepath.Join(videoID, requestPath)
	local := filepath.Clean(filepath.Join(root, rel))

	absRoot, _ := filepath.Abs(root)
	absLocal, _ := filepath.Abs(local)
	if relToRoot, err := filepath.Rel(absRoot, absLocal); err != nil || strings.HasPrefix(relToRoot, "..") {
		http.NotFound(w, r)
		return
	}

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
