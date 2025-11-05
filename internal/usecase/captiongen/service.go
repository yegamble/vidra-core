package captiongen

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"athena/internal/domain"
	"athena/internal/port"
	"athena/internal/storage"
	"athena/internal/whisper"

	"github.com/google/uuid"
)

// Service defines the caption generation service interface
type Service interface {
	// Run starts N workers that consume caption generation jobs until the context is canceled
	Run(ctx context.Context, workers int) error

	// ProcessNext fetches the next pending job and processes it
	ProcessNext(ctx context.Context) (processed bool, err error)

	// CreateJob creates a new caption generation job for a video
	CreateJob(ctx context.Context, videoID uuid.UUID, userID uuid.UUID, req *domain.CreateCaptionGenerationJobRequest) (*domain.CaptionGenerationJob, error)

	// RegenerateCaption regenerates captions for a video (manual trigger)
	RegenerateCaption(ctx context.Context, videoID uuid.UUID, userID uuid.UUID, targetLanguage *string) (*domain.CaptionGenerationJob, error)

	// GetJobStatus retrieves the status of a caption generation job
	GetJobStatus(ctx context.Context, jobID uuid.UUID) (*domain.CaptionGenerationJob, error)

	// GetJobsByVideo retrieves all caption generation jobs for a video
	GetJobsByVideo(ctx context.Context, videoID uuid.UUID) ([]domain.CaptionGenerationJob, error)
}

type service struct {
	jobRepo     port.CaptionGenerationRepository
	captionRepo port.CaptionRepository
	videoRepo   port.VideoRepository
	whisperCli  whisper.Client
	uploadsDir  string
}

// NewService creates a new caption generation service
func NewService(
	jobRepo port.CaptionGenerationRepository,
	captionRepo port.CaptionRepository,
	videoRepo port.VideoRepository,
	whisperCli whisper.Client,
	uploadsDir string,
) Service {
	return &service{
		jobRepo:     jobRepo,
		captionRepo: captionRepo,
		videoRepo:   videoRepo,
		whisperCli:  whisperCli,
		uploadsDir:  uploadsDir,
	}
}

// Run starts N workers that consume caption generation jobs
func (s *service) Run(ctx context.Context, workers int) error {
	if workers <= 0 {
		workers = runtime.NumCPU()
		if workers < 2 {
			workers = 2
		}
	}

	wg := &sync.WaitGroup{}
	errCh := make(chan error, workers)

	worker := func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
			}

			processed, err := s.ProcessNext(ctx)
			if err != nil {
				// Report but continue
				select {
				case errCh <- err:
				default:
				}
			}
			if !processed {
				// No jobs available, back off briefly
				select {
				case <-ctx.Done():
					return
				case <-time.After(2 * time.Second):
				}
			}
		}
	}

	for i := 0; i < workers; i++ {
		wg.Add(1)
		go worker()
	}

	// If the context is cancelled, wait for workers to exit
	<-ctx.Done()
	wg.Wait()
	close(errCh)

	// Return the first error if any (non-fatal)
	for e := range errCh {
		if e != nil {
			return e
		}
	}
	return nil
}

// ProcessNext fetches and processes the next pending caption generation job
func (s *service) ProcessNext(ctx context.Context) (bool, error) {
	job, err := s.jobRepo.GetNextPendingJob(ctx)
	if err != nil {
		return false, fmt.Errorf("failed to get next job: %w", err)
	}
	if job == nil {
		return false, nil // No pending jobs
	}

	// Mark as processing
	if err := s.jobRepo.UpdateStatus(ctx, job.ID, domain.CaptionGenStatusProcessing); err != nil {
		return true, fmt.Errorf("failed to mark job as processing: %w", err)
	}

	// Process the job
	if err := s.processJob(ctx, job); err != nil {
		// Mark as failed
		_ = s.jobRepo.MarkFailed(ctx, job.ID, err.Error())
		return true, err
	}

	return true, nil
}

