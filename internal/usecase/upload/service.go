package upload

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"vidra-core/internal/config"
	"vidra-core/internal/domain"
	"vidra-core/internal/media"
	"vidra-core/internal/port"
	"vidra-core/internal/security"
	"vidra-core/internal/storage"
	"vidra-core/internal/validation"
)

type Service interface {
	InitiateUpload(ctx context.Context, userID string, req *domain.InitiateUploadRequest) (*domain.InitiateUploadResponse, error)
	UploadChunk(ctx context.Context, sessionID string, chunk *domain.ChunkUpload) (*domain.ChunkUploadResponse, error)
	CompleteUpload(ctx context.Context, sessionID string) error
	GetUploadStatus(ctx context.Context, sessionID string) (*domain.UploadSession, error)
	AssembleChunks(ctx context.Context, session *domain.UploadSession) error
	CleanupTempFiles(ctx context.Context, sessionID string) error
	InitiateBatchUpload(ctx context.Context, userID string, req *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error)
	GetBatchStatus(ctx context.Context, batchID string, userID string) (*domain.BatchUploadStatus, error)
}

type service struct {
	uploadRepo          port.UploadRepository
	encodingRepo        port.EncodingRepository
	videoRepo           port.VideoRepository
	uploadsDir          string
	paths               storage.Paths
	validator           *validation.ChecksumValidator
	cfg                 *config.Config
	generateThumbnailFn func(ctx context.Context, input string, output string) error
	probeMetadataFn     func(ctx context.Context, input string) (*domain.VideoMetadata, time.Duration, error)
}

func NewService(uploadRepo port.UploadRepository, encodingRepo port.EncodingRepository, videoRepo port.VideoRepository, uploadsDir string, cfg *config.Config) Service {
	return &service{
		uploadRepo:          uploadRepo,
		encodingRepo:        encodingRepo,
		videoRepo:           videoRepo,
		uploadsDir:          uploadsDir,
		paths:               storage.NewPaths(uploadsDir),
		validator:           validation.NewChecksumValidator(cfg),
		cfg:                 cfg,
		generateThumbnailFn: defaultGenerateThumbnail(cfg),
		probeMetadataFn:     defaultProbeMetadata(cfg),
	}
}

// MaxChunkSize is the maximum allowed chunk size (64 MiB) to prevent memory
// exhaustion during chunk assembly.
const MaxChunkSize = 64 * 1024 * 1024

func (s *service) InitiateUpload(ctx context.Context, userID string, req *domain.InitiateUploadRequest) (*domain.InitiateUploadResponse, error) {
	if ext := filepath.Ext(req.FileName); !validUploadExt(ext) {
		return nil, domain.NewDomainError("INVALID_FILE_EXTENSION", "Invalid file extension")
	}
	const maxFileSize = 10 * 1024 * 1024 * 1024
	if req.FileSize > maxFileSize {
		return nil, domain.NewDomainError("FILE_TOO_LARGE", "File size exceeds maximum limit of 10GB")
	}
	if req.ChunkSize <= 0 || req.ChunkSize > MaxChunkSize {
		return nil, domain.NewDomainError("INVALID_CHUNK_SIZE",
			fmt.Sprintf("Chunk size must be between 1 and %d bytes", MaxChunkSize))
	}
	totalChunks := int((req.FileSize + req.ChunkSize - 1) / req.ChunkSize)
	now := time.Now()
	safeFileName := security.SanitizeStrictText(req.FileName)
	if safeFileName == "" {
		safeFileName = "Untitled Upload"
	}
	video := &domain.Video{
		ID:              uuid.NewString(),
		ThumbnailID:     uuid.NewString(),
		Title:           fmt.Sprintf("Uploading: %s", safeFileName),
		Description:     "Upload in progress",
		Privacy:         domain.PrivacyPrivate,
		Status:          domain.StatusUploading,
		UploadDate:      now,
		UserID:          userID,
		FileSize:        req.FileSize,
		WaitTranscoding: req.WaitTranscoding,
		ProcessedCIDs:   make(map[string]string),
		Tags:            []string{},
		Metadata:        domain.VideoMetadata{},
		CreatedAt:       now,
		UpdatedAt:       now,
	}
	if err := s.videoRepo.Create(ctx, video); err != nil {
		return nil, fmt.Errorf("failed to create video record: %w", err)
	}
	sessionID := uuid.NewString()
	sp := storage.NewPaths(s.uploadsDir)
	tempDir := sp.UploadTempDir(sessionID)
	if err := os.MkdirAll(tempDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}
	session := &domain.UploadSession{
		ID:               sessionID,
		VideoID:          video.ID,
		UserID:           userID,
		FileName:         req.FileName,
		FileSize:         req.FileSize,
		ChunkSize:        req.ChunkSize,
		TotalChunks:      totalChunks,
		UploadedChunks:   []int{},
		Status:           domain.UploadStatusActive,
		TempFilePath:     sp.UploadTempChunksDir(sessionID),
		ExpectedChecksum: req.ExpectedChecksum,
		CreatedAt:        now,
		UpdatedAt:        now,
		ExpiresAt:        now.Add(24 * time.Hour),
	}
	if err := s.uploadRepo.CreateSession(ctx, session); err != nil {
		_ = os.RemoveAll(tempDir)
		return nil, fmt.Errorf("failed to create upload session: %w", err)
	}
	return &domain.InitiateUploadResponse{SessionID: sessionID, ChunkSize: req.ChunkSize, TotalChunks: totalChunks, UploadURL: fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID)}, nil
}

