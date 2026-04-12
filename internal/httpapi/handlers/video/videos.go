package video

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/google/uuid"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/obs"
	"vidra-core/internal/security"
	"vidra-core/internal/usecase"
)

func ListVideosHandler(repo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset, pageSize := shared.ParsePagination(r, 20)

		sort := r.URL.Query().Get("sort")
		if sort == "" {
			sort = "upload_date"
		}

		order := r.URL.Query().Get("order")
		if order != "asc" && order != "desc" {
			order = "desc"
		}

		req := &domain.VideoSearchRequest{
			Language: r.URL.Query().Get("language"),
			Host:     r.URL.Query().Get("host"),
			Sort:     sort,
			Order:    order,
			Limit:    limit,
			Offset:   offset,
		}

		videos, total, err := repo.List(r.Context(), req)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_FAILED", "Failed to list videos"))
			return
		}

		for _, v := range videos {
			v.ComputeThumbnails()
		}

		meta := &shared.Meta{
			Total:    total,
			Limit:    limit,
			Offset:   offset,
			Page:     page,
			PageSize: pageSize,
		}

		shared.WriteJSONWithMeta(w, http.StatusOK, videos, meta)
	}
}

func SearchVideosHandler(repo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		query := r.URL.Query().Get("q")
		if query == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_QUERY", "Search query is required"))
			return
		}

		page, limit, offset, pageSize := shared.ParsePagination(r, 20)

		tags := r.URL.Query()["tags"]

		req := &domain.VideoSearchRequest{
			Query:    query,
			Tags:     tags,
			Language: r.URL.Query().Get("language"),
			Host:     r.URL.Query().Get("host"),
			Sort:     r.URL.Query().Get("sort"),
			Order:    r.URL.Query().Get("order"),
			Limit:    limit,
			Offset:   offset,
		}

		videos, total, err := repo.Search(r.Context(), req)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("SEARCH_FAILED", "Failed to search videos"))
			return
		}

		for _, v := range videos {
			v.ComputeThumbnails()
		}

		meta := &shared.Meta{
			Total:    total,
			Limit:    limit,
			Offset:   offset,
			Page:     page,
			PageSize: pageSize,
		}

		shared.WriteJSONWithMeta(w, http.StatusOK, videos, meta)
	}
}

func GetVideoHandler(repo usecase.VideoRepository, captionService *usecase.CaptionService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		videoID, ok := shared.RequireUUIDParam(w, r, "id", "MISSING_VIDEO_ID", "INVALID_VIDEO_ID", "Video ID is required", "Invalid video ID format")
		if !ok {
			return
		}

		video, err := repo.GetByID(r.Context(), videoID)
		if err != nil {
			if isVideoNotFoundError(err) {
				shared.WriteError(w, http.StatusNotFound, videoErrorOrDefault(err, "VIDEO_NOT_FOUND", "Video not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_FAILED", "Failed to get video"))
			return
		}
		requesterID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if video.Privacy == domain.PrivacyPrivate && requesterID != video.UserID {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
			return
		}

		// Visibility: processing/queued videos are only visible to the owner,
		// moderators, and admins. Other users get 404 (PeerTube parity for waitTranscoding).
		if video.Status != domain.StatusCompleted {
			requesterRole, _ := r.Context().Value(middleware.UserRoleKey).(string)
			isOwner := requesterID == video.UserID
			isPrivileged := requesterRole == "admin" || requesterRole == "moderator"
			if !isOwner && !isPrivileged {
				shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
				return
			}
		}

		video.ComputeThumbnails()

		var captions []domain.Caption
		if captionService != nil {
			videoUUID, _ := uuid.Parse(videoID)
			if captionList, err := captionService.GetCaptionsByVideoID(r.Context(), videoUUID); err == nil {
				captions = captionList.Captions
			}
		}

		// Build PeerTube-compatible files[] and streamingPlaylists[]
		webFiles, streamingPlaylists := BuildVideoFilesResponse(video)

		type VideoWithExtras struct {
			*domain.Video
			Captions           []domain.Caption            `json:"captions"`
			Files              []domain.VideoFile          `json:"files"`
			StreamingPlaylists []domain.StreamingPlaylist   `json:"streamingPlaylists"`
		}

		response := VideoWithExtras{
			Video:              video,
			Captions:           captions,
			Files:              webFiles,
			StreamingPlaylists: streamingPlaylists,
		}

		shared.WriteJSON(w, http.StatusOK, response)
	}
}

