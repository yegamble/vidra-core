package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"

	"gotube/internal/model"
)

// VideoRepository defines storage operations for videos and their renditions.
// Implementations manage persistence of video metadata and encoded files.
type VideoRepository interface {
	Create(ctx context.Context, video *model.Video) error
	UpdateStatus(ctx context.Context, id int64, status string) error
	GetByID(ctx context.Context, id int64) (*model.Video, error)
	// List returns a slice of videos ordered by creation time descending.
	// Limit and offset control pagination.
	List(ctx context.Context, limit, offset int) ([]*model.Video, error)
	CreateRendition(ctx context.Context, r *model.VideoRendition) error
	ListRenditions(ctx context.Context, videoID int64) ([]*model.VideoRendition, error)
}

// MySQLVideoRepository is a MySQL-based implementation using sqlx. It
// operates on `videos` and `video_renditions` tables.
type MySQLVideoRepository struct {
	db *sqlx.DB
}

// NewMySQLVideoRepository returns a new MySQLVideoRepository.
func NewMySQLVideoRepository(db *sqlx.DB) *MySQLVideoRepository {
	return &MySQLVideoRepository{db: db}
}

// Create inserts a new video record. It sets the ID on the passed struct.
func (r *MySQLVideoRepository) Create(ctx context.Context, video *model.Video) error {
	query := `INSERT INTO videos (user_id, title, description, original_name, ipfs_cid, status, created_at, updated_at)
              VALUES (:user_id, :title, :description, :original_name, :ipfs_cid, :status, :created_at, :updated_at)`
	res, err := r.db.NamedExecContext(ctx, query, video)
	if err != nil {
		return fmt.Errorf("create video: %w", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return fmt.Errorf("get last insert id: %w", err)
	}
	video.ID = id
	return nil
}

// UpdateStatus changes the status of a video by ID.
func (r *MySQLVideoRepository) UpdateStatus(ctx context.Context, id int64, status string) error {
	res, err := r.db.ExecContext(ctx, `UPDATE videos SET status = ?, updated_at = NOW() WHERE id = ?`, status, id)
	if err != nil {
		return fmt.Errorf("update video status: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if n == 0 {
		return sql.ErrNoRows
	}
	return nil
}

// GetByID returns a video by its ID. It returns sql.ErrNoRows if not found.
func (r *MySQLVideoRepository) GetByID(ctx context.Context, id int64) (*model.Video, error) {
	var v model.Video
	err := r.db.GetContext(ctx, &v, `SELECT * FROM videos WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// List returns videos ordered by creation date (newest first).
func (r *MySQLVideoRepository) List(ctx context.Context, limit, offset int) ([]*model.Video, error) {
	var videos []*model.Video
	query := `SELECT * FROM videos ORDER BY created_at DESC LIMIT ? OFFSET ?`
	if err := r.db.SelectContext(ctx, &videos, query, limit, offset); err != nil {
		return nil, fmt.Errorf("list videos: %w", err)
	}
	return videos, nil
}

// CreateRendition inserts a new rendition row for a video.
func (r *MySQLVideoRepository) CreateRendition(ctx context.Context, vr *model.VideoRendition) error {
	query := `INSERT INTO video_renditions (video_id, resolution, bitrate, file_path, created_at)
              VALUES (:video_id, :resolution, :bitrate, :file_path, :created_at)`
	_, err := r.db.NamedExecContext(ctx, query, vr)
	if err != nil {
		return fmt.Errorf("create rendition: %w", err)
	}
	return nil
}

// ListRenditions returns all renditions for a given video ID. It returns an
// empty slice if no renditions exist.
func (r *MySQLVideoRepository) ListRenditions(ctx context.Context, videoID int64) ([]*model.VideoRendition, error) {
	var list []*model.VideoRendition
	err := r.db.SelectContext(ctx, &list, `SELECT * FROM video_renditions WHERE video_id = ? ORDER BY created_at`, videoID)
	if err != nil {
		return nil, fmt.Errorf("list renditions: %w", err)
	}
	return list, nil
}
