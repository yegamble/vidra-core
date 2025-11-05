package social

import (
	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase/captiongen"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// CaptionGenerationHandlers handles caption generation API requests
type CaptionGenerationHandlers struct {
	captionGenService captiongen.Service
	videoRepo         VideoRepository
}

// VideoRepository interface for video operations
type VideoRepository interface {
	GetByID(ctx interface{}, videoID string) (*domain.Video, error)
}

func NewCaptionGenerationHandlers(captionGenService captiongen.Service, videoRepo VideoRepository) *CaptionGenerationHandlers {
	return &CaptionGenerationHandlers{
		captionGenService: captionGenService,
		videoRepo:         videoRepo,
	}
}

// GenerateCaptions handles POST /api/v1/videos/{id}/captions/generate
// Triggers automatic caption generation for a video
func (h *CaptionGenerationHandlers) GenerateCaptions(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

	// Verify user owns the video
	video, err := h.videoRepo.GetByID(r.Context(), videoIDStr)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("video not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	if video.UserID != userID.String() {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you don't have permission to generate captions for this video"))
		return
	}

	// Check if video is fully processed
	if video.Status != domain.StatusCompleted {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("video must be fully processed before generating captions"))
		return
	}

	// Parse request body
	var req GenerateCaptionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		// Allow empty body (use defaults)
		req = GenerateCaptionsRequest{}
	}

	// Validate target language if provided
	var targetLang *string
	if req.TargetLanguage != "" {
		// Validate language code (2-letter ISO 639-1)
		if len(req.TargetLanguage) != 2 {
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("target_language must be a 2-letter ISO 639-1 code (e.g., 'en', 'es')"))
			return
		}
		targetLang = &req.TargetLanguage
	}

	// Set defaults
	modelSize := domain.WhisperModelBase
	if req.ModelSize != "" {
		modelSize = domain.WhisperModelSize(req.ModelSize)
		if !modelSize.IsValid() {
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid model_size, must be one of: tiny, base, small, medium, large"))
			return
		}
	}

	outputFormat := domain.CaptionFormatVTT
	if req.OutputFormat != "" {
		outputFormat = domain.CaptionFormat(req.OutputFormat)
		if !outputFormat.IsValid() {
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid output_format, must be 'vtt' or 'srt'"))
			return
		}
	}

	// Create caption generation job
	userUUID, err := uuid.Parse(userID.String())
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("invalid user ID"))
		return
	}

	jobReq := &domain.CreateCaptionGenerationJobRequest{
		VideoID:        videoID,
		TargetLanguage: targetLang,
		ModelSize:      modelSize,
		OutputFormat:   outputFormat,
	}

	job, err := h.captionGenService.CreateJob(r.Context(), videoID, userUUID, jobReq)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to create caption generation job: %w", err))
		return
	}

	// Return job details
	response := GenerateCaptionsResponse{
		JobID:     job.ID,
		VideoID:   job.VideoID,
		Status:    string(job.Status),
		Progress:  job.Progress,
		CreatedAt: job.CreatedAt,
		Message:   "Caption generation job created successfully. Check job status for progress.",
	}

	shared.WriteJSON(w, http.StatusAccepted, response)
}

// GetCaptionGenerationJob handles GET /api/v1/videos/{id}/captions/jobs/{jobId}
// Retrieves the status of a caption generation job
func (h *CaptionGenerationHandlers) GetCaptionGenerationJob(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

	jobIDStr := chi.URLParam(r, "jobId")
	jobID, err := uuid.Parse(jobIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid job ID"))
		return
	}

	// Verify user owns the video
	video, err := h.videoRepo.GetByID(r.Context(), videoIDStr)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("video not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	if video.UserID != userID.String() {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you don't have permission to view this job"))
		return
	}

	// Get job
	job, err := h.captionGenService.GetJobStatus(r.Context(), jobID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("job not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	// Verify job belongs to this video
	if job.VideoID != videoID {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("job does not belong to this video"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, job)
}

// ListCaptionGenerationJobs handles GET /api/v1/videos/{id}/captions/jobs
// Lists all caption generation jobs for a video
func (h *CaptionGenerationHandlers) ListCaptionGenerationJobs(w http.ResponseWriter, r *http.Request) {
	userID, ok := middleware.GetUserIDFromContext(r.Context())
	if !ok {
		shared.WriteError(w, http.StatusUnauthorized, fmt.Errorf("unauthorized"))
		return
	}

	videoIDStr := chi.URLParam(r, "id")
	videoID, err := uuid.Parse(videoIDStr)
	if err != nil {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("invalid video ID"))
		return
	}

	// Verify user owns the video
	video, err := h.videoRepo.GetByID(r.Context(), videoIDStr)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("video not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	if video.UserID != userID.String() {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you don't have permission to view jobs for this video"))
		return
	}

	// Get jobs
	jobs, err := h.captionGenService.GetJobsByVideo(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, err)
		return
	}

	response := ListCaptionGenerationJobsResponse{
		Jobs:  jobs,
		Count: len(jobs),
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

// Request/Response DTOs

type GenerateCaptionsRequest struct {
	TargetLanguage string `json:"target_language,omitempty"` // Optional: 2-letter ISO 639-1 code (e.g., 'en', 'es'). If not provided, auto-detect.
	ModelSize      string `json:"model_size,omitempty"`      // Optional: 'tiny', 'base', 'small', 'medium', 'large'. Default: 'base'
	OutputFormat   string `json:"output_format,omitempty"`   // Optional: 'vtt' or 'srt'. Default: 'vtt'
}

type GenerateCaptionsResponse struct {
	JobID     uuid.UUID   `json:"job_id"`
	VideoID   uuid.UUID   `json:"video_id"`
	Status    string      `json:"status"`
	Progress  int         `json:"progress"`
	CreatedAt interface{} `json:"created_at"`
	Message   string      `json:"message"`
}

type ListCaptionGenerationJobsResponse struct {
	Jobs  []domain.CaptionGenerationJob `json:"jobs"`
	Count int                           `json:"count"`
}
