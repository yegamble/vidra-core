package usecase

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/google/uuid"

	"athena/internal/domain"
)

type uploadService struct {
	uploadRepo    UploadRepository
	encodingRepo  EncodingRepository
	videoRepo     VideoRepository
	uploadsDir    string
}

func NewUploadService(
	uploadRepo UploadRepository,
	encodingRepo EncodingRepository,
	videoRepo VideoRepository,
	uploadsDir string,
) UploadService {
	return &uploadService{
		uploadRepo:   uploadRepo,
		encodingRepo: encodingRepo,
		videoRepo:    videoRepo,
		uploadsDir:   uploadsDir,
	}
}

func (s *uploadService) InitiateUpload(ctx context.Context, userID string, req *domain.InitiateUploadRequest) (*domain.InitiateUploadResponse, error) {
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
		ID:          uuid.NewString(),
		ThumbnailID: uuid.NewString(),
		Title:       fmt.Sprintf("Uploading: %s", req.FileName),
		Description: "Upload in progress",
		Privacy:     domain.PrivacyPrivate, // Default to private until user sets metadata
		Status:      domain.StatusUploading,
		UploadDate:  now,
		UserID:      userID,
		FileSize:    req.FileSize,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.videoRepo.Create(ctx, video); err != nil {
		return nil, fmt.Errorf("failed to create video record: %w", err)
	}

	// Create upload session
	sessionID := uuid.NewString()
	tempDir := filepath.Join(s.uploadsDir, "temp", sessionID)
	if err := os.MkdirAll(tempDir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create temp directory: %w", err)
	}

	session := &domain.UploadSession{
		ID:             sessionID,
		VideoID:        video.ID,
		UserID:         userID,
		FileName:       req.FileName,
		FileSize:       req.FileSize,
		ChunkSize:      req.ChunkSize,
		TotalChunks:    totalChunks,
		UploadedChunks: []int{},
		Status:         domain.UploadStatusActive,
		TempFilePath:   filepath.Join(tempDir, "chunks"),
		CreatedAt:      now,
		UpdatedAt:      now,
		ExpiresAt:      now.Add(24 * time.Hour), // 24 hour expiry
	}

	if err := s.uploadRepo.CreateSession(ctx, session); err != nil {
		os.RemoveAll(tempDir) // Cleanup on failure
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
	finalFilePath := filepath.Join(s.uploadsDir, "completed", session.VideoID+filepath.Ext(session.FileName))
	
	// TODO: Detect video resolution from file metadata
	sourceResolution := "1080p" // Placeholder - should be detected from video metadata
	
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
	finalDir := filepath.Join(s.uploadsDir, "completed")
	if err := os.MkdirAll(finalDir, 0755); err != nil {
		return fmt.Errorf("failed to create completed directory: %w", err)
	}

	finalPath := filepath.Join(finalDir, session.VideoID+filepath.Ext(session.FileName))
	finalFile, err := os.Create(finalPath)
	if err != nil {
		return fmt.Errorf("failed to create final file: %w", err)
	}
	defer finalFile.Close()

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