package httpapi

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/middleware"
)

func ListVideos(w http.ResponseWriter, r *http.Request) {
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

	videos := []domain.Video{
		{
			ID:          "1",
			Title:       "Sample Video 1",
			Description: "This is a sample video",
			Duration:    300,
			Views:       1000,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Now().AddDate(0, 0, -1),
			UserID:      "user123",
			Tags:        []string{"sample", "demo"},
			Category:    "education",
			Language:    "en",
			FileSize:    1024000,
			MimeType:    "video/mp4",
		},
		{
			ID:          "2",
			Title:       "Sample Video 2",
			Description: "Another sample video",
			Duration:    450,
			Views:       2500,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Now().AddDate(0, 0, -2),
			UserID:      "user456",
			Tags:        []string{"tutorial", "example"},
			Category:    "technology",
			Language:    "en",
			FileSize:    2048000,
			MimeType:    "video/mp4",
		},
	}

	meta := &Meta{
		Total:  int64(len(videos)),
		Limit:  limit,
		Offset: offset,
	}

	WriteJSONWithMeta(w, http.StatusOK, videos, meta)
}

func SearchVideos(w http.ResponseWriter, r *http.Request) {
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

	videos := []domain.Video{
		{
			ID:          "1",
			Title:       "Search Result 1",
			Description: "This video matches your search query",
			Duration:    300,
			Views:       1000,
			Privacy:     domain.PrivacyPublic,
			Status:      domain.StatusCompleted,
			UploadDate:  time.Now().AddDate(0, 0, -1),
			UserID:      "user123",
		},
	}

	meta := &Meta{
		Total:  1,
		Limit:  limit,
		Offset: offset,
	}

	WriteJSONWithMeta(w, http.StatusOK, videos, meta)
}

func GetVideo(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	video := domain.Video{
		ID:          videoID,
		Title:       "Sample Video",
		Description: "This is a detailed sample video",
		Duration:    300,
		Views:       1000,
		Privacy:     domain.PrivacyPublic,
		Status:      domain.StatusCompleted,
		UploadDate:  time.Now().AddDate(0, 0, -1),
		UserID:      "user123",
		Tags:        []string{"sample", "demo"},
		Category:    "education",
		Language:    "en",
		FileSize:    1024000,
		MimeType:    "video/mp4",
		Metadata: domain.VideoMetadata{
			Width:       1920,
			Height:      1080,
			Framerate:   30.0,
			Bitrate:     5000000,
			AudioCodec:  "aac",
			VideoCodec:  "h264",
			AspectRatio: "16:9",
		},
		CreatedAt: time.Now().AddDate(0, 0, -1),
		UpdatedAt: time.Now().AddDate(0, 0, -1),
	}

	WriteJSON(w, http.StatusOK, video)
}

func CreateVideo(w http.ResponseWriter, r *http.Request) {
	var req domain.VideoUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	video := domain.Video{
		ID:          "new-video-" + strconv.FormatInt(time.Now().UnixNano(), 10),
		Title:       req.Title,
		Description: req.Description,
		Privacy:     req.Privacy,
		Status:      domain.StatusUploading,
		UploadDate:  time.Now(),
		UserID:      userID,
		Tags:        req.Tags,
		Category:    req.Category,
		Language:    req.Language,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}

	WriteJSON(w, http.StatusCreated, video)
}

func UpdateVideo(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	var req domain.VideoUploadRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	userID := r.Context().Value(middleware.UserIDKey).(string)

	video := domain.Video{
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

	WriteJSON(w, http.StatusOK, video)
}

func DeleteVideo(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
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

	w.Write([]byte(hlsPlaylist))
}