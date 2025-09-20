package repository

import (
	"athena/internal/domain"
	"athena/internal/usecase"
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type commentRepository struct {
	db *sqlx.DB
}

func NewCommentRepository(db *sqlx.DB) usecase.CommentRepository {
	return &commentRepository{db: db}
}

func (r *commentRepository) Create(ctx context.Context, comment *domain.Comment) error {
	query := `
		INSERT INTO comments (video_id, user_id, parent_id, body, status, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at, updated_at`

	err := r.db.QueryRowContext(
		ctx,
		query,
		comment.VideoID,
		comment.UserID,
		comment.ParentID,
		comment.Body,
		domain.CommentStatusActive,
		time.Now(),
		time.Now(),
	).Scan(&comment.ID, &comment.CreatedAt, &comment.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create comment: %w", err)
	}

	comment.Status = domain.CommentStatusActive
	comment.FlagCount = 0
	return nil
}

func (r *commentRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Comment, error) {
	comment := &domain.Comment{}
	query := `
		SELECT id, video_id, user_id, parent_id, body, status, flag_count,
		       edited_at, created_at, updated_at
		FROM comments
		WHERE id = $1`

	err := r.db.GetContext(ctx, comment, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get comment: %w", err)
	}

	return comment, nil
}

func (r *commentRepository) GetByIDWithUser(ctx context.Context, id uuid.UUID) (*domain.CommentWithUser, error) {
	comment := &domain.CommentWithUser{}
	query := `
		SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status,
		       c.flag_count, c.edited_at, c.created_at, c.updated_at,
		       u.username, ua.webp_ipfs_cid as avatar
		FROM comments c
		JOIN users u ON c.user_id = u.id
		LEFT JOIN user_avatars ua ON u.id = ua.user_id
		WHERE c.id = $1`

	err := r.db.GetContext(ctx, comment, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("failed to get comment with user: %w", err)
	}

	return comment, nil
}

func (r *commentRepository) Update(ctx context.Context, id uuid.UUID, body string) error {
	query := `
		UPDATE comments
		SET body = $1, edited_at = $2, updated_at = $3
		WHERE id = $4 AND status = 'active'`

	now := time.Now()
	result, err := r.db.ExecContext(ctx, query, body, now, now, id)
	if err != nil {
		return fmt.Errorf("failed to update comment: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

func (r *commentRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `
		UPDATE comments
		SET status = 'deleted', updated_at = $1
		WHERE id = $2 AND status = 'active'`

	result, err := r.db.ExecContext(ctx, query, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to delete comment: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

func (r *commentRepository) ListByVideo(ctx context.Context, opts domain.CommentListOptions) ([]*domain.CommentWithUser, error) {
	comments := []*domain.CommentWithUser{}

	query := `
		SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status,
		       c.flag_count, c.edited_at, c.created_at, c.updated_at,
		       u.username, ua.webp_ipfs_cid as avatar
		FROM comments c
		JOIN users u ON c.user_id = u.id
		LEFT JOIN user_avatars ua ON u.id = ua.user_id
		WHERE c.video_id = $1 AND c.status = 'active'`

	args := []interface{}{opts.VideoID}
	argCount := 1

	// Add parent_id filter for threading
	if opts.ParentID == nil {
		query += " AND c.parent_id IS NULL"
	} else {
		argCount++
		query += fmt.Sprintf(" AND c.parent_id = $%d", argCount)
		args = append(args, *opts.ParentID)
	}

	// Add ordering
	if opts.OrderBy == "oldest" {
		query += " ORDER BY c.created_at ASC"
	} else {
		query += " ORDER BY c.created_at DESC"
	}

	// Add pagination
	argCount++
	query += fmt.Sprintf(" LIMIT $%d", argCount)
	args = append(args, opts.Limit)

	argCount++
	query += fmt.Sprintf(" OFFSET $%d", argCount)
	args = append(args, opts.Offset)

	err := r.db.SelectContext(ctx, &comments, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list comments: %w", err)
	}

	return comments, nil
}

func (r *commentRepository) ListReplies(ctx context.Context, parentID uuid.UUID, limit, offset int) ([]*domain.CommentWithUser, error) {
	comments := []*domain.CommentWithUser{}

	query := `
		SELECT c.id, c.video_id, c.user_id, c.parent_id, c.body, c.status,
		       c.flag_count, c.edited_at, c.created_at, c.updated_at,
		       u.username, ua.webp_ipfs_cid as avatar
		FROM comments c
		JOIN users u ON c.user_id = u.id
		LEFT JOIN user_avatars ua ON u.id = ua.user_id
		WHERE c.parent_id = $1 AND c.status = 'active'
		ORDER BY c.created_at ASC
		LIMIT $2 OFFSET $3`

	err := r.db.SelectContext(ctx, &comments, query, parentID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to list replies: %w", err)
	}

	return comments, nil
}

func (r *commentRepository) CountByVideo(ctx context.Context, videoID uuid.UUID, activeOnly bool) (int, error) {
	var count int
	query := `SELECT COUNT(*) FROM comments WHERE video_id = $1`

	if activeOnly {
		query += " AND status = 'active'"
	}

	err := r.db.GetContext(ctx, &count, query, videoID)
	if err != nil {
		return 0, fmt.Errorf("failed to count comments: %w", err)
	}

	return count, nil
}

func (r *commentRepository) FlagComment(ctx context.Context, flag *domain.CommentFlag) error {
	query := `
		INSERT INTO comment_flags (comment_id, user_id, reason, details, created_at)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (comment_id, user_id) DO UPDATE
		SET reason = EXCLUDED.reason, details = EXCLUDED.details, created_at = EXCLUDED.created_at
		RETURNING id`

	err := r.db.QueryRowContext(
		ctx,
		query,
		flag.CommentID,
		flag.UserID,
		flag.Reason,
		flag.Details,
		time.Now(),
	).Scan(&flag.ID)

	if err != nil {
		return fmt.Errorf("failed to flag comment: %w", err)
	}

	flag.CreatedAt = time.Now()
	return nil
}

func (r *commentRepository) UnflagComment(ctx context.Context, commentID, userID uuid.UUID) error {
	query := `DELETE FROM comment_flags WHERE comment_id = $1 AND user_id = $2`

	result, err := r.db.ExecContext(ctx, query, commentID, userID)
	if err != nil {
		return fmt.Errorf("failed to unflag comment: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

func (r *commentRepository) GetFlags(ctx context.Context, commentID uuid.UUID) ([]*domain.CommentFlag, error) {
	flags := []*domain.CommentFlag{}

	query := `
		SELECT id, comment_id, user_id, reason, details, created_at
		FROM comment_flags
		WHERE comment_id = $1
		ORDER BY created_at DESC`

	err := r.db.SelectContext(ctx, &flags, query, commentID)
	if err != nil {
		return nil, fmt.Errorf("failed to get comment flags: %w", err)
	}

	return flags, nil
}

func (r *commentRepository) UpdateStatus(ctx context.Context, id uuid.UUID, status domain.CommentStatus) error {
	query := `
		UPDATE comments
		SET status = $1, updated_at = $2
		WHERE id = $3`

	result, err := r.db.ExecContext(ctx, query, status, time.Now(), id)
	if err != nil {
		return fmt.Errorf("failed to update comment status: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrNotFound
	}

	return nil
}

func (r *commentRepository) IsOwner(ctx context.Context, commentID, userID uuid.UUID) (bool, error) {
	var exists bool
	query := `SELECT EXISTS(SELECT 1 FROM comments WHERE id = $1 AND user_id = $2)`

	err := r.db.GetContext(ctx, &exists, query, commentID, userID)
	if err != nil {
		return false, fmt.Errorf("failed to check comment ownership: %w", err)
	}

	return exists, nil
}
