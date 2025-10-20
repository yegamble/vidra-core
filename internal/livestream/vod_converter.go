package livestream

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/repository"
	"athena/internal/usecase"
)

// VODConversionJob represents a VOD conversion job
type VODConversionJob struct {
	StreamID    uuid.UUID
	StreamTitle string
	UserID      uuid.UUID
	SegmentsDir string
	OutputPath  string
	Status      string // "pending", "processing", "completed", "failed"
	StartedAt   time.Time
	CompletedAt *time.Time
	Error       string
	IPFSCid     string
	VideoID     *uuid.UUID
	Ctx         context.Context
	Cancel      context.CancelFunc
}

// VODConverter manages conversion of live streams to VOD
type VODConverter struct {
	cfg          *config.Config
	streamRepo   repository.LiveStreamRepository
	videoRepo    usecase.VideoRepository
	logger       *logrus.Logger
	jobs         map[uuid.UUID]*VODConversionJob
	mu           sync.RWMutex
	jobQueue     chan *VODConversionJob
	workers      int
	shutdownChan chan struct{}
	wg           sync.WaitGroup
}

// NewVODConverter creates a new VOD converter
func NewVODConverter(
	cfg *config.Config,
	streamRepo repository.LiveStreamRepository,
	videoRepo usecase.VideoRepository,
	logger *logrus.Logger,
	workers int,
) *VODConverter {
	if workers <= 0 {
		workers = 2 // Default to 2 workers
	}

	v := &VODConverter{
		cfg:          cfg,
		streamRepo:   streamRepo,
		videoRepo:    videoRepo,
		logger:       logger,
		jobs:         make(map[uuid.UUID]*VODConversionJob),
		jobQueue:     make(chan *VODConversionJob, 100),
		workers:      workers,
		shutdownChan: make(chan struct{}),
	}

	// Start worker pool
	v.startWorkers()

	return v
}

// startWorkers starts the worker pool for processing VOD conversions
func (v *VODConverter) startWorkers() {
	for i := 0; i < v.workers; i++ {
		v.wg.Add(1)
		go v.worker(i)
	}
	v.logger.WithField("workers", v.workers).Info("Started VOD conversion workers")
}

// worker processes VOD conversion jobs from the queue
func (v *VODConverter) worker(id int) {
	defer v.wg.Done()

	v.logger.WithField("worker_id", id).Debug("VOD worker started")

	for {
		select {
		case <-v.shutdownChan:
			v.logger.WithField("worker_id", id).Debug("VOD worker shutting down")
			return

		case job := <-v.jobQueue:
			v.logger.WithFields(logrus.Fields{
				"worker_id": id,
				"stream_id": job.StreamID,
			}).Info("Processing VOD conversion job")

			v.processJob(job)
		}
	}
}

// ConvertStreamToVOD queues a stream for VOD conversion
func (v *VODConverter) ConvertStreamToVOD(ctx context.Context, stream *domain.LiveStream) error {
	if !v.cfg.EnableReplayConversion {
		v.logger.Debug("VOD conversion disabled in config")
		return nil
	}

	// Check if job already exists
	v.mu.RLock()
	if _, exists := v.jobs[stream.ID]; exists {
		v.mu.RUnlock()
		return fmt.Errorf("conversion job already exists for stream %s", stream.ID)
	}
	v.mu.RUnlock()

	// Create output directory
	outputDir := v.cfg.ReplayStorageDir
	if outputDir == "" {
		outputDir = filepath.Join(v.cfg.HLSOutputDir, "..", "replays")
	}
	if err := os.MkdirAll(outputDir, 0755); err != nil {
		return fmt.Errorf("failed to create replay directory: %w", err)
	}

	// Create job context
	jobCtx, cancel := context.WithCancel(context.Background())

	// Create job
	job := &VODConversionJob{
		StreamID:    stream.ID,
		StreamTitle: stream.Title,
		UserID:      stream.UserID,
		SegmentsDir: filepath.Join(v.cfg.HLSOutputDir, stream.ID.String()),
		OutputPath:  filepath.Join(outputDir, fmt.Sprintf("%s.mp4", stream.ID)),
		Status:      "pending",
		StartedAt:   time.Now(),
		Ctx:         jobCtx,
		Cancel:      cancel,
	}

	// Store job
	v.mu.Lock()
	v.jobs[stream.ID] = job
	v.mu.Unlock()

	// Queue job
	select {
	case v.jobQueue <- job:
		v.logger.WithField("stream_id", stream.ID).Info("Queued VOD conversion job")
		return nil
	case <-ctx.Done():
		cancel()
		return ctx.Err()
	}
}

