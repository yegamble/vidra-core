package video

import (
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/storage"
	"athena/internal/usecase"
)

func StreamVideo(w http.ResponseWriter, r *http.Request) {
	videoID, ok := shared.RequireUUIDParam(w, r, "id", "MISSING_VIDEO_ID", "INVALID_VIDEO_ID", "Video ID is required", "Invalid video ID format")
	if !ok {
		return
	}

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	baseDir := filepath.Join("./storage", "streaming-playlists", "hls", videoID)
	quality := r.URL.Query().Get("quality")
	var path string
	if quality == "" {
		path = filepath.Join(baseDir, "master.m3u8")
	} else {
		if !domain.IsValidResolution(quality) {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_QUALITY", "Unsupported quality"))
			return
		}
		if h, ok := domain.HeightForResolution(quality); ok {
			path = filepath.Join(baseDir, fmt.Sprintf("%dp", h), "stream.m3u8")
		}
	}
	if path != "" {
		if data, err := os.ReadFile(path); err == nil {
			_, _ = w.Write(data)
			return
		}
	}

	if quality == "" {
		quality = domain.DefaultResolution
	}
	hlsPlaylist := fmt.Sprintf(`#EXTM3U
#EXT-X-VERSION:3
# QUALITY:%s
#EXT-X-TARGETDURATION:10
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:10.0,
segment-00000.ts
#EXTINF:10.0,
segment-00001.ts
#EXTINF:10.0,
segment-00002.ts
#EXT-X-ENDLIST`, quality)
	_, _ = w.Write([]byte(hlsPlaylist))
}

func GetSupportedQualities(w http.ResponseWriter, r *http.Request) {
	resp := map[string]interface{}{
		"qualities": domain.SupportedResolutions,
		"default":   domain.DefaultResolution,
	}
	shared.WriteJSON(w, http.StatusOK, resp)
}

type streamHandlerContext struct {
	videoID string
	quality string
	video   *domain.Video
}

func validateStreamRequest(w http.ResponseWriter, r *http.Request) (*streamHandlerContext, bool) {
	ctx := &streamHandlerContext{
		videoID: chi.URLParam(r, "id"),
		quality: r.URL.Query().Get("quality"),
	}

	if ctx.videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return nil, false
	}

	if ctx.quality != "" && ctx.quality != "master" && !domain.IsValidResolution(ctx.quality) {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_QUALITY", "Unsupported quality"))
		return nil, false
	}

	return ctx, true
}

func fetchVideo(w http.ResponseWriter, r *http.Request, videoRepo usecase.VideoRepository, videoID string) (*domain.Video, bool) {
	if videoRepo == nil {
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video repository not available"))
		return nil, false
	}

	video, err := videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		var domainErr domain.DomainError
		if de, ok := err.(domain.DomainError); ok {
			domainErr = de
		} else if de, ok := err.(*domain.DomainError); ok {
			domainErr = *de
		} else {
			slog.Error("stream handler: failed to fetch video", "id", videoID, "error", err)
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("DB_ERROR", "Failed to fetch video"))
			return nil, false
		}

		if domainErr.Code == "VIDEO_NOT_FOUND" {
			shared.WriteError(w, http.StatusNotFound, domainErr)
		} else {
			shared.WriteError(w, http.StatusInternalServerError, domainErr)
		}
		return nil, false
	}

	return video, true
}

func tryServeFromOutputPaths(w http.ResponseWriter, r *http.Request, ctx *streamHandlerContext) bool {
	if ctx.video == nil {
		return false
	}

	var outputPath string
	if ctx.quality == "" {
		outputPath = ctx.video.OutputPaths["master"]
	} else {
		if !domain.IsValidResolution(ctx.quality) {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_QUALITY", "Unsupported quality"))
			return true
		}
		outputPath = ctx.video.OutputPaths[ctx.quality]
	}

	if outputPath == "" {
		return false
	}

	if isRemoteURL(outputPath) {
		http.Redirect(w, r, outputPath, http.StatusTemporaryRedirect)
		return true
	}

	data, err := os.ReadFile(outputPath)
	if err != nil {
		return false
	}

	if rel, ok := hlsRelPath(outputPath); ok {
		http.Redirect(w, r, "/api/v1/hls/"+rel, http.StatusTemporaryRedirect)
		return true
	}

	_, _ = w.Write(data)
	return true
}

