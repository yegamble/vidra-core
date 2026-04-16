package video

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/obs"
	"vidra-core/internal/security"
	"vidra-core/internal/usecase"
)

func parseVideoSort(raw string, defaultSort string, defaultOrder string, allowRelevance bool) (string, string) {
	if raw == "" {
		return defaultSort, defaultOrder
	}

	order := "asc"
	if strings.HasPrefix(raw, "-") {
		order = "desc"
		raw = strings.TrimPrefix(raw, "-")
	}

	switch raw {
	case "match":
		if allowRelevance {
			return "relevance", order
		}
	case "publishedAt", "createdAt", "upload_date":
		return "upload_date", order
	case "views", "duration", "title", "name":
		if raw == "name" {
			return "title", order
		}
		return raw, order
	}

	return defaultSort, defaultOrder
}

func applyOrderOverride(rawSort string, currentOrder string, requestedOrder string) string {
	if strings.HasPrefix(rawSort, "-") {
		return currentOrder
	}

	switch requestedOrder {
	case "asc", "desc":
		return requestedOrder
	default:
		return currentOrder
	}
}

func parseStringListParam(r *http.Request, keys ...string) []string {
	for _, key := range keys {
		values := r.URL.Query()[key]
		if len(values) == 0 {
			continue
		}

		var out []string
		for _, value := range values {
			for _, item := range strings.Split(value, ",") {
				trimmed := strings.TrimSpace(item)
				if trimmed != "" {
					out = append(out, trimmed)
				}
			}
		}

		if len(out) > 0 {
			return out
		}
	}

	return nil
}

func parseOptionalIntParam(r *http.Request, key string) *int {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil
	}

	value, err := strconv.Atoi(raw)
	if err != nil {
		return nil
	}

	return &value
}

func parseOptionalTimeParam(r *http.Request, key string) *time.Time {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return nil
	}

	layouts := []string{time.RFC3339, "2006-01-02"}
	for _, layout := range layouts {
		value, err := time.Parse(layout, raw)
		if err == nil {
			return &value
		}
	}

	return nil
}

func parseOptionalCategoryID(r *http.Request, keys ...string) *uuid.UUID {
	values := parseStringListParam(r, keys...)
	if len(values) == 0 {
		return nil
	}

	parsed, err := uuid.Parse(values[0])
	if err != nil {
		return nil
	}

	return &parsed
}

