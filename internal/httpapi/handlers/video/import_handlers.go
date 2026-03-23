package video

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"vidra-core/internal/domain"
	"vidra-core/internal/middleware"
	"vidra-core/internal/security"
	importuc "vidra-core/internal/usecase/import"

	"github.com/go-chi/chi/v5"
)

// URLValidator defines the interface for URL validation
type URLValidator interface {
	ValidateVideoURL(urlStr string) error
}

// RealURLValidator implements URLValidator using the security package
type RealURLValidator struct{}

func (v *RealURLValidator) ValidateVideoURL(urlStr string) error {
	return security.ValidateVideoURL(urlStr)
}

// ImportHandlers handles HTTP requests for video imports
type ImportHandlers struct {
	importService importuc.Service
	urlValidator  URLValidator
}

// NewImportHandlers creates new import handlers
// If urlValidator is nil, it will use the real security validator
func NewImportHandlers(importService importuc.Service, urlValidator ...URLValidator) *ImportHandlers {
	var validator URLValidator
	if len(urlValidator) > 0 && urlValidator[0] != nil {
		validator = urlValidator[0]
	} else {
		validator = &RealURLValidator{}
	}

	return &ImportHandlers{
		importService: importService,
		urlValidator:  validator,
	}
}

// CreateImportRequest represents the request body for creating an import
type CreateImportRequest struct {
	SourceURL      string  `json:"source_url"`
	ChannelID      *string `json:"channel_id,omitempty"`
	TargetPrivacy  string  `json:"target_privacy"`
	TargetCategory *string `json:"target_category,omitempty"`
	TargetURL      string  `json:"targetUrl,omitempty"`
	ChannelIDAlias *string `json:"channelId,omitempty"`
	MagnetURI      string  `json:"magnetUri,omitempty"`
	Privacy        any     `json:"privacy,omitempty"`
	Category       any     `json:"category,omitempty"`
	Video          *struct {
		Name        string `json:"name,omitempty"`
		Description string `json:"description,omitempty"`
		Privacy     any    `json:"privacy,omitempty"`
		Category    any    `json:"category,omitempty"`
	} `json:"video,omitempty"`
}

// ImportResponse represents the response for import operations
type ImportResponse struct {
	ID             string                 `json:"id"`
	SourceURL      string                 `json:"source_url"`
	Status         string                 `json:"status"`
	Progress       int                    `json:"progress"`
	VideoID        *string                `json:"video_id,omitempty"`
	ErrorMessage   *string                `json:"error_message,omitempty"`
	Metadata       *domain.ImportMetadata `json:"metadata,omitempty"`
	TargetPrivacy  string                 `json:"target_privacy"`
	SourcePlatform string                 `json:"source_platform"`
	CreatedAt      string                 `json:"created_at"`
	StartedAt      *string                `json:"started_at,omitempty"`
	CompletedAt    *string                `json:"completed_at,omitempty"`
}

// ImportListResponse represents a paginated list of imports
type ImportListResponse struct {
	Imports    []ImportResponse `json:"imports"`
	TotalCount int              `json:"total_count"`
	Limit      int              `json:"limit"`
	Offset     int              `json:"offset"`
}

// CreateImport handles POST /api/v1/videos/imports
func (h *ImportHandlers) CreateImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required", nil)
		return
	}

	req, err := parseCreateImportRequest(r)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	if unsupported := unsupportedImportSource(req); unsupported != "" {
		writeError(w, http.StatusBadRequest, unsupported+" imports are not supported", nil)
		return
	}

	// Validate required fields
	if req.SourceURL == "" {
		writeError(w, http.StatusBadRequest, "source_url is required", nil)
		return
	}

	// SECURITY FIX: Validate URL for SSRF attacks and file size limits
	// This prevents attacks on internal services like AWS metadata (169.254.169.254)
	// and DoS attacks using extremely large files (e.g., 100GB videos)
	if err := h.urlValidator.ValidateVideoURL(req.SourceURL); err != nil {
		// Log detailed error server-side for debugging
		// Return generic error to client to avoid information disclosure
		writeError(w, http.StatusBadRequest, "Invalid or unsafe URL", err)
		return
	}

	// Default privacy to private if not specified
	if req.TargetPrivacy == "" {
		req.TargetPrivacy = string(domain.PrivacyPrivate)
	}

	// Create import request
	importReq := &importuc.ImportRequest{
		UserID:         userID,
		ChannelID:      req.ChannelID,
		SourceURL:      req.SourceURL,
		TargetPrivacy:  req.TargetPrivacy,
		TargetCategory: req.TargetCategory,
	}

	imp, err := h.importService.ImportVideo(ctx, importReq)
	if err != nil {
		handleImportError(w, err)
		return
	}

	resp := mapImportToResponse(imp)
	writeJSON(w, http.StatusCreated, resp)
}

