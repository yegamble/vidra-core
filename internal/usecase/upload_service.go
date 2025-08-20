package usecase

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"athena/internal/config"
	"athena/internal/domain"
	"athena/internal/storage"
	"athena/internal/validation"
)

type uploadService struct {
	uploadRepo   UploadRepository
	encodingRepo EncodingRepository
	videoRepo    VideoRepository
	uploadsDir   string // storage root
	paths        storage.Paths
	validator    *validation.ChecksumValidator
	cfg          *config.Config
}

func NewUploadService(
	uploadRepo UploadRepository,
	encodingRepo EncodingRepository,
	videoRepo VideoRepository,
	uploadsDir string,
	cfg *config.Config,
) UploadService {
	return &uploadService{
		uploadRepo:   uploadRepo,
		encodingRepo: encodingRepo,
		videoRepo:    videoRepo,
		uploadsDir:   uploadsDir,
		paths:        storage.NewPaths(uploadsDir),
		validator:    validation.NewChecksumValidator(cfg),
		cfg:          cfg,
	}
}

func (s *uploadService) InitiateUpload(ctx context.Context, userID string, req *domain.InitiateUploadRequest) (*domain.InitiateUploadResponse, error) {
	// Validate file extension (defense-in-depth)
	if ext := filepath.Ext(req.FileName); !validUploadExt(ext) {
		return nil, domain.NewDomainError("INVALID_FILE_EXTENSION", "Invalid file extension")
	}
	// Validate file size (max 10GB)
	const maxFileSize = 10 * 1024 * 1024 * 1024
	if req.FileSize > maxFileSize {
		return nil, domain.NewDomainError("FILE_TOO_LARGE", "File size exceeds maximum limit of 10GB")
	}

	// Calculate total chunks
	totalChunks := int((req.FileSize + req.ChunkSize - 1) / req.ChunkSize)

	// Create video record first
	now := time.Now()
	video := &domain.Video{
		ID:            uuid.NewString(),
		ThumbnailID:   uuid.NewString(),
		Title:         fmt.Sprintf("Uploading: %s", req.FileName),
		Description:   "Upload in progress",
		Privacy:       domain.PrivacyPrivate, // Default to private until user sets metadata
		Status:        domain.StatusUploading,
		UploadDate:    now,
		UserID:        userID,
		FileSize:      req.FileSize,
		ProcessedCIDs: make(map[string]string),
		Tags:          []string{},
		Metadata:      domain.VideoMetadata{},
		CreatedAt:     now,
		UpdatedAt:     now,
	}

	if err := s.videoRepo.Create(ctx, video); err != nil {
		return nil, fmt.Errorf("failed to create video record: %w", err)
	}

	// Create upload session
	sessionID := uuid.NewString()
	sp := storage.NewPaths(s.uploadsDir)
	tempDir := sp.UploadTempDir(sessionID)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
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
		ExpiresAt:        now.Add(24 * time.Hour), // 24 hour expiry
	}

	if err := s.uploadRepo.CreateSession(ctx, session); err != nil {
		_ = os.RemoveAll(tempDir) // Cleanup on failure
		return nil, fmt.Errorf("failed to create upload session: %w", err)
	}

	response := &domain.InitiateUploadResponse{
		SessionID:   sessionID,
		ChunkSize:   req.ChunkSize,
		TotalChunks: totalChunks,
		UploadURL:   fmt.Sprintf("/api/v1/uploads/%s/chunks", sessionID),
	}

	return response, nil
}

var uploadExtRe = regexp.MustCompile(`^\.[A-Za-z0-9]{1,8}$`)

func validUploadExt(ext string) bool {
	if ext == "" { // permit no extension
		return true
	}
	return uploadExtRe.MatchString(ext)
}

