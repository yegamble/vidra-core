package httpapi

import (
	"encoding/json"
	"net/http"

	"athena/internal/domain"
	"athena/internal/usecase/redundancy"

	"github.com/go-chi/chi/v5"
)

// RedundancyHandler handles HTTP requests for video redundancy
type RedundancyHandler struct {
	service   *redundancy.Service
	discovery *redundancy.InstanceDiscovery
}

// NewRedundancyHandler creates a new redundancy handler
func NewRedundancyHandler(service *redundancy.Service, discovery *redundancy.InstanceDiscovery) *RedundancyHandler {
	return &RedundancyHandler{
		service:   service,
		discovery: discovery,
	}
}

// RegisterRoutes registers redundancy routes
func (h *RedundancyHandler) RegisterRoutes(r chi.Router) {
	r.Route("/api/v1/admin/redundancy", func(r chi.Router) {
		// Instance peer management
		r.Get("/instances", h.ListInstancePeers)
		r.Post("/instances", h.RegisterInstancePeer)
		r.Get("/instances/{id}", h.GetInstancePeer)
		r.Put("/instances/{id}", h.UpdateInstancePeer)
		r.Delete("/instances/{id}", h.DeleteInstancePeer)
		r.Post("/instances/discover", h.DiscoverInstance)

		// Policy management
		r.Get("/policies", h.ListPolicies)
		r.Post("/policies", h.CreatePolicy)
		r.Get("/policies/{id}", h.GetPolicy)
		r.Put("/policies/{id}", h.UpdatePolicy)
		r.Delete("/policies/{id}", h.DeletePolicy)
		r.Post("/policies/evaluate", h.EvaluatePolicies)

		// Redundancy management
		r.Post("/create", h.CreateRedundancy)
		r.Get("/redundancies/{id}", h.GetRedundancy)
		r.Delete("/redundancies/{id}", h.DeleteRedundancy)
		r.Post("/redundancies/{id}/cancel", h.CancelRedundancy)
		r.Post("/redundancies/{id}/sync", h.SyncRedundancy)

		// Statistics
		r.Get("/stats", h.GetStats)
	})

	// Public redundancy endpoints
	r.Route("/api/v1/redundancy", func(r chi.Router) {
		r.Get("/videos/{id}/redundancies", h.ListVideoRedundancies)
		r.Get("/videos/{id}/health", h.GetVideoHealth)
	})
}

// ==================== Instance Peer Handlers ====================

// ListInstancePeers lists all instance peers
// GET /api/v1/admin/redundancy/instances?limit=50&offset=0&active_only=true
func (h *RedundancyHandler) ListInstancePeers(w http.ResponseWriter, r *http.Request) {
	limit := getIntParam(r, "limit", 50)
	offset := getIntParam(r, "offset", 0)
	activeOnly := getBoolParam(r, "active_only", false)

	peers, err := h.service.ListInstancePeers(r.Context(), limit, offset, activeOnly)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to list instance peers", err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"instances": peers,
		"limit":     limit,
		"offset":    offset,
	})
}

// RegisterInstancePeer registers a new instance peer
// POST /api/v1/admin/redundancy/instances
func (h *RedundancyHandler) RegisterInstancePeer(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InstanceURL          string `json:"instance_url"`
		AutoAcceptRedundancy bool   `json:"auto_accept_redundancy"`
		MaxRedundancySizeGB  int    `json:"max_redundancy_size_gb"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	if req.InstanceURL == "" {
		respondError(w, http.StatusBadRequest, "instance_url is required", nil)
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
			respondError(w, http.StatusConflict, "Instance peer already exists", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to register instance peer", err)
		}
		return
	}

	respondJSON(w, http.StatusCreated, peer)
}

// GetInstancePeer retrieves an instance peer
// GET /api/v1/admin/redundancy/instances/{id}
func (h *RedundancyHandler) GetInstancePeer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	peer, err := h.service.GetInstancePeer(r.Context(), id)
	if err != nil {
		if err == domain.ErrInstancePeerNotFound {
			respondError(w, http.StatusNotFound, "Instance peer not found", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to get instance peer", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, peer)
}

// UpdateInstancePeer updates an instance peer
// PUT /api/v1/admin/redundancy/instances/{id}
func (h *RedundancyHandler) UpdateInstancePeer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var req struct {
		AutoAcceptRedundancy bool `json:"auto_accept_redundancy"`
		MaxRedundancySizeGB  int  `json:"max_redundancy_size_gb"`
		AcceptsNewRedundancy bool `json:"accepts_new_redundancy"`
		IsActive             bool `json:"is_active"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	peer, err := h.service.GetInstancePeer(r.Context(), id)
	if err != nil {
		if err == domain.ErrInstancePeerNotFound {
			respondError(w, http.StatusNotFound, "Instance peer not found", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to get instance peer", err)
		}
		return
	}

	// Update fields
	peer.AutoAcceptRedundancy = req.AutoAcceptRedundancy
	peer.MaxRedundancySizeGB = req.MaxRedundancySizeGB
	peer.AcceptsNewRedundancy = req.AcceptsNewRedundancy
	peer.IsActive = req.IsActive

	if err := h.service.UpdateInstancePeer(r.Context(), peer); err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to update instance peer", err)
		return
	}

	respondJSON(w, http.StatusOK, peer)
}