// GetImport handles GET /api/v1/videos/imports/:id
func (h *ImportHandlers) GetImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	importID := chi.URLParam(r, "id")

	if importID == "" {
		writeError(w, http.StatusBadRequest, "import id is required", nil)
		return
	}

	imp, err := h.importService.GetImport(ctx, importID, userID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrImportNotFound):
			writeError(w, http.StatusNotFound, "import not found", err)
			return
		case errors.Is(err, domain.ErrForbidden):
			writeError(w, http.StatusForbidden, "access denied", err)
			return
		case errors.Is(err, domain.ErrUnauthorized):
			writeError(w, http.StatusUnauthorized, "authentication required", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to get import", err)
		return
	}

	resp := mapImportToResponse(imp)
	writeJSON(w, http.StatusOK, resp)
}

// ListImports handles GET /api/v1/videos/imports
func (h *ImportHandlers) ListImports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required", nil)
		return
	}

	// Parse pagination parameters
	limit, offset := parsePagination(r)

	imports, totalCount, err := h.importService.ListUserImports(ctx, userID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list imports", err)
		return
	}

	if statusFilter := r.URL.Query().Get("status"); statusFilter != "" {
		filtered := make([]*domain.VideoImport, 0, len(imports))
		for _, imp := range imports {
			if string(imp.Status) == statusFilter {
				filtered = append(filtered, imp)
			}
		}
		imports = filtered
		totalCount = len(filtered)
	}

	// Map to response
	importResponses := make([]ImportResponse, len(imports))
	for i, imp := range imports {
		importResponses[i] = mapImportToResponse(imp)
	}

	resp := ImportListResponse{
		Imports:    importResponses,
		TotalCount: totalCount,
		Limit:      limit,
		Offset:     offset,
	}

	writeJSON(w, http.StatusOK, resp)
}

// CancelImport handles DELETE /api/v1/videos/imports/:id
func (h *ImportHandlers) CancelImport(w http.ResponseWriter, r *http.Request) {
	h.cancelImport(w, r, false)
}

// CancelImportCanonical handles POST /api/v1/videos/imports/:id/cancel.
func (h *ImportHandlers) CancelImportCanonical(w http.ResponseWriter, r *http.Request) {
	h.cancelImport(w, r, true)
}

