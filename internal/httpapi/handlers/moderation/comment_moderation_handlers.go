package moderation

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/port"
)

// CommentModerationHandlers handles comment approval, admin listing,
// and bulk removal operations.
type CommentModerationHandlers struct {
	commentRepo port.CommentRepository
}

// NewCommentModerationHandlers creates a new CommentModerationHandlers instance.
func NewCommentModerationHandlers(commentRepo port.CommentRepository) *CommentModerationHandlers {
	return &CommentModerationHandlers{commentRepo: commentRepo}
}

// ApproveComment handles POST /api/v1/comments/{commentId}/approve
func (h *CommentModerationHandlers) ApproveComment(w http.ResponseWriter, r *http.Request) {
	commentIDStr := chi.URLParam(r, "commentId")
	commentID, err := uuid.Parse(commentIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid comment ID"))
		return
	}

	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	if err := h.commentRepo.Approve(r.Context(), commentID); err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	w.WriteHeader(http.StatusNoContent)
}

// ListAllComments handles GET /api/v1/admin/comments
func (h *CommentModerationHandlers) ListAllComments(w http.ResponseWriter, r *http.Request) {
	opts := domain.AdminCommentListOptions{
		Limit:   20,
		Offset:  0,
		OrderBy: "newest",
	}

	if v := r.URL.Query().Get("limit"); v != "" {
		if limit, err := strconv.Atoi(v); err == nil && limit > 0 && limit <= 100 {
			opts.Limit = limit
		}
	}
	if v := r.URL.Query().Get("offset"); v != "" {
		if offset, err := strconv.Atoi(v); err == nil && offset >= 0 {
			opts.Offset = offset
		}
	}
	if v := r.URL.Query().Get("videoId"); v != "" {
		if vid, err := uuid.Parse(v); err == nil {
			opts.VideoID = &vid
		}
	}
	if v := r.URL.Query().Get("accountName"); v != "" {
		opts.AccountName = &v
	}
	if v := r.URL.Query().Get("status"); v != "" {
		status := domain.CommentStatus(v)
		opts.Status = &status
	}
	if v := r.URL.Query().Get("heldForReview"); v != "" {
		held := v == "true"
		opts.HeldForReview = &held
	}
	if v := r.URL.Query().Get("search"); v != "" {
		opts.SearchText = &v
	}
	if v := r.URL.Query().Get("orderBy"); v != "" {
		opts.OrderBy = v
	}

	comments, total, err := h.commentRepo.ListAll(r.Context(), opts)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to list comments"))
		return
	}

	shared.WriteJSONWithMeta(w, http.StatusOK, comments, &shared.Meta{
		Total:  total,
		Limit:  opts.Limit,
		Offset: opts.Offset,
	})
}

// BulkRemoveComments handles POST /api/v1/bulk/comments/remove
func (h *CommentModerationHandlers) BulkRemoveComments(w http.ResponseWriter, r *http.Request) {
	_, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("authentication required"))
		return
	}

	var req domain.BulkRemoveCommentsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body"))
		return
	}

	if req.AccountName == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("accountName is required"))
		return
	}

	removed, err := h.commentRepo.BulkRemoveByAccount(r.Context(), req.AccountName)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to bulk remove comments"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]int64{"removed": removed})
}