func validUploadExt(ext string) bool {
	switch strings.ToLower(ext) {
	case ".mp4", ".mov", ".mkv", ".webm", ".avi":
		return true
	default:
		return false
	}
}

// streamChunkToFile streams a chunk file into the destination without loading
// the entire chunk into memory, preventing DoS via large ChunkSize values.
func streamChunkToFile(chunkPath string, dst *os.File, chunkIndex int) error {
	src, err := os.Open(chunkPath)
	if err != nil {
		return fmt.Errorf("failed to read chunk %d: %w", chunkIndex, err)
	}
	defer func() { _ = src.Close() }()
	if _, err := io.Copy(dst, src); err != nil {
		return fmt.Errorf("failed to write chunk %d to final file: %w", chunkIndex, err)
	}
	return nil
}

func validateFilePath(path, expectedRoot string) error {
	cleanPath := filepath.Clean(path)
	if !filepath.IsAbs(cleanPath) {
		cleanPath = filepath.Join(expectedRoot, cleanPath)
	}
	if expectedRoot != "" {
		expectedRoot = filepath.Clean(expectedRoot)
		rel, err := filepath.Rel(expectedRoot, cleanPath)
		if err != nil || strings.HasPrefix(rel, "..") {
			return fmt.Errorf("path traversal detected: %s", path)
		}
	}
	if strings.Contains(cleanPath, "..") {
		return fmt.Errorf("path contains directory traversal: %s", path)
	}
	return nil
}

func (s *service) UploadChunk(ctx context.Context, sessionID string, chunk *domain.ChunkUpload) (*domain.ChunkUploadResponse, error) {
	session, err := s.uploadRepo.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get upload session: %w", err)
	}
	if session.Status != domain.UploadStatusActive {
		return nil, domain.NewDomainError("INVALID_SESSION", "Upload session is not active")
	}
	if time.Now().After(session.ExpiresAt) {
		return nil, domain.NewDomainError("SESSION_EXPIRED", "Upload session has expired")
	}
	if chunk.ChunkIndex < 0 || chunk.ChunkIndex >= session.TotalChunks {
		return nil, domain.NewDomainError("INVALID_CHUNK_INDEX", "Chunk index out of range")
	}
	if err := s.validator.ValidateChunkChecksum(chunk.Data, chunk.Checksum); err != nil {
		return nil, err
	}
	isUploaded, err := s.uploadRepo.IsChunkUploaded(ctx, sessionID, chunk.ChunkIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to check chunk status: %w", err)
	}
	if !isUploaded {
		chunkPath := filepath.Join(session.TempFilePath, fmt.Sprintf("chunk_%d", chunk.ChunkIndex))
		if err := os.MkdirAll(filepath.Dir(chunkPath), 0750); err != nil {
			return nil, fmt.Errorf("failed to create chunk directory: %w", err)
		}
		if err := os.WriteFile(chunkPath, chunk.Data, 0600); err != nil {
			return nil, fmt.Errorf("failed to save chunk: %w", err)
		}
		if err := s.uploadRepo.RecordChunk(ctx, sessionID, chunk.ChunkIndex); err != nil {
			return nil, fmt.Errorf("failed to record chunk: %w", err)
		}
	}
	uploadedChunks, err := s.uploadRepo.GetUploadedChunks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get uploaded chunks: %w", err)
	}
	uploadedSet := make(map[int]bool)
	for _, idx := range uploadedChunks {
		uploadedSet[idx] = true
	}
	var remaining []int
	for i := 0; i < session.TotalChunks; i++ {
		if !uploadedSet[i] {
			remaining = append(remaining, i)
		}
	}
	return &domain.ChunkUploadResponse{ChunkIndex: chunk.ChunkIndex, Uploaded: true, RemainingChunks: remaining}, nil
}

