package studio

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"athena/internal/domain"
	"athena/internal/port"
)

// VideoRepository is the subset of port.VideoRepository used by StudioService.
type VideoRepository interface {
	GetByID(ctx context.Context, id string) (*domain.Video, error)
}

// CommandRunner abstracts os/exec for testability.
type CommandRunner interface {
	RunCommand(ctx context.Context, name string, args ...string) ([]byte, error)
}

// Service orchestrates video studio editing jobs.
type Service struct {
	jobRepo   port.StudioJobRepository
	videoRepo VideoRepository
	runner    CommandRunner
	logger    *slog.Logger
}

// NewService creates a new studio editing service.
func NewService(jobRepo port.StudioJobRepository, videoRepo VideoRepository, runner CommandRunner, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.Default()
	}
	return &Service{
		jobRepo:   jobRepo,
		videoRepo: videoRepo,
		runner:    runner,
		logger:    logger,
	}
}

// CreateEditJob validates the request, persists a new job, and starts
// asynchronous processing.
func (s *Service) CreateEditJob(ctx context.Context, videoID, userID string, req domain.StudioEditRequest) (*domain.StudioJob, error) {
	if err := req.Validate(); err != nil {
		return nil, fmt.Errorf("validate studio request: %w", err)
	}

	// Verify video exists.
	_, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("get video for studio edit: %w", err)
	}

	// Check for an existing in-progress job.
	existing, err := s.jobRepo.GetByVideoID(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("check existing studio jobs: %w", err)
	}
	for _, j := range existing {
		if j.Status == domain.StudioJobStatusPending || j.Status == domain.StudioJobStatusProcessing {
			return nil, domain.ErrStudioJobInProgress
		}
	}

	tasksJSON, err := json.Marshal(req.Tasks)
	if err != nil {
		return nil, fmt.Errorf("marshal studio tasks: %w", err)
	}

	now := time.Now().UTC()
	job := &domain.StudioJob{
		ID:        uuid.New().String(),
		VideoID:   videoID,
		UserID:    userID,
		Status:    domain.StudioJobStatusPending,
		Tasks:     string(tasksJSON),
		CreatedAt: now,
		UpdatedAt: now,
	}

	if err := s.jobRepo.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("create studio job: %w", err)
	}

	// Fire-and-forget: process asynchronously.
	go s.processEditJob(context.Background(), job)

	return job, nil
}

// GetJob returns a studio job by its ID.
func (s *Service) GetJob(ctx context.Context, jobID string) (*domain.StudioJob, error) {
	job, err := s.jobRepo.GetByID(ctx, jobID)
	if err != nil {
		return nil, fmt.Errorf("get studio job: %w", err)
	}
	return job, nil
}

// ListJobsForVideo returns all studio jobs associated with a video.
func (s *Service) ListJobsForVideo(ctx context.Context, videoID string) ([]*domain.StudioJob, error) {
	jobs, err := s.jobRepo.GetByVideoID(ctx, videoID)
	if err != nil {
		return nil, fmt.Errorf("list studio jobs for video: %w", err)
	}
	return jobs, nil
}

// processEditJob runs the FFmpeg editing pipeline for a job and updates its
// status on completion or failure.
func (s *Service) processEditJob(ctx context.Context, job *domain.StudioJob) {
	defer func() {
		if r := recover(); r != nil {
			s.logger.Error("panic in studio job processing", "jobID", job.ID, "panic", r)
			_ = s.jobRepo.UpdateStatus(ctx, job.ID, domain.StudioJobStatusFailed, fmt.Sprintf("panic: %v", r))
		}
	}()

	// Mark as processing.
	if err := s.jobRepo.UpdateStatus(ctx, job.ID, domain.StudioJobStatusProcessing, ""); err != nil {
		s.logger.Error("failed to mark studio job as processing", "jobID", job.ID, "error", err)
		return
	}

	var tasks []domain.StudioTask
	if err := json.Unmarshal([]byte(job.Tasks), &tasks); err != nil {
		s.logger.Error("failed to unmarshal studio tasks", "jobID", job.ID, "error", err)
		_ = s.jobRepo.UpdateStatus(ctx, job.ID, domain.StudioJobStatusFailed, "invalid tasks JSON")
		return
	}

	for i, task := range tasks {
		args, err := buildFFmpegArgs(task)
		if err != nil {
			errMsg := fmt.Sprintf("task %d (%s): %s", i, task.Name, err.Error())
			s.logger.Error("failed to build FFmpeg args", "jobID", job.ID, "task", task.Name, "error", err)
			_ = s.jobRepo.UpdateStatus(ctx, job.ID, domain.StudioJobStatusFailed, errMsg)
			return
		}

		if _, err := s.runner.RunCommand(ctx, "ffmpeg", args...); err != nil {
			errMsg := fmt.Sprintf("task %d (%s) ffmpeg failed: %s", i, task.Name, err.Error())
			s.logger.Error("FFmpeg failed for studio task", "jobID", job.ID, "task", task.Name, "error", err)
			_ = s.jobRepo.UpdateStatus(ctx, job.ID, domain.StudioJobStatusFailed, errMsg)
			return
		}
	}

	if err := s.jobRepo.UpdateStatus(ctx, job.ID, domain.StudioJobStatusCompleted, ""); err != nil {
		s.logger.Error("failed to mark studio job as completed", "jobID", job.ID, "error", err)
	}
}

// buildFFmpegArgs constructs FFmpeg CLI arguments for a single editing task.
func buildFFmpegArgs(task domain.StudioTask) ([]string, error) {
	switch task.Name {
	case "cut":
		return []string{
			"-ss", fmt.Sprintf("%.2f", *task.Options.Start),
			"-to", fmt.Sprintf("%.2f", *task.Options.End),
			"-c", "copy",
		}, nil
	case "add-intro":
		return []string{
			"-i", task.Options.File,
			"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[outv]",
			"-map", "[outv]",
		}, nil
	case "add-outro":
		return []string{
			"-i", task.Options.File,
			"-filter_complex", "[1:v][0:v]concat=n=2:v=1:a=0[outv]",
			"-map", "[outv]",
		}, nil
	case "add-watermark":
		return []string{
			"-i", task.Options.File,
			"-filter_complex", "overlay=W-w-10:H-h-10",
		}, nil
	default:
		return nil, fmt.Errorf("unsupported studio task: %s", task.Name)
	}
}
