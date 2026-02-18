package social

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/httpapi/shared"
	"athena/internal/middleware"
	"athena/internal/usecase/captiongen"
)

type CaptionGenerationHandlers struct {
	captionGenService captiongen.Service
	videoRepo         VideoRepository
}

type VideoRepository interface {
	GetByID(ctx context.Context, videoID string) (*domain.Video, error)
}

func NewCaptionGenerationHandlers(captionGenService captiongen.Service, videoRepo VideoRepository) *CaptionGenerationHandlers {
	return &CaptionGenerationHandlers{
		captionGenService: captionGenService,
		videoRepo:         videoRepo,
	}
}

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

	video, err := h.videoRepo.GetByID(r.Context(), videoIDStr)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("video not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to retrieve video"))
		return
	}

	if video.UserID != userID.String() {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you don't have permission to generate captions for this video"))
		return
	}

	if video.Status != domain.StatusCompleted {
		shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("video must be fully processed before generating captions"))
		return
	}

	var req GenerateCaptionsRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		req = GenerateCaptionsRequest{}
	}

	var targetLang *string
	if req.TargetLanguage != "" {
		if len(req.TargetLanguage) != 2 {
			shared.WriteError(w, http.StatusBadRequest, fmt.Errorf("target_language must be a 2-letter ISO 639-1 code (e.g., 'en', 'es')"))
			return
		}
		targetLang = &req.TargetLanguage
	}

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
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to create caption generation job"))
		return
	}

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

	video, err := h.videoRepo.GetByID(r.Context(), videoIDStr)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("video not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to retrieve video"))
		return
	}

	if video.UserID != userID.String() {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you don't have permission to view this job"))
		return
	}

	job, err := h.captionGenService.GetJobStatus(r.Context(), jobID)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("job not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to retrieve job status"))
		return
	}

	if job.VideoID != videoID {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("job does not belong to this video"))
		return
	}

	shared.WriteJSON(w, http.StatusOK, job)
}

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

	video, err := h.videoRepo.GetByID(r.Context(), videoIDStr)
	if err != nil {
		if err == domain.ErrNotFound {
			shared.WriteError(w, http.StatusNotFound, fmt.Errorf("video not found"))
			return
		}
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to retrieve video"))
		return
	}

	if video.UserID != userID.String() {
		shared.WriteError(w, http.StatusForbidden, fmt.Errorf("you don't have permission to view jobs for this video"))
		return
	}

	jobs, err := h.captionGenService.GetJobsByVideo(r.Context(), videoID)
	if err != nil {
		shared.WriteError(w, http.StatusInternalServerError, fmt.Errorf("failed to retrieve caption jobs"))
		return
	}

	response := ListCaptionGenerationJobsResponse{
		Jobs:  jobs,
		Count: len(jobs),
	}

	shared.WriteJSON(w, http.StatusOK, response)
}

type GenerateCaptionsRequest struct {
	TargetLanguage string `json:"target_language,omitempty"`
	ModelSize      string `json:"model_size,omitempty"`
	OutputFormat   string `json:"output_format,omitempty"`
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