func (s *service) CompleteUpload(ctx context.Context, sessionID string) error {
	session, err := s.uploadRepo.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get upload session: %w", err)
	}
	// Idempotent: if already completed, return success
	if session.Status == domain.UploadStatusCompleted {
		return nil
	}
	if session.Status != domain.UploadStatusActive {
		return domain.NewDomainError("INVALID_SESSION", "Upload session is not active")
	}
	uploadedChunks, err := s.uploadRepo.GetUploadedChunks(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get uploaded chunks: %w", err)
	}
	if len(uploadedChunks) < session.TotalChunks {
		return domain.NewDomainError("INCOMPLETE_UPLOAD", "Not all chunks have been uploaded")
	}
	if err := s.AssembleChunks(ctx, session); err != nil {
		return fmt.Errorf("failed to assemble chunks: %w", err)
	}
	session.Status = domain.UploadStatusCompleted
	session.UpdatedAt = time.Now()
	if err := s.uploadRepo.UpdateSession(ctx, session); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}
	video, err := s.videoRepo.GetByID(ctx, session.VideoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	// Store the HTTP-servable path so the API returns a valid URL for playback.
	ext := filepath.Ext(session.FileName)
	finalFilePath := s.paths.WebVideoFilePath(session.VideoID, ext)
	if video.OutputPaths == nil {
		video.OutputPaths = make(map[string]string)
	}
	video.OutputPaths["source"] = s.paths.WebVideoHTTPPath(session.VideoID, ext)

	if metadata, duration, probeErr := s.probeMetadataFn(ctx, finalFilePath); probeErr != nil {
		slog.Warn("failed to probe upload metadata", "video_id", session.VideoID, "error", probeErr)
	} else {
		s.applySourceMetadata(video, metadata, duration)
	}

	thumbnailHTTPPath, thumbErr := s.generateInitialThumbnail(ctx, session.VideoID, finalFilePath)
	if thumbErr != nil {
		slog.Warn("failed to generate initial upload thumbnail", "video_id", session.VideoID, "error", thumbErr)
	}
	if thumbnailHTTPPath != "" {
		video.ThumbnailPath = thumbnailHTTPPath
	}

	// Publish only after the first thumbnail exists. This keeps freshly uploaded
	// videos from surfacing a broken 404 thumbnail in the frontend while the rest
	// of the encoding pipeline continues.
	switch {
	case video.WaitTranscoding:
		video.Status = domain.StatusProcessing
	case video.ThumbnailPath != "":
		video.Status = domain.StatusCompleted
	default:
		video.Status = domain.StatusProcessing
	}

	video.UpdatedAt = time.Now()
	if err := s.videoRepo.Update(ctx, video); err != nil {
		return fmt.Errorf("failed to update video status: %w", err)
	}
	// Prevent duplicate encoding jobs — check if one already exists for this video
	existingJob, _ := s.encodingRepo.GetJobByVideoID(ctx, session.VideoID)
	if existingJob != nil {
		return nil
	}
	sourceResolution := s.detectSourceResolution(video)
	job := &domain.EncodingJob{
		ID:                uuid.NewString(),
		VideoID:           session.VideoID,
		SourceFilePath:    finalFilePath,
		SourceResolution:  sourceResolution,
		TargetResolutions: domain.GetTargetResolutions(sourceResolution),
		Status:            domain.EncodingStatusPending,
		Progress:          0,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}
	if err := s.encodingRepo.CreateJob(ctx, job); err != nil {
		return fmt.Errorf("failed to create encoding job: %w", err)
	}
	return nil
}

