package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/jmoiron/sqlx"
	"gotube/internal/model"
)

// VideoRepository defines storage operations for videos and their renditions
type VideoRepository interface {
	Create(ctx context.Context, video *model.Video) error
	UpdateStatus(ctx context.Context, id int64, status string) error
	GetByID(ctx context.Context, id int64) (*model.Video, error)
	CreateRendition(ctx context.Context, r *model.VideoRendition) error
	ListRenditions(ctx context.Context, videoID int64) ([]*model.VideoRendition, error)
	GetPaginated(ctx context.Context, limit, offset int) ([]*model.Video, error)
	GetTotalCount(ctx context.Context) (int, error)
	Update(ctx context.Context, video *model.Video) error
	Delete(ctx context.Context, id int64) error
	GetByUserID(ctx context.Context, userID int64) ([]*model.Video, error)
	Search(ctx context.Context, query string, limit, offset int) ([]*model.Video, error)
}

// MySQLVideoRepository is a MySQL-based implementation
type MySQLVideoRepository struct {
	db *sqlx.DB
}

// NewMySQLVideoRepository returns a new MySQLVideoRepository
func NewMySQLVideoRepository(db *sqlx.DB) *MySQLVideoRepository {
	return &MySQLVideoRepository{db: db}
}

// Create inserts a new video record
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

// UpdateStatus changes the status of a video by ID
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

// GetByID returns a video by its ID
func (r *MySQLVideoRepository) GetByID(ctx context.Context, id int64) (*model.Video, error) {
	var v model.Video
	err := r.db.GetContext(ctx, &v, `SELECT * FROM videos WHERE id = ?`, id)
	if err != nil {
		return nil, err
	}
	return &v, nil
}

// CreateRendition inserts a new rendition row for a video
func (r *MySQLVideoRepository) CreateRendition(ctx context.Context, vr *model.VideoRendition) error {
	query := `INSERT INTO video_renditions (video_id, resolution, bitrate, file_path, created_at)
              VALUES (:video_id, :resolution, :bitrate, :file_path, :created_at)`
	_, err := r.db.NamedExecContext(ctx, query, vr)
	if err != nil {
		return fmt.Errorf("create rendition: %w", err)
	}
	return nil
}

// ListRenditions returns all renditions for a given video ID
func (r *MySQLVideoRepository) ListRenditions(ctx context.Context, videoID int64) ([]*model.VideoRendition, error) {
	var list []*model.VideoRendition
	err := r.db.SelectContext(ctx, &list, `SELECT * FROM video_renditions WHERE video_id = ? ORDER BY created_at`, videoID)
	if err != nil {
		return nil, fmt.Errorf("list renditions: %w", err)
	}
	return list, nil
}

// GetPaginated returns paginated videos
func (r *MySQLVideoRepository) GetPaginated(ctx context.Context, limit, offset int) ([]*model.Video, error) {
	var videos []*model.Video
	query := `SELECT * FROM videos WHERE status = 'ready' ORDER BY created_at DESC LIMIT ? OFFSET ?`
	err := r.db.SelectContext(ctx, &videos, query, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get paginated videos: %w", err)
	}
	return videos, nil
}

// GetTotalCount returns the total number of videos
func (r *MySQLVideoRepository) GetTotalCount(ctx context.Context) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM videos WHERE status = 'ready'`
	err := r.db.GetContext(ctx, &count, query)
	if err != nil {
		return 0, fmt.Errorf("failed to get total count: %w", err)
	}
	return count, nil
}

// Update updates video metadata
func (r *MySQLVideoRepository) Update(ctx context.Context, video *model.Video) error {
	query := `UPDATE videos SET title = :title, description = :description, updated_at = NOW() WHERE id = :id`
	_, err := r.db.NamedExecContext(ctx, query, video)
	if err != nil {
		return fmt.Errorf("failed to update video: %w", err)
	}
	return nil
}

// Delete deletes a video by ID
func (r *MySQLVideoRepository) Delete(ctx context.Context, id int64) error {
	query := `DELETE FROM videos WHERE id = ?`
	_, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete video: %w", err)
	}
	return nil
}

// GetByUserID returns all videos for a user
func (r *MySQLVideoRepository) GetByUserID(ctx context.Context, userID int64) ([]*model.Video, error) {
	var videos []*model.Video
	query := `SELECT * FROM videos WHERE user_id = ? ORDER BY created_at DESC`
	err := r.db.SelectContext(ctx, &videos, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get videos by user: %w", err)
	}
	return videos, nil
}

// Search searches videos by title or description
func (r *MySQLVideoRepository) Search(ctx context.Context, query string, limit, offset int) ([]*model.Video, error) {
	var videos []*model.Video
	searchQuery := `SELECT * FROM videos WHERE (title LIKE ? OR description LIKE ?) AND status = 'ready' ORDER BY created_at DESC LIMIT ? OFFSET ?`
	searchTerm := "%" + query + "%"
	err := r.db.SelectContext(ctx, &videos, searchQuery, searchTerm, searchTerm, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to search videos: %w", err)
	}
	return videos, nil
}
