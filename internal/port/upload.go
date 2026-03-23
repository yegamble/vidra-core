package port

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
