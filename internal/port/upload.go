package port

import (
	"context"
	"vidra-core/internal/domain"
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

	// Batch upload management
	CreateBatch(ctx context.Context, batch *domain.BatchUpload) error
	GetBatch(ctx context.Context, batchID string) (*domain.BatchUpload, error)
	GetSessionsByBatchID(ctx context.Context, batchID string) ([]*domain.UploadSession, error)

	// Transaction support — fn receives a context carrying the transaction.
	// Repository methods using GetExecutor(ctx, db) will automatically
	// use the transaction when called within fn.
	WithTransaction(ctx context.Context, fn func(txCtx context.Context) error) error
}
