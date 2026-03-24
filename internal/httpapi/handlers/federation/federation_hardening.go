package federation

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
	"vidra-core/internal/httpapi/shared"

	"vidra-core/internal/domain"
	"vidra-core/internal/usecase"

	"github.com/go-chi/chi/v5"
)

type FederationHardeningHandler struct {
	service *usecase.FederationHardeningService
}

func NewFederationHardeningHandler(service *usecase.FederationHardeningService) *FederationHardeningHandler {
	return &FederationHardeningHandler{
		service: service,
	}
}

func (h *FederationHardeningHandler) RegisterRoutes(r chi.Router) {
	r.Route("/api/v1/federation/hardening", func(r chi.Router) {
		r.Get("/dashboard", h.GetDashboard)
		r.Get("/health", h.GetHealthMetrics)

		r.Get("/dlq", h.GetDLQJobs)
		r.Post("/dlq/{id}/retry", h.RetryDLQJob)

		r.Route("/blocklist", func(r chi.Router) {
			r.Get("/instances", h.GetInstanceBlocks)
			r.Post("/instances", h.BlockInstance)
			r.Delete("/instances/{domain}", h.UnblockInstance)

			r.Post("/actors", h.BlockActor)
			r.Get("/check", h.CheckBlocked)
		})

		r.Route("/abuse", func(r chi.Router) {
			r.Post("/report", h.ReportAbuse)
			r.Get("/reports", h.GetAbuseReports)
			r.Post("/reports/{id}/resolve", h.ResolveAbuseReport)
		})

		r.Post("/cleanup", h.RunCleanup)
	})
}

func (h *FederationHardeningHandler) GetDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	data, err := h.service.GetDashboardData(ctx)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, data)
}

func (h *FederationHardeningHandler) GetHealthMetrics(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	metrics, err := h.service.GetHealthMetrics(ctx)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, metrics)
}

func (h *FederationHardeningHandler) GetDLQJobs(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	canRetryOnly := r.URL.Query().Get("can_retry") == "true"

	jobs, err := h.service.GetDLQJobs(ctx, limit, canRetryOnly)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, jobs)
}

func (h *FederationHardeningHandler) RetryDLQJob(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	dlqID := chi.URLParam(r, "id")

	if err := h.service.RetryDLQJob(ctx, dlqID); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "retry_queued"})
}

type BlockInstanceRequest struct {
	InstanceDomain string               `json:"instance_domain"`
	Reason         string               `json:"reason"`
	Severity       domain.BlockSeverity `json:"severity"`
	BlockedBy      string               `json:"blocked_by"`
	Duration       string               `json:"duration,omitempty"`
}

func (h *FederationHardeningHandler) BlockInstance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req BlockInstanceRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	var duration time.Duration
	if req.Duration != "" {
		d, err := time.ParseDuration(req.Duration)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, errors.New("invalid duration format"))
			return
		}
		duration = d
	}

	if err := h.service.BlockInstance(ctx, req.InstanceDomain, req.Reason, req.Severity, req.BlockedBy, duration); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "blocked"})
}

func (h *FederationHardeningHandler) UnblockInstance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	domain := chi.URLParam(r, "domain")

	if err := h.service.UnblockInstance(ctx, domain); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "unblocked"})
}

func (h *FederationHardeningHandler) GetInstanceBlocks(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	blocks, err := h.service.GetInstanceBlocks(ctx)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, blocks)
}

type BlockActorRequest struct {
	ActorDID    string               `json:"actor_did"`
	ActorHandle string               `json:"actor_handle"`
	Reason      string               `json:"reason"`
	Severity    domain.BlockSeverity `json:"severity"`
	BlockedBy   string               `json:"blocked_by"`
	Duration    string               `json:"duration,omitempty"`
}

func (h *FederationHardeningHandler) BlockActor(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req BlockActorRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	var duration time.Duration
	if req.Duration != "" {
		d, err := time.ParseDuration(req.Duration)
		if err != nil {
			shared.WriteError(w, http.StatusBadRequest, errors.New("invalid duration format"))
			return
		}
		duration = d
	}

	if err := h.service.BlockActor(ctx, req.ActorDID, req.ActorHandle, req.Reason, req.Severity, req.BlockedBy, duration); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "blocked"})
}

func (h *FederationHardeningHandler) CheckBlocked(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	instanceDomain := r.URL.Query().Get("instance")
	actorDID := r.URL.Query().Get("actor_did")
	actorHandle := r.URL.Query().Get("actor_handle")

	result := map[string]bool{
		"instance_blocked": false,
		"actor_blocked":    false,
	}

	if instanceDomain != "" {
		blocked, _ := h.service.IsInstanceBlocked(ctx, instanceDomain)
		result["instance_blocked"] = blocked
	}

	if actorDID != "" || actorHandle != "" {
		blocked, _ := h.service.IsActorBlocked(ctx, actorDID, actorHandle)
		result["actor_blocked"] = blocked
	}

	shared.WriteJSON(w, http.StatusOK, result)
}

type ReportAbuseRequest struct {
	ReporterDID string          `json:"reporter_did"`
	ReportType  string          `json:"report_type"`
	ContentURI  string          `json:"content_uri,omitempty"`
	ActorDID    string          `json:"actor_did,omitempty"`
	Description string          `json:"description"`
	Evidence    json.RawMessage `json:"evidence,omitempty"`
}

func (h *FederationHardeningHandler) ReportAbuse(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var req ReportAbuseRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	if err := h.service.ReportAbuse(ctx, req.ReporterDID, req.ReportType, req.ContentURI, req.ActorDID, req.Description, req.Evidence); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "reported"})
}

func (h *FederationHardeningHandler) GetAbuseReports(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	limit := 50
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	reports, err := h.service.GetPendingAbuseReports(ctx, limit)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, reports)
}

type ResolveAbuseReportRequest struct {
	Resolution string `json:"resolution"`
	ResolvedBy string `json:"resolved_by"`
	TakeAction bool   `json:"take_action"`
}

func (h *FederationHardeningHandler) ResolveAbuseReport(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	reportID := chi.URLParam(r, "id")

	var req ResolveAbuseReportRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, errors.New("invalid request"))
		return
	}

	if err := h.service.ResolveAbuseReport(ctx, reportID, req.Resolution, req.ResolvedBy, req.TakeAction); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "resolved"})
}

func (h *FederationHardeningHandler) RunCleanup(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := h.service.RunCleanup(ctx); err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, map[string]string{"status": "cleanup_completed"})
}

func FederationMiddleware(service *usecase.FederationHardeningService) func(next http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			instanceDomain := r.Header.Get("X-Federation-Instance")
			if instanceDomain == "" {
				instanceDomain = r.Host
			}

			signature := r.Header.Get("X-Federation-Signature")
			ts := r.Header.Get("X-Federation-Timestamp")

			maxSize := int64(10485760)
			if service != nil && service.Config() != nil && service.Config().MaxRequestSize > 0 {
				maxSize = service.Config().MaxRequestSize
			}
			body, err := io.ReadAll(io.LimitReader(r.Body, maxSize))
			if err != nil {
				shared.WriteError(w, http.StatusBadRequest, errors.New("failed to read request body"))
				return
			}

			if err := service.ValidateFederationRequest(r.Context(), instanceDomain, signature, r.URL.Path, body, ts); err != nil {
				shared.WriteError(w, http.StatusForbidden, err)
				return
			}

			r.Body = io.NopCloser(bytes.NewReader(body))

			next.ServeHTTP(w, r)
		})
	}
}