func (s *uploadService) UploadChunk(ctx context.Context, sessionID string, chunk *domain.ChunkUpload) (*domain.ChunkUploadResponse, error) {
	// Get session
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

	// Validate chunk index
	if chunk.ChunkIndex < 0 || chunk.ChunkIndex >= session.TotalChunks {
		return nil, domain.NewDomainError("INVALID_CHUNK_INDEX", "Chunk index out of range")
	}

	// Validate checksum using strict validation
	if err := s.validator.ValidateChunkChecksum(chunk.Data, chunk.Checksum); err != nil {
		return nil, err
	}

	// Check if chunk already uploaded
	isUploaded, err := s.uploadRepo.IsChunkUploaded(ctx, sessionID, chunk.ChunkIndex)
	if err != nil {
		return nil, fmt.Errorf("failed to check chunk status: %w", err)
	}

	if !isUploaded {
		// Save chunk to disk
		chunkPath := filepath.Join(session.TempFilePath, fmt.Sprintf("chunk_%d", chunk.ChunkIndex))
		if err := os.MkdirAll(filepath.Dir(chunkPath), 0755); err != nil {
			return nil, fmt.Errorf("failed to create chunk directory: %w", err)
		}

		if err := os.WriteFile(chunkPath, chunk.Data, 0644); err != nil {
			return nil, fmt.Errorf("failed to save chunk: %w", err)
		}

		// Record chunk as uploaded
		if err := s.uploadRepo.RecordChunk(ctx, sessionID, chunk.ChunkIndex); err != nil {
			return nil, fmt.Errorf("failed to record chunk: %w", err)
		}
	}

	// Get updated uploaded chunks list
	uploadedChunks, err := s.uploadRepo.GetUploadedChunks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get uploaded chunks: %w", err)
	}

	// Calculate remaining chunks
	uploadedSet := make(map[int]bool)
	for _, chunkIdx := range uploadedChunks {
		uploadedSet[chunkIdx] = true
	}

	var remainingChunks []int
	for i := 0; i < session.TotalChunks; i++ {
		if !uploadedSet[i] {
			remainingChunks = append(remainingChunks, i)
		}
	}

	response := &domain.ChunkUploadResponse{
		ChunkIndex:      chunk.ChunkIndex,
		Uploaded:        true,
		RemainingChunks: remainingChunks,
	}

	return response, nil
}

func (s *uploadService) CompleteUpload(ctx context.Context, sessionID string) error {
	// Get session
	session, err := s.uploadRepo.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get upload session: %w", err)
	}

	if session.Status != domain.UploadStatusActive {
		return domain.NewDomainError("INVALID_SESSION", "Upload session is not active")
	}

	// Verify all chunks are uploaded
	uploadedChunks, err := s.uploadRepo.GetUploadedChunks(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get uploaded chunks: %w", err)
	}

	if len(uploadedChunks) != session.TotalChunks {
		return domain.NewDomainError("INCOMPLETE_UPLOAD", "Not all chunks have been uploaded")
	}

	// Assemble chunks into final file
	if err := s.AssembleChunks(ctx, session); err != nil {
		return fmt.Errorf("failed to assemble chunks: %w", err)
	}

	// Update session status
	session.Status = domain.UploadStatusCompleted
	session.UpdatedAt = time.Now()
	if err := s.uploadRepo.UpdateSession(ctx, session); err != nil {
		return fmt.Errorf("failed to update session: %w", err)
	}

	// Update video status to queued
	video, err := s.videoRepo.GetByID(ctx, session.VideoID)
	if err != nil {
		return fmt.Errorf("failed to get video: %w", err)
	}

	video.Status = domain.StatusQueued
	video.UpdatedAt = time.Now()
	if err := s.videoRepo.Update(ctx, video); err != nil {
		return fmt.Errorf("failed to update video status: %w", err)
	}

	// Create encoding job
	finalFilePath := s.paths.WebVideoFilePath(session.VideoID, filepath.Ext(session.FileName))

	// Detect video resolution from metadata height if available, otherwise fallback.
	sourceResolution := domain.DefaultResolution
	if video.Metadata.Height > 0 {
		sourceResolution = domain.DetectResolutionFromHeight(video.Metadata.Height)
	} else if video.Metadata.Width > 0 { // derive from width and aspect ratio
		// Default to 16:9 if aspect ratio is missing or invalid
		aspectRatio := 16.0 / 9.0
		usedDefaultAR := true
		if ar := strings.TrimSpace(video.Metadata.AspectRatio); ar != "" {
			if strings.Contains(ar, ":") || strings.Contains(ar, "/") {
				sep := ":"
				if strings.Contains(ar, "/") {
					sep = "/"
				}
				parts := strings.SplitN(ar, sep, 2)
				if len(parts) == 2 {
					if num, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64); err1 == nil && num > 0 {
						if den, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64); err2 == nil && den > 0 {
							aspectRatio = num / den
							usedDefaultAR = false
						}
					}
				}
			} else {
				if v, err := strconv.ParseFloat(ar, 64); err == nil && v > 0 {
					aspectRatio = v
					usedDefaultAR = false
				}
			}
		}
		estHeight := int(math.Round(float64(video.Metadata.Width) / aspectRatio))
		if estHeight > 0 {
			sourceResolution = domain.DetectResolutionFromHeight(estHeight)
			// Log estimation usage for observability (non-fatal)
			if s != nil && s.cfg != nil {
				lvl := strings.ToLower(s.cfg.LogLevel)
				if lvl == "debug" || lvl == "trace" {
					if usedDefaultAR {
						log.Printf("estimating source resolution using width=%d, default AR=16:9 -> estHeight=%d -> %s", video.Metadata.Width, estHeight, sourceResolution)
					} else {
						log.Printf("estimating source resolution using width=%d, AR=%q -> estHeight=%d -> %s", video.Metadata.Width, video.Metadata.AspectRatio, estHeight, sourceResolution)
					}
				}
			}
		}
	}

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

