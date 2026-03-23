package federation

import (
	"encoding/json"
	"net/http"

	chi "github.com/go-chi/chi/v5"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/port"
)

// ServerFollowingHandlers handles instance following/followers endpoints.
type ServerFollowingHandlers struct {
	repo port.ServerFollowingRepository
}

// NewServerFollowingHandlers returns a new ServerFollowingHandlers.
func NewServerFollowingHandlers(repo port.ServerFollowingRepository) *ServerFollowingHandlers {
	return &ServerFollowingHandlers{repo: repo}
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
	// TODO: emit ActivityPub Follow activity once federation wiring is verified end-to-end
	if err := h.repo.Follow(r.Context(), req.Host); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to follow instance"))
		return
	}
	w.WriteHeader(http.StatusNoContent)
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