// DeleteInstancePeer deletes an instance peer
// DELETE /api/v1/admin/redundancy/instances/{id}
func (h *RedundancyHandler) DeleteInstancePeer(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.DeleteInstancePeer(r.Context(), id); err != nil {
		if err == domain.ErrInstancePeerNotFound {
			respondError(w, http.StatusNotFound, "Instance peer not found", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to delete instance peer", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Instance peer deleted successfully",
	})
}

// DiscoverInstance discovers and registers a new instance
// POST /api/v1/admin/redundancy/instances/discover
func (h *RedundancyHandler) DiscoverInstance(w http.ResponseWriter, r *http.Request) {
	var req struct {
		InstanceURL string `json:"instance_url"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	if req.InstanceURL == "" {
		respondError(w, http.StatusBadRequest, "instance_url is required", nil)
		return
	}

	peer, err := h.discovery.DiscoverInstance(r.Context(), req.InstanceURL)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to discover instance", err)
		return
	}

	// Register the discovered instance
	if err := h.service.RegisterInstancePeer(r.Context(), peer); err != nil {
		if err == domain.ErrInstancePeerAlreadyExists {
			respondError(w, http.StatusConflict, "Instance peer already exists", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to register instance peer", err)
		}
		return
	}

	respondJSON(w, http.StatusCreated, peer)
}

// ==================== Redundancy Handlers ====================

// CreateRedundancy creates a new video redundancy
// POST /api/v1/admin/redundancy/create
func (h *RedundancyHandler) CreateRedundancy(w http.ResponseWriter, r *http.Request) {
	var req struct {
		VideoID    string `json:"video_id"`
		InstanceID string `json:"instance_id"`
		Strategy   string `json:"strategy"`
		Priority   int    `json:"priority"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	if req.VideoID == "" || req.InstanceID == "" {
		respondError(w, http.StatusBadRequest, "video_id and instance_id are required", nil)
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
			respondError(w, http.StatusConflict, "Redundancy already exists", err)
		case domain.ErrInstancePeerInactive:
			respondError(w, http.StatusBadRequest, "Instance peer is inactive", err)
		case domain.ErrInsufficientStorage:
			respondError(w, http.StatusBadRequest, "Insufficient storage on target instance", err)
		default:
			respondError(w, http.StatusInternalServerError, "Failed to create redundancy", err)
		}
		return
	}

	respondJSON(w, http.StatusCreated, redundancy)
}

// GetRedundancy retrieves a redundancy
// GET /api/v1/admin/redundancy/redundancies/{id}
func (h *RedundancyHandler) GetRedundancy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	redundancy, err := h.service.GetRedundancy(r.Context(), id)
	if err != nil {
		if err == domain.ErrRedundancyNotFound {
			respondError(w, http.StatusNotFound, "Redundancy not found", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to get redundancy", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, redundancy)
}

// ListVideoRedundancies lists redundancies for a video
// GET /api/v1/redundancy/videos/{id}/redundancies
func (h *RedundancyHandler) ListVideoRedundancies(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")

	redundancies, err := h.service.ListVideoRedundancies(r.Context(), videoID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to list video redundancies", err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"video_id":     videoID,
		"redundancies": redundancies,
	})
}

// CancelRedundancy cancels a redundancy sync
// POST /api/v1/admin/redundancy/redundancies/{id}/cancel
func (h *RedundancyHandler) CancelRedundancy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.CancelRedundancy(r.Context(), id); err != nil {
		if err == domain.ErrRedundancyNotFound {
			respondError(w, http.StatusNotFound, "Redundancy not found", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to cancel redundancy", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Redundancy cancelled successfully",
	})
}

// SyncRedundancy triggers a redundancy sync
// POST /api/v1/admin/redundancy/redundancies/{id}/sync
func (h *RedundancyHandler) SyncRedundancy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.SyncRedundancy(r.Context(), id); err != nil {
		switch err {
		case domain.ErrRedundancyNotFound:
			respondError(w, http.StatusNotFound, "Redundancy not found", err)
		case domain.ErrRedundancyInProgress:
			respondError(w, http.StatusConflict, "Redundancy sync already in progress", err)
		case domain.ErrRedundancyMaxAttempts:
			respondError(w, http.StatusBadRequest, "Maximum sync attempts exceeded", err)
		default:
			respondError(w, http.StatusInternalServerError, "Failed to sync redundancy", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Redundancy sync completed successfully",
	})
}

// DeleteRedundancy deletes a redundancy
// DELETE /api/v1/admin/redundancy/redundancies/{id}
func (h *RedundancyHandler) DeleteRedundancy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.DeleteRedundancy(r.Context(), id); err != nil {
		if err == domain.ErrRedundancyNotFound {
			respondError(w, http.StatusNotFound, "Redundancy not found", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to delete redundancy", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Redundancy deleted successfully",
	})
}

// ==================== Policy Handlers ====================

// ListPolicies lists all redundancy policies
// GET /api/v1/admin/redundancy/policies?enabled_only=false
func (h *RedundancyHandler) ListPolicies(w http.ResponseWriter, r *http.Request) {
	enabledOnly := getBoolParam(r, "enabled_only", false)

	policies, err := h.service.ListPolicies(r.Context(), enabledOnly)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to list policies", err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"policies": policies,
	})
}

