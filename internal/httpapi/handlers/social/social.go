package social

import (
	"athena/internal/httpapi/shared"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"athena/internal/usecase"

	"github.com/go-chi/chi/v5"
)

// SocialHandler handles social interaction endpoints
type SocialHandler struct {
	socialService *usecase.SocialService
}

// NewSocialHandler creates a new social handler
func NewSocialHandler(socialService *usecase.SocialService) *SocialHandler {
	return &SocialHandler{
		socialService: socialService,
	}
}

// RegisterRoutes registers social API routes
func (h *SocialHandler) RegisterRoutes(r chi.Router) {
	r.Route("/social", func(r chi.Router) {
		// Actor endpoints
		r.Get("/actors/{handle}", h.GetActor)
		r.Get("/actors/{handle}/stats", h.GetActorStats)

		// Follow endpoints
		r.Post("/follow", h.Follow)
		r.Delete("/follow/{handle}", h.Unfollow)
		r.Get("/followers/{handle}", h.GetFollowers)
		r.Get("/following/{handle}", h.GetFollowing)

		// Like endpoints
		r.Post("/like", h.Like)
		r.Delete("/like", h.Unlike)
		r.Get("/likes/{uri}", h.GetLikes)

		// Comment endpoints
		r.Post("/comment", h.CreateComment)
		r.Delete("/comment/{uri}", h.DeleteComment)
		r.Get("/comments/{uri}", h.GetComments)
		r.Get("/comments/{uri}/thread", h.GetCommentThread)

		// Moderation endpoints
		r.Post("/moderation/label", h.ApplyLabel)
		r.Delete("/moderation/label/{id}", h.RemoveLabel)
		r.Get("/moderation/labels/{did}", h.GetLabels)

		// Feed ingestion
		r.Post("/ingest/{handle}", h.IngestFeed)
	})
}

// GetActor retrieves actor profile
func (h *SocialHandler) GetActor(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")

	actor, err := h.socialService.ResolveActor(r.Context(), handle)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, actor)
}

// GetActorStats retrieves social statistics for an actor
func (h *SocialHandler) GetActorStats(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")

	stats, err := h.socialService.GetSocialStats(r.Context(), handle)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, stats)
}

// FollowRequest represents a follow request
type FollowRequest struct {
	FollowerDID string `json:"follower_did"`
	Target      string `json:"target"` // Handle or DID
}

// Follow creates a follow relationship
func (h *SocialHandler) Follow(w http.ResponseWriter, r *http.Request) {
	var req FollowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	if err := h.socialService.Follow(r.Context(), req.FollowerDID, req.Target); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "followed"})
}

// Unfollow removes a follow relationship
func (h *SocialHandler) Unfollow(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	followerDID := r.URL.Query().Get("follower_did")

	if followerDID == "" {
		shared.WriteError(w, http.StatusBadRequest, errors.New("follower_did required"))
		return
	}

	if err := h.socialService.Unfollow(r.Context(), followerDID, handle); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "unfollowed"})
}

// GetFollowers retrieves followers list
func (h *SocialHandler) GetFollowers(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	followers, err := h.socialService.GetFollowers(r.Context(), handle, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get followers: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, followers)
}

// GetFollowing retrieves following list
func (h *SocialHandler) GetFollowing(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	following, err := h.socialService.GetFollowing(r.Context(), handle, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get following: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, following)
}

// LikeRequest represents a like request
type LikeRequest struct {
	ActorDID   string `json:"actor_did"`
	SubjectURI string `json:"subject_uri"`
	SubjectCID string `json:"subject_cid,omitempty"`
}

// Like creates a like
func (h *SocialHandler) Like(w http.ResponseWriter, r *http.Request) {
	var req LikeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	if err := h.socialService.Like(r.Context(), req.ActorDID, req.SubjectURI, req.SubjectCID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "liked"})
}

// Unlike removes a like
func (h *SocialHandler) Unlike(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ActorDID   string `json:"actor_did"`
		SubjectURI string `json:"subject_uri"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	if err := h.socialService.Unlike(r.Context(), req.ActorDID, req.SubjectURI); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "unliked"})
}

// GetLikes retrieves likes for a subject
func (h *SocialHandler) GetLikes(w http.ResponseWriter, r *http.Request) {
	uri := chi.URLParam(r, "uri")
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	likes, err := h.socialService.GetLikes(r.Context(), uri, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get likes: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, likes)
}

// CommentRequest represents a comment request
type CommentRequest struct {
	ActorDID  string `json:"actor_did"`
	Text      string `json:"text"`
	RootURI   string `json:"root_uri"`
	RootCID   string `json:"root_cid,omitempty"`
	ParentURI string `json:"parent_uri,omitempty"`
	ParentCID string `json:"parent_cid,omitempty"`
}

// CreateComment creates a new comment
func (h *SocialHandler) CreateComment(w http.ResponseWriter, r *http.Request) {
	var req CommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	comment, err := h.socialService.Comment(
		r.Context(),
		req.ActorDID,
		req.Text,
		req.RootURI,
		req.RootCID,
		req.ParentURI,
		req.ParentCID,
	)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusCreated, comment)
}

// DeleteComment removes a comment
func (h *SocialHandler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	uri := chi.URLParam(r, "uri")

	if err := h.socialService.DeleteComment(r.Context(), uri); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

// GetComments retrieves comments for a subject
func (h *SocialHandler) GetComments(w http.ResponseWriter, r *http.Request) {
	uri := chi.URLParam(r, "uri")
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	comments, err := h.socialService.GetComments(r.Context(), uri, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get comments: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, comments)
}

// GetCommentThread retrieves a comment thread
func (h *SocialHandler) GetCommentThread(w http.ResponseWriter, r *http.Request) {
	uri := chi.URLParam(r, "uri")
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	thread, err := h.socialService.GetCommentThread(r.Context(), uri, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get thread: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, thread)
}

// LabelRequest represents a moderation label request
type LabelRequest struct {
	ActorDID  string `json:"actor_did"`
	LabelType string `json:"label_type"`
	Reason    string `json:"reason,omitempty"`
	AppliedBy string `json:"applied_by"`
	URI       string `json:"uri,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"` // Minutes
}

// ApplyLabel applies a moderation label
func (h *SocialHandler) ApplyLabel(w http.ResponseWriter, r *http.Request) {
	var req LabelRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	var expiration time.Duration
	if req.ExpiresIn > 0 {
		expiration = time.Duration(req.ExpiresIn) * time.Minute
	}

	err := h.socialService.ApplyModerationLabel(
		r.Context(),
		req.ActorDID,
		req.LabelType,
		req.Reason,
		req.AppliedBy,
		req.URI,
		expiration,
	)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "label_applied"})
}

// RemoveLabel removes a moderation label
func (h *SocialHandler) RemoveLabel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.socialService.RemoveModerationLabel(r.Context(), id); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "label_removed"})
}

// GetLabels retrieves moderation labels for an actor
func (h *SocialHandler) GetLabels(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")

	labels, err := h.socialService.GetModerationLabels(r.Context(), did)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get labels: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, labels)
}

// IngestFeed ingests an actor's feed
func (h *SocialHandler) IngestFeed(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	limit := shared.GetIntParam(r, "limit", 50)

	if err := h.socialService.IngestActorFeed(r.Context(), handle, limit); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ingested"})
}