func (s *service) GetUploadStatus(ctx context.Context, sessionID string) (*domain.UploadSession, error) {
	session, err := s.uploadRepo.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get upload session: %w", err)
	}
	uploadedChunks, err := s.uploadRepo.GetUploadedChunks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get uploaded chunks: %w", err)
	}
	session.UploadedChunks = uploadedChunks
	return session, nil
}

func (s *service) AssembleChunks(ctx context.Context, session *domain.UploadSession) error {
	sp := storage.NewPaths(s.uploadsDir)
	finalDir := sp.WebVideosDir()
	if err := os.MkdirAll(finalDir, 0750); err != nil {
		return fmt.Errorf("failed to create completed directory: %w", err)
	}
	finalPath := filepath.Join(finalDir, session.VideoID+filepath.Ext(session.FileName))
	if err := validateFilePath(finalPath, s.uploadsDir); err != nil {
		return fmt.Errorf("invalid final file path: %w", err)
	}
	finalFile, err := os.Create(finalPath)
	if err != nil {
		return fmt.Errorf("failed to create final file: %w", err)
	}
	defer func() { _ = finalFile.Close() }()
	uploadedChunks, err := s.uploadRepo.GetUploadedChunks(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("failed to get uploaded chunks: %w", err)
	}
	sort.Ints(uploadedChunks)
	for _, chunkIndex := range uploadedChunks {
		chunkPath := filepath.Join(session.TempFilePath, fmt.Sprintf("chunk_%d", chunkIndex))
		if err := validateFilePath(chunkPath, s.uploadsDir); err != nil {
			return fmt.Errorf("invalid chunk file path: %w", err)
		}
		if err := streamChunkToFile(chunkPath, finalFile, chunkIndex); err != nil {
			return err
		}
	}
	_ = finalFile.Close()
	if session.ExpectedChecksum != "" {
		if err := s.validator.ValidateFileChecksum(finalPath, session.ExpectedChecksum); err != nil {
			_ = os.Remove(finalPath)
			return fmt.Errorf("assembled file checksum validation failed: %w", err)
		}
	}
	return nil
}

func (s *service) generateInitialThumbnail(ctx context.Context, videoID, sourcePath string) (string, error) {
	thumbnailPath := s.paths.ThumbnailPath(videoID)
	if err := os.MkdirAll(filepath.Dir(thumbnailPath), 0o750); err != nil {
		return "", fmt.Errorf("create thumbnail dir: %w", err)
	}

	if err := s.generateThumbnailFn(ctx, sourcePath, thumbnailPath); err != nil {
		return "", err
	}

	if _, err := os.Stat(thumbnailPath); err != nil {
		return "", fmt.Errorf("initial thumbnail missing after generation: %w", err)
	}

	return s.paths.ThumbnailHTTPPath(videoID), nil
}

func defaultGenerateThumbnail(cfg *config.Config) func(ctx context.Context, input string, output string) error {
	ffmpegPath := "ffmpeg"
	if cfg != nil && cfg.FFMPEGPath != "" {
		ffmpegPath = cfg.FFMPEGPath
	}

	return func(ctx context.Context, input string, output string) error {
		duration, err := media.ProbeDuration(ctx, ffmpegPath, input)
		if err != nil {
			slog.Debug("falling back to default upload thumbnail capture", "input", input, "error", err)
			duration = 0
		}

		cmd := exec.CommandContext(ctx, ffmpegPath, media.BuildRepresentativeThumbnailArgs(input, output, duration)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			return fmt.Errorf("ffmpeg thumbnail generation failed: %w (%s)", err, strings.TrimSpace(string(out)))
		}
		return nil
	}
}

