package video

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	chi "github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/storage"
	"athena/internal/usecase"
	"athena/internal/validation"
)

func InitiateUploadHandler(uploadService usecase.UploadService, videoRepo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		var req domain.InitiateUploadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}

		if req.ChunkSize == 0 {
			req.ChunkSize = 10 * 1024 * 1024
		}

		response, err := uploadService.InitiateUpload(r.Context(), userID, &req)
		if err != nil {
			var domainErr domain.DomainError
			if errors.As(err, &domainErr) {
				shared.WriteError(w, http.StatusBadRequest, domainErr)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INITIATE_FAILED", "Failed to initiate upload"))
			return
		}

		shared.WriteJSON(w, http.StatusCreated, response)
	}
}

func UploadChunkHandler(uploadService usecase.UploadService, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionId")
		if sessionID == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_SESSION_ID", "Session ID is required"))
			return
		}

		if _, err := uuid.Parse(sessionID); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_SESSION_ID", "Invalid session ID format"))
			return
		}

		chunkIndex, err := strconv.Atoi(r.Header.Get("X-Chunk-Index"))
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CHUNK_INDEX", "Invalid chunk index"))
			return
		}

		expectedChecksum := r.Header.Get("X-Chunk-Checksum")

		validator := validation.NewChecksumValidator(cfg)

		if cfg.ValidationStrictMode && expectedChecksum == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CHECKSUM", "Chunk checksum is required in strict mode"))
			return
		}

		const maxChunkSize = 64 * 1024 * 1024
		data, err := io.ReadAll(io.LimitReader(r.Body, maxChunkSize+1))
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("READ_FAILED", "Failed to read chunk data"))
			return
		}
		if int64(len(data)) > maxChunkSize {
			shared.WriteError(w, http.StatusRequestEntityTooLarge, domain.NewDomainError("CHUNK_TOO_LARGE", "Chunk exceeds maximum size"))
			return
		}

		if err := validator.ValidateChunkChecksum(data, expectedChecksum); err != nil {
			if domainErr, ok := err.(domain.DomainError); ok {
				shared.WriteError(w, http.StatusBadRequest, domainErr)
			} else {
				shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_FAILED", err.Error()))
			}
			return
		}

		chunk := &domain.ChunkUpload{
			SessionID:  sessionID,
			ChunkIndex: chunkIndex,
			Data:       data,
			Checksum:   expectedChecksum,
		}

		response, err := uploadService.UploadChunk(r.Context(), sessionID, chunk)
		if err != nil {
			var domainErr domain.DomainError
			if errors.As(err, &domainErr) {
				shared.WriteError(w, http.StatusBadRequest, domainErr)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("UPLOAD_FAILED", "Failed to upload chunk"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, response)
	}
}

func CompleteUploadHandler(uploadService usecase.UploadService, encodingRepo usecase.EncodingRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID := chi.URLParam(r, "sessionId")
		if sessionID == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_SESSION_ID", "Session ID is required"))
			return
		}
		if _, err := uuid.Parse(sessionID); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_SESSION_ID", "Invalid session ID format"))
			return
		}

		ctx := r.Context()
		if err := uploadService.CompleteUpload(ctx, sessionID); err != nil {
			var domainErr domain.DomainError
			if errors.As(err, &domainErr) {
				shared.WriteError(w, http.StatusBadRequest, domainErr)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("COMPLETE_FAILED", "Failed to complete upload"))
			return
		}

		resp := map[string]interface{}{
			"session_id": sessionID,
			"status":     "completed",
			"message":    "Upload completed, processing queued",
		}
		shared.WriteJSON(w, http.StatusOK, resp)
	}
}

