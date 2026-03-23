package social

import (
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"time"

	"athena/internal/usecase"

	"github.com/go-chi/chi/v5"
)

type SocialHandler struct {
	socialService *usecase.SocialService
}

func NewSocialHandler(socialService *usecase.SocialService) *SocialHandler {
	return &SocialHandler{
		socialService: socialService,
	}
}

func decodeURIParam(r *http.Request, name string) (string, error) {
	uri := chi.URLParam(r, name)
	if uri == "" {
		return "", nil
	}

	decoded, err := url.PathUnescape(uri)
	if err != nil {
		return "", err
	}

	return decoded, nil
}

func (h *SocialHandler) RegisterRoutes(r chi.Router, jwtSecret string) {
	r.Route("/social", func(r chi.Router) {
		r.Get("/actors/{handle}", h.GetActor)
		r.Get("/actors/{handle}/stats", h.GetActorStats)
		r.Get("/followers/{handle}", h.GetFollowers)
		r.Get("/following/{handle}", h.GetFollowing)
		r.Get("/likes/{uri}", h.GetLikes)
		r.Get("/comments/{uri}", h.GetComments)
		r.Get("/comments/{uri}/thread", h.GetCommentThread)
		r.Get("/moderation/labels/{did}", h.GetLabels)

		r.Group(func(r chi.Router) {
			r.Use(middleware.Auth(jwtSecret))
			r.Post("/follow", h.Follow)
			r.Delete("/follow/{handle}", h.Unfollow)
			r.Post("/like", h.Like)
			r.Delete("/like", h.Unlike)
			r.Post("/comment", h.CreateComment)
			r.Delete("/comment/{uri}", h.DeleteComment)
			r.Post("/moderation/label", h.ApplyLabel)
			r.Delete("/moderation/label/{id}", h.RemoveLabel)
			r.Post("/ingest/{handle}", h.IngestFeed)
		})
	})
}

func (h *SocialHandler) GetActor(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")

	actor, err := h.socialService.ResolveActor(r.Context(), handle)
	if err != nil {
		shared.WriteError(w, http.StatusNotFound, errors.New("actor not found"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, actor)
}

func (h *SocialHandler) GetActorStats(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")

	stats, err := h.socialService.GetSocialStats(r.Context(), handle)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get actor stats"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, stats)
}

type FollowRequest struct {
	Target string `json:"target"`
}

func (h *SocialHandler) Follow(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	var req FollowRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	if err := h.socialService.Follow(r.Context(), userID.String(), req.Target); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to follow"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "followed"})
}

func (h *SocialHandler) Unfollow(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, errors.New("unauthorized"))
		return
	}

	handle := chi.URLParam(r, "handle")

	if err := h.socialService.Unfollow(r.Context(), userID.String(), handle); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to unfollow"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "unfollowed"})
}

func (h *SocialHandler) GetFollowers(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	followers, err := h.socialService.GetFollowers(r.Context(), handle, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get followers"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, followers)
}

func (h *SocialHandler) GetFollowing(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	following, err := h.socialService.GetFollowing(r.Context(), handle, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get following"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, following)
}

type LikeRequest struct {
	ActorDID   string `json:"actor_did"`
	SubjectURI string `json:"subject_uri"`
	SubjectCID string `json:"subject_cid,omitempty"`
}

func (h *SocialHandler) Like(w http.ResponseWriter, r *http.Request) {
	var req LikeRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	if err := h.socialService.Like(r.Context(), req.ActorDID, req.SubjectURI, req.SubjectCID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to like"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "liked"})
}

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
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to unlike"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "unliked"})
}

func (h *SocialHandler) GetLikes(w http.ResponseWriter, r *http.Request) {
	uri, err := decodeURIParam(r, "uri")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid uri"))
		return
	}
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	likes, err := h.socialService.GetLikes(r.Context(), uri, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get likes"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, likes)
}

type CommentRequest struct {
	ActorDID  string `json:"actor_did"`
	Text      string `json:"text"`
	RootURI   string `json:"root_uri"`
	RootCID   string `json:"root_cid,omitempty"`
	ParentURI string `json:"parent_uri,omitempty"`
	ParentCID string `json:"parent_cid,omitempty"`
}

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
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to create comment"))
		return
	}

	shared.WriteJSON(w, http.StatusCreated, comment)
}

func (h *SocialHandler) DeleteComment(w http.ResponseWriter, r *http.Request) {
	uri, err := decodeURIParam(r, "uri")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid uri"))
		return
	}

	if err := h.socialService.DeleteComment(r.Context(), uri); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to delete comment"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
}

func (h *SocialHandler) GetComments(w http.ResponseWriter, r *http.Request) {
	uri, err := decodeURIParam(r, "uri")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid uri"))
		return
	}
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	comments, err := h.socialService.GetComments(r.Context(), uri, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get comments"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, comments)
}

func (h *SocialHandler) GetCommentThread(w http.ResponseWriter, r *http.Request) {
	uri, err := decodeURIParam(r, "uri")
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid uri"))
		return
	}
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)

	thread, err := h.socialService.GetCommentThread(r.Context(), uri, limit, offset)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get thread"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, thread)
}

type LabelRequest struct {
	ActorDID  string `json:"actor_did"`
	LabelType string `json:"label_type"`
	Reason    string `json:"reason,omitempty"`
	AppliedBy string `json:"applied_by"`
	URI       string `json:"uri,omitempty"`
	ExpiresIn int    `json:"expires_in,omitempty"`
}

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
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to apply label"))
		return
	}

	response := map[string]string{"status": "label_applied"}
	if labels, listErr := h.socialService.GetModerationLabels(r.Context(), req.ActorDID); listErr == nil {
		for _, label := range labels {
			if label.LabelType != req.LabelType || label.AppliedBy != req.AppliedBy {
				continue
			}
			if req.URI == "" && label.URI != nil {
				continue
			}
			if req.URI != "" && (label.URI == nil || *label.URI != req.URI) {
				continue
			}
			response["id"] = label.ID
			break
		}
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

func (h *SocialHandler) RemoveLabel(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.socialService.RemoveModerationLabel(r.Context(), id); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to remove label"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "label_removed"})
}

func (h *SocialHandler) GetLabels(w http.ResponseWriter, r *http.Request) {
	did := chi.URLParam(r, "did")

	labels, err := h.socialService.GetModerationLabels(r.Context(), did)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to get labels"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, labels)
}

func (h *SocialHandler) IngestFeed(w http.ResponseWriter, r *http.Request) {
	handle := chi.URLParam(r, "handle")
	limit := shared.GetIntParam(r, "limit", 50)

	if err := h.socialService.IngestActorFeed(r.Context(), handle, limit); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, errors.New("failed to ingest feed"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "ingested"})
}
