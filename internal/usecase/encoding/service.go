package encoding

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/ipfs"
	"athena/internal/metrics"
	"athena/internal/port"
	"athena/internal/storage"

	ucn "athena/internal/usecase/notification"

	"github.com/google/uuid"
)

// Service defines a background worker to process encoding jobs
type Service interface {
	// Run starts N workers that consume jobs until the context is canceled
	Run(ctx context.Context, workers int) error
	// ProcessNext fetches the next pending job and processes it
	ProcessNext(ctx context.Context) (processed bool, err error)
}

// Publisher publishes activity (best-effort) when encoding completes.
// Only the PublishVideo method is required by this package.
type Publisher interface {
	PublishVideo(ctx context.Context, v *domain.Video) error
}

// JobEnqueuer enqueues federation jobs for retry/out-of-band processing.
type JobEnqueuer interface {
	EnqueueJob(ctx context.Context, jobType string, payload any, runAt time.Time) (string, error)
}

// CaptionGenerator enqueues caption generation jobs after encoding completes.
type CaptionGenerator interface {
	CreateJob(ctx context.Context, videoID uuid.UUID, userID uuid.UUID, req *domain.CreateCaptionGenerationJobRequest) (*domain.CaptionGenerationJob, error)
}

type service struct {
	repo            port.EncodingRepository
	videoRepo       port.VideoRepository
	notificationSvc ucn.Service
	uploadsDir      string // storage root
	cfg             *config.Config
	atproto         Publisher
	fedEnq          JobEnqueuer
	ipfsClient      *ipfs.Client
	captionGen      CaptionGenerator // Optional caption generation service
}

func NewService(repo port.EncodingRepository, videoRepo port.VideoRepository, notificationSvc ucn.Service, uploadsDir string, cfg *config.Config, atproto Publisher, enq JobEnqueuer, ipfsClient *ipfs.Client) Service {
	return &service{repo: repo, videoRepo: videoRepo, notificationSvc: notificationSvc, uploadsDir: uploadsDir, cfg: cfg, atproto: atproto, fedEnq: enq, ipfsClient: ipfsClient}
}

// WithFederationEnqueuer optionally attaches a federation job enqueuer to the encoding service.
func (s *service) WithFederationEnqueuer(enq JobEnqueuer) *service {
	s.fedEnq = enq
	return s
}

// WithCaptionGenerator optionally attaches a caption generator to the encoding service.
func (s *service) WithCaptionGenerator(gen CaptionGenerator) *service {
	s.captionGen = gen
	return s
}

