package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"athena/internal/domain"
)

// BlacklistRepository defines data operations for video blacklisting.
type BlacklistRepository interface {
	AddToBlacklist(ctx context.Context, entry *domain.VideoBlacklist) error
	RemoveFromBlacklist(ctx context.Context, videoID uuid.UUID) error
	GetByVideoID(ctx context.Context, videoID uuid.UUID) (*domain.VideoBlacklist, error)
	List(ctx context.Context, limit, offset int) ([]*domain.VideoBlacklist, int, error)
}

type blacklistRepository struct {
	db *sqlx.DB
}

// NewBlacklistRepository creates a new BlacklistRepository.
func NewBlacklistRepository(db *sqlx.DB) BlacklistRepository {
	return &blacklistRepository{db: db}
}

func (r *blacklistRepository) AddToBlacklist(ctx context.Context, entry *domain.VideoBlacklist) error {
	if entry.ID == uuid.Nil {
		entry.ID = uuid.New()
	}
	_, err := r.db.ExecContext(ctx,
		`INSERT INTO video_blacklist (id, video_id, reason, unfederated, created_at)
		 VALUES ($1, $2, $3, $4, $5)`,
		entry.ID, entry.VideoID, entry.Reason, entry.Unfederated, entry.CreatedAt,
	)
	if err != nil {
		return fmt.Errorf("add to blacklist: %w", err)
	}
	return nil
}

func (r *blacklistRepository) RemoveFromBlacklist(ctx context.Context, videoID uuid.UUID) error {
	result, err := r.db.ExecContext(ctx,
		`DELETE FROM video_blacklist WHERE video_id = $1`, videoID)
	if err != nil {
		return fmt.Errorf("remove from blacklist: %w", err)
	}
	rows, _ := result.RowsAffected()
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}

func (r *blacklistRepository) GetByVideoID(ctx context.Context, videoID uuid.UUID) (*domain.VideoBlacklist, error) {
	var entry domain.VideoBlacklist
	err := r.db.GetContext(ctx, &entry,
		`SELECT id, video_id, reason, unfederated, created_at FROM video_blacklist WHERE video_id = $1`,
		videoID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get blacklist entry: %w", err)
	}
	return &entry, nil
}

func (r *blacklistRepository) List(ctx context.Context, limit, offset int) ([]*domain.VideoBlacklist, int, error) {
	var entries []*domain.VideoBlacklist
	err := r.db.SelectContext(ctx, &entries,
		`SELECT id, video_id, reason, unfederated, created_at FROM video_blacklist ORDER BY created_at DESC LIMIT $1 OFFSET $2`,
		limit, offset)
	if err != nil {
		return nil, 0, fmt.Errorf("list blacklist: %w", err)
	}
	var total int
	err = r.db.GetContext(ctx, &total, `SELECT COUNT(*) FROM video_blacklist`)
	if err != nil {
		return nil, 0, fmt.Errorf("count blacklist: %w", err)
	}
	return entries, total, nil
}