func tryServeFromLocalDirectory(w http.ResponseWriter, r *http.Request, ctx *streamHandlerContext) bool {
	baseDir := filepath.Join("./storage", "streaming-playlists", "hls", ctx.videoID)
	var path string

	if ctx.quality == "" {
		path = filepath.Join(baseDir, "master.m3u8")
	} else {
		if !domain.IsValidResolution(ctx.quality) {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_QUALITY", "Unsupported quality"))
			return true
		}
		if h, ok := domain.HeightForResolution(ctx.quality); ok {
			path = filepath.Join(baseDir, fmt.Sprintf("%dp", h), "stream.m3u8")
		}
	}

	if path == "" {
		return false
	}

	if _, err := os.Stat(path); err != nil {
		return false
	}

	if rel, ok := hlsRelPath(path); ok {
		http.Redirect(w, r, "/api/v1/hls/"+rel, http.StatusTemporaryRedirect)
		return true
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return false
	}

	_, _ = w.Write(data)
	return true
}

func tryServeFromS3URLs(w http.ResponseWriter, r *http.Request, ctx *streamHandlerContext) bool {
	if ctx.video == nil || len(ctx.video.S3URLs) == 0 {
		return false
	}

	quality := ctx.quality
	if quality == "" {
		quality = "master"
	}

	s3URL, ok := ctx.video.S3URLs[quality]
	if !ok || s3URL == "" {
		return false
	}

	http.Redirect(w, r, s3URL, http.StatusTemporaryRedirect)
	return true
}

func StreamVideoHandler(videoRepo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx, ok := validateStreamRequest(w, r)
		if !ok {
			return
		}

		video, ok := fetchVideo(w, r, videoRepo, ctx.videoID)
		if !ok {
			return
		}
		ctx.video = video

		w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
		w.Header().Set("Access-Control-Allow-Origin", "*")

		if tryServeFromOutputPaths(w, r, ctx) {
			return
		}

		if tryServeFromS3URLs(w, r, ctx) {
			return
		}

		if tryServeFromLocalDirectory(w, r, ctx) {
			return
		}

		if ctx.video != nil && (ctx.video.Status == domain.StatusProcessing || ctx.video.Status == domain.StatusQueued) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_READY", "Video is still processing"))
		} else {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_FILES_NOT_FOUND", "No HLS content available for this video"))
		}
	}
}

func isRemoteURL(s string) bool {
	return len(s) > 7 && (s[:7] == "http://" || (len(s) > 8 && s[:8] == "https://"))
}

func hlsRelPath(localPath string) (string, bool) {
	sp := storage.NewPaths("./storage")
	return sp.HLSRelPath(localPath)
}

func HLSHandler(videoRepo usecase.VideoRepository) http.HandlerFunc {
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

		// Fetch video once — needed for privacy check and S3 fallback.
		var vid *domain.Video
		if v, err := videoRepo.GetByID(r.Context(), videoID); err == nil && v != nil {
			vid = v
			if v.Privacy == domain.PrivacyPrivate {
				userID, _ := r.Context().Value(middleware.UserIDKey).(string)
				if userID == "" || userID != v.UserID {
					shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
					return
				}
			}
		}

		sp := storage.NewPaths("./storage")
		root := sp.HLSRootDir()
		local := filepath.Clean(filepath.Join(root, rel))
		absRoot, _ := filepath.Abs(root)
		absLocal, _ := filepath.Abs(local)
		if relToRoot, err := filepath.Rel(absRoot, absLocal); err != nil || strings.HasPrefix(relToRoot, "..") {
			http.NotFound(w, r)
			return
		}

		// Serve local file when it exists.
		if _, err := os.Stat(local); err == nil {
			if strings.HasSuffix(local, ".m3u8") {
				w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
				w.Header().Set("Cache-Control", "public, max-age=60")
			} else if strings.HasSuffix(local, ".ts") {
				w.Header().Set("Content-Type", "video/MP2T")
				w.Header().Set("Cache-Control", "public, max-age=86400, immutable")
			} else {
				w.Header().Set("Cache-Control", "public, max-age=300")
			}
			http.ServeFile(w, r, local)
			return
		}

		// Local file missing — try S3 redirect using the master URL as base.
		if vid != nil && len(vid.S3URLs) > 0 {
			if masterURL, ok := vid.S3URLs["master"]; ok && masterURL != "" && len(parts) > 1 {
				s3Base := strings.TrimSuffix(masterURL, "master.m3u8")
				s3URL := s3Base + parts[1]
				http.Redirect(w, r, s3URL, http.StatusTemporaryRedirect)
				return
			}
		}

		http.NotFound(w, r)
	}
}