func (s *service) Run(ctx context.Context, workers int) error {
	if workers <= 0 {
		workers = runtime.NumCPU()
		if workers < 2 {
			workers = 2
		}
	}

	// Recover orphaned jobs that were stuck in "processing" from a prior crash.
	// The heartbeat in processJob keeps updated_at fresh every 5 minutes for
	// active jobs, so a 30-minute threshold safely identifies only truly dead jobs.
	const defaultStaleDuration = 30 * time.Minute
	resetCount, err := s.repo.ResetStaleJobs(ctx, defaultStaleDuration)
	if err != nil {
		log.Printf("Warning: failed to reset stale encoding jobs: %v", err)
	} else if resetCount > 0 {
		log.Printf("Recovered %d stale encoding job(s) back to pending", resetCount)
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

func (s *service) ProcessNext(ctx context.Context) (bool, error) {
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
func (s *service) validateJob(job *domain.EncodingJob) error {
	if job.SourceFilePath == "" {
		return errors.New("missing source file path")
	}
	if _, err := os.Stat(job.SourceFilePath); err != nil {
		return fmt.Errorf("source file not found: %w", err)
	}
	return nil
}

// createProgressUpdater creates a function to update job progress
func (s *service) createProgressUpdater(ctx context.Context, jobID string, totalTasks int) func() {
	var done int32
	return func() {
		atomic.AddInt32(&done, 1)
		progress := int(float64(atomic.LoadInt32(&done)) / float64(totalTasks) * 100)
		_ = s.repo.UpdateJobProgress(ctx, jobID, progress)
	}
}

// encodeResolutions encodes all target resolutions concurrently
func (s *service) encodeResolutions(ctx context.Context, job *domain.EncodingJob, outBaseDir string, update func()) error {
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
			if err := s.transcodeHLS(ctx, job.SourceFilePath, h, outPlaylist, segPattern, job.ID); err != nil {
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

func (s *service) processJob(ctx context.Context, job *domain.EncodingJob) error {
	// Start heartbeat to keep updated_at fresh during long encodes.
	// This prevents ResetStaleJobs from incorrectly resetting active jobs
	// that may take hours (e.g., 4K video on a low-spec server).
	hbCtx, hbCancel := context.WithCancel(ctx)
	defer hbCancel()
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				_ = s.repo.UpdateJobProgress(hbCtx, job.ID, job.Progress)
			}
		}
	}()

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

	// Upload variants to IPFS (if enabled)
	processedCIDs, err := s.uploadVariantsToIPFS(ctx, job, outBaseDir)
	if err != nil {
		// Log error but don't fail the job - local files are still available
		// In production, you'd use a proper logger here
		fmt.Printf("Warning: Failed to upload variants to IPFS: %v\n", err)
		processedCIDs = make(map[string]string) // Empty map
	}

	// Generate media assets (thumbnail and preview)
	thumb, preview, err := s.generateMediaAssets(ctx, job, &sp, update)
	if err != nil {
		return err
	}

	// Upload thumbnail and preview to IPFS (if enabled)
	thumbCID, previewCID := s.uploadMediaToIPFS(ctx, thumb, preview)

	// Update video processing info
	if err := s.updateVideoInfo(ctx, job, outBaseDir, thumb, preview, processedCIDs, thumbCID, previewCID); err != nil {
		return err
	}

	// Publish to ATProto (best-effort) if enabled and video is public
	if s.atproto != nil {
		if v, err := s.videoRepo.GetByID(ctx, job.VideoID); err == nil && v != nil {
			if err := s.atproto.PublishVideo(ctx, v); err != nil && s.fedEnq != nil {
				// Enqueue retry with small delay
				_ = s.enqueuePublishRetry(ctx, v.ID, 30*time.Second)
			}
		}
	}

	// Trigger notifications
	s.triggerNotifications(ctx, job.VideoID)

	// Trigger automatic caption generation (if enabled)
	s.triggerCaptionGeneration(ctx, job.VideoID)

	return nil
}

func (s *service) enqueuePublishRetry(ctx context.Context, videoID string, delay time.Duration) error {
	if s.fedEnq == nil {
		return nil
	}
	payload := map[string]any{"videoId": videoID}
	when := time.Now().Add(delay)
	_, err := s.fedEnq.EnqueueJob(ctx, "publish_post", payload, when)
	return err
}

// generateMediaAssets generates thumbnail and preview for the video
func (s *service) generateMediaAssets(ctx context.Context, job *domain.EncodingJob, sp *storage.Paths, update func()) (string, string, error) {
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

// uploadVariantsToIPFS uploads all resolution variants to IPFS
func (s *service) uploadVariantsToIPFS(ctx context.Context, job *domain.EncodingJob, outBaseDir string) (map[string]string, error) {
	processedCIDs := make(map[string]string)

	// Skip if IPFS is not enabled
	if s.ipfsClient == nil || !s.ipfsClient.IsEnabled() {
		return processedCIDs, nil
	}

	// Upload master playlist
	masterPath := filepath.Join(outBaseDir, "master.m3u8")
	if _, err := os.Stat(masterPath); err == nil {
		cid, err := s.ipfsClient.AddAndPin(ctx, masterPath)
		if err != nil {
			return nil, fmt.Errorf("failed to upload master playlist: %w", err)
		}
		processedCIDs["master"] = cid
	}

	// Upload each resolution variant directory
	// Using goroutines for concurrent uploads
	var wg sync.WaitGroup
	var mu sync.Mutex
	errCh := make(chan error, len(job.TargetResolutions))

	for _, res := range job.TargetResolutions {
		height, ok := domain.HeightForResolution(res)
		if !ok {
			continue
		}

		wg.Add(1)
		go func(resolution string, h int) {
			defer wg.Done()

			resDir := filepath.Join(outBaseDir, fmt.Sprintf("%dp", h))

			// Verify directory exists
			if _, err := os.Stat(resDir); os.IsNotExist(err) {
				errCh <- fmt.Errorf("resolution directory not found: %s", resDir)
				return
			}

			// Upload directory to IPFS
			cid, err := s.ipfsClient.AddDirectoryAndPin(ctx, resDir)
			if err != nil {
				errCh <- fmt.Errorf("failed to upload %s variant: %w", resolution, err)
				return
			}

			// Store CID
			mu.Lock()
			processedCIDs[resolution] = cid
			mu.Unlock()
		}(res, height)
	}

	wg.Wait()
	close(errCh)

	// Check for errors
	for err := range errCh {
		if err != nil {
			return processedCIDs, err
		}
	}

	return processedCIDs, nil
}

// uploadMediaToIPFS uploads thumbnail and preview to IPFS
func (s *service) uploadMediaToIPFS(ctx context.Context, thumbPath, previewPath string) (thumbCID, previewCID string) {
	// Skip if IPFS is not enabled
	if s.ipfsClient == nil || !s.ipfsClient.IsEnabled() {
		return "", ""
	}

	// Upload thumbnail (best-effort)
	if thumbPath != "" {
		if _, err := os.Stat(thumbPath); err == nil {
			cid, err := s.ipfsClient.AddAndPin(ctx, thumbPath)
			if err == nil {
				thumbCID = cid
			}
		}
	}

	// Upload preview (best-effort)
	if previewPath != "" {
		if _, err := os.Stat(previewPath); err == nil {
			cid, err := s.ipfsClient.AddAndPin(ctx, previewPath)
			if err == nil {
				previewCID = cid
			}
		}
	}

	return thumbCID, previewCID
}

// updateVideoInfo updates the video processing info in the repository
func (s *service) updateVideoInfo(ctx context.Context, job *domain.EncodingJob, outBaseDir, thumb, preview string, processedCIDs map[string]string, thumbCID, previewCID string) error {
	outputs := make(map[string]string)
	outputs["master"] = filepath.ToSlash(filepath.Join(outBaseDir, "master.m3u8"))
	for _, res := range job.TargetResolutions {
		if h, ok := domain.HeightForResolution(res); ok {
			outputs[res] = filepath.ToSlash(filepath.Join(outBaseDir, fmt.Sprintf("%dp/stream.m3u8", h)))
		}
	}

	// Update with IPFS CIDs
	return s.videoRepo.UpdateProcessingInfoWithCIDs(ctx, job.VideoID, domain.StatusCompleted, outputs, filepath.ToSlash(thumb), filepath.ToSlash(preview), processedCIDs, thumbCID, previewCID)
}

// triggerNotifications triggers notifications for video processing completion
func (s *service) triggerNotifications(ctx context.Context, videoID string) {
	if s.notificationSvc != nil {
		video, err := s.videoRepo.GetByID(ctx, videoID)
		if err == nil && video != nil {
			// Notifications will only be created if video is public and completed
			_ = s.notificationSvc.CreateVideoNotificationForSubscribers(ctx, video, "")
		}
	}
}

// triggerCaptionGeneration triggers automatic caption generation after video encoding completes
func (s *service) triggerCaptionGeneration(ctx context.Context, videoID string) {
	if s.captionGen == nil {
		return
	}

	// Check if automatic caption generation is enabled in config
	if s.cfg != nil && !s.cfg.EnableCaptionGeneration {
		return
	}

	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil || video == nil {
		return
	}

	// Create caption generation job (automatic, no target language = auto-detect)
	vidUUID, err := uuid.Parse(videoID)
	if err != nil {
		return
	}

	userUUID, err := uuid.Parse(video.UserID)
	if err != nil {
		return
	}

	req := &domain.CreateCaptionGenerationJobRequest{
		VideoID:        vidUUID,
		TargetLanguage: nil, // Auto-detect language
		ModelSize:      domain.WhisperModelBase,
		OutputFormat:   domain.CaptionFormatVTT,
	}

	// Best-effort: don't fail encoding if caption generation enqueue fails
	_, _ = s.captionGen.CreateJob(ctx, vidUUID, userUUID, req)
}

func (s *service) transcodeHLS(ctx context.Context, input string, height int, outPlaylist string, segPattern string, jobID string) error {
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

	// Use progress tracking if jobID is provided
	if jobID != "" {
		resLabel := fmt.Sprintf("%dp", height)
		return s.execFFmpegWithProgress(ctx, args, jobID, resLabel)
	}
	return s.execFFmpeg(ctx, args)
}

func (s *service) generateThumbnail(ctx context.Context, input string, output string) error {
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

func (s *service) generatePreviewWebP(ctx context.Context, input string, output string) error {
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

func (s *service) generateMasterPlaylist(outBaseDir string, resolutions []string) error {
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

func (s *service) execFFmpeg(ctx context.Context, args []string) error {
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

// getVideoDuration uses ffprobe to get the duration of a video file
func (s *service) getVideoDuration(ctx context.Context, input string) (time.Duration, error) {
	bin := "ffprobe"
	if s.cfg.FFMPEGPath != "" {
		// If FFMPEGPath is specified, assume ffprobe is in the same directory
		bin = filepath.Join(filepath.Dir(s.cfg.FFMPEGPath), "ffprobe")
	}

	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "json",
		input,
	}

	// #nosec G204 - ffprobe binary path is controlled and args are vetted
	cmd := exec.CommandContext(ctx, bin, args...)
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var result struct {
		Format struct {
			Duration string `json:"duration"`
		} `json:"format"`
	}

	if err := json.Unmarshal(output, &result); err != nil {
		return 0, fmt.Errorf("failed to parse ffprobe output: %w", err)
	}

	seconds, err := strconv.ParseFloat(result.Format.Duration, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse duration: %w", err)
	}

	return time.Duration(seconds) * time.Second, nil
}

// execFFmpegWithProgress executes FFmpeg with real-time progress tracking
func (s *service) execFFmpegWithProgress(ctx context.Context, args []string, jobID string, resolutionLabel string) error {
	bin := s.cfg.FFMPEGPath
	if bin == "" {
		bin = "ffmpeg"
	}

	// Validate binary path to prevent command injection
	if err := validateBinaryPath(bin); err != nil {
		return fmt.Errorf("invalid ffmpeg binary path: %w", err)
	}

	// Get video duration first for progress calculation
	// Extract input file from args
	var inputFile string
	for i, arg := range args {
		if arg == "-i" && i+1 < len(args) {
			inputFile = args[i+1]
			break
		}
	}

	if inputFile == "" {
		return fmt.Errorf("no input file found in ffmpeg args")
	}

	duration, err := s.getVideoDuration(ctx, inputFile)
	if err != nil {
		// Log error but continue without fine-grained progress
		log.Printf("Warning: Could not get video duration for progress tracking: %v", err)
		// Fall back to regular execution
		return s.execFFmpeg(ctx, args)
	}

	// Add progress reporting to stderr
	progressArgs := append([]string{"-progress", "pipe:2", "-stats"}, args...)

	// #nosec G204 - ffmpeg binary path is validated by validateBinaryPath and args are vetted
	cmd := exec.CommandContext(ctx, bin, progressArgs...)

	// Create pipes for stderr to capture progress
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	// Create progress parser
	progressParser := NewProgressParser(duration)

	// Parse progress in a goroutine
	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		scanner := bufio.NewScanner(stderr)
		lastProgress := -1
		progressBatch := make(map[string]string)

		for scanner.Scan() {
			line := scanner.Text()

			// FFmpeg -progress outputs key=value pairs
			if strings.Contains(line, "=") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					progressBatch[parts[0]] = parts[1]
				}
			}

			// When we see "progress=", process the batch
			if line == "progress=continue" || line == "progress=end" {
				// Convert batch to string for parsing
				var batchStr strings.Builder
				for k, v := range progressBatch {
					batchStr.WriteString(k)
					batchStr.WriteString("=")
					batchStr.WriteString(v)
					batchStr.WriteString("\n")
				}

				progress, _, _, found := progressParser.ParseProgressStats(batchStr.String())
				if found && progress != lastProgress {
					// Update every 5% or on significant changes
					if progress >= lastProgress+5 || progress == 100 {
						if err := s.repo.UpdateJobProgress(ctx, jobID, progress); err != nil {
							log.Printf("Failed to update job progress: %v", err)
						}
						lastProgress = progress
						log.Printf("Encoding %s for job %s: %d%%", resolutionLabel, jobID, progress)
					}
				}

				// Clear batch for next set
				progressBatch = make(map[string]string)
			}

			// Also try to parse the line directly for older FFmpeg versions
			if progress, found := progressParser.ParseLine(line); found && progress != lastProgress {
				if progress >= lastProgress+5 || progress == 100 {
					if err := s.repo.UpdateJobProgress(ctx, jobID, progress); err != nil {
						log.Printf("Failed to update job progress: %v", err)
					}
					lastProgress = progress
					log.Printf("Encoding %s for job %s: %d%%", resolutionLabel, jobID, progress)
				}
			}
		}

		if err := scanner.Err(); err != nil {
			log.Printf("Error reading ffmpeg output: %v", err)
		}
	}()

	// Wait for command to complete
	err = cmd.Wait()

	// Wait for progress parsing to finish
	<-progressDone

	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	return nil
}
