package federation

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/port"
)

// ServerFollowingHandlers handles instance following/followers endpoints.
type ServerFollowingHandlers struct {
	repo        port.ServerFollowingRepository
	apService   port.ActivityPubService
	instanceURL string
}

// NewServerFollowingHandlers returns a new ServerFollowingHandlers.
func NewServerFollowingHandlers(repo port.ServerFollowingRepository) *ServerFollowingHandlers {
	return &ServerFollowingHandlers{repo: repo}
}

// SetActivityPubService injects an ActivityPub service for emitting Follow activities.
// When set, FollowInstance will asynchronously deliver an ActivityPub Follow activity
// to the target instance after the database record is created.
func (h *ServerFollowingHandlers) SetActivityPubService(svc port.ActivityPubService, instanceURL string) {
	h.apService = svc
	h.instanceURL = instanceURL
}

// ListFollowers handles GET /server/followers
func (h *ServerFollowingHandlers) ListFollowers(w http.ResponseWriter, r *http.Request) {
	followers, err := h.repo.ListFollowers(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list followers"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, followers)
}

// ListFollowing handles GET /server/following
func (h *ServerFollowingHandlers) ListFollowing(w http.ResponseWriter, r *http.Request) {
	following, err := h.repo.ListFollowing(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list following"))
		return
	}
	shared.WriteJSON(w, http.StatusOK, following)
}

// FollowInstance handles POST /server/following
func (h *ServerFollowingHandlers) FollowInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Host string `json:"host"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil || req.Host == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_REQUEST", "host is required"))
		return
	}
	if err := h.repo.Follow(r.Context(), req.Host); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to follow instance"))
		return
	}
	// Emit ActivityPub Follow activity asynchronously (best-effort).
	// Uses the authenticated admin's actor ID as the sending actor.
	if h.apService != nil {
		actorUID, ok := middleware.GetUserIDFromContext(r.Context())
		if ok {
			go h.emitFollowActivity(context.Background(), req.Host, actorUID.String())
		}
	}
	w.WriteHeader(http.StatusNoContent)
}

// emitFollowActivity delivers an ActivityPub Follow activity to the target instance.
// Errors are logged but do not affect the caller — delivery is best-effort.
func (h *ServerFollowingHandlers) emitFollowActivity(ctx context.Context, host, actorID string) {
	// PeerTube-compatible server actor URI on the target instance.
	targetActorURI := "https://" + host + "/accounts/peertube"

	remoteActor, err := h.apService.FetchRemoteActor(ctx, targetActorURI)
	if err != nil {
		slog.Info(fmt.Sprintf("federation: failed to fetch remote actor for %s: %v", host, err))
		return
	}

	followActivity := map[string]interface{}{
		"@context": domain.ActivityStreamsContext,
		"type":     domain.ActivityTypeFollow,
		"id":       fmt.Sprintf("%s/activities/follow/%s", h.instanceURL, host),
		"actor":    fmt.Sprintf("%s/users/%s", h.instanceURL, actorID),
		"object":   targetActorURI,
	}

	if err := h.apService.DeliverActivity(ctx, actorID, remoteActor.InboxURL, followActivity); err != nil {
		slog.Info(fmt.Sprintf("federation: failed to deliver Follow activity to %s: %v", host, err))
	}
}

// UnfollowInstance handles DELETE /server/following/{host}
func (h *ServerFollowingHandlers) UnfollowInstance(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r, "host")
	if err := h.repo.Unfollow(r.Context(), host); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to unfollow instance"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// AcceptFollower handles POST /server/followers/{host}/accept
func (h *ServerFollowingHandlers) AcceptFollower(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r, "host")
	if err := h.repo.SetFollowerState(r.Context(), host, domain.ServerFollowingStateAccepted); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to accept follower"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// RejectFollower handles POST /server/followers/{host}/reject
func (h *ServerFollowingHandlers) RejectFollower(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r, "host")
	if err := h.repo.SetFollowerState(r.Context(), host, domain.ServerFollowingStateRejected); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to reject follower"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// DeleteFollower handles DELETE /server/followers/{host}
func (h *ServerFollowingHandlers) DeleteFollower(w http.ResponseWriter, r *http.Request) {
	host := chi.URLParam(r, "host")
	if err := h.repo.DeleteFollower(r.Context(), host); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to delete follower"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