// processJob processes a single VOD conversion job
func (v *VODConverter) processJob(job *VODConversionJob) {
	// Update status
	v.mu.Lock()
	job.Status = "processing"
	v.mu.Unlock()

	// Use job context if available, otherwise use background context
	parentCtx := job.Ctx
	if parentCtx == nil {
		parentCtx = context.Background()
	}

	// Process with timeout
	ctx, cancel := context.WithTimeout(parentCtx, 30*time.Minute)
	defer cancel()

	// Step 1: Find the best variant (highest quality available)
	variant, err := v.findBestVariant(job.SegmentsDir)
	if err != nil {
		v.failJob(job, fmt.Errorf("failed to find variant: %w", err))
		return
	}

	v.logger.WithFields(logrus.Fields{
		"stream_id": job.StreamID,
		"variant":   variant,
	}).Info("Selected variant for VOD conversion")

	// Step 2: Concatenate segments
	tempFile := job.OutputPath + ".tmp"
	if err := v.concatenateSegments(ctx, job.SegmentsDir, variant, tempFile); err != nil {
		v.failJob(job, fmt.Errorf("failed to concatenate segments: %w", err))
		return
	}

	// Step 3: Optimize for web streaming (+faststart)
	if err := v.optimizeVideo(ctx, tempFile, job.OutputPath); err != nil {
		if removeErr := os.Remove(tempFile); removeErr != nil {
			v.logger.WithError(removeErr).Warn("Failed to remove temp file after optimization failure")
		}
		v.failJob(job, fmt.Errorf("failed to optimize video: %w", err))
		return
	}

	// Clean up temp file
	if err := os.Remove(tempFile); err != nil {
		v.logger.WithError(err).Warn("Failed to remove temp file")
	}

	// Step 4: Upload to IPFS (if enabled)
	if v.cfg.ReplayUploadToIPFS {
		cid, err := v.uploadToIPFS(ctx, job.OutputPath)
		if err != nil {
			v.logger.WithError(err).WithField("stream_id", job.StreamID).Warn("Failed to upload replay to IPFS")
		} else {
			job.IPFSCid = cid
			v.logger.WithFields(logrus.Fields{
				"stream_id": job.StreamID,
				"cid":       cid,
			}).Info("Uploaded replay to IPFS")
		}
	}

	// Step 5: Create video entry in database (optional - depends on business logic)
	// This creates a permanent video record from the live stream
	videoID, err := v.createVideoFromStream(ctx, job)
	if err != nil {
		v.logger.WithError(err).WithField("stream_id", job.StreamID).Warn("Failed to create video entry")
	} else {
		job.VideoID = &videoID
	}

	// Step 6: Clean up HLS segments (if retention policy allows)
	v.cleanupSegments(job.SegmentsDir)

	// Mark job as completed
	v.completeJob(job)
}

// findBestVariant finds the highest quality variant available
func (v *VODConverter) findBestVariant(segmentsDir string) (string, error) {
	// Preferred order: 1080p -> 720p -> 480p -> 360p
	variants := []string{"1080p", "720p", "480p", "360p"}

	for _, variant := range variants {
		variantDir := filepath.Join(segmentsDir, variant)
		if _, err := os.Stat(variantDir); err == nil {
			// Check if directory has segments
			entries, err := os.ReadDir(variantDir)
			if err == nil && len(entries) > 0 {
				return variant, nil
			}
		}
	}

	return "", fmt.Errorf("no valid variant found in %s", segmentsDir)
}

// concatenateSegments concatenates HLS segments into a single file
func (v *VODConverter) concatenateSegments(ctx context.Context, segmentsDir, variant, outputPath string) error {
	variantDir := filepath.Join(segmentsDir, variant)
	playlistPath := filepath.Join(variantDir, "index.m3u8")

	// Check if playlist exists
	if _, err := os.Stat(playlistPath); os.IsNotExist(err) {
		return fmt.Errorf("playlist not found: %s", playlistPath)
	}

	// Use FFmpeg to concatenate segments
	// -allowed_extensions ALL allows reading .ts files
	// -protocol_whitelist file,http,https,tcp,tls for security
	args := []string{
		"-allowed_extensions", "ALL",
		"-protocol_whitelist", "file,http,https,tcp,tls",
		"-i", playlistPath,
		"-c", "copy", // Copy streams without re-encoding
		"-bsf:a", "aac_adtstoasc", // Convert AAC to MP4 format
		"-y", // Overwrite output file
		outputPath,
	}

	cmd := exec.CommandContext(ctx, v.cfg.FFmpegPath, args...)
	cmd.Env = os.Environ()

	// Capture output for logging
	output, err := cmd.CombinedOutput()
	if err != nil {
		v.logger.WithError(err).WithField("output", string(output)).Error("FFmpeg concatenation failed")
		return fmt.Errorf("ffmpeg concatenation failed: %w", err)
	}

	v.logger.WithField("output_path", outputPath).Debug("Segments concatenated successfully")
	return nil
}

