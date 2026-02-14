package federation

import (
	"athena/internal/httpapi/shared"
	"encoding/json"
	"fmt"
	"net/http"

	"athena/internal/domain"

	"github.com/go-chi/chi/v5"
)

type RedundancyHandler struct {
	service   RedundancyServiceInterface
	discovery InstanceDiscoveryInterface
}

func NewRedundancyHandler(service RedundancyServiceInterface, discovery InstanceDiscoveryInterface) *RedundancyHandler {
	return &RedundancyHandler{
		service:   service,
		discovery: discovery,
	}
}

func (h *RedundancyHandler) RegisterRoutes(r chi.Router) {
	r.Route("/api/v1/admin/redundancy", func(r chi.Router) {
		r.Get("/instances", h.ListInstancePeers)
		r.Post("/instances", h.RegisterInstancePeer)
		r.Get("/instances/{id}", h.GetInstancePeer)
		r.Put("/instances/{id}", h.UpdateInstancePeer)
		r.Delete("/instances/{id}", h.DeleteInstancePeer)
		r.Post("/instances/discover", h.DiscoverInstance)

		r.Get("/policies", h.ListPolicies)
		r.Post("/policies", h.CreatePolicy)
		r.Get("/policies/{id}", h.GetPolicy)
		r.Put("/policies/{id}", h.UpdatePolicy)
		r.Delete("/policies/{id}", h.DeletePolicy)
		r.Post("/policies/evaluate", h.EvaluatePolicies)

		r.Post("/create", h.CreateRedundancy)
		r.Get("/redundancies/{id}", h.GetRedundancy)
		r.Delete("/redundancies/{id}", h.DeleteRedundancy)
		r.Post("/redundancies/{id}/cancel", h.CancelRedundancy)
		r.Post("/redundancies/{id}/sync", h.SyncRedundancy)

		r.Get("/stats", h.GetStats)
	})

	r.Route("/api/v1/redundancy", func(r chi.Router) {
		r.Get("/videos/{id}/redundancies", h.ListVideoRedundancies)
		r.Get("/videos/{id}/health", h.GetVideoHealth)
	})
}

func (h *RedundancyHandler) ListInstancePeers(w http.ResponseWriter, r *http.Request) {
	limit := shared.GetIntParam(r, "limit", 50)
	offset := shared.GetIntParam(r, "offset", 0)
	activeOnly := shared.GetBoolParam(r, "active_only", false)

	peers, err := h.service.ListInstancePeers(r.Context(), limit, offset, activeOnly)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to list instance peers: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"instances": peers,
		"limit":     limit,
		"offset":    offset,
	})
}

func (h *RedundancyHandler) RegisterInstancePeer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InstanceURL          string `json:"instance_url"`
		AutoAcceptRedundancy bool   `json:"auto_accept_redundancy"`
		MaxRedundancySizeGB  int    `json:"max_redundancy_size_gb"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.InstanceURL == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("instance_url is required"))
		return
	}

	peer := &domain.InstancePeer{
		InstanceURL:          req.InstanceURL,
		AutoAcceptRedundancy: req.AutoAcceptRedundancy,
		MaxRedundancySizeGB:  req.MaxRedundancySizeGB,
		AcceptsNewRedundancy: true,
		IsActive:             true,
	}

	if err := h.service.RegisterInstancePeer(r.Context(), peer); err != nil {
		if err == domain.ErrInstancePeerAlreadyExists {
			shared.WriteError(w, http.StatusConflict, fmt.Errorf("instance peer already exists: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to register instance peer: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusCreated, peer)
}

func (h *RedundancyHandler) GetInstancePeer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	peer, err := h.service.GetInstancePeer(r.Context(), id)
	if err != nil {
		if err == domain.ErrInstancePeerNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("instance peer not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get instance peer: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, peer)
}

func (h *RedundancyHandler) UpdateInstancePeer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req struct {
		AutoAcceptRedundancy bool `json:"auto_accept_redundancy"`
		MaxRedundancySizeGB  int  `json:"max_redundancy_size_gb"`
		AcceptsNewRedundancy bool `json:"accepts_new_redundancy"`
		IsActive             bool `json:"is_active"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	peer, err := h.service.GetInstancePeer(r.Context(), id)
	if err != nil {
		if err == domain.ErrInstancePeerNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("instance peer not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get instance peer: %w", err))
		}
		return
	}

	peer.AutoAcceptRedundancy = req.AutoAcceptRedundancy
	peer.MaxRedundancySizeGB = req.MaxRedundancySizeGB
	peer.AcceptsNewRedundancy = req.AcceptsNewRedundancy
	peer.IsActive = req.IsActive

	if err := h.service.UpdateInstancePeer(r.Context(), peer); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to update instance peer: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, peer)
}

func (h *RedundancyHandler) DeleteInstancePeer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.DeleteInstancePeer(r.Context(), id); err != nil {
		if err == domain.ErrInstancePeerNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("instance peer not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to delete instance peer: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Instance peer deleted successfully",
	})
}