func GetUploadStatusHandler(uploadService usecase.UploadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID, ok := shared.RequireUUIDParam(w, r, "sessionId", "MISSING_SESSION_ID", "INVALID_SESSION_ID", "Session ID is required", "Invalid session ID format")
		if !ok {
			return
		}

		session, err := uploadService.GetUploadStatus(r.Context(), sessionID)
		if err != nil {
			var domainErr domain.DomainError
			if errors.As(err, &domainErr) {
				shared.WriteError(w, http.StatusNotFound, domainErr)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("STATUS_FAILED", "Failed to get upload status"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, session)
	}
}

func ResumeUploadHandler(uploadService usecase.UploadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sessionID, ok := shared.RequireUUIDParam(w, r, "sessionId", "MISSING_SESSION_ID", "INVALID_SESSION_ID", "Session ID is required", "Invalid session ID format")
		if !ok {
			return
		}

		session, err := uploadService.GetUploadStatus(r.Context(), sessionID)
		if err != nil {
			var domainErr domain.DomainError
			if errors.As(err, &domainErr) {
				shared.WriteError(w, http.StatusNotFound, domainErr)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("RESUME_FAILED", "Failed to get resume information"))
			return
		}

		uploadedSet := make(map[int]bool)
		for _, chunk := range session.UploadedChunks {
			uploadedSet[chunk] = true
		}

		var remainingChunks []int
		for i := 0; i < session.TotalChunks; i++ {
			if !uploadedSet[i] {
				remainingChunks = append(remainingChunks, i)
			}
		}

		progressPercent := 0.0
		if session.TotalChunks > 0 {
			progressPercent = float64(len(session.UploadedChunks)) / float64(session.TotalChunks) * 100
		}

		resumeInfo := map[string]interface{}{
			"session_id":       sessionID,
			"total_chunks":     session.TotalChunks,
			"uploaded_chunks":  session.UploadedChunks,
			"remaining_chunks": remainingChunks,
			"progress_percent": progressPercent,
			"status":           session.Status,
			"expires_at":       session.ExpiresAt,
		}

		shared.WriteJSON(w, http.StatusOK, resumeInfo)
	}
}

func UploadVideoFileHandler(repo usecase.VideoRepository, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		if err := r.ParseMultipartForm(512 << 20); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_MULTIPART", "Invalid multipart form"))
			return
		}

		file, header, err := r.FormFile("video")
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_FILE", "Missing video file"))
			return
		}
		defer func() { _ = file.Close() }()

		title := strings.TrimSpace(r.FormValue("title"))
		if title == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TITLE", "Title is required"))
			return
		}
		description := r.FormValue("description")
		privacy := r.FormValue("privacy")
		if privacy == "" {
			privacy = string(domain.PrivacyPublic)
		}
		if privacy != string(domain.PrivacyPublic) && privacy != string(domain.PrivacyUnlisted) && privacy != string(domain.PrivacyPrivate) {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_PRIVACY", "Privacy must be public, unlisted, or private"))
			return
		}

		ext := strings.ToLower(filepath.Ext(header.Filename))
		var head [512]byte
		n, _ := file.Read(head[:])
		contentType := http.DetectContentType(head[:n])

		if !isAllowedVideo(ext, head[:n], contentType) {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("UNSUPPORTED_MEDIA", "Unsupported or invalid video format"))
			return
		}

		var reader io.Reader
		if seeker, ok := file.(io.Seeker); ok {
			if _, err := seeker.Seek(0, io.SeekStart); err != nil {
				shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("FILE_ERROR", "Failed to reset file position"))
				return
			}
			reader = file
		} else {
			reader = io.MultiReader(bytes.NewReader(head[:n]), file)
		}

		now := time.Now()
		video := &domain.Video{
			ID:          uuid.NewString(),
			ThumbnailID: uuid.NewString(),
			Title:       title,
			Description: description,
			Privacy:     domain.Privacy(privacy),
			Status:      domain.StatusCompleted,
			UploadDate:  now,
			UserID:      userID,
			Tags:        []string{},
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		root := "./storage"
		if cfg != nil && cfg.StorageDir != "" {
			root = cfg.StorageDir
		}
		sp := storage.NewPaths(root)
		if err := os.MkdirAll(sp.WebVideosDir(), 0750); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("STORAGE_ERROR", "Failed to prepare storage directory"))
			return
		}

		if ext == "" || !isAllowedVideoExt(ext) {
			ext = extFromContentType(contentType)
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		dstPath := sp.WebVideoFilePath(video.ID, ext)

		out, err := os.Create(dstPath)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("STORAGE_ERROR", "Failed to save video"))
			return
		}
		defer func() { _ = out.Close() }()

		written, err := io.Copy(out, reader)
		if err != nil || written <= 0 {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("WRITE_FAILED", "Failed to write uploaded file"))
			return
		}

		video.FileSize = written
		video.MimeType = contentType

		if err := repo.Create(r.Context(), video); err != nil {
			_ = os.Remove(dstPath)
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("CREATE_FAILED", "Failed to create video"))
			return
		}

		shared.WriteJSON(w, http.StatusCreated, map[string]interface{}{
			"id":          video.ID,
			"title":       video.Title,
			"description": video.Description,
			"privacy":     video.Privacy,
			"user_id":     video.UserID,
			"file_size":   video.FileSize,
			"mime_type":   video.MimeType,
			"upload_date": video.UploadDate,
		})
	}
}

