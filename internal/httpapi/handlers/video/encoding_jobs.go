package video

import (
	"errors"
	"net/http"

	"vidra-core/internal/domain"
	"vidra-core/internal/httpapi/shared"
	"vidra-core/internal/middleware"
	"vidra-core/internal/usecase"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// GetEncodingJobHandler returns details for a specific encoding job
// Only accessible by the video owner, administrators, or moderators
func GetEncodingJobHandler(repo usecase.EncodingRepository, videoRepo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		jobID := chi.URLParam(r, "jobID")
		if jobID == "" {
			shared.WriteError(w, http.StatusBadRequest, errors.New("missing job ID"))
			return
		}

		// Get the encoding job
		job, err := repo.GetJob(r.Context(), jobID)
		if err != nil {
			// Check for domain error first
			if domainErr, ok := err.(domain.DomainError); ok && domainErr.Code == "JOB_NOT_FOUND" {
				shared.WriteError(w, http.StatusNotFound, err)
				return
			}
			if errors.Is(err, domain.ErrNotFound) {
				shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("JOB_NOT_FOUND", "Encoding job not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, err)
			return
		}

		// Check authorization
		userID, ok := middleware.GetUserIDFromContext(r.Context())
		if !ok {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		userRole, _ := r.Context().Value(middleware.UserRoleKey).(string)

		// Get the video to check ownership
		video, err := videoRepo.GetByID(r.Context(), job.VideoID)
		if err != nil {
			// Check for domain error first
			if domainErr, ok := err.(domain.DomainError); ok && domainErr.Code == "VIDEO_NOT_FOUND" {
				shared.WriteError(w, http.StatusNotFound, err)
				return
			}
			if errors.Is(err, domain.ErrVideoNotFound) {
				shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, err)
			return
		}

		// Check if user is authorized to view this job
		if video.UserID != userID.String() && userRole != "admin" && userRole != "moderator" {
			shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
			return
		}

		// Return job details
		shared.WriteJSON(w, http.StatusOK, job)
	}
}

// GetEncodingJobsByVideoHandler returns all encoding jobs for a specific video
// Only accessible by the video owner, administrators, or moderators
func GetEncodingJobsByVideoHandler(repo usecase.EncodingRepository, videoRepo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		videoID := chi.URLParam(r, "id")
		if videoID == "" {
			shared.WriteError(w, http.StatusBadRequest, errors.New("missing video ID"))
			return
		}

		// Validate video ID format
		if _, err := uuid.Parse(videoID); err != nil {
			shared.WriteError(w, http.StatusBadRequest, errors.New("invalid video ID format"))
			return
		}

		// Get user info from context
		userID, ok := middleware.GetUserIDFromContext(r.Context())
		if !ok {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		userRole, _ := r.Context().Value(middleware.UserRoleKey).(string)

		// Check if video exists and get ownership info
		video, err := videoRepo.GetByID(r.Context(), videoID)
		if err != nil {
			// Check for domain error first
			if domainErr, ok := err.(domain.DomainError); ok && domainErr.Code == "VIDEO_NOT_FOUND" {
				shared.WriteError(w, http.StatusNotFound, err)
				return
			}
			if errors.Is(err, domain.ErrVideoNotFound) {
				shared.WriteError(w, http.StatusNotFound, domain.NewDomainError("VIDEO_NOT_FOUND", "Video not found"))
				return
			}
			shared.WriteError(w, http.StatusInternalServerError, err)
			return
		}

		// Check authorization
		if video.UserID != userID.String() && userRole != "admin" && userRole != "moderator" {
			shared.WriteError(w, http.StatusForbidden, domain.ErrForbidden)
			return
		}

		// Get query parameter for filtering
		activeOnly := r.URL.Query().Get("active") == "true"

		var jobs []*domain.EncodingJob
		if activeOnly {
			jobs, err = repo.GetActiveJobsByVideoID(r.Context(), videoID)
		} else {
			jobs, err = repo.GetJobsByVideoID(r.Context(), videoID)
		}

		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, err)
			return
		}

		// Calculate aggregate progress if there are active jobs
		var overallProgress int
		activeJobCount := 0
		for _, job := range jobs {
			if job.Status == domain.EncodingStatusPending || job.Status == domain.EncodingStatusProcessing {
				overallProgress += job.Progress
				activeJobCount++
			}
		}
		if activeJobCount > 0 {
			overallProgress = overallProgress / activeJobCount
		}

		response := map[string]interface{}{
			"video_id":         videoID,
			"jobs":             jobs,
			"total":            len(jobs),
			"active_count":     activeJobCount,
			"overall_progress": overallProgress,
		}

		shared.WriteJSON(w, http.StatusOK, response)
	}
}

// GetMyEncodingJobsHandler returns all encoding jobs for videos owned by the authenticated user
func GetMyEncodingJobsHandler(repo usecase.EncodingRepository, videoRepo usecase.VideoRepository) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Get user ID from context
		userID, ok := middleware.GetUserIDFromContext(r.Context())
		if !ok {
			shared.WriteError(w, http.StatusUnauthorized, domain.ErrUnauthorized)
			return
		}

		// Get query parameters
		status := r.URL.Query().Get("status")
		limit := 50 // Default limit

		// Get user's videos first
		videos, _, err := videoRepo.GetByUserID(r.Context(), userID.String(), limit, 0)
		if err != nil {
			shared.WriteError(w, http.StatusInternalServerError, err)
			return
		}

		// Collect all jobs for user's videos
		allJobs := make([]*domain.EncodingJob, 0)
		for _, video := range videos {
			jobs, err := repo.GetJobsByVideoID(r.Context(), video.ID)
			if err != nil {
				continue // Skip on error
			}

			// Filter by status if requested
			if status != "" {
				filteredJobs := make([]*domain.EncodingJob, 0)
				for _, job := range jobs {
					if string(job.Status) == status {
						filteredJobs = append(filteredJobs, job)
					}
				}
				jobs = filteredJobs
			}

			allJobs = append(allJobs, jobs...)
		}

		response := map[string]interface{}{
			"jobs":  allJobs,
			"total": len(allJobs),
		}

		shared.WriteJSON(w, http.StatusOK, response)
	}
}
