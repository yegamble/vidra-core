package repository

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"

	"athena/internal/domain"
	"athena/internal/port"
)

type videoPasswordRepository struct {
	db *sqlx.DB
}

// NewVideoPasswordRepository creates a new VideoPasswordRepository.
func NewVideoPasswordRepository(db *sqlx.DB) port.VideoPasswordRepository {
	return &videoPasswordRepository{db: db}
}

func (r *videoPasswordRepository) ListByVideoID(ctx context.Context, videoID string) ([]domain.VideoPassword, error) {
	var passwords []domain.VideoPassword
	err := r.db.SelectContext(ctx, &passwords,
		`SELECT id, video_id, password_hash, created_at
		 FROM video_passwords
		 WHERE video_id = $1
		 ORDER BY created_at ASC`, videoID)
	if err != nil {
		return nil, fmt.Errorf("list video passwords: %w", err)
	}
	return passwords, nil
}

func (r *videoPasswordRepository) Create(ctx context.Context, videoID string, passwordHash string) (*domain.VideoPassword, error) {
	var pw domain.VideoPassword
	err := r.db.QueryRowxContext(ctx,
		`INSERT INTO video_passwords (video_id, password_hash)
		 VALUES ($1, $2)
		 RETURNING id, video_id, password_hash, created_at`,
		videoID, passwordHash).StructScan(&pw)
	if err != nil {
		return nil, fmt.Errorf("create video password: %w", err)
	}
	return &pw, nil
}

func (r *videoPasswordRepository) ReplaceAll(ctx context.Context, videoID string, passwordHashes []string) ([]domain.VideoPassword, error) {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, `DELETE FROM video_passwords WHERE video_id = $1`, videoID); err != nil {
		return nil, fmt.Errorf("delete existing passwords: %w", err)
	}

	passwords := make([]domain.VideoPassword, 0, len(passwordHashes))
	for _, hash := range passwordHashes {
		var pw domain.VideoPassword
		err := tx.QueryRowxContext(ctx,
			`INSERT INTO video_passwords (video_id, password_hash)
			 VALUES ($1, $2)
			 RETURNING id, video_id, password_hash, created_at`,
			videoID, hash).StructScan(&pw)
		if err != nil {
			return nil, fmt.Errorf("insert video password: %w", err)
		}
		passwords = append(passwords, pw)
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit video passwords: %w", err)
	}
	return passwords, nil
}

func (r *videoPasswordRepository) Delete(ctx context.Context, passwordID int64) error {
	result, err := r.db.ExecContext(ctx, `DELETE FROM video_passwords WHERE id = $1`, passwordID)
	if err != nil {
		return fmt.Errorf("delete video password: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("check rows affected: %w", err)
	}
	if rows == 0 {
		return domain.ErrNotFound
	}
	return nil
}
