package repository

import (
	"context"
	"fmt"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// LiveStreamSessionRepository handles DB operations for live stream session history.
type LiveStreamSessionRepository struct {
	db *sqlx.DB
}

// NewLiveStreamSessionRepository creates a new LiveStreamSessionRepository.
func NewLiveStreamSessionRepository(db *sqlx.DB) *LiveStreamSessionRepository {
	return &LiveStreamSessionRepository{db: db}
}

// ListSessions returns past sessions for a stream, newest first.
func (r *LiveStreamSessionRepository) ListSessions(ctx context.Context, streamID uuid.UUID, limit, offset int) ([]*domain.LiveStreamSession, error) {
	var sessions []*domain.LiveStreamSession
	err := r.db.SelectContext(ctx, &sessions,
		`SELECT id, stream_id, started_at, ended_at, peak_viewers, total_seconds, avg_viewers
		 FROM live_stream_sessions
		 WHERE stream_id = $1
		 ORDER BY started_at DESC
		 LIMIT $2 OFFSET $3`,
		streamID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("listing live stream sessions: %w", err)
	}
	return sessions, nil
}