func defaultProbeMetadata(cfg *config.Config) func(ctx context.Context, input string) (*domain.VideoMetadata, time.Duration, error) {
	ffmpegPath := "ffmpeg"
	if cfg != nil && cfg.FFMPEGPath != "" {
		ffmpegPath = cfg.FFMPEGPath
	}

	return func(ctx context.Context, input string) (*domain.VideoMetadata, time.Duration, error) {
		return media.ProbeVideoMetadata(ctx, ffmpegPath, input)
	}
}

func (s *service) applySourceMetadata(video *domain.Video, metadata *domain.VideoMetadata, duration time.Duration) {
	if metadata != nil {
		if metadata.Width > 0 {
			video.Metadata.Width = metadata.Width
		}
		if metadata.Height > 0 {
			video.Metadata.Height = metadata.Height
		}
		if metadata.Framerate > 0 {
			video.Metadata.Framerate = metadata.Framerate
		}
		if metadata.Bitrate > 0 {
			video.Metadata.Bitrate = metadata.Bitrate
		}
		if metadata.VideoCodec != "" {
			video.Metadata.VideoCodec = metadata.VideoCodec
		}
		if metadata.AspectRatio != "" {
			video.Metadata.AspectRatio = metadata.AspectRatio
		}
	}

	if duration > 0 {
		video.Duration = int(duration.Seconds())
	}
}

func (s *service) CleanupTempFiles(ctx context.Context, sessionID string) error {
	session, err := s.uploadRepo.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get upload session: %w", err)
	}
	tempDir := filepath.Dir(session.TempFilePath)
	if err := os.RemoveAll(tempDir); err != nil {
		return fmt.Errorf("failed to remove temp directory: %w", err)
	}
	return nil
}

func (s *service) detectSourceResolution(video *domain.Video) string {
	if video.Metadata.Height > 0 {
		return domain.DetectResolutionFromHeight(video.Metadata.Height)
	}
	if video.Metadata.Width > 0 {
		ar := s.parseAspectRatio(video.Metadata.AspectRatio)
		est := int(math.Round(float64(video.Metadata.Width) / ar.ratio))
		if est > 0 {
			res := domain.DetectResolutionFromHeight(est)
			s.logResolutionEstimation(video, ar, est, res)
			return res
		}
	}
	return domain.DefaultResolution
}

type aspectRatioInfo struct {
	ratio       float64
	usedDefault bool
}

func (s *service) parseAspectRatio(ar string) aspectRatioInfo {
	result := aspectRatioInfo{ratio: 16.0 / 9.0, usedDefault: true}
	ar = strings.TrimSpace(ar)
	if ar == "" {
		return result
	}
	if strings.Contains(ar, ":") || strings.Contains(ar, "/") {
		sep := ":"
		if strings.Contains(ar, "/") {
			sep = "/"
		}
		parts := strings.SplitN(ar, sep, 2)
		if len(parts) == 2 {
			if num, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err1 == nil && num > 0 {
				if den, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err2 == nil && den > 0 {
					result.ratio = num / den
					result.usedDefault = false
				}
			}
		}
	} else {
		if v, err := strconv.ParseFloat(ar, 64); err == nil && v > 0 {
			result.ratio = v
			result.usedDefault = false
		}
	}
	return result
}

func (s *service) logResolutionEstimation(video *domain.Video, arInfo aspectRatioInfo, estHeight int, resolution string) {
	if s == nil || s.cfg == nil {
		return
	}
	lvl := strings.ToLower(s.cfg.LogLevel)
	if lvl != "debug" && lvl != "trace" {
		return
	}
	if arInfo.usedDefault {
		slog.Info(fmt.Sprintf("estimating source resolution using width=%d, default AR=16:9 -> estHeight=%d -> %s", video.Metadata.Width, estHeight, resolution))
	} else {
		slog.Info(fmt.Sprintf("estimating source resolution using width=%d, AR=%q -> estHeight=%d -> %s", video.Metadata.Width, video.Metadata.AspectRatio, estHeight, resolution))
	}
}

const (
	defaultBatchChunkSize = 10 * 1024 * 1024        // 10MB default chunk size
	maxSingleFileSize     = 10 * 1024 * 1024 * 1024 // 10GB per file
)

