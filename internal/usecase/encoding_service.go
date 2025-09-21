package usecase

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/metrics"
	"athena/internal/storage"
)

// EncodingService defines a background worker to process encoding jobs
type EncodingService interface {
	// Run starts N workers that consume jobs until the context is canceled
	Run(ctx context.Context, workers int) error
	// ProcessNext fetches the next pending job and processes it
	ProcessNext(ctx context.Context) (processed bool, err error)
}

type encodingService struct {
	repo            EncodingRepository
	videoRepo       VideoRepository
	notificationSvc NotificationService
	uploadsDir      string // storage root
	cfg             *config.Config
}

func NewEncodingService(repo EncodingRepository, videoRepo VideoRepository, notificationSvc NotificationService, uploadsDir string, cfg *config.Config) EncodingService {
	return &encodingService{repo: repo, videoRepo: videoRepo, notificationSvc: notificationSvc, uploadsDir: uploadsDir, cfg: cfg}
}

func (s *encodingService) Run(ctx context.Context, workers int) error {
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
				// report but continue
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
				case <-time.After(750 * time.Millisecond):
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

func (s *encodingService) ProcessNext(ctx context.Context) (bool, error) {
	job, err := s.repo.GetNextJob(ctx)
	if err != nil {
		return false, err
	}
	if job == nil {
		return false, nil
	}
	metrics.IncInFlight()

	if err := s.processJob(ctx, job); err != nil {
		metrics.IncFailed()
		metrics.DecInFlight()
		_ = s.repo.SetJobError(ctx, job.ID, err.Error())
		return true, err
	}

	// Mark completed and remove from queue
	now := time.Now()
	job.Status = domain.EncodingStatusCompleted
	job.Progress = 100
	job.CompletedAt = &now
	job.UpdatedAt = now
	if err := s.repo.UpdateJob(ctx, job); err != nil {
		metrics.DecInFlight()
		return true, err
	}
	// Optional: delete job to remove from queue
	_ = s.repo.DeleteJob(ctx, job.ID)
	metrics.IncProcessed()
	metrics.DecInFlight()
	return true, nil
}

// validateJob validates the encoding job parameters
func (s *encodingService) validateJob(job *domain.EncodingJob) error {
	if job.SourceFilePath == "" {
		return errors.New("missing source file path")
	}
	if _, err := os.Stat(job.SourceFilePath); err != nil {
		return fmt.Errorf("source file not found: %w", err)
	}
	return nil
}

// createProgressUpdater creates a function to update job progress
func (s *encodingService) createProgressUpdater(ctx context.Context, jobID string, totalTasks int) func() {
	var done int32
	return func() {
		atomic.AddInt32(&done, 1)
		progress := int(float64(atomic.LoadInt32(&done)) / float64(totalTasks) * 100)
		_ = s.repo.UpdateJobProgress(ctx, jobID, progress)
	}
}

// encodeResolutions encodes all target resolutions concurrently
func (s *encodingService) encodeResolutions(ctx context.Context, job *domain.EncodingJob, outBaseDir string, update func()) error {
	var wg sync.WaitGroup
	errCh := make(chan error, len(job.TargetResolutions))

	for _, res := range job.TargetResolutions {
		height, ok := domain.HeightForResolution(res)
		if !ok {
			continue
		}
		wg.Add(1)
		go func(h int, label string) {
			defer wg.Done()
			resDir := filepath.Join(outBaseDir, fmt.Sprintf("%dp", h))
			if err := os.MkdirAll(resDir, 0o750); err != nil {
				errCh <- err
				return
			}
			outPlaylist := filepath.Join(resDir, "stream.m3u8")
			segPattern := filepath.Join(resDir, "segment_%05d.ts")
			if err := s.transcodeHLS(ctx, job.SourceFilePath, h, outPlaylist, segPattern); err != nil {
				errCh <- fmt.Errorf("encode %s: %w", label, err)
				return
			}
			update()
		}(height, res)
	}

	wg.Wait()
	close(errCh)
	for e := range errCh {
		if e != nil {
			return e
		}
	}
	return nil
}

func (s *encodingService) processJob(ctx context.Context, job *domain.EncodingJob) error {
	// Validate job
	if err := s.validateJob(job); err != nil {
		return err
	}

	// Setup directories
	sp := storage.NewPaths(s.uploadsDir)
	outBaseDir := sp.HLSVideoDir(job.VideoID)
	if err := os.MkdirAll(outBaseDir, 0o750); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	// Create progress updater
	totalTasks := len(job.TargetResolutions) + 2 // + thumbnail + preview
	update := s.createProgressUpdater(ctx, job.ID, totalTasks)

	// Encode all resolutions
	if err := s.encodeResolutions(ctx, job, outBaseDir, update); err != nil {
		return err
	}

	// Generate master playlist
	if err := s.generateMasterPlaylist(outBaseDir, job.TargetResolutions); err != nil {
		return fmt.Errorf("master playlist: %w", err)
	}

	// Generate media assets (thumbnail and preview)
	sp := storage.NewPaths(s.uploadsDir)
	thumb, preview, err := s.generateMediaAssets(ctx, job, sp, update)
	if err != nil {
		return err
	}

	// Update video processing info
	if err := s.updateVideoInfo(ctx, job, outBaseDir, thumb, preview); err != nil {
		return err
	}

	// Trigger notifications
	s.triggerNotifications(ctx, job.VideoID)

	return nil
}

// generateMediaAssets generates thumbnail and preview for the video
func (s *encodingService) generateMediaAssets(ctx context.Context, job *domain.EncodingJob, sp *storage.Paths, update func()) (string, string, error) {
	// Thumbnail
	thumb := sp.ThumbnailPath(job.VideoID)
	if err := os.MkdirAll(filepath.Dir(thumb), 0o750); err != nil {
		return "", "", fmt.Errorf("failed to create thumbnail dir: %w", err)
	}
	if err := s.generateThumbnail(ctx, job.SourceFilePath, thumb); err != nil {
		return "", "", fmt.Errorf("thumbnail: %w", err)
	}
	update()

	// Preview (animated webp)
	preview := sp.PreviewPath(job.VideoID)
	if err := os.MkdirAll(filepath.Dir(preview), 0o750); err != nil {
		return "", "", fmt.Errorf("failed to create preview dir: %w", err)
	}
	if err := s.generatePreviewWebP(ctx, job.SourceFilePath, preview); err != nil {
		return "", "", fmt.Errorf("preview: %w", err)
	}
	update()

	return thumb, preview, nil
}

// updateVideoInfo updates the video processing info in the repository
func (s *encodingService) updateVideoInfo(ctx context.Context, job *domain.EncodingJob, outBaseDir, thumb, preview string) error {
	outputs := make(map[string]string)
	outputs["master"] = filepath.ToSlash(filepath.Join(outBaseDir, "master.m3u8"))
	for _, res := range job.TargetResolutions {
		if h, ok := domain.HeightForResolution(res); ok {
			outputs[res] = filepath.ToSlash(filepath.Join(outBaseDir, fmt.Sprintf("%dp/stream.m3u8", h)))
		}
	}
	return s.videoRepo.UpdateProcessingInfo(ctx, job.VideoID, domain.StatusCompleted, outputs, filepath.ToSlash(thumb), filepath.ToSlash(preview))
}

// triggerNotifications triggers notifications for video processing completion
func (s *encodingService) triggerNotifications(ctx context.Context, videoID string) {
	if s.notificationSvc != nil {
		video, err := s.videoRepo.GetByID(ctx, videoID)
		if err == nil && video != nil {
			// Notifications will only be created if video is public and completed
			_ = s.notificationSvc.CreateVideoNotificationForSubscribers(ctx, video, "")
		}
	}
}

func (s *encodingService) transcodeHLS(ctx context.Context, input string, height int, outPlaylist string, segPattern string) error {
	args := []string{
		"-y",
		"-i", input,
		"-vf", fmt.Sprintf("scale=-2:%d", height),
		"-c:v", "libx264",
		"-preset", "veryfast",
		"-crf", "23",
		"-c:a", "aac",
		"-b:a", "128k",
		"-f", "hls",
		"-hls_time", fmt.Sprintf("%d", s.cfg.HLSSegmentDuration),
		"-hls_playlist_type", "vod",
		"-hls_segment_filename", segPattern,
		outPlaylist,
	}
	return s.execFFmpeg(ctx, args)
}

func (s *encodingService) generateThumbnail(ctx context.Context, input string, output string) error {
	args := []string{
		"-y",
		"-ss", "00:00:01",
		"-i", input,
		"-frames:v", "1",
		"-q:v", "2",
		output,
	}
	return s.execFFmpeg(ctx, args)
}

func (s *encodingService) generatePreviewWebP(ctx context.Context, input string, output string) error {
	args := []string{
		"-y",
		"-ss", "00:00:01",
		"-t", "3",
		"-i", input,
		"-vf", "fps=10,scale=320:-2",
		"-loop", "0",
		"-an",
		"-vsync", "0",
		"-c:v", "libwebp",
		"-quality", "80",
		output,
	}
	return s.execFFmpeg(ctx, args)
}

func (s *encodingService) generateMasterPlaylist(outBaseDir string, resolutions []string) error {
	// Simple bandwidth estimates (in bits per second)
	bw := map[string]int{
		"240p": 400000, "360p": 800000, "480p": 1400000, "720p": 2800000,
		"1080p": 5000000, "1440p": 8000000, "2160p": 12000000, "4320p": 50000000,
	}
	var b strings.Builder
	b.WriteString("#EXTM3U\n#EXT-X-VERSION:3\n")
	for _, res := range resolutions {
		if _, ok := bw[res]; !ok {
			continue
		}
		// path: {height}p/stream.m3u8
		if h, ok := domain.HeightForResolution(res); ok {
			b.WriteString(fmt.Sprintf("#EXT-X-STREAM-INF:BANDWIDTH=%d,NAME=\"%s\"\n", bw[res], res))
			b.WriteString(fmt.Sprintf("%dp/stream.m3u8\n", h))
		}
	}
	return os.WriteFile(filepath.Join(outBaseDir, "master.m3u8"), []byte(b.String()), 0o600)
}

func (s *encodingService) execFFmpeg(ctx context.Context, args []string) error {
	bin := s.cfg.FFMPEGPath
	if bin == "" {
		bin = "ffmpeg"
	}
	// Validate binary path to prevent command injection
	if err := validateBinaryPath(bin); err != nil {
		return fmt.Errorf("invalid ffmpeg binary path: %w", err)
	}
	// #nosec G204 - ffmpeg binary path is validated by validateBinaryPath and args are vetted
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}
	return nil
}

// validateBinaryPath validates that the binary path is safe to execute
func validateBinaryPath(path string) error {
	// For system binaries like "ffmpeg", allow simple names
	if !strings.Contains(path, "/") && !strings.Contains(path, "\\") {
		// Simple binary name, should be in PATH - this is safe
		return nil
	}

	// For full paths, ensure they don't contain suspicious elements
	cleanPath := filepath.Clean(path)

	// Reject paths containing directory traversal
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path contains directory traversal: %s", path)
	}

	// Reject paths with suspicious characters that could be used for injection
	if strings.ContainsAny(cleanPath, ";|&$`") {
		return fmt.Errorf("path contains suspicious characters: %s", path)
	}

	return nil
}
