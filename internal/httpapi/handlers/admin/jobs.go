package admin

import (
	"context"
	"net/http"

	chi "github.com/go-chi/chi/v5"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
)

// JobRepository is the narrow storage interface for job listing.
type JobRepository interface {
	ListJobsByStatus(ctx context.Context, status string) ([]*domain.EncodingJob, error)
}

// JobScheduler is the narrow interface for pausing/resuming job processing.
type JobScheduler interface {
	Pause()
	Resume()
}

// JobHandlers handles job queue management endpoints.
type JobHandlers struct {
	repo  JobRepository
	sched JobScheduler
}

// NewJobHandlers returns a new JobHandlers. Either repo or sched may be nil.
func NewJobHandlers(repo JobRepository, sched JobScheduler) *JobHandlers {
	return &JobHandlers{repo: repo, sched: sched}
}

// ListJobs handles GET /admin/jobs/{state}
func (h *JobHandlers) ListJobs(w http.ResponseWriter, r *http.Request) {
	state := chi.URLParam(r, "state")
	if h.repo == nil {
		shared.WriteJSON(w, http.StatusOK, []*domain.EncodingJob{})
		return
	}

	jobs, err := h.repo.ListJobsByStatus(r.Context(), state)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, domain.NewDomainError("INTERNAL_ERROR", "Failed to list jobs"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, jobs)
}

// PauseJobs handles POST /admin/jobs/pause
func (h *JobHandlers) PauseJobs(w http.ResponseWriter, r *http.Request) {
	if h.sched == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, domain.NewDomainError("SCHEDULER_UNAVAILABLE", "Job scheduler not configured"))
		return
	}
	h.sched.Pause()
	w.WriteHeader(http.StatusNoContent)
}

// ResumeJobs handles POST /admin/jobs/resume
func (h *JobHandlers) ResumeJobs(w http.ResponseWriter, r *http.Request) {
	if h.sched == nil {
		shared.WriteError(w, http.StatusServiceUnavailable, domain.NewDomainError("SCHEDULER_UNAVAILABLE", "Job scheduler not configured"))
		return
	}
	h.sched.Resume()
	w.WriteHeader(http.StatusNoContent)
}
