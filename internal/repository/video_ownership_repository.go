package repository

import (
	"vidra-core/internal/domain"
	"vidra-core/internal/port"
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type videoOwnershipRepository struct {
	db *sqlx.DB
}

// NewVideoOwnershipRepository creates a new VideoOwnershipRepository backed by PostgreSQL.
func NewVideoOwnershipRepository(db *sqlx.DB) port.VideoOwnershipRepository {
	return &videoOwnershipRepository{db: db}
}

func (r *videoOwnershipRepository) Create(ctx context.Context, c *domain.VideoOwnershipChange) error {
	if c.ID == uuid.Nil {
		c.ID = uuid.New()
	}
	now := time.Now().UTC()
	c.CreatedAt = now
	c.UpdatedAt = now
	c.Status = domain.VideoOwnershipChangePending

	_, err := r.db.ExecContext(ctx, `
		INSERT INTO video_ownership_changes (id, video_id, initiator_id, next_owner_id, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		c.ID, c.VideoID, c.InitiatorID, c.NextOwnerID, c.Status, c.CreatedAt, c.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create video ownership change: %w", err)
	}
	return nil
}

func (r *videoOwnershipRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.VideoOwnershipChange, error) {
	var c domain.VideoOwnershipChange
	err := r.db.GetContext(ctx, &c, `
		SELECT id, video_id, initiator_id, next_owner_id, status, created_at, updated_at
		FROM video_ownership_changes WHERE id = $1`, id)
	if err != nil {
		return nil, fmt.Errorf("get video ownership change: %w", err)
	}
	return &c, nil
}

func (r *videoOwnershipRepository) ListPendingForUser(ctx context.Context, userID string) ([]*domain.VideoOwnershipChange, error) {
	var changes []*domain.VideoOwnershipChange
	err := r.db.SelectContext(ctx, &changes, `
		SELECT id, video_id, initiator_id, next_owner_id, status, created_at, updated_at
		FROM video_ownership_changes
		WHERE next_owner_id = $1 AND status = 'waiting'
		ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, fmt.Errorf("list pending ownership changes: %w", err)
	}
	return changes, nil
}

func (r *videoOwnershipRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.VideoOwnershipChangeStatus) error {
	_, err := r.db.ExecContext(ctx, `
		UPDATE video_ownership_changes SET status = $1, updated_at = $2 WHERE id = $3`,
		status, time.Now().UTC(), id)
	if err != nil {
		return fmt.Errorf("update ownership change status: %w", err)
	}
	return nil
}