func CreateVideoHandler(repo usecase.VideoRepository, auditLogger ...*obs.AuditLogger) http.HandlerFunc {
	var al *obs.AuditLogger
	if len(auditLogger) > 0 {
		al = auditLogger[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		var req domain.VideoUploadRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}

		if req.Title == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TITLE", "Title is required"))
			return
		}
		if req.Privacy == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_PRIVACY", "Privacy setting is required"))
			return
		}
		if req.Privacy != domain.PrivacyPublic && req.Privacy != domain.PrivacyUnlisted && req.Privacy != domain.PrivacyPrivate {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_PRIVACY", "Privacy must be public, unlisted, or private"))
			return
		}

		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		tags := req.Tags
		if tags == nil {
			tags = []string{}
		}

		sanitizedTitle := security.SanitizeStrictText(req.Title)
		if sanitizedTitle == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_TITLE", "Title contains only disallowed content"))
			return
		}
		sanitizedDescription := security.SanitizeCommentHTML(req.Description)

		now := time.Now()
		video := &domain.Video{
			ID:          uuid.NewString(),
			ThumbnailID: uuid.NewString(),
			Title:       sanitizedTitle,
			Description: sanitizedDescription,
			Privacy:     req.Privacy,
			Status:      domain.StatusUploading,
			UploadDate:  now,
			UserID:      userID,
			Tags:        tags,
			CategoryID:  req.CategoryID,
			Language:    req.Language,
			CreatedAt:   now,
			UpdatedAt:   now,
		}

		if err := repo.Create(r.Context(), video); err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("CREATE_FAILED", "Failed to create video"))
			return
		}

		if al != nil {
			al.Create("videos", userID, obs.NewVideoAuditView(video))
		}

		w.Header().Set("Location", "/api/v1/videos/"+video.ID)
		shared.WriteJSON(w, http.StatusCreated, video)
	}
}

func UpdateVideoHandler(repo usecase.VideoRepository, auditLogger ...*obs.AuditLogger) http.HandlerFunc {
	var al *obs.AuditLogger
	if len(auditLogger) > 0 {
		al = auditLogger[0]
	}
	type updateRequest struct {
		Title       string     `json:"title"`
		Description string     `json:"description"`
		Privacy     string     `json:"privacy"`
		Tags        []string   `json:"tags"`
		Category    string     `json:"category"`
		CategoryID  *uuid.UUID `json:"category_id"`
		Language    string     `json:"language"`
	}

	return func(w http.ResponseWriter, r *http.Request) {
		videoID, ok := shared.RequireUUIDParam(w, r, "id", "MISSING_VIDEO_ID", "INVALID_VIDEO_ID", "Video ID is required", "Invalid video ID format")
		if !ok {
			return
		}

		var rawReq updateRequest
		if err := json.NewDecoder(r.Body).Decode(&rawReq); err != nil {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
			return
		}

		req := domain.VideoUpdateRequest{
			Title:       rawReq.Title,
			Description: rawReq.Description,
			Privacy:     domain.Privacy(rawReq.Privacy),
			Tags:        rawReq.Tags,
			CategoryID:  rawReq.CategoryID,
			Language:    rawReq.Language,
		}

		if req.Title == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_TITLE", "Title is required"))
			return
		}
		if req.Privacy == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_PRIVACY", "Privacy setting is required"))
			return
		}
		if req.Privacy != domain.PrivacyPublic && req.Privacy != domain.PrivacyUnlisted && req.Privacy != domain.PrivacyPrivate {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_PRIVACY", "Privacy must be public, unlisted, or private"))
			return
		}

		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		existingVideo, err := repo.GetByID(r.Context(), videoID)
		if err != nil {
			if isVideoNotFoundError(err) {
				shared.WriteError(w, http.StatusNotFound, videoErrorOrDefault(err, "VIDEO_NOT_FOUND", "Video not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_FAILED", "Failed to get video"))
			return
		}

		if existingVideo.UserID != userID {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("UNAUTHORIZED", "You don't have permission to update this video"))
			return
		}

		sanitizedTitle := security.SanitizeStrictText(req.Title)
		if sanitizedTitle == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_TITLE", "Title contains only disallowed content"))
			return
		}
		sanitizedDescription := security.SanitizeCommentHTML(req.Description)

		tags := req.Tags
		if tags == nil {
			tags = []string{}
		}

		video := &domain.Video{
			ID:          videoID,
			Title:       sanitizedTitle,
			Description: sanitizedDescription,
			Privacy:     req.Privacy,
			Status:      existingVideo.Status,
			UserID:      userID,
			Tags:        tags,
			CategoryID:  req.CategoryID,
			Language:    req.Language,
			UpdatedAt:   time.Now(),
		}

		if err := repo.Update(r.Context(), video); err != nil {
			var domainErr domain.DomainError
			if errors.As(err, &domainErr) {
				shared.WriteError(w, http.StatusNotFound, domainErr)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("UPDATE_FAILED", "Failed to update video"))
			return
		}

		updatedVideo, err := repo.GetByID(r.Context(), videoID)
		if err != nil {
			if isVideoNotFoundError(err) {
				shared.WriteError(w, http.StatusNotFound, videoErrorOrDefault(err, "VIDEO_NOT_FOUND", "Video not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_FAILED", "Failed to retrieve updated video"))
			return
		}

		type videoResponse struct {
			*domain.Video
			Category string `json:"category,omitempty"`
		}

		response := &videoResponse{
			Video: updatedVideo,
		}

		if updatedVideo.Category != nil {
			response.Category = updatedVideo.Category.Slug
		} else if rawReq.Category != "" {
			response.Category = rawReq.Category
		}

		if al != nil {
			al.Update("videos", userID, obs.NewVideoAuditView(updatedVideo), obs.NewVideoAuditView(existingVideo))
		}

		shared.WriteJSON(w, http.StatusOK, response)
	}
}

