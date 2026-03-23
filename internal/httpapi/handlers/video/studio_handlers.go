package video

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
)

// StudioService defines the business operations needed by the studio handlers.
type StudioService interface {
	CreateEditJob(ctx context.Context, videoID, userID string, req domain.StudioEditRequest) (*domain.StudioJob, error)
	GetJob(ctx context.Context, jobID string) (*domain.StudioJob, error)
	ListJobsForVideo(ctx context.Context, videoID string) ([]*domain.StudioJob, error)
}

// StudioVideoRepository defines the video lookup needed for ownership checks.
type StudioVideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
}

// StudioHandlers handles video studio editing endpoints.
type StudioHandlers struct {
	service   StudioService
	videoRepo StudioVideoRepository
}

// NewStudioHandlers creates a new StudioHandlers.
func NewStudioHandlers(service StudioService, videoRepo StudioVideoRepository) *StudioHandlers {
	return &StudioHandlers{service: service, videoRepo: videoRepo}
}

// CreateEditJob handles POST /api/v1/videos/{id}/studio/edit.
func (h *StudioHandlers) CreateEditJob(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	userID, _ := r.Context().Value(middleware.UserIDKey).(string)
	if userID == "" {
		shared.WriteError(w, http.StatusUnauthorized, domain.NewDomainError("UNAUTHORIZED", "Authentication required"))
		return
	}

	// Verify video ownership.
	video, err := h.videoRepo.GetByID(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}
	if video.UserID != userID {
		shared.WriteError(w, http.StatusForbidden, domain.NewDomainError("FORBIDDEN", "Only the video owner can create studio edit jobs"))
		return
	}

	var req domain.StudioEditRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("INVALID_JSON", "Invalid JSON payload"))
		return
	}

	job, err := h.service.CreateEditJob(r.Context(), videoID, userID, req)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusCreated, job)
}

// ListJobs handles GET /api/v1/videos/{id}/studio/jobs.
func (h *StudioHandlers) ListJobs(w http.ResponseWriter, r *http.Request) {
	videoID := chi.URLParam(r, "id")
	if videoID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_VIDEO_ID", "Video ID is required"))
		return
	}

	jobs, err := h.service.ListJobsForVideo(r.Context(), videoID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	if jobs == nil {
		jobs = []*domain.StudioJob{}
	}

	shared.WriteJSON(w, http.StatusOK, jobs)
}

// GetJob handles GET /api/v1/videos/{id}/studio/jobs/{jobId}.
func (h *StudioHandlers) GetJob(w http.ResponseWriter, r *http.Request) {
	jobID := chi.URLParam(r, "jobId")
	if jobID == "" {
		shared.WriteError(w, http.StatusBadRequest, domain.NewDomainError("MISSING_JOB_ID", "Job ID is required"))
		return
	}

	job, err := h.service.GetJob(r.Context(), jobID)
	if err != nil {
		status := shared.MapDomainErrorToHTTP(err)
		shared.WriteError(w, status, err)
		return
	}

	shared.WriteJSON(w, http.StatusOK, job)
}
