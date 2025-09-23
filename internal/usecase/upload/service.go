package upload

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
	"athena/internal/port"
	"athena/internal/storage"
	"athena/internal/validation"
)

type Service interface {
	InitiateUpload(ctx context.Context, userID string, req *domain.InitiateUploadRequest) (*domain.InitiateUploadResponse, error)
	UploadChunk(ctx context.Context, sessionID string, chunk *domain.ChunkUpload) (*domain.ChunkUploadResponse, error)
	CompleteUpload(ctx context.Context, sessionID string) error
	GetUploadStatus(ctx context.Context, sessionID string) (*domain.UploadSession, error)
	AssembleChunks(ctx context.Context, session *domain.UploadSession) error
	CleanupTempFiles(ctx context.Context, sessionID string) error
}

type service struct {
	uploadRepo   port.UploadRepository
	encodingRepo port.EncodingRepository
	videoRepo    port.VideoRepository
	uploadsDir   string
	paths        storage.Paths
	validator    *validation.ChecksumValidator
	cfg          *config.Config
}

func NewService(uploadRepo port.UploadRepository, encodingRepo port.EncodingRepository, videoRepo port.VideoRepository, uploadsDir string, cfg *config.Config) Service {
	return &service{
		uploadRepo:   uploadRepo,
		encodingRepo: encodingRepo,
		videoRepo:    videoRepo,
		uploadsDir:   uploadsDir,
		paths:        storage.NewPaths(uploadsDir),
		validator:    validation.NewChecksumValidator(cfg),
		cfg:          cfg,
	}
}

func (s *service) InitiateUpload(ctx context.Context, userID string, req *domain.InitiateUploadRequest) (*domain.InitiateUploadResponse, error) {
	if ext := filepath.Ext(req.FileName); !validUploadExt(ext) {
		return nil, domain.NewDomainError("INVALID_FILE_EXTENSION", "Invalid file extension")
	}
	const maxFileSize = 10 * 1024 * 1024 * 1024
	if req.FileSize > maxFileSize {
		return nil, domain.NewDomainError("FILE_TOO_LARGE", "File size exceeds maximum limit of 10GB")
	}
	totalChunks := int((req.FileSize + req.ChunkSize - 1) / req.ChunkSize)
	now := time.Now()
	video := &domain.Video{
		ID:            uuid.NewString(),
		ThumbnailID:   uuid.NewString(),
		Title:         fmt.Sprintf("Uploading: %s", req.FileName),
		Description:   "Upload in progress",
		Privacy:       domain.PrivacyPrivate,
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

var uploadExtRe = regexp.MustCompile(`^\.[A-Za-z0-9]{1,8}$`)

func validUploadExt(ext string) bool {
	if ext == "" {
		return true
	}
	return uploadExtRe.MatchString(ext)
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
	video.Status = domain.StatusQueued
	video.UpdatedAt = time.Now()
	if err := s.videoRepo.Update(ctx, video); err != nil {
		return fmt.Errorf("failed to update video status: %w", err)
	}
	finalFilePath := s.paths.WebVideoFilePath(session.VideoID, filepath.Ext(session.FileName))
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
		chunkData, err := os.ReadFile(chunkPath)
		if err != nil {
			return fmt.Errorf("failed to read chunk %d: %w", chunkIndex, err)
		}
		if _, err := finalFile.Write(chunkData); err != nil {
			return fmt.Errorf("failed to write chunk %d to final file: %w", chunkIndex, err)
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
		log.Printf("estimating source resolution using width=%d, default AR=16:9 -> estHeight=%d -> %s", video.Metadata.Width, estHeight, resolution)
	} else {
		log.Printf("estimating source resolution using width=%d, AR=%q -> estHeight=%d -> %s", video.Metadata.Width, video.Metadata.AspectRatio, estHeight, resolution)
	}
}