// CreateJob creates a new caption generation job for a video
func (s *service) CreateJob(
	ctx context.Context,
	videoID uuid.UUID,
	userID uuid.UUID,
	req *domain.CreateCaptionGenerationJobRequest,
) (*domain.CaptionGenerationJob, error) {
	// Get video to validate it exists and get source file
	video, err := s.videoRepo.GetByID(ctx, videoID.String())
	if err != nil {
		return nil, fmt.Errorf("failed to get video: %w", err)
	}

	if video.Status != domain.StatusCompleted {
		return nil, fmt.Errorf("video must be fully processed before generating captions")
	}

	// Determine source file path using storage.Paths helper
	sp := storage.NewPaths(s.uploadsDir)
	ext := getExtensionFromMimeType(video.MimeType)
	sourceVideoPath := sp.WebVideoFilePath(video.ID, ext)
	if _, err := os.Stat(sourceVideoPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("source video file not found: %s", sourceVideoPath)
	}

	// Create audio extraction path
	audioPath := filepath.Join(s.uploadsDir, "temp", fmt.Sprintf("%s_audio.wav", video.ID))

	// Set defaults
	modelSize := req.ModelSize
	if modelSize == "" {
		modelSize = domain.WhisperModelBase
	}

	outputFormat := req.OutputFormat
	if outputFormat == "" {
		outputFormat = domain.CaptionFormatVTT
	}

	provider := req.Provider
	if provider == "" {
		provider = s.whisperCli.GetProvider()
	}

	// Create job
	job := &domain.CaptionGenerationJob{
		ID:              uuid.New(),
		VideoID:         videoID,
		UserID:          userID,
		SourceAudioPath: audioPath,
		TargetLanguage:  req.TargetLanguage,
		Status:          domain.CaptionGenStatusPending,
		Progress:        0,
		ModelSize:       modelSize,
		Provider:        provider,
		OutputFormat:    outputFormat,
		IsAutomatic:     false, // Manual creation
		RetryCount:      0,
		MaxRetries:      3,
	}

	if err := s.jobRepo.Create(ctx, job); err != nil {
		return nil, fmt.Errorf("failed to create job: %w", err)
	}

	return job, nil
}

// RegenerateCaption creates a new caption generation job, handling existing captions
// If targetLanguage is specified, deletes existing caption for that language before regenerating
// If targetLanguage is nil (auto-detect), the old caption will be replaced after detection
func (s *service) RegenerateCaption(
	ctx context.Context,
	videoID uuid.UUID,
	userID uuid.UUID,
	targetLanguage *string,
) (*domain.CaptionGenerationJob, error) {
	req := &domain.CreateCaptionGenerationJobRequest{
		VideoID:        videoID,
		TargetLanguage: targetLanguage,
	}

	// If regenerating with a specific language, delete old caption for THAT language only
	// This ensures other language captions remain untouched
	if targetLanguage != nil && *targetLanguage != "" {
		existingCaption, err := s.captionRepo.GetByVideoAndLanguage(ctx, videoID, *targetLanguage)
		if err == nil && existingCaption != nil {
			// Delete old caption file
			if existingCaption.FilePath != nil {
				_ = os.Remove(*existingCaption.FilePath)
			}
			// Delete caption record for this language only
			_ = s.captionRepo.Delete(ctx, existingCaption.ID)
		}
	}
	// Note: If targetLanguage is nil (auto-detect), we'll delete the detected language
	// after transcription completes, handled in processJob()

	return s.CreateJob(ctx, videoID, userID, req)
}

// GetJobStatus retrieves the status of a caption generation job
func (s *service) GetJobStatus(ctx context.Context, jobID uuid.UUID) (*domain.CaptionGenerationJob, error) {
	return s.jobRepo.GetByID(ctx, jobID)
}

// GetJobsByVideo retrieves all caption generation jobs for a video
func (s *service) GetJobsByVideo(ctx context.Context, videoID uuid.UUID) ([]domain.CaptionGenerationJob, error) {
	return s.jobRepo.GetByVideoID(ctx, videoID)
}