// optimizeVideo optimizes the video for web streaming
func (v *VODConverter) optimizeVideo(ctx context.Context, inputPath, outputPath string) error {
	// Use FFmpeg to add faststart flag for web streaming
	// This moves the moov atom to the beginning of the file
	args := []string{
		"-i", inputPath,
		"-c", "copy", // Copy streams without re-encoding
		"-movflags", "+faststart", // Enable fast start for web streaming
		"-y", // Overwrite output file
		outputPath,
	}

	cmd := exec.CommandContext(ctx, v.cfg.FFmpegPath, args...)
	cmd.Env = os.Environ()

	output, err := cmd.CombinedOutput()
	if err != nil {
		v.logger.WithError(err).WithField("output", string(output)).Error("FFmpeg optimization failed")
		return fmt.Errorf("ffmpeg optimization failed: %w", err)
	}

	v.logger.WithField("output_path", outputPath).Debug("Video optimized successfully")
	return nil
}

// uploadToIPFS uploads the replay to IPFS
func (v *VODConverter) uploadToIPFS(ctx context.Context, filePath string) (string, error) {
	// This is a placeholder for IPFS upload logic
	// In a real implementation, you would use the IPFS client
	// to upload the file and return the CID

	// TODO: Implement IPFS upload using internal/ipfs package
	// For now, return empty string to indicate no upload
	v.logger.WithField("file_path", filePath).Debug("IPFS upload not yet implemented")
	return "", fmt.Errorf("IPFS upload not yet implemented")
}

// createVideoFromStream creates a video entry from the stream
func (v *VODConverter) createVideoFromStream(ctx context.Context, job *VODConversionJob) (uuid.UUID, error) {
	// This is a placeholder for video creation logic
	// In a real implementation, you would create a video entry in the database
	// linking it to the original stream and the replay file

	// For now, return a dummy UUID
	v.logger.WithField("stream_id", job.StreamID).Debug("Video creation not yet implemented")
	return uuid.New(), fmt.Errorf("video creation not yet implemented")
}

// cleanupSegments removes HLS segments based on retention policy
func (v *VODConverter) cleanupSegments(segmentsDir string) {
	if v.cfg.ReplayRetentionDays == 0 {
		// Retention disabled, keep segments forever
		return
	}

	// Remove entire segments directory
	if err := os.RemoveAll(segmentsDir); err != nil {
		v.logger.WithError(err).WithField("dir", segmentsDir).Warn("Failed to clean up segments")
	} else {
		v.logger.WithField("dir", segmentsDir).Info("Cleaned up HLS segments")
	}
}

// failJob marks a job as failed
func (v *VODConverter) failJob(job *VODConversionJob, err error) {
	v.mu.Lock()
	defer v.mu.Unlock()

	job.Status = "failed"
	job.Error = err.Error()
	now := time.Now()
	job.CompletedAt = &now

	v.logger.WithError(err).WithField("stream_id", job.StreamID).Error("VOD conversion failed")
}

// completeJob marks a job as completed
func (v *VODConverter) completeJob(job *VODConversionJob) {
	v.mu.Lock()
	defer v.mu.Unlock()

	job.Status = "completed"
	now := time.Now()
	job.CompletedAt = &now

	v.logger.WithFields(logrus.Fields{
		"stream_id": job.StreamID,
		"output":    job.OutputPath,
		"ipfs_cid":  job.IPFSCid,
		"video_id":  job.VideoID,
		"duration":  now.Sub(job.StartedAt),
	}).Info("VOD conversion completed")
}

// GetJob returns a conversion job
func (v *VODConverter) GetJob(streamID uuid.UUID) (*VODConversionJob, bool) {
	v.mu.RLock()
	defer v.mu.RUnlock()
	job, exists := v.jobs[streamID]
	return job, exists
}

// CancelJob cancels a conversion job
func (v *VODConverter) CancelJob(streamID uuid.UUID) error {
	v.mu.Lock()
	job, exists := v.jobs[streamID]
	if !exists {
		v.mu.Unlock()
		return fmt.Errorf("job not found for stream %s", streamID)
	}
	v.mu.Unlock()

	job.Cancel()
	v.logger.WithField("stream_id", streamID).Info("Cancelled VOD conversion job")
	return nil
}

// GetActiveJobCount returns the number of active jobs
func (v *VODConverter) GetActiveJobCount() int {
	v.mu.RLock()
	defer v.mu.RUnlock()
	return len(v.jobs)
}

// GetQueueLength returns the number of jobs in the queue
func (v *VODConverter) GetQueueLength() int {
	return len(v.jobQueue)
}

// Shutdown gracefully stops all conversion jobs
func (v *VODConverter) Shutdown(ctx context.Context) error {
	close(v.shutdownChan)

	// Cancel all active jobs
	v.mu.Lock()
	jobs := make([]*VODConversionJob, 0, len(v.jobs))
	for _, job := range v.jobs {
		jobs = append(jobs, job)
	}
	v.mu.Unlock()

	for _, job := range jobs {
		if job.Cancel != nil {
			job.Cancel()
		}
	}

	// Wait for workers to finish
	done := make(chan struct{})
	go func() {
		v.wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		v.logger.Info("All VOD conversion workers stopped")
		return nil
	case <-ctx.Done():
		v.logger.Warn("Timeout waiting for VOD conversion workers to stop")
		return ctx.Err()
	}
}
