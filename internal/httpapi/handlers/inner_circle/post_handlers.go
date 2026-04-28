package inner_circle

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	icusecase "vidra-core/internal/usecase/inner_circle"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// PostHandler exposes channel-post CRUD routes.
type PostHandler struct {
	service *icusecase.PostService
}

// NewPostHandler builds the handler.
func NewPostHandler(service *icusecase.PostService) *PostHandler {
	return &PostHandler{service: service}
}

type postResponse struct {
	ID        string  `json:"id"`
	ChannelID string  `json:"channelId"`
	Body      string  `json:"body,omitempty"`
	TierID    *string `json:"tierId,omitempty"`
	Locked    bool    `json:"locked,omitempty"`
	CreatedAt string  `json:"createdAt"`
	UpdatedAt string  `json:"updatedAt"`
}

func toPostResponse(view icusecase.PostView) postResponse {
	p := view.Post
	return postResponse{
		ID:        p.ID.String(),
		ChannelID: p.ChannelID.String(),
		Body:      p.Body,
		TierID:    p.TierID,
		Locked:    view.Locked,
		CreatedAt: p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt: p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

func toBarePostResponse(p *domain.ChannelPost) postResponse {
	return postResponse{
		ID:        p.ID.String(),
		ChannelID: p.ChannelID.String(),
		Body:      p.Body,
		TierID:    p.TierID,
		CreatedAt: p.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt: p.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

// rawPostBody is decoded with explicit attachment detection so the service
// layer can reject the field even when callers send empty arrays.
type rawPostBody struct {
	Body        *string         `json:"body"`
	TierID      *string         `json:"tierId"`
	Attachments json.RawMessage `json:"attachments,omitempty"`
}

// List handles GET /api/v1/channels/{id}/posts.
func (h *PostHandler) List(w http.ResponseWriter, r *http.Request) {
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "channel id must be a UUID"))
		return
	}
	callerID, _ := userIDFromContext(r) // anonymous viewers OK
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	var cursor *uuid.UUID
	if c := r.URL.Query().Get("cursor"); c != "" {
		if cu, err := uuid.Parse(c); err == nil {
			cursor = &cu
		}
	}
	views, err := h.service.List(r.Context(), channelID, callerID, cursor, limit)
	if err != nil {
		if errors.Is(err, icusecase.ErrChannelNotFound) {
			shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("CHANNEL_NOT_FOUND", "channel not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("LIST_POSTS_FAILED", "failed to list posts"))
		return
	}
	out := make([]postResponse, len(views))
	for i, v := range views {
		out[i] = toPostResponse(v)
	}
	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{"data": out})
}

// Create handles POST /api/v1/channels/{id}/posts (creator only).
func (h *PostHandler) Create(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		writeUnauthorized(w)
		return
	}
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "channel id must be a UUID"))
		return
	}
	var body rawPostBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "invalid request body"))
		return
	}
	if body.Body == nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "body required"))
		return
	}
	post, err := h.service.Create(r.Context(), channelID, userID, icusecase.CreateInput{
		Body:              *body.Body,
		TierID:            body.TierID,
		HasAttachmentsRaw: len(body.Attachments) > 0,
	})
	if err != nil {
		writePostError(w, err)
		return
	}
	shared.WriteJSON(w, http.StatusCreated, toBarePostResponse(post))
}

// Update handles PATCH /api/v1/channels/{id}/posts/{post_id} (creator only).
func (h *PostHandler) Update(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		writeUnauthorized(w)
		return
	}
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "channel id must be a UUID"))
		return
	}
	postID, err := uuid.Parse(chi.URLParam(r, "post_id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "post id must be a UUID"))
		return
	}
	var body rawPostBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "invalid request body"))
		return
	}
	post, err := h.service.Update(r.Context(), postID, channelID, userID, icusecase.UpdateInput{
		Body:              body.Body,
		TierID:            body.TierID,
		HasAttachmentsRaw: len(body.Attachments) > 0,
	})
	if err != nil {
		writePostError(w, err)
		return
	}
	shared.WriteJSON(w, http.StatusOK, toBarePostResponse(post))
}

// Delete handles DELETE /api/v1/channels/{id}/posts/{post_id} (creator only).
func (h *PostHandler) Delete(w http.ResponseWriter, r *http.Request) {
	userID, ok := userIDFromContext(r)
	if !ok {
		writeUnauthorized(w)
		return
	}
	channelID, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "channel id must be a UUID"))
		return
	}
	postID, err := uuid.Parse(chi.URLParam(r, "post_id"))
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "post id must be a UUID"))
		return
	}
	if err := h.service.Delete(r.Context(), postID, channelID, userID); err != nil {
		writePostError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func writePostError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, icusecase.ErrAttachmentsRejected):
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("ATTACHMENTS_REJECTED", err.Error()))
	case errors.Is(err, icusecase.ErrPostBodyEmpty), errors.Is(err, icusecase.ErrPostBodyTooLong):
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_BODY", err.Error()))
	case errors.Is(err, icusecase.ErrChannelNotFound):
		shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("CHANNEL_NOT_FOUND", "channel not found"))
	case errors.Is(err, icusecase.ErrNotChannelOwner):
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("NOT_CHANNEL_OWNER", "only the channel owner can write posts"))
	default:
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("POST_OP_FAILED", err.Error()))
	}
}
