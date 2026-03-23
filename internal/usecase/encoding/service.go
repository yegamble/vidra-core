package encoding

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/ipfs"
	"vidra-core/internal/metrics"
	"vidra-core/internal/port"
	"vidra-core/internal/storage"

	ucn "vidra-core/internal/usecase/notification"

	"github.com/google/uuid"
)

type Service interface {
	Run(ctx context.Context, workers int) error
	ProcessNext(ctx context.Context) (processed bool, err error)
}

type Publisher interface {
	PublishVideo(ctx context.Context, v *domain.Video) error
}

type JobEnqueuer interface {
	EnqueueJob(ctx context.Context, jobType string, payload any, runAt time.Time) (string, error)
}

type CaptionGenerator interface {
	CreateJob(ctx context.Context, videoID uuid.UUID, userID uuid.UUID, req *domain.CreateCaptionGenerationJobRequest) (*domain.CaptionGenerationJob, error)
}

type service struct {
	repo            port.EncodingRepository
	videoRepo       port.VideoRepository
	notificationSvc ucn.Service
	uploadsDir      string
	cfg             *config.Config
	atproto         Publisher
	fedEnq          JobEnqueuer
	ipfsClient      *ipfs.Client
	captionGen      CaptionGenerator
	s3Backend       storage.StorageBackend
}

func NewService(repo port.EncodingRepository, videoRepo port.VideoRepository, notificationSvc ucn.Service, uploadsDir string, cfg *config.Config, atproto Publisher, enq JobEnqueuer, ipfsClient *ipfs.Client) Service {
	return &service{repo: repo, videoRepo: videoRepo, notificationSvc: notificationSvc, uploadsDir: uploadsDir, cfg: cfg, atproto: atproto, fedEnq: enq, ipfsClient: ipfsClient}
}

func (s *service) WithFederationEnqueuer(enq JobEnqueuer) *service {
	s.fedEnq = enq
	return s
}

func (s *service) WithCaptionGenerator(gen CaptionGenerator) Service {
	s.captionGen = gen
	return s
}

func (s *service) WithS3Backend(backend storage.StorageBackend) Service {
	s.s3Backend = backend
	return s
}

