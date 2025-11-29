package video

import (
	"encoding/json"
	"net/http"
	"strconv"

	"athena/internal/domain"
	"athena/internal/middleware"
	"athena/internal/security"
	importuc "athena/internal/usecase/import"

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

	var req CreateImportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
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
	importID := chi.URLParam(r, "id")

	if importID == "" {
		writeError(w, http.StatusBadRequest, "import id is required", nil)
		return
	}

	imp, err := h.importService.GetImport(ctx, importID, userID)
	if err != nil {
		if err == domain.ErrImportNotFound {
			writeError(w, http.StatusNotFound, "import not found", err)
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

	// Parse pagination parameters
	limit, offset := parsePagination(r)

	imports, totalCount, err := h.importService.ListUserImports(ctx, userID, limit, offset)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "failed to list imports", err)
		return
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
	ctx := r.Context()
	userID := getUserID(r)
	importID := chi.URLParam(r, "id")

	if importID == "" {
		writeError(w, http.StatusBadRequest, "import id is required", nil)
		return
	}

	err := h.importService.CancelImport(ctx, importID, userID)
	if err != nil {
		if err == domain.ErrImportNotFound {
			writeError(w, http.StatusNotFound, "import not found", err)
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
	switch err {
	case domain.ErrImportQuotaExceeded:
		writeError(w, http.StatusTooManyRequests, "daily import quota exceeded (max 100 per day)", err)
	case domain.ErrImportRateLimited:
		writeError(w, http.StatusTooManyRequests, "too many concurrent imports (max 5)", err)
	case domain.ErrImportUnsupportedURL:
		writeError(w, http.StatusBadRequest, "unsupported URL or platform", err)
	case domain.ErrImportInvalidURL:
		writeError(w, http.StatusBadRequest, "invalid URL format", err)
	default:
		writeError(w, http.StatusInternalServerError, "failed to create import", err)
	}
}

// parsePagination parses limit and offset query parameters
func parsePagination(r *http.Request) (limit, offset int) {
	limit = 20 // Default
	offset = 0

	if limitStr := r.URL.Query().Get("limit"); limitStr != "" {
		if parsed, err := strconv.Atoi(limitStr); err == nil && parsed > 0 && parsed <= 100 {
			limit = parsed
		}
	}

	if offsetStr := r.URL.Query().Get("offset"); offsetStr != "" {
		if parsed, err := strconv.Atoi(offsetStr); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	return limit, offset
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
