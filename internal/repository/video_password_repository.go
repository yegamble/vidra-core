package repository

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
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
	if len(passwordHashes) > 0 {
		query := `
			INSERT INTO video_passwords (video_id, password_hash)
			SELECT $1, t.password_hash
			FROM UNNEST($2::text[]) AS t(password_hash)
			RETURNING id, video_id, password_hash, created_at
		`
		rows, err := tx.QueryxContext(ctx, query, videoID, pq.Array(passwordHashes))
		if err != nil {
			return nil, fmt.Errorf("insert video passwords: %w", err)
		}
		defer rows.Close()

		for rows.Next() {
			var pw domain.VideoPassword
			if err := rows.StructScan(&pw); err != nil {
				return nil, fmt.Errorf("scan video password: %w", err)
			}
			passwords = append(passwords, pw)
		}
		if err := rows.Err(); err != nil {
			return nil, fmt.Errorf("rows error: %w", err)
		}
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