func ListVideosHandler(repo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		page, limit, offset, pageSize := shared.ParsePagination(r, 20)
		rawSort := r.URL.Query().Get("sort")
		sort, order := parseVideoSort(rawSort, "upload_date", "desc", false)
		order = applyOrderOverride(rawSort, order, r.URL.Query().Get("order"))
		categoryID := parseOptionalCategoryID(r, "categoryOneOf", "category")

		req := &domain.VideoSearchRequest{
			CategoryID: categoryID,
			Language:   r.URL.Query().Get("language"),
			Host:       r.URL.Query().Get("host"),
			Sort:       sort,
			Order:      order,
			Limit:      limit,
			Offset:     offset,
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
			query = r.URL.Query().Get("search")
		}
		if query == "" {
			shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_QUERY", "Search query is required"))
			return
		}

		page, limit, offset, pageSize := shared.ParsePagination(r, 20)
		rawSort := r.URL.Query().Get("sort")
		sort, order := parseVideoSort(rawSort, "relevance", "desc", true)
		order = applyOrderOverride(rawSort, order, r.URL.Query().Get("order"))
		tags := parseStringListParam(r, "tagsOneOf", "tags")
		categoryID := parseOptionalCategoryID(r, "categoryOneOf", "category")

		req := &domain.VideoSearchRequest{
			Query:           query,
			Tags:            tags,
			CategoryID:      categoryID,
			Language:        r.URL.Query().Get("language"),
			Host:            r.URL.Query().Get("host"),
			DurationMin:     parseOptionalIntParam(r, "durationMin"),
			DurationMax:     parseOptionalIntParam(r, "durationMax"),
			PublishedAfter:  parseOptionalTimeParam(r, "startDate"),
			PublishedBefore: parseOptionalTimeParam(r, "endDate"),
			Sort:            sort,
			Order:           order,
			Limit:           limit,
			Offset:          offset,
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
		requesterRole, _ := r.Context().Value(middleware.UserRoleKey).(string)
		if video.Privacy == domain.PrivacyPrivate && requesterID != video.UserID {
			shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Access denied"))
			return
		}

		// Public access starts once the video has either fully completed or exposed
		// at least one encoded HLS rendition. Owners/admins/moderators can inspect
		// the processing state earlier via authenticated requests.
		webFiles, streamingPlaylists := BuildVideoFilesResponse(video)
		hasEncodedRendition := video.Status == domain.StatusCompleted
		if !hasEncodedRendition {
			for _, playlist := range streamingPlaylists {
				if playlist.PlaylistUrl != "" || len(playlist.Files) > 0 {
					hasEncodedRendition = true
					break
				}
			}
		}
		hasPlayableSource := len(webFiles) > 0 && !video.WaitTranscoding
		canInspectProcessing := requesterID == video.UserID || requesterRole == "admin" || requesterRole == "moderator"
		if !hasEncodedRendition && !hasPlayableSource && !canInspectProcessing {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
			return
		}

		video.ComputeThumbnails()

		var captions []domain.Caption
		if captionService != nil {
			videoUUID, _ := uuid.Parse(videoID)
			if captionList, err := captionService.GetCaptionsByVideoID(r.Context(), videoUUID); err == nil {
				captions = captionList.Captions
			}
		}

		type VideoWithExtras struct {
			*domain.Video
			Captions           []domain.Caption           `json:"captions"`
			Files              []domain.VideoFile         `json:"files"`
			StreamingPlaylists []domain.StreamingPlaylist `json:"streamingPlaylists"`
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
		Title           string     `json:"title"`
		Description     string     `json:"description"`
		Privacy         string     `json:"privacy"`
		ChannelID       *uuid.UUID `json:"channelId"`
		Tags            []string   `json:"tags"`
		Category        string     `json:"category"`
		CategoryID      *uuid.UUID `json:"category_id"`
		Language        string     `json:"language"`
		WaitTranscoding *bool      `json:"waitTranscoding"`
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
			Title:           rawReq.Title,
			Description:     rawReq.Description,
			Privacy:         domain.Privacy(rawReq.Privacy),
			ChannelID:       rawReq.ChannelID,
			Tags:            rawReq.Tags,
			CategoryID:      rawReq.CategoryID,
			Language:        rawReq.Language,
			WaitTranscoding: rawReq.WaitTranscoding,
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

		tags := existingVideo.Tags
		if req.Tags != nil {
			tags = req.Tags
		}
		if tags == nil {
			tags = []string{}
		}

		categoryID := existingVideo.CategoryID
		if req.CategoryID != nil {
			categoryID = req.CategoryID
		}

		language := existingVideo.Language
		if req.Language != "" {
			language = req.Language
		}

		channelID := existingVideo.ChannelID
		if req.ChannelID != nil {
			channelID = *req.ChannelID
		}

		waitTranscoding := existingVideo.WaitTranscoding
		if req.WaitTranscoding != nil {
			waitTranscoding = *req.WaitTranscoding
		}

		video := *existingVideo
		video.Title = sanitizedTitle
		video.Description = sanitizedDescription
		video.Privacy = req.Privacy
		video.UserID = userID
		video.Tags = tags
		video.CategoryID = categoryID
		video.Language = language
		video.ChannelID = channelID
		video.WaitTranscoding = waitTranscoding
		video.UpdatedAt = time.Now()

		if err := repo.Update(r.Context(), &video); err != nil {
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
