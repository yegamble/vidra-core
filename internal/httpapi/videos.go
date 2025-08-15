package httpapi

import (
    "encoding/json"
    "net/http"
    "strconv"
    "time"

    "github.com/go-chi/chi/v5"
    "github.com/google/uuid"

    "athena/internal/domain"
    "athena/internal/middleware"
    "athena/internal/usecase"
)

func ListVideosHandler(repo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
        if limit == 0 || limit > 100 {
            limit = 20
        }

        offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
        if offset < 0 {
            offset = 0
        }

        sort := r.URL.Query().Get("sort")
        if sort == "" {
            sort = "upload_date"
        }

        order := r.URL.Query().Get("order")
        if order != "asc" && order != "desc" {
            order = "desc"
        }

        req := &domain.VideoSearchRequest{
            Category: r.URL.Query().Get("category"),
            Language: r.URL.Query().Get("language"),
            Sort:     sort,
            Order:    order,
            Limit:    limit,
            Offset:   offset,
        }

        videos, total, err := repo.List(r.Context(), req)
        if err != nil {
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list videos"))
            return
        }

        meta := &Meta{
            Total:  total,
            Limit:  limit,
            Offset: offset,
        }

        WriteJSONWithMeta(w, http.StatusOK, videos, meta)
    }
}

func SearchVideosHandler(repo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        query := r.URL.Query().Get("q")
        if query == "" {
            WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_QUERY", "Search query is required"))
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

        tags := r.URL.Query()["tags"]
        
        req := &domain.VideoSearchRequest{
            Query:    query,
            Tags:     tags,
            Category: r.URL.Query().Get("category"),
            Language: r.URL.Query().Get("language"),
            Sort:     r.URL.Query().Get("sort"),
            Order:    r.URL.Query().Get("order"),
            Limit:    limit,
            Offset:   offset,
        }

        videos, total, err := repo.Search(r.Context(), req)
        if err != nil {
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SEARCH_FAILED", "Failed to search videos"))
            return
        }

        meta := &Meta{
            Total:  total,
            Limit:  limit,
            Offset: offset,
        }

        WriteJSONWithMeta(w, http.StatusOK, videos, meta)
    }
}

func GetVideoHandler(repo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        videoID := chi.URLParam(r, "id")
        if videoID == "" {
            WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
            return
        }

        video, err := repo.GetByID(r.Context(), videoID)
        if err != nil {
            WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
            return
        }

        WriteJSON(w, http.StatusOK, video)
    }
}

func CreateVideoHandler(repo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        var req domain.VideoUploadRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
            return
        }

        userID, _ := r.Context().Value(middleware.UserIDKey).(string)
        if userID == "" {
            WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
            return
        }

        now := time.Now()
        video := &domain.Video{
            ID:          uuid.NewString(),
            ThumbnailID: uuid.NewString(),
            Title:       req.Title,
            Description: req.Description,
            Privacy:     req.Privacy,
            Status:      domain.StatusUploading,
            UploadDate:  now,
            UserID:      userID,
            Tags:        req.Tags,
            Category:    req.Category,
            Language:    req.Language,
            CreatedAt:   now,
            UpdatedAt:   now,
        }

        if repo != nil {
            if err := repo.Create(r.Context(), video); err != nil {
                WriteError(w, http.StatusInternalServerError, domain.NewDomainError("CREATE_FAILED", "Failed to create video"))
                return
            }
        }

        w.Header().Set("Location", "/api/v1/videos/"+video.ID)
        WriteJSON(w, http.StatusCreated, video)
    }
}