func validateBatchVideos(videos []domain.BatchUploadVideoItem) (int64, error) {
	var aggregateSize int64
	for i := range videos {
		v := &videos[i]
		ext := filepath.Ext(v.FileName)
		if !validUploadExt(ext) {
			return 0, domain.NewDomainError("INVALID_FILE_EXTENSION",
				fmt.Sprintf("Video %d: invalid file extension %q", i+1, ext))
		}
		if v.FileSize <= 0 || v.FileSize > maxSingleFileSize {
			return 0, domain.NewDomainError("INVALID_FILE_SIZE",
				fmt.Sprintf("Video %d: file size must be between 1 byte and 10GB", i+1))
		}
		if v.ChunkSize == 0 {
			v.ChunkSize = defaultBatchChunkSize
		}
		if v.ChunkSize < 0 || v.ChunkSize > MaxChunkSize {
			return 0, domain.NewDomainError("INVALID_CHUNK_SIZE",
				fmt.Sprintf("Video %d: chunk size must be between 1 and %d bytes", i+1, MaxChunkSize))
		}
		if strings.TrimSpace(v.Title) == "" {
			return 0, domain.NewDomainError("MISSING_TITLE",
				fmt.Sprintf("Video %d: title is required", i+1))
		}
		if v.Privacy != "" && v.Privacy != string(domain.PrivacyPublic) && v.Privacy != string(domain.PrivacyUnlisted) && v.Privacy != string(domain.PrivacyPrivate) {
			return 0, domain.NewDomainError("INVALID_PRIVACY",
				fmt.Sprintf("Video %d: privacy must be public, unlisted, or private", i+1))
		}
		aggregateSize += v.FileSize
	}
	return aggregateSize, nil
}