func DeleteVideoHandler(repo usecase.VideoRepository, auditLogger ...*obs.AuditLogger) http.HandlerFunc {
	var al *obs.AuditLogger
	if len(auditLogger) > 0 {
		al = auditLogger[0]
	}
	return func(w http.ResponseWriter, r *http.Request) {
		videoID, ok := shared.RequireUUIDParam(w, r, "id", "MISSING_VIDEO_ID", "INVALID_VIDEO_ID", "Video ID is required", "Invalid video ID format")
		if !ok {
			return
		}

		userID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if userID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Missing or invalid authentication"))
			return
		}

		// Fetch before delete for audit
		existingForDelete, _ := repo.GetByID(r.Context(), videoID)

		if err := repo.Delete(r.Context(), videoID, userID); err != nil {
			var domainErr domain.DomainError
			if errors.As(err, &domainErr) {
				shared.WriteError(w, http.StatusNotFound, domainErr)
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("DELETE_FAILED", "Failed to delete video"))
			return
		}

		if al != nil && existingForDelete != nil {
			al.Delete("videos", userID, obs.NewVideoAuditView(existingForDelete))
		}

		w.WriteHeader(http.StatusNoContent)
	}
}

func GetUserVideosHandler(repo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		userID, ok := shared.RequireUUIDParam(w, r, "id", "MISSING_USER_ID", "INVALID_USER_ID", "User ID is required", "Invalid user ID format")
		if !ok {
			return
		}

		page, _ := strconv.Atoi(r.URL.Query().Get("page"))
		pageSize, _ := strconv.Atoi(r.URL.Query().Get("pageSize"))
		limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
		offset, _ := strconv.Atoi(r.URL.Query().Get("offset"))
		if pageSize <= 0 || pageSize > 100 {
			if limit > 0 {
				pageSize = limit
			} else {
				pageSize = 20
			}
		}
		if page <= 0 {
			if offset < 0 {
				offset = 0
			}
			page = (offset / pageSize) + 1
			if page <= 0 {
				page = 1
			}
		}
		limit = pageSize
		offset = (page - 1) * pageSize

		videos, total, err := repo.GetByUserID(r.Context(), userID, limit, offset)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_FAILED", "Failed to get user videos"))
			return
		}

		requesterID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if requesterID != userID {
			filtered := make([]*domain.Video, 0, len(videos))
			for _, v := range videos {
				if v.Privacy != domain.PrivacyPrivate {
					filtered = append(filtered, v)
				}
			}
			videos = filtered
			total = int64(len(videos))
		}

		meta := &shared.Meta{Total: total, Limit: limit, Offset: offset, Page: page, PageSize: pageSize}
		shared.WriteJSONWithMeta(w, http.StatusOK, videos, meta)
	}
}

// GetVideoSourceHandler handles GET /api/v1/videos/{id}/source.
// Returns the download URL for the original source file (S3 or local path).
// Only the video owner may access the source file.
func GetVideoSourceHandler(repo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		videoID, ok := shared.RequireUUIDParam(w, r, "id", "MISSING_VIDEO_ID", "INVALID_VIDEO_ID", "Video ID is required", "Invalid video ID format")
		if !ok {
			return
		}

		requesterID, _ := r.Context().Value(middleware.UserIDKey).(string)
		if requesterID == "" {
			shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
			return
		}

		video, err := repo.GetByID(r.Context(), videoID)
		if err != nil {
			if isVideoNotFoundError(err) {
				shared.WriteError(w, http.StatusNotFound, videoErrorOrDefault(err, "VIDEO_NOT_FOUND", "Video not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("GET_FAILED", "Failed to get video"))
			return
		}

		requesterRole, _ := r.Context().Value(middleware.UserRoleKey).(string)
		if video.UserID != requesterID && requesterRole != string(domain.RoleAdmin) {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner or an admin can access the source file"))
			return
		}

		// Prefer S3 URL, fall back to local output path.
		downloadURL := video.S3URLs["source"]
		if downloadURL == "" {
			downloadURL = video.OutputPaths["source"]
		}
		if downloadURL == "" {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("SOURCE_NOT_FOUND", "Source file is no longer available"))
			return
		}

		shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
			"fileDownloadUrl": downloadURL,
			"filename":        video.Title + ".mp4",
		})
	}
}