// processJob processes a caption generation job
func (s *service) processJob(ctx context.Context, job *domain.CaptionGenerationJob) error {
	startTime := time.Now()

	// Step 1: Get video
	video, err := s.videoRepo.GetByID(ctx, job.VideoID.String())
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	// Update progress: 10%
	_ = s.jobRepo.UpdateProgress(ctx, job.ID, 10)

	// Step 2: Extract audio from video
	sp := storage.NewPaths(s.uploadsDir)
	ext := getExtensionFromMimeType(video.MimeType)
	sourceVideoPath := sp.WebVideoFilePath(video.ID, ext)
	if err := s.whisperCli.ExtractAudioFromVideo(ctx, sourceVideoPath, job.SourceAudioPath); err != nil {
		return fmt.Errorf("failed to extract audio: %w", err)
	}
	defer os.Remove(job.SourceAudioPath) // Cleanup temp audio file

	// Update progress: 30%
	_ = s.jobRepo.UpdateProgress(ctx, job.ID, 30)

	// Step 3: Transcribe audio with Whisper
	result, err := s.whisperCli.Transcribe(ctx, job.SourceAudioPath, job.TargetLanguage)
	if err != nil {
		return fmt.Errorf("failed to transcribe audio: %w", err)
	}

	// Update progress: 70%
	_ = s.jobRepo.UpdateProgress(ctx, job.ID, 70)

	// Step 4: Format caption file
	var captionContent string
	switch job.OutputFormat {
	case domain.CaptionFormatVTT:
		captionContent, err = s.whisperCli.FormatToVTT(result)
	case domain.CaptionFormatSRT:
		captionContent, err = s.whisperCli.FormatToSRT(result)
	default:
		return fmt.Errorf("unsupported caption format: %s", job.OutputFormat)
	}

	if err != nil {
		return fmt.Errorf("failed to format caption: %w", err)
	}

	// Update progress: 80%
	_ = s.jobRepo.UpdateProgress(ctx, job.ID, 80)

	// Step 5: Save caption file
	sp = storage.NewPaths(s.uploadsDir)
	captionPath := sp.CaptionFilePath(job.VideoID.String(), result.DetectedLanguage, string(job.OutputFormat))
	if err := os.MkdirAll(filepath.Dir(captionPath), 0755); err != nil {
		return fmt.Errorf("failed to create caption directory: %w", err)
	}

	if err := os.WriteFile(captionPath, []byte(captionContent), 0644); err != nil {
		return fmt.Errorf("failed to write caption file: %w", err)
	}

	fileSize := int64(len(captionContent))

	// Update progress: 90%
	_ = s.jobRepo.UpdateProgress(ctx, job.ID, 90)

	// Step 6: Delete existing caption for the detected language (if regenerating with auto-detect)
	// This ensures we replace the caption for the detected language without affecting other languages
	if job.TargetLanguage == nil || *job.TargetLanguage == "" {
		existingCaption, err := s.captionRepo.GetByVideoAndLanguage(ctx, job.VideoID, result.DetectedLanguage)
		if err == nil && existingCaption != nil {
			// Delete old caption file
			if existingCaption.FilePath != nil {
				_ = os.Remove(*existingCaption.FilePath)
			}
			// Delete caption record for detected language only
			_ = s.captionRepo.Delete(ctx, existingCaption.ID)
		}
	}

	// Step 7: Create caption record
	caption := &domain.Caption{
		ID:              uuid.New(),
		VideoID:         job.VideoID,
		LanguageCode:    result.DetectedLanguage,
		Label:           getLanguageLabel(result.DetectedLanguage),
		FilePath:        &captionPath,
		FileFormat:      job.OutputFormat,
		FileSizeBytes:   &fileSize,
		IsAutoGenerated: true,
	}

	if err := s.captionRepo.Create(ctx, caption); err != nil {
		return fmt.Errorf("failed to create caption record: %w", err)
	}

	// Step 8: Update video language if not set
	if video.Language == "" {
		video.Language = result.DetectedLanguage
		if err := s.videoRepo.Update(ctx, video); err != nil {
			// Non-fatal: log but don't fail the job
			fmt.Printf("Warning: failed to update video language: %v\n", err)
		}
	}

	// Step 9: Mark job as completed
	transcriptionTime := int(time.Since(startTime).Seconds())
	if err := s.jobRepo.MarkCompleted(ctx, job.ID, caption.ID, result.DetectedLanguage, transcriptionTime); err != nil {
		return fmt.Errorf("failed to mark job as completed: %w", err)
	}

	return nil
}

// getLanguageLabel returns a human-readable label for a language code
func getLanguageLabel(code string) string {
	// Map of common ISO 639-1 codes to labels
	labels := map[string]string{
		"en": "English",
		"es": "Spanish",
		"fr": "French",
		"de": "German",
		"it": "Italian",
		"pt": "Portuguese",
		"ru": "Russian",
		"ja": "Japanese",
		"ko": "Korean",
		"zh": "Chinese",
		"ar": "Arabic",
		"hi": "Hindi",
		"nl": "Dutch",
		"pl": "Polish",
		"tr": "Turkish",
		"sv": "Swedish",
		"da": "Danish",
		"fi": "Finnish",
		"no": "Norwegian",
		"cs": "Czech",
		"el": "Greek",
		"he": "Hebrew",
		"id": "Indonesian",
		"th": "Thai",
		"vi": "Vietnamese",
	}

	if label, ok := labels[strings.ToLower(code)]; ok {
		return label
	}

	// Fallback: capitalize the code
	return strings.ToUpper(code)
}

// getExtensionFromMimeType returns the file extension for a given MIME type
func getExtensionFromMimeType(mimeType string) string {
	// Common video MIME types
	mimeToExt := map[string]string{
		"video/mp4":        ".mp4",
		"video/mpeg":       ".mpeg",
		"video/quicktime":  ".mov",
		"video/x-msvideo":  ".avi",
		"video/x-matroska": ".mkv",
		"video/webm":       ".webm",
		"video/ogg":        ".ogv",
		"video/3gpp":       ".3gp",
		"video/x-flv":      ".flv",
	}

	if ext, ok := mimeToExt[mimeType]; ok {
		return ext
	}

	// Fallback to .mp4 if unknown
	return ".mp4"
}