func (s *uploadService) GetUploadStatus(ctx context.Context, sessionID string) (*domain.UploadSession, error) {
	session, err := s.uploadRepo.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get upload session: %w", err)
	}

	// Update uploaded chunks from database
	uploadedChunks, err := s.uploadRepo.GetUploadedChunks(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get uploaded chunks: %w", err)
	}

	session.UploadedChunks = uploadedChunks
	return session, nil
}

func (s *uploadService) AssembleChunks(ctx context.Context, session *domain.UploadSession) error {
	// Create final file path
	sp := storage.NewPaths(s.uploadsDir)
	finalDir := sp.WebVideosDir()
	if err := os.MkdirAll(finalDir, 0755); err != nil {
		return fmt.Errorf("failed to create completed directory: %w", err)
	}

	finalPath := filepath.Join(finalDir, session.VideoID+filepath.Ext(session.FileName))
	finalFile, err := os.Create(finalPath)
	if err != nil {
		return fmt.Errorf("failed to create final file: %w", err)
	}
	defer func() { _ = finalFile.Close() }()

	// Get uploaded chunks and sort them
	uploadedChunks, err := s.uploadRepo.GetUploadedChunks(ctx, session.ID)
	if err != nil {
		return fmt.Errorf("failed to get uploaded chunks: %w", err)
	}

	sort.Ints(uploadedChunks)

	// Assemble chunks in order
	for _, chunkIndex := range uploadedChunks {
		chunkPath := filepath.Join(session.TempFilePath, fmt.Sprintf("chunk_%d", chunkIndex))
		chunkData, err := os.ReadFile(chunkPath)
		if err != nil {
			return fmt.Errorf("failed to read chunk %d: %w", chunkIndex, err)
		}

		if _, err := finalFile.Write(chunkData); err != nil {
			return fmt.Errorf("failed to write chunk %d to final file: %w", chunkIndex, err)
		}
	}

	// Close the file before checksum validation
	_ = finalFile.Close()

	// Validate assembled file checksum if expected checksum is provided
	if session.ExpectedChecksum != "" {
		if err := s.validator.ValidateFileChecksum(finalPath, session.ExpectedChecksum); err != nil {
			// Remove invalid file
			_ = os.Remove(finalPath)
			return fmt.Errorf("assembled file checksum validation failed: %w", err)
		}
	}

	return nil
}

func (s *uploadService) CleanupTempFiles(ctx context.Context, sessionID string) error {
	session, err := s.uploadRepo.GetSession(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("failed to get upload session: %w", err)
	}

	// Remove temp directory
	tempDir := filepath.Dir(session.TempFilePath)
	if err := os.RemoveAll(tempDir); err != nil {
		return fmt.Errorf("failed to remove temp directory: %w", err)
	}

	return nil
}
