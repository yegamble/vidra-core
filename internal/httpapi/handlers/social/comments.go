package social

import (
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	uccmt "athena/internal/usecase/comment"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type CommentHandlers struct {
	commentService *uccmt.Service
}

func NewCommentHandlers(commentService *uccmt.Service) *CommentHandlers {
	return &CommentHandlers{
		commentService: commentService,
	}
}

// CreateComment handles POST /api/v1/videos/{videoId}/comments
func (h *CommentHandlers) CreateComment(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoId")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	var req domain.CreateCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Set the video ID from URL
	req.VideoID = videoID

	// Validate request
	if len(req.Body) == 0 || len(req.Body) > 10000 {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	comment, err := h.commentService.CreateComment(r.Context(), userID, &req)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, comment)
}

// GetComments handles GET /api/v1/videos/{videoId}/comments
func (h *CommentHandlers) GetComments(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoId")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

	// Parse query parameters
	limitStr := r.URL.Query().Get("limit")
	offsetStr := r.URL.Query().Get("offset")
	parentIDStr := r.URL.Query().Get("parentId")
	orderBy := r.URL.Query().Get("orderBy")

	limit := 20
	if limitStr != "" {
		if l, err := strconv.Atoi(limitStr); err == nil && l > 0 && l <= 100 {
			limit = l
		}
	}

	offset := 0
	if offsetStr != "" {
		if o, err := strconv.Atoi(offsetStr); err == nil && o >= 0 {
			offset = o
		}
	}

	var parentID *uuid.UUID
	if parentIDStr != "" {
		pid, err := uuid.Parse(parentIDStr)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
			return
		}
		parentID = &pid
	}

	if orderBy == "" {
		orderBy = "newest"
	}

	comments, err := h.commentService.ListComments(r.Context(), videoID, parentID, limit, offset, orderBy)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	shared.WriteJSONWithMeta(w, http.StatusOK, comments, &shared.Meta{
		Total:  int64(len(comments)),
		Limit:  limit,
		Offset: offset,
	})
}

// GetComment handles GET /api/v1/comments/{commentId}
func (h *CommentHandlers) GetComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	comment, err := h.commentService.GetComment(r.Context(), commentID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, comment)
}

// UpdateComment handles PUT /api/v1/comments/{commentId}
func (h *CommentHandlers) UpdateComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	var req domain.UpdateCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Validate request
	if len(req.Body) == 0 || len(req.Body) > 10000 {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	err = h.commentService.UpdateComment(r.Context(), userID, commentID, &req)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, fmt.Errorf("forbidden"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// DeleteComment handles DELETE /api/v1/comments/{commentId}
func (h *CommentHandlers) DeleteComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	// Check if user is admin or moderator
	isAdmin := shared.IsAdminFromContext(r)

	err = h.commentService.DeleteComment(r.Context(), userID, commentID, isAdmin)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, fmt.Errorf("forbidden"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// FlagComment handles POST /api/v1/comments/{commentId}/flag
func (h *CommentHandlers) FlagComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	var req domain.FlagCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Validate reason
	validReasons := map[string]bool{
		"spam":           true,
		"harassment":     true,
		"hate_speech":    true,
		"inappropriate":  true,
		"misinformation": true,
		"other":          true,
	}

	if !validReasons[string(req.Reason)] {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	err = h.commentService.FlagComment(r.Context(), userID, commentID, &req)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, map[string]string{
		"message": "Comment flagged successfully",
	})
}

// UnflagComment handles DELETE /api/v1/comments/{commentId}/flag
func (h *CommentHandlers) UnflagComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	err = h.commentService.UnflagComment(r.Context(), userID, commentID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ModerateComment handles POST /api/v1/comments/{commentId}/moderate
func (h *CommentHandlers) ModerateComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Get user ID from context
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("Unauthorized"))
		return
	}

	var req struct {
		Status string `json:"status"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Validate status
	var status domain.CommentStatus
	switch req.Status {
	case "active":
		status = domain.CommentStatusActive
	case "hidden":
		status = domain.CommentStatusHidden
	case "flagged":
		status = domain.CommentStatusFlagged
	default:
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	// Check if user is admin or moderator
	isAdmin := shared.IsAdminFromContext(r)

	err = h.commentService.ModerateComment(r.Context(), userID, commentID, status, isAdmin)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		if err == domain.ErrUnauthorized {
			shared.WriteError(w, http.StatusForbidden, fmt.Errorf("forbidden"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