func UpdateVideoHandler(repo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        videoID := chi.URLParam(r, "id")
        if videoID == "" {
            WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
            return
        }

        var req domain.VideoUpdateRequest
        if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
            WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
            return
        }

        userID, _ := r.Context().Value(middleware.UserIDKey).(string)
        if userID == "" {
            WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
            return
        }

        // First check if video exists and user owns it
        existingVideo, err := repo.GetByID(r.Context(), videoID)
        if err != nil {
            WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
            return
        }

        if existingVideo.UserID != userID {
            WriteError(w, http.StatusForbidden, domain.NewDomainError("UNAUTHORIZED", "You don't have permission to update this video"))
            return
        }

        // Update the video
        video := &domain.Video{
            ID:          videoID,
            Title:       req.Title,
            Description: req.Description,
            Privacy:     req.Privacy,
            UserID:      userID,
            Tags:        req.Tags,
            Category:    req.Category,
            Language:    req.Language,
            UpdatedAt:   time.Now(),
        }

        if err := repo.Update(r.Context(), video); err != nil {
            if domainErr, ok := err.(*domain.DomainError); ok {
                WriteError(w, http.StatusNotFound, domainErr)
                return
            }
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("UPDATE_FAILED", "Failed to update video"))
            return
        }

        // Return updated video
        updatedVideo, err := repo.GetByID(r.Context(), videoID)
        if err != nil {
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_FAILED", "Failed to retrieve updated video"))
            return
        }

        WriteJSON(w, http.StatusOK, updatedVideo)
    }
}

func DeleteVideoHandler(repo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
        videoID := chi.URLParam(r, "id")
        if videoID == "" {
            WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
            return
        }

        userID, _ := r.Context().Value(middleware.UserIDKey).(string)
        if userID == "" {
            WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
            return
        }

        if err := repo.Delete(r.Context(), videoID, userID); err != nil {
            if domainErr, ok := err.(*domain.DomainError); ok {
                WriteError(w, http.StatusNotFound, domainErr)
                return
            }
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("DELETE_FAILED", "Failed to delete video"))
            return
        }

        w.WriteHeader(http.StatusNoContent)
    }
}

func UploadVideoChunk(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	chunkIndex, err := strconv.Atoi(r.Header.Get("X-Chunk-Index"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_CHUNK_INDEX", "Invalid chunk index"))
		return
	}

	totalChunks, err := strconv.Atoi(r.Header.Get("X-Total-Chunks"))
	if err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_TOTAL_CHUNKS", "Invalid total chunks"))
		return
	}

	checksum := r.Header.Get("X-Chunk-Checksum")
	if checksum == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_CHECKSUM", "Chunk checksum is required"))
		return
	}

	response := map[string]interface{}{
		"video_id":     videoID,
		"chunk_index":  chunkIndex,
		"total_chunks": totalChunks,
		"uploaded":     true,
	}

	WriteJSON(w, http.StatusOK, response)
}

func CompleteVideoUpload(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	response := map[string]interface{}{
		"video_id": videoID,
		"status":   domain.StatusQueued,
		"message":  "Upload completed, processing queued",
	}

	WriteJSON(w, http.StatusOK, response)
}

func GetUserVideosHandler(repo usecase.VideoRepository) http.HandlerFunc {
    return func(w http.ResponseWriter, r *http.Request) {
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

        videos, err := repo.GetByUserID(r.Context(), userID, limit, offset)
        if err != nil {
            WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_FAILED", "Failed to get user videos"))
            return
        }

        meta := &Meta{
            Total:  int64(len(videos)),
            Limit:  limit,
            Offset: offset,
        }

        WriteJSONWithMeta(w, http.StatusOK, videos, meta)
    }
}

func StreamVideo(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	quality := r.URL.Query().Get("quality")
	if quality == "" {
		quality = "720p"
	}
	_ = quality // Will be used when implementing actual HLS quality selection

	w.Header().Set("Content-Type", "application/vnd.apple.mpegurl")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	hlsPlaylist := `#EXTM3U
#EXT-X-VERSION:3
#EXT-X-TARGETDURATION:10
#EXT-X-MEDIA-SEQUENCE:0
#EXTINF:10.0,
segment-00000.ts
#EXTINF:10.0,
segment-00001.ts
#EXTINF:10.0,
segment-00002.ts
#EXT-X-ENDLIST`

	if _, err := w.Write([]byte(hlsPlaylist)); err != nil {
		// Log error but don't return as headers are already sent
		_ = err
	}
}