// CreatePolicy creates a new redundancy policy
// POST /api/v1/admin/redundancy/policies
func (h *RedundancyHandler) CreatePolicy(w http.ResponseWriter, r *http.Request) {
	var policy domain.RedundancyPolicy

	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	if err := h.service.CreatePolicy(r.Context(), &policy); err != nil {
		if err == domain.ErrPolicyAlreadyExists {
			respondError(w, http.StatusConflict, "Policy already exists", err)
		} else {
			respondError(w, http.StatusBadRequest, "Failed to create policy", err)
		}
		return
	}

	respondJSON(w, http.StatusCreated, policy)
}

// GetPolicy retrieves a policy
// GET /api/v1/admin/redundancy/policies/{id}
func (h *RedundancyHandler) GetPolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	policy, err := h.service.GetPolicy(r.Context(), id)
	if err != nil {
		if err == domain.ErrPolicyNotFound {
			respondError(w, http.StatusNotFound, "Policy not found", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to get policy", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, policy)
}

// UpdatePolicy updates a policy
// PUT /api/v1/admin/redundancy/policies/{id}
func (h *RedundancyHandler) UpdatePolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	var policy domain.RedundancyPolicy
	if err := json.NewDecoder(r.Body).Decode(&policy); err != nil {
		respondError(w, http.StatusBadRequest, "Invalid request body", err)
		return
	}

	policy.ID = id

	if err := h.service.UpdatePolicy(r.Context(), &policy); err != nil {
		if err == domain.ErrPolicyNotFound {
			respondError(w, http.StatusNotFound, "Policy not found", err)
		} else {
			respondError(w, http.StatusBadRequest, "Failed to update policy", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, policy)
}

// DeletePolicy deletes a policy
// DELETE /api/v1/admin/redundancy/policies/{id}
func (h *RedundancyHandler) DeletePolicy(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if err := h.service.DeletePolicy(r.Context(), id); err != nil {
		if err == domain.ErrPolicyNotFound {
			respondError(w, http.StatusNotFound, "Policy not found", err)
		} else {
			respondError(w, http.StatusInternalServerError, "Failed to delete policy", err)
		}
		return
	}

	respondJSON(w, http.StatusOK, map[string]string{
		"message": "Policy deleted successfully",
	})
}

// EvaluatePolicies triggers policy evaluation
// POST /api/v1/admin/redundancy/policies/evaluate
func (h *RedundancyHandler) EvaluatePolicies(w http.ResponseWriter, r *http.Request) {
	count, err := h.service.EvaluatePolicies(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to evaluate policies", err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"message":              "Policies evaluated successfully",
		"redundancies_created": count,
	})
}

// ==================== Statistics Handlers ====================

// GetStats retrieves redundancy statistics
// GET /api/v1/admin/redundancy/stats
func (h *RedundancyHandler) GetStats(w http.ResponseWriter, r *http.Request) {
	stats, err := h.service.GetStats(r.Context())
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get statistics", err)
		return
	}

	respondJSON(w, http.StatusOK, stats)
}

// GetVideoHealth retrieves redundancy health for a video
// GET /api/v1/redundancy/videos/{id}/health
func (h *RedundancyHandler) GetVideoHealth(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")

	health, err := h.service.GetVideoHealth(r.Context(), videoID)
	if err != nil {
		respondError(w, http.StatusInternalServerError, "Failed to get video health", err)
		return
	}

	respondJSON(w, http.StatusOK, map[string]interface{}{
		"video_id":     videoID,
		"health_score": health,
	})
}

// Note: Helper functions (getIntParam, getBoolParam, respondJSON, respondError)
// are defined in social.go and shared across the httpapi package