func (s *service) InitiateBatchUpload(ctx context.Context, userID string, req *domain.BatchUploadRequest) (*domain.BatchUploadResponse, error) {
	if len(req.Videos) == 0 {
		return nil, domain.NewDomainError("EMPTY_BATCH", "Batch must contain at least one video")
	}
	if s.cfg.MaxBatchUploadSize > 0 && len(req.Videos) > s.cfg.MaxBatchUploadSize {
		return nil, domain.NewDomainError("BATCH_TOO_LARGE",
			fmt.Sprintf("Batch size %d exceeds maximum of %d", len(req.Videos), s.cfg.MaxBatchUploadSize))
	}

	// Validate each video and compute aggregate size
	aggregateSize, err := validateBatchVideos(req.Videos)
	if err != nil {
		return nil, err
	}

	// Check aggregate quota
	if s.cfg.MaxUserVideoQuota > 0 {
		currentUsage, err := s.videoRepo.GetVideoQuotaUsed(ctx, userID)
		if err != nil {
			return nil, fmt.Errorf("failed to check video quota: %w", err)
		}
		if currentUsage+aggregateSize > s.cfg.MaxUserVideoQuota {
			return nil, domain.NewDomainError("QUOTA_EXCEEDED",
				fmt.Sprintf("Batch total size %d bytes would exceed your quota (used: %d, limit: %d)",
					aggregateSize, currentUsage, s.cfg.MaxUserVideoQuota))
		}
	}

	now := time.Now()
	batchID := uuid.NewString()
	batch := &domain.BatchUpload{
		ID:          batchID,
		UserID:      userID,
		TotalVideos: len(req.Videos),
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	var responses []domain.InitiateUploadResponse
	var createdTempDirs []string

	err = s.uploadRepo.WithTransaction(ctx, func(txCtx context.Context) error {
		if err := s.uploadRepo.CreateBatch(txCtx, batch); err != nil {
			return fmt.Errorf("failed to create batch record: %w", err)
		}

		// Pass 1: build all video objects and batch-insert them.
		videos := make([]*domain.Video, 0, len(req.Videos))
		for _, v := range req.Videos {
			safeTitle := security.SanitizeStrictText(v.Title)
			if safeTitle == "" {
				safeTitle = "Untitled Upload"
			}
			privacy := domain.Privacy(v.Privacy)
			if privacy == "" {
				privacy = domain.PrivacyPrivate
			}
			videos = append(videos, &domain.Video{
				ID:            uuid.NewString(),
				ThumbnailID:   uuid.NewString(),
				Title:         safeTitle,
				Description:   v.Description,
				Privacy:       privacy,
				Status:        domain.StatusUploading,
				UploadDate:    now,
				UserID:        userID,
				FileSize:      v.FileSize,
				ProcessedCIDs: make(map[string]string),
				Tags:          []string{},
				Metadata:      domain.VideoMetadata{},
				CreatedAt:     now,
				UpdatedAt:     now,
			})
		}
		if batcher, ok := s.videoRepo.(port.VideoBatchCreator); ok {
			if err := batcher.CreateBatch(txCtx, videos); err != nil {
				return fmt.Errorf("failed to batch create video records: %w", err)
			}
		} else {
			for _, video := range videos {
				if err := s.videoRepo.Create(txCtx, video); err != nil {
					return fmt.Errorf("failed to create video record: %w", err)
				}
			}
		}

		// Pass 2: per-video side effects (temp dirs, sessions, responses).
		for i, v := range req.Videos {
			video := videos[i]
			chunkSize := v.ChunkSize
			totalChunks := int((v.FileSize + chunkSize - 1) / chunkSize)

			sessionID := uuid.NewString()
			tempDir := s.paths.UploadTempDir(sessionID)
			if err := os.MkdirAll(tempDir, 0750); err != nil {
				return fmt.Errorf("failed to create temp directory: %w", err)
			}
			createdTempDirs = append(createdTempDirs, tempDir)

			session := &domain.UploadSession{
				ID:             sessionID,
				VideoID:        video.ID,
				UserID:         userID,
				BatchID:        &batchID,
				FileName:       v.FileName,
				FileSize:       v.FileSize,
				ChunkSize:      chunkSize,
				TotalChunks:    totalChunks,
				UploadedChunks: []int{},
				Status:         domain.UploadStatusActive,
				TempFilePath:   s.paths.UploadTempChunksDir(sessionID),
				CreatedAt:      now,
				UpdatedAt:      now,
				ExpiresAt:      now.Add(24 * time.Hour),
			}

			if err := s.uploadRepo.CreateSession(txCtx, session); err != nil {
				return fmt.Errorf("failed to create upload session: %w", err)
			}

			responses = append(responses, domain.InitiateUploadResponse{
				SessionID:   sessionID,
				ChunkSize:   chunkSize,
				TotalChunks: totalChunks,
				UploadURL:   fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID),
			})
		}
		return nil
	})

	if err != nil {
		// Clean up any temp dirs created before the transaction failed
		for _, dir := range createdTempDirs {
			_ = os.RemoveAll(dir)
		}
		return nil, err
	}

	return &domain.BatchUploadResponse{
		BatchID:  batchID,
		Sessions: responses,
	}, nil
}

func (s *service) GetBatchStatus(ctx context.Context, batchID string, userID string) (*domain.BatchUploadStatus, error) {
	batch, err := s.uploadRepo.GetBatch(ctx, batchID)
	if err != nil {
		return nil, err
	}

	// Ownership check — return same error as not found to avoid info leak
	if batch.UserID != userID {
		return nil, domain.NewDomainError("BATCH_NOT_FOUND", "Batch upload not found")
	}

	sessions, err := s.uploadRepo.GetSessionsByBatchID(ctx, batchID)
	if err != nil {
		return nil, fmt.Errorf("failed to get batch sessions: %w", err)
	}

	var completed, active, failed int
	for _, s := range sessions {
		switch s.Status {
		case domain.UploadStatusCompleted:
			completed++
		case domain.UploadStatusActive:
			active++
		default: // expired, failed
			failed++
		}
	}

	// Dereference sessions from pointers
	sessionList := make([]domain.UploadSession, len(sessions))
	for i, s := range sessions {
		sessionList[i] = *s
	}

	return &domain.BatchUploadStatus{
		BatchID:          batchID,
		TotalVideos:      batch.TotalVideos,
		CompletedUploads: completed,
		ActiveUploads:    active,
		FailedUploads:    failed,
		Sessions:         sessionList,
	}, nil
}