// RetryImport handles POST /api/v1/videos/imports/:id/retry.
func (h *ImportHandlers) RetryImport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	userID := getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	importID := chi.URLParam(r, "id")

	if importID == "" {
		writeError(w, http.StatusBadRequest, "import id is required", nil)
		return
	}

	err := h.importService.RetryImport(ctx, importID, userID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrImportNotFound):
			writeError(w, http.StatusNotFound, "import not found", err)
			return
		case errors.Is(err, domain.ErrForbidden):
			writeError(w, http.StatusForbidden, "access denied", err)
			return
		case errors.Is(err, domain.ErrBadRequest):
			writeError(w, http.StatusBadRequest, "cannot retry import in current state", err)
			return
		case errors.Is(err, domain.ErrUnauthorized):
			writeError(w, http.StatusUnauthorized, "authentication required", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to retry import", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *ImportHandlers) cancelImport(w http.ResponseWriter, r *http.Request, peerTubeCanonical bool) {
	ctx := r.Context()
	userID := getUserID(r)
	if userID == "" {
		writeError(w, http.StatusUnauthorized, "authentication required", nil)
		return
	}
	importID := chi.URLParam(r, "id")

	if importID == "" {
		writeError(w, http.StatusBadRequest, "import id is required", nil)
		return
	}

	if peerTubeCanonical {
		imp, err := h.importService.GetImport(ctx, importID, userID)
		if err != nil {
			switch {
			case errors.Is(err, domain.ErrImportNotFound):
				writeError(w, http.StatusNotFound, "import not found", err)
				return
			case errors.Is(err, domain.ErrForbidden):
				writeError(w, http.StatusForbidden, "access denied", err)
				return
			case errors.Is(err, domain.ErrUnauthorized):
				writeError(w, http.StatusUnauthorized, "authentication required", err)
				return
			}
			writeError(w, http.StatusInternalServerError, "failed to get import", err)
			return
		}

		if imp.Status != domain.ImportStatusPending {
			writeError(w, http.StatusConflict, "cannot cancel a non pending video import", nil)
			return
		}
	}

	err := h.importService.CancelImport(ctx, importID, userID)
	if err != nil {
		switch {
		case errors.Is(err, domain.ErrImportNotFound):
			writeError(w, http.StatusNotFound, "import not found", err)
			return
		case errors.Is(err, domain.ErrForbidden):
			writeError(w, http.StatusForbidden, "access denied", err)
			return
		case errors.Is(err, domain.ErrBadRequest):
			if peerTubeCanonical {
				writeError(w, http.StatusConflict, "cannot cancel a non pending video import", err)
				return
			}
			writeError(w, http.StatusBadRequest, "cannot cancel import in current state", err)
			return
		case errors.Is(err, domain.ErrUnauthorized):
			writeError(w, http.StatusUnauthorized, "authentication required", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "failed to cancel import", err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// mapImportToResponse maps a domain import to API response
func mapImportToResponse(imp *domain.VideoImport) ImportResponse {
	resp := ImportResponse{
		ID:             imp.ID,
		SourceURL:      imp.SourceURL,
		Status:         string(imp.Status),
		Progress:       imp.Progress,
		VideoID:        imp.VideoID,
		ErrorMessage:   imp.ErrorMessage,
		TargetPrivacy:  imp.TargetPrivacy,
		SourcePlatform: imp.GetSourcePlatform(),
		CreatedAt:      imp.CreatedAt.Format("2006-01-02T15:04:05Z"),
	}

	if imp.StartedAt != nil {
		startedStr := imp.StartedAt.Format("2006-01-02T15:04:05Z")
		resp.StartedAt = &startedStr
	}

	if imp.CompletedAt != nil {
		completedStr := imp.CompletedAt.Format("2006-01-02T15:04:05Z")
		resp.CompletedAt = &completedStr
	}

	// Parse metadata if available
	if metadata, err := imp.GetMetadata(); err == nil {
		resp.Metadata = metadata
	}

	return resp
}

// handleImportError handles import-specific errors and returns appropriate HTTP responses
func handleImportError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, domain.ErrImportQuotaExceeded):
		writeError(w, http.StatusTooManyRequests, "daily import quota exceeded (max 100 per day)", err)
	case errors.Is(err, domain.ErrImportRateLimited):
		writeError(w, http.StatusTooManyRequests, "too many concurrent imports (max 5)", err)
	case errors.Is(err, domain.ErrImportUnsupportedURL):
		writeError(w, http.StatusBadRequest, "unsupported URL or platform", err)
	case errors.Is(err, domain.ErrImportInvalidURL), errors.Is(err, domain.ErrBadRequest):
		writeError(w, http.StatusBadRequest, "invalid URL format", err)
	case errors.Is(err, domain.ErrUnauthorized):
		writeError(w, http.StatusUnauthorized, "authentication required", err)
	default:
		writeError(w, http.StatusInternalServerError, "failed to create import", err)
	}
}

// parsePagination parses limit and offset query parameters
func parsePagination(r *http.Request) (limit, offset int) {
	limit = 20 // Default
	offset = 0

	if limitStr := firstNonEmpty(r.URL.Query().Get("limit"), r.URL.Query().Get("count")); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	if offsetStr := firstNonEmpty(r.URL.Query().Get("offset"), r.URL.Query().Get("start")); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	return limit, offset
}

func parseCreateImportRequest(r *http.Request) (CreateImportRequest, error) {
	var req CreateImportRequest

	contentType := r.Header.Get("Content-Type")
	if strings.HasPrefix(contentType, "multipart/form-data") || strings.HasPrefix(contentType, "application/x-www-form-urlencoded") {
		if err := r.ParseMultipartForm(32 << 20); err != nil && !errors.Is(err, http.ErrNotMultipart) {
			return req, err
		}

		req.SourceURL = strings.TrimSpace(firstNonEmpty(r.FormValue("source_url"), r.FormValue("targetUrl")))
		req.TargetURL = strings.TrimSpace(r.FormValue("targetUrl"))
		req.MagnetURI = strings.TrimSpace(r.FormValue("magnetUri"))
		req.ChannelID = nonEmptyStringPtr(r.FormValue("channel_id"))
		req.ChannelIDAlias = nonEmptyStringPtr(r.FormValue("channelId"))
		req.TargetPrivacy = strings.TrimSpace(r.FormValue("target_privacy"))
		req.TargetCategory = nonEmptyStringPtr(r.FormValue("target_category"))
		req.Privacy = firstNonEmpty(r.FormValue("privacy"), r.FormValue("video.privacy"))
		req.Category = firstNonEmpty(r.FormValue("category"), r.FormValue("video.category"))

		if file, _, err := r.FormFile("torrentfile"); err == nil {
			req.MagnetURI = firstNonEmpty(req.MagnetURI, "torrentfile")
			_ = file.Close()
		}

		normalizeCreateImportRequest(&req)
		return req, nil
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		return req, err
	}

	normalizeCreateImportRequest(&req)
	return req, nil
}

func normalizeCreateImportRequest(req *CreateImportRequest) {
	req.SourceURL = strings.TrimSpace(firstNonEmpty(req.SourceURL, req.TargetURL))
	req.TargetURL = strings.TrimSpace(req.TargetURL)
	req.MagnetURI = strings.TrimSpace(req.MagnetURI)

	if req.ChannelID == nil {
		req.ChannelID = cloneStringPtr(req.ChannelIDAlias)
	}

	if req.TargetPrivacy == "" {
		req.TargetPrivacy = normalizeImportPrivacy(req.Privacy)
	}
	if req.TargetPrivacy == "" && req.Video != nil {
		req.TargetPrivacy = normalizeImportPrivacy(req.Video.Privacy)
	}

	if req.TargetCategory == nil {
		req.TargetCategory = normalizeImportCategory(req.Category)
	}
	if req.TargetCategory == nil && req.Video != nil {
		req.TargetCategory = normalizeImportCategory(req.Video.Category)
	}
}

func unsupportedImportSource(req CreateImportRequest) string {
	if strings.TrimSpace(req.SourceURL) != "" {
		return ""
	}
	if strings.TrimSpace(req.MagnetURI) != "" {
		return "magnetUri"
	}
	return ""
}

func normalizeImportPrivacy(value any) string {
	switch v := value.(type) {
	case nil:
		return ""
	case string:
		switch strings.ToLower(strings.TrimSpace(v)) {
		case "1", string(domain.PrivacyPublic):
			return string(domain.PrivacyPublic)
		case "2", string(domain.PrivacyUnlisted):
			return string(domain.PrivacyUnlisted)
		case "3", "4", string(domain.PrivacyPrivate), "internal":
			return string(domain.PrivacyPrivate)
		default:
			return ""
		}
	case float64:
		return normalizeImportPrivacy(strconv.Itoa(int(v)))
	case json.Number:
		return normalizeImportPrivacy(v.String())
	default:
		return ""
	}
}

func normalizeImportCategory(value any) *string {
	switch v := value.(type) {
	case nil:
		return nil
	case string:
		return nonEmptyStringPtr(v)
	case float64:
		return nonEmptyStringPtr(strconv.Itoa(int(v)))
	case json.Number:
		return nonEmptyStringPtr(v.String())
	default:
		return nil
	}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func nonEmptyStringPtr(value string) *string {
	if trimmed := strings.TrimSpace(value); trimmed != "" {
		return &trimmed
	}
	return nil
}

func cloneStringPtr(value *string) *string {
	if value == nil {
		return nil
	}
	return nonEmptyStringPtr(*value)
}

// getUserID extracts user ID from request context (set by auth middleware)
func getUserID(r *http.Request) string {
	if userID, ok := r.Context().Value(middleware.UserIDKey).(string); ok {
		return userID
	}
	return ""
}

// writeJSON writes a JSON response
func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
}

// ErrorResponse represents an error response
type ErrorResponse struct {
	Error   string `json:"error"`
	Message string `json:"message"`
	Details string `json:"details,omitempty"`
}

// writeError writes an error response
func writeError(w http.ResponseWriter, status int, message string, err error) {
	resp := ErrorResponse{
		Error:   http.StatusText(status),
		Message: message,
	}

	if err != nil {
		resp.Details = err.Error()
	}

	writeJSON(w, status, resp)
}
