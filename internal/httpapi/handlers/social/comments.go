package social

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/obs"
	uccmt "vidra-core/internal/usecase/comment"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

type CommentHandlers struct {
	commentService CommentServiceInterface
	auditLogger    *obs.AuditLogger
}

func NewCommentHandlers(commentService *uccmt.Service, auditLogger ...*obs.AuditLogger) *CommentHandlers {
	h := &CommentHandlers{commentService: commentService}
	if len(auditLogger) > 0 {
		h.auditLogger = auditLogger[0]
	}
	return h
}

func (h *CommentHandlers) CreateComment(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoId")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

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

	req.VideoID = videoID

	if len(req.Body) == 0 || len(req.Body) > 10000 {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	comment, err := h.commentService.CreateComment(r.Context(), userID, &req)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	if h.auditLogger != nil {
		h.auditLogger.Create("comments", userID.String(), obs.NewCommentAuditView(comment))
	}

	shared.WriteJSON(w, http.StatusCreated, comment)
}

func (h *CommentHandlers) GetComments(w http.ResponseWriter, r *http.Request) {
	videoIDStr := chi.URLParam(r, "videoId")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

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
		if errors.Is(err, domain.ErrNotFound) {
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

func (h *CommentHandlers) GetComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	comment, err := h.commentService.GetComment(r.Context(), commentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, comment)
}

func (h *CommentHandlers) UpdateComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

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

	if len(req.Body) == 0 || len(req.Body) > 10000 {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	err = h.commentService.UpdateComment(r.Context(), userID, commentID, &req)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		if errors.Is(err, domain.ErrUnauthorized) {
			shared.WriteError(w, http.StatusForbidden, fmt.Errorf("forbidden"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *CommentHandlers) DeleteComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	isAdmin := shared.IsAdminFromContext(r)

	err = h.commentService.DeleteComment(r.Context(), userID, commentID, isAdmin)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		if errors.Is(err, domain.ErrUnauthorized) {
			shared.WriteError(w, http.StatusForbidden, fmt.Errorf("forbidden"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	if h.auditLogger != nil {
		h.auditLogger.Delete("comments", userID.String(), obs.MapAuditView{"comment-id": commentID.String()})
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *CommentHandlers) FlagComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

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
		if errors.Is(err, domain.ErrNotFound) {
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

func (h *CommentHandlers) UnflagComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	err = h.commentService.UnflagComment(r.Context(), userID, commentID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

func (h *CommentHandlers) ModerateComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request"))
		return
	}

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

	isAdmin := shared.IsAdminFromContext(r)

	err = h.commentService.ModerateComment(r.Context(), userID, commentID, status, isAdmin)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("not found"))
			return
		}
		if errors.Is(err, domain.ErrUnauthorized) {
			shared.WriteError(w, http.StatusForbidden, fmt.Errorf("forbidden"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("internal server error"))
		return
	}

	w.WriteHeader(http.StatusNoContent)
}