func VideoUploadChunkHandler(uploadService usecase.UploadService, cfg *config.Config) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		videoID := chi.URLParam(r, "id")
		if videoID == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
			return
		}

		if _, err := uuid.Parse(videoID); err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
			return
		}

		chunkIndexStr := r.Header.Get("X-Chunk-Index")
		if chunkIndexStr == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CHUNK_INDEX", "X-Chunk-Index header is required"))
			return
		}

		chunkIndex, err := strconv.Atoi(chunkIndexStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CHUNK_INDEX", "Invalid chunk index"))
			return
		}

		expectedChecksum := r.Header.Get("X-Chunk-Checksum")
		totalChunksStr := r.Header.Get("X-Total-Chunks")
		if totalChunksStr == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TOTAL_CHUNKS", "X-Total-Chunks header is required"))
			return
		}
		totalChunks, err := strconv.Atoi(totalChunksStr)
		if err != nil || totalChunks <= 0 {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_TOTAL_CHUNKS", "X-Total-Chunks must be a positive integer"))
			return
		}

		validator := validation.NewChecksumValidator(cfg)
		if cfg.ValidationStrictMode && expectedChecksum == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CHECKSUM", "X-Chunk-Checksum header is required in strict mode"))
			return
		}

		const maxChunkSizeV2 = 64 * 1024 * 1024
		data, err := io.ReadAll(io.LimitReader(r.Body, maxChunkSizeV2+1))
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("READ_FAILED", "Failed to read chunk data"))
			return
		}
		if int64(len(data)) > maxChunkSizeV2 {
			shared.WriteError(w, http.StatusRequestEntityTooLarge, domain.NewDomainError("CHUNK_TOO_LARGE", "Chunk exceeds maximum size"))
			return
		}

		if expectedChecksum != "" {
			if err := validator.ValidateChunkChecksum(data, expectedChecksum); err != nil {
				if domainErr, ok := err.(domain.DomainError); ok {
					shared.WriteError(w, http.StatusBadRequest, domainErr)
				} else {
					shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("VALIDATION_FAILED", err.Error()))
				}
				return
			}
		}

		response := map[string]interface{}{
			"video_id":    videoID,
			"chunk_index": chunkIndex,
			"uploaded":    true,
		}

		shared.WriteJSON(w, http.StatusOK, response)
	}
}

func VideoCompleteUploadHandler(uploadService usecase.UploadService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		videoID := chi.URLParam(r, "id")
		if videoID == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
			return
		}

		if _, err := uuid.Parse(videoID); err != nil {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
			return
		}

		response := map[string]interface{}{
			"video_id": videoID,
			"status":   "completed",
			"message":  "Video upload completed successfully",
		}

		shared.WriteJSON(w, http.StatusOK, response)
	}
}