func (h *RedundancyHandler) DiscoverInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InstanceURL string `json:"instance_url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.InstanceURL == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("instance_url is required"))
		return
	}

	peer, err := h.discovery.DiscoverInstance(r.Context(), req.InstanceURL)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to discover instance: %w", err))
		return
	}

	if err := h.service.RegisterInstancePeer(r.Context(), peer); err != nil {
		if err == domain.ErrInstancePeerAlreadyExists {
			shared.WriteError(w, http.StatusConflict, fmt.Errorf("instance peer already exists: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to register instance peer: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusCreated, peer)
}

func (h *RedundancyHandler) CreateRedundancy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VideoID    string `json:"video_id"`
		InstanceID string `json:"instance_id"`
		Strategy   string `json:"strategy"`
		Priority   int    `json:"priority"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if req.VideoID == "" || req.InstanceID == "" {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("video_id and instance_id are required"))
		return
	}

	strategy := domain.RedundancyStrategy(req.Strategy)
	if strategy == "" {
		strategy = domain.RedundancyStrategyManual
	}

	redundancy, err := h.service.CreateRedundancy(r.Context(), req.VideoID, req.InstanceID, strategy, req.Priority)
	if err != nil {
		switch err {
		case domain.ErrRedundancyAlreadyExists:
			shared.WriteError(w, http.StatusConflict, fmt.Errorf("redundancy already exists: %w", err))
		case domain.ErrInstancePeerInactive:
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("instance peer is inactive: %w", err))
		case domain.ErrInsufficientStorage:
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("insufficient storage on target instance: %w", err))
		default:
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to create redundancy: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusCreated, redundancy)
}

func (h *RedundancyHandler) GetRedundancy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	redundancy, err := h.service.GetRedundancy(r.Context(), id)
	if err != nil {
		if err == domain.ErrRedundancyNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("redundancy not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get redundancy: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, redundancy)
}

func (h *RedundancyHandler) ListVideoRedundancies(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")

	redundancies, err := h.service.ListVideoRedundancies(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to list video redundancies: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"video_id":     videoID,
		"redundancies": redundancies,
	})
}

func (h *RedundancyHandler) CancelRedundancy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.CancelRedundancy(r.Context(), id); err != nil {
		if err == domain.ErrRedundancyNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("redundancy not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to cancel redundancy: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Redundancy cancelled successfully",
	})
}

func (h *RedundancyHandler) SyncRedundancy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.SyncRedundancy(r.Context(), id); err != nil {
		switch err {
		case domain.ErrRedundancyNotFound:
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("redundancy not found: %w", err))
		case domain.ErrRedundancyInProgress:
			shared.WriteError(w, http.StatusConflict, fmt.Errorf("redundancy sync already in progress: %w", err))
		case domain.ErrRedundancyMaxAttempts:
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("maximum sync attempts exceeded: %w", err))
		default:
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to sync redundancy: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Redundancy sync completed successfully",
	})
}

func (h *RedundancyHandler) DeleteRedundancy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.DeleteRedundancy(r.Context(), id); err != nil {
		if err == domain.ErrRedundancyNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("redundancy not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to delete redundancy: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Redundancy deleted successfully",
	})
}

func (h *RedundancyHandler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	enabledOnly := shared.GetBoolParam(r, "enabled_only", false)

	policies, err := h.service.ListPolicies(r.Context(), enabledOnly)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to list policies: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"policies": policies,
	})
}

func (h *RedundancyHandler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	var policy domain.RedundancyPolicy

	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	if err := h.service.CreatePolicy(r.Context(), &policy); err != nil {
		if err == domain.ErrPolicyAlreadyExists {
			shared.WriteError(w, http.StatusConflict, fmt.Errorf("policy already exists: %w", err))
		} else {
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("failed to create policy: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusCreated, policy)
}

func (h *RedundancyHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	policy, err := h.service.GetPolicy(r.Context(), id)
	if err != nil {
		if err == domain.ErrPolicyNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("policy not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get policy: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, policy)
}

func (h *RedundancyHandler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var policy domain.RedundancyPolicy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid request body: %w", err))
		return
	}

	policy.ID = id

	if err := h.service.UpdatePolicy(r.Context(), &policy); err != nil {
		if err == domain.ErrPolicyNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("policy not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("failed to update policy: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, policy)
}

func (h *RedundancyHandler) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.DeletePolicy(r.Context(), id); err != nil {
		if err == domain.ErrPolicyNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("policy not found: %w", err))
		} else {
			shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to delete policy: %w", err))
		}
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{
		"message": "Policy deleted successfully",
	})
}

func (h *RedundancyHandler) EvaluatePolicies(w http.ResponseWriter, r *http.Request) {
	count, err := h.service.EvaluatePolicies(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to evaluate policies: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"message":              "Policies evaluated successfully",
		"redundancies_created": count,
	})
}

func (h *RedundancyHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.GetStats(r.Context())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get statistics: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, stats)
}

func (h *RedundancyHandler) GetVideoHealth(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")

	health, err := h.service.GetVideoHealth(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to get video health: %w", err))
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]interface{}{
		"video_id":     videoID,
		"health_score": health,
	})
}

// Note: Helper functions (getIntParam, getBoolParam, WriteJSON, WriteError)