func (s *service) Run(ctx context.Context, workers int) error {
	if workers <= 0 {
		workers = runtime.NumCPU()
		if workers < 2 {
			workers = 2
		}
	}

	const defaultStaleDuration = 30 * time.Minute
	resetCount, err := s.repo.ResetStaleJobs(ctx, defaultStaleDuration)
	if err != nil {
		slog.Warn("failed to reset stale encoding jobs", "error", err)
	} else if resetCount > 0 {
		slog.Info("recovered stale encoding jobs", "count", resetCount)
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
				select {
				case errCh <- err:
				default:
				}
			}
			if !processed {
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

	<-ctx.Done()
	wg.Wait()
	close(errCh)
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

	now := time.Now()
	job.Status = domain.EncodingStatusCompleted
	job.Progress = 100
	job.CompletedAt = &now
	job.UpdatedAt = now
	if err := s.repo.UpdateJob(ctx, job); err != nil {
		metrics.DecInFlight()
		return true, err
	}
	_ = s.repo.DeleteJob(ctx, job.ID)
	metrics.IncProcessed()
	metrics.DecInFlight()
	return true, nil
}

func (s *service) validateJob(job *domain.EncodingJob) error {
	if job.SourceFilePath == "" {
		return errors.New("missing source file path")
	}
	if _, err := os.Stat(job.SourceFilePath); err != nil {
		return fmt.Errorf("source file not found: %w", err)
	}
	return nil
}

func (s *service) createProgressUpdater(ctx context.Context, jobID string, totalTasks int) func() {
	var done int32
	return func() {
		atomic.AddInt32(&done, 1)
		progress := int(float64(atomic.LoadInt32(&done)) / float64(totalTasks) * 100)
		_ = s.repo.UpdateJobProgress(ctx, jobID, progress)
	}
}

type progressAggregator struct {
	mu           sync.Mutex
	resProgress  map[string]int
	jobID        string
	repo         port.EncodingRepository
	lastReported int
}

func newProgressAggregator(jobID string, repo port.EncodingRepository, resolutions []string) *progressAggregator {
	resProgress := make(map[string]int, len(resolutions))
	for _, res := range resolutions {
		resProgress[res] = 0
	}
	return &progressAggregator{
		resProgress: resProgress,
		jobID:       jobID,
		repo:        repo,
	}
}

func (a *progressAggregator) update(ctx context.Context, resolution string, progress int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.resProgress[resolution] = progress
	total := 0
	for _, p := range a.resProgress {
		total += p
	}
	aggregate := total / len(a.resProgress)
	if aggregate >= a.lastReported+5 || aggregate == 100 {
		if err := a.repo.UpdateJobProgress(ctx, a.jobID, aggregate); err != nil {
			slog.Warn("failed to update job progress", "error", err)
		}
		a.lastReported = aggregate
	}
}

func (s *service) encodeResolutions(ctx context.Context, job *domain.EncodingJob, outBaseDir string, update func()) error {
	duration, err := s.getVideoDuration(ctx, job.SourceFilePath)
	if err != nil {
		slog.Warn("could not get video duration for progress tracking", "error", err)
		duration = 0
	}

	agg := newProgressAggregator(job.ID, s.repo, job.TargetResolutions)

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
			onProgress := func(pct int) { agg.update(ctx, label, pct) }
			if err := s.transcodeHLS(ctx, job.SourceFilePath, h, outPlaylist, segPattern, duration, onProgress); err != nil {
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

	if err := s.validateJob(job); err != nil {
		return err
	}

	sp := storage.NewPaths(s.uploadsDir)
	outBaseDir := sp.HLSVideoDir(job.VideoID)
	if err := os.MkdirAll(outBaseDir, 0o750); err != nil {
		return fmt.Errorf("failed to create output dir: %w", err)
	}

	totalTasks := len(job.TargetResolutions) + 2
	update := s.createProgressUpdater(ctx, job.ID, totalTasks)

	if err := s.encodeResolutions(ctx, job, outBaseDir, update); err != nil {
		return err
	}

	if err := s.generateMasterPlaylist(outBaseDir, job.TargetResolutions); err != nil {
		return fmt.Errorf("master playlist: %w", err)
	}

	processedCIDs, err := s.uploadVariantsToIPFS(ctx, job, outBaseDir)
	if err != nil {
		slog.Warn("failed to upload variants to IPFS", "error", err)
		processedCIDs = make(map[string]string)
	}

	thumb, preview, err := s.generateMediaAssets(ctx, job, &sp, update)
	if err != nil {
		return err
	}

	thumbCID, previewCID := s.uploadMediaToIPFS(ctx, thumb, preview)

	if err := s.updateVideoInfo(ctx, job, outBaseDir, thumb, preview, processedCIDs, thumbCID, previewCID); err != nil {
		return err
	}

	s3URLs, err := s.uploadHLSToS3(ctx, job.VideoID, job.SourceFilePath, outBaseDir, thumb, preview, job.TargetResolutions)
	if err != nil {
		slog.Warn("failed to upload HLS to S3", "video_id", job.VideoID, "error", err)
	} else if len(s3URLs) > 0 {
		if v, getErr := s.videoRepo.GetByID(ctx, job.VideoID); getErr == nil && v != nil {
			v.S3URLs = s3URLs
			if updateErr := s.videoRepo.Update(ctx, v); updateErr != nil {
				slog.Warn("failed to save S3URLs to video", "video_id", job.VideoID, "error", updateErr)
			}
		}
	}

	if s.atproto != nil {
		if v, err := s.videoRepo.GetByID(ctx, job.VideoID); err == nil && v != nil {
			if err := s.atproto.PublishVideo(ctx, v); err != nil && s.fedEnq != nil {
				_ = s.enqueuePublishRetry(ctx, v.ID, 30*time.Second)
			}
		}
	}

	s.triggerNotifications(ctx, job.VideoID)

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

func (s *service) generateMediaAssets(ctx context.Context, job *domain.EncodingJob, sp *storage.Paths, update func()) (string, string, error) {
	thumb := sp.ThumbnailPath(job.VideoID)
	if err := os.MkdirAll(filepath.Dir(thumb), 0o750); err != nil {
		return "", "", fmt.Errorf("failed to create thumbnail dir: %w", err)
	}
	if err := s.generateThumbnail(ctx, job.SourceFilePath, thumb); err != nil {
		return "", "", fmt.Errorf("thumbnail: %w", err)
	}
	update()

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

func (s *service) uploadVariantsToIPFS(ctx context.Context, job *domain.EncodingJob, outBaseDir string) (map[string]string, error) {
	processedCIDs := make(map[string]string)

	if s.ipfsClient == nil || !s.ipfsClient.IsEnabled() {
		return processedCIDs, nil
	}

	masterPath := filepath.Join(outBaseDir, "master.m3u8")
	if _, err := os.Stat(masterPath); err == nil {
		cid, err := s.ipfsClient.AddAndPin(ctx, masterPath)
		if err != nil {
			return nil, fmt.Errorf("failed to upload master playlist: %w", err)
		}
		processedCIDs["master"] = cid
	}

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

			if _, err := os.Stat(resDir); os.IsNotExist(err) {
				errCh <- fmt.Errorf("resolution directory not found: %s", resDir)
				return
			}

			cid, err := s.ipfsClient.AddDirectoryAndPin(ctx, resDir)
			if err != nil {
				errCh <- fmt.Errorf("failed to upload %s variant: %w", resolution, err)
				return
			}

			mu.Lock()
			processedCIDs[resolution] = cid
			mu.Unlock()
		}(res, height)
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		if err != nil {
			return processedCIDs, err
		}
	}

	return processedCIDs, nil
}

func (s *service) uploadMediaToIPFS(ctx context.Context, thumbPath, previewPath string) (thumbCID, previewCID string) {
	if s.ipfsClient == nil || !s.ipfsClient.IsEnabled() {
		return "", ""
	}

	if thumbPath != "" {
		if _, err := os.Stat(thumbPath); err == nil {
			cid, err := s.ipfsClient.AddAndPin(ctx, thumbPath)
			if err == nil {
				thumbCID = cid
			}
		}
	}

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

func (s *service) updateVideoInfo(ctx context.Context, job *domain.EncodingJob, outBaseDir, thumb, preview string, processedCIDs map[string]string, thumbCID, previewCID string) error {
	outputs := make(map[string]string)
	outputs["master"] = filepath.ToSlash(filepath.Join(outBaseDir, "master.m3u8"))
	for _, res := range job.TargetResolutions {
		if h, ok := domain.HeightForResolution(res); ok {
			outputs[res] = filepath.ToSlash(filepath.Join(outBaseDir, fmt.Sprintf("%dp/stream.m3u8", h)))
		}
	}

	return s.videoRepo.UpdateProcessingInfoWithCIDs(ctx, job.VideoID, domain.StatusCompleted, outputs, filepath.ToSlash(thumb), filepath.ToSlash(preview), processedCIDs, thumbCID, previewCID)
}

// uploadHLSToS3 walks outBaseDir, uploads every HLS file, the source video,
// and the thumbnail/preview images to the configured s3Backend.
// Returns a map of S3URLs keyed by quality/role.
// Returns an empty map (no error) when s3Backend is nil.
func (s *service) uploadHLSToS3(ctx context.Context, videoID, sourceFilePath, outBaseDir, thumbPath, previewPath string, targetResolutions []string) (map[string]string, error) {
	if s.s3Backend == nil {
		return map[string]string{}, nil
	}

	s3URLs := make(map[string]string)

	// Upload entire HLS tree preserving relative paths.
	err := filepath.Walk(outBaseDir, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if info.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(outBaseDir, path)
		if err != nil {
			return fmt.Errorf("rel path: %w", err)
		}
		key := fmt.Sprintf("videos/%s/hls/%s", videoID, filepath.ToSlash(rel))
		if err := s.s3Backend.UploadFile(ctx, key, path, contentTypeForPath(path)); err != nil {
			return fmt.Errorf("upload hls %s: %w", rel, err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("uploadHLSToS3 walk: %w", err)
	}

	// Record master playlist URL.
	masterKey := fmt.Sprintf("videos/%s/hls/master.m3u8", videoID)
	s3URLs["master"] = s.s3Backend.GetURL(masterKey)

	// Record each quality playlist URL.
	for _, res := range targetResolutions {
		h, ok := domain.HeightForResolution(res)
		if !ok {
			continue
		}
		playlistKey := fmt.Sprintf("videos/%s/hls/%dp/stream.m3u8", videoID, h)
		s3URLs[res] = s.s3Backend.GetURL(playlistKey)
	}

	// Upload the source video.
	if sourceFilePath != "" {
		ext := filepath.Ext(sourceFilePath)
		srcKey := fmt.Sprintf("videos/%s/source%s", videoID, ext)
		if err := s.s3Backend.UploadFile(ctx, srcKey, sourceFilePath, "video/mp4"); err != nil {
			slog.Warn("failed to upload source video to S3", "video_id", videoID, "error", err)
		} else {
			s3URLs["source"] = s.s3Backend.GetURL(srcKey)
		}
	}

	// Upload thumbnail.
	if thumbPath != "" {
		if _, err := os.Stat(thumbPath); err == nil {
			thumbKey := fmt.Sprintf("videos/%s/thumbnail%s", videoID, filepath.Ext(thumbPath))
			if err := s.s3Backend.UploadFile(ctx, thumbKey, thumbPath, "image/jpeg"); err != nil {
				slog.Warn("failed to upload thumbnail to S3", "video_id", videoID, "error", err)
			} else {
				s3URLs["thumbnail"] = s.s3Backend.GetURL(thumbKey)
			}
		}
	}

	// Upload preview.
	if previewPath != "" {
		if _, err := os.Stat(previewPath); err == nil {
			previewKey := fmt.Sprintf("videos/%s/preview%s", videoID, filepath.Ext(previewPath))
			if err := s.s3Backend.UploadFile(ctx, previewKey, previewPath, "image/webp"); err != nil {
				slog.Warn("failed to upload preview to S3", "video_id", videoID, "error", err)
			} else {
				s3URLs["preview"] = s.s3Backend.GetURL(previewKey)
			}
		}
	}

	return s3URLs, nil
}

func contentTypeForPath(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".m3u8":
		return "application/vnd.apple.mpegurl"
	case ".ts":
		return "video/MP2T"
	default:
		return "application/octet-stream"
	}
}

func (s *service) triggerNotifications(ctx context.Context, videoID string) {
	if s.notificationSvc != nil {
		video, err := s.videoRepo.GetByID(ctx, videoID)
		if err == nil && video != nil {
			_ = s.notificationSvc.CreateVideoNotificationForSubscribers(ctx, video, "")
		}
	}
}

func (s *service) triggerCaptionGeneration(ctx context.Context, videoID string) {
	if s.captionGen == nil {
		return
	}

	if s.cfg != nil && !s.cfg.EnableCaptionGeneration {
		return
	}

	video, err := s.videoRepo.GetByID(ctx, videoID)
	if err != nil || video == nil {
		return
	}

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
		TargetLanguage: nil,
		ModelSize:      domain.WhisperModelBase,
		OutputFormat:   domain.CaptionFormatVTT,
	}

	_, _ = s.captionGen.CreateJob(ctx, vidUUID, userUUID, req)
}

func (s *service) transcodeHLS(ctx context.Context, input string, height int, outPlaylist string, segPattern string, duration time.Duration, onProgress func(int)) error {
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

	if onProgress != nil {
		resLabel := fmt.Sprintf("%dp", height)
		return s.execFFmpegWithProgress(ctx, args, duration, resLabel, onProgress)
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
		if h, ok := domain.HeightForResolution(res); ok {
			fmt.Fprintf(&b, "#EXT-X-STREAM-INF:BANDWIDTH=%d,NAME=\"%s\"\n", bw[res], res)
			fmt.Fprintf(&b, "%dp/stream.m3u8\n", h)
		}
	}
	return os.WriteFile(filepath.Join(outBaseDir, "master.m3u8"), []byte(b.String()), 0o600)
}

func (s *service) execFFmpeg(ctx context.Context, args []string) error {
	bin := s.cfg.FFMPEGPath
	if bin == "" {
		bin = "ffmpeg"
	}
	if err := validateBinaryPath(bin); err != nil {
		return fmt.Errorf("invalid ffmpeg binary path: %w", err)
	}
	cmd := exec.CommandContext(ctx, bin, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}
	return nil
}

func validateBinaryPath(path string) error {
	if !strings.Contains(path, "/") && !strings.Contains(path, "\\") {
		return nil
	}

	cleanPath := filepath.Clean(path)

	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path contains directory traversal: %s", path)
	}

	if strings.ContainsAny(cleanPath, ";|&$`") {
		return fmt.Errorf("path contains suspicious characters: %s", path)
	}

	return nil
}

func (s *service) getVideoDuration(ctx context.Context, input string) (time.Duration, error) {
	bin := "ffprobe"
	if s.cfg.FFMPEGPath != "" {
		bin = filepath.Join(filepath.Dir(s.cfg.FFMPEGPath), "ffprobe")
	}

	args := []string{
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "json",
		input,
	}

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

func (s *service) execFFmpegWithProgress(ctx context.Context, args []string, duration time.Duration, resolutionLabel string, onProgress func(int)) error {
	bin := s.cfg.FFMPEGPath
	if bin == "" {
		bin = "ffmpeg"
	}

	if err := validateBinaryPath(bin); err != nil {
		return fmt.Errorf("invalid ffmpeg binary path: %w", err)
	}

	if duration == 0 {
		return s.execFFmpeg(ctx, args)
	}

	progressArgs := append([]string{"-progress", "pipe:2", "-stats"}, args...)

	cmd := exec.CommandContext(ctx, bin, progressArgs...)

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to create stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start ffmpeg: %w", err)
	}

	progressParser := NewProgressParser(duration)

	progressDone := make(chan struct{})
	go func() {
		defer close(progressDone)
		scanner := bufio.NewScanner(stderr)
		lastProgress := -1
		progressBatch := make(map[string]string)

		for scanner.Scan() {
			line := scanner.Text()

			if strings.Contains(line, "=") {
				parts := strings.SplitN(line, "=", 2)
				if len(parts) == 2 {
					progressBatch[parts[0]] = parts[1]
				}
			}

			if line == "progress=continue" || line == "progress=end" {
				var batchStr strings.Builder
				for k, v := range progressBatch {
					batchStr.WriteString(k)
					batchStr.WriteString("=")
					batchStr.WriteString(v)
					batchStr.WriteString("\n")
				}

				progress, _, _, found := progressParser.ParseProgressStats(batchStr.String())
				if found && progress != lastProgress {
					if progress >= lastProgress+5 || progress == 100 {
						onProgress(progress)
						lastProgress = progress
						slog.Debug("encoding progress", "resolution", resolutionLabel, "percent", progress)
					}
				}

				progressBatch = make(map[string]string)
			}

			if progress, found := progressParser.ParseLine(line); found && progress != lastProgress {
				if progress >= lastProgress+5 || progress == 100 {
					onProgress(progress)
					lastProgress = progress
					slog.Debug("encoding progress", "resolution", resolutionLabel, "percent", progress)
				}
			}
		}

		if err := scanner.Err(); err != nil {
			slog.Warn("error reading ffmpeg output", "error", err)
		}
	}()

	err = cmd.Wait()

	<-progressDone

	if err != nil {
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	return nil
}
