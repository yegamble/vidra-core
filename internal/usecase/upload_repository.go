package usecase

import (
	"athena/internal/domain"
	"context"
)

type UploadRepository interface {
	// Upload session management
	CreateSession(ctx context.Context, session *domain.UploadSession) error
	GetSession(ctx context.Context, sessionID string) (*domain.UploadSession, error)
	UpdateSession(ctx context.Context, session *domain.UploadSession) error
	DeleteSession(ctx context.Context, sessionID string) error

	// Chunk tracking
	RecordChunk(ctx context.Context, sessionID string, chunkIndex int) error
	GetUploadedChunks(ctx context.Context, sessionID string) ([]int, error)
	IsChunkUploaded(ctx context.Context, sessionID string, chunkIndex int) (bool, error)

	// Session cleanup
	ExpireOldSessions(ctx context.Context) error
	GetExpiredSessions(ctx context.Context) ([]*domain.UploadSession, error)
}

type EncodingRepository interface {
	// Encoding job management
	CreateJob(ctx context.Context, job *domain.EncodingJob) error
	GetJob(ctx context.Context, jobID string) (*domain.EncodingJob, error)
	GetJobByVideoID(ctx context.Context, videoID string) (*domain.EncodingJob, error)
	UpdateJob(ctx context.Context, job *domain.EncodingJob) error
	DeleteJob(ctx context.Context, jobID string) error

	// Queue operations
	GetPendingJobs(ctx context.Context, limit int) ([]*domain.EncodingJob, error)
	GetNextJob(ctx context.Context) (*domain.EncodingJob, error)

	// Job status tracking
	UpdateJobStatus(ctx context.Context, jobID string, status domain.EncodingStatus) error
	UpdateJobProgress(ctx context.Context, jobID string, progress int) error
	SetJobError(ctx context.Context, jobID string, errorMsg string) error
	GetJobCounts(ctx context.Context) (map[string]int64, error)
}

type UploadService interface {
	// Upload workflow
	InitiateUpload(ctx context.Context, userID string, req *domain.InitiateUploadRequest) (*domain.InitiateUploadResponse, error)
	UploadChunk(ctx context.Context, sessionID string, chunk *domain.ChunkUpload) (*domain.ChunkUploadResponse, error)
	CompleteUpload(ctx context.Context, sessionID string) error
	GetUploadStatus(ctx context.Context, sessionID string) (*domain.UploadSession, error)

	// File management
	AssembleChunks(ctx context.Context, session *domain.UploadSession) error
	CleanupTempFiles(ctx context.Context, sessionID string) error
}
