package repository

import (
	"context"
	"database/sql"
	"fmt"

	"vidra-core/internal/domain"
	"vidra-core/internal/usecase"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

type videoCategoryRepository struct {
	db *sqlx.DB
}

func NewVideoCategoryRepository(db *sqlx.DB) usecase.VideoCategoryRepository {
	return &videoCategoryRepository{db: db}
}

func (r *videoCategoryRepository) Create(ctx context.Context, category *domain.VideoCategory) error {
	query := `
		INSERT INTO video_categories (
			name, slug, description, icon, color, display_order,
			is_active, created_by
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8
		) RETURNING id, created_at, updated_at`

	err := r.db.QueryRowContext(ctx, query,
		category.Name,
		category.Slug,
		category.Description,
		category.Icon,
		category.Color,
		category.DisplayOrder,
		category.IsActive,
		category.CreatedBy,
	).Scan(&category.ID, &category.CreatedAt, &category.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create video category: %w", err)
	}

	return nil
}

func (r *videoCategoryRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.VideoCategory, error) {
	var category domain.VideoCategory
	query := `
		SELECT id, name, slug, description, icon, color, display_order,
		       is_active, created_at, updated_at, created_by
		FROM video_categories
		WHERE id = $1`

	err := r.db.GetContext(ctx, &category, query, id)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("video category not found")
		}
		return nil, fmt.Errorf("failed to get video category: %w", err)
	}

	return &category, nil
}

func (r *videoCategoryRepository) GetBySlug(ctx context.Context, slug string) (*domain.VideoCategory, error) {
	var category domain.VideoCategory
	query := `
		SELECT id, name, slug, description, icon, color, display_order,
		       is_active, created_at, updated_at, created_by
		FROM video_categories
		WHERE slug = $1`

	err := r.db.GetContext(ctx, &category, query, slug)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("video category not found")
		}
		return nil, fmt.Errorf("failed to get video category: %w", err)
	}

	return &category, nil
}

func (r *videoCategoryRepository) List(ctx context.Context, opts domain.VideoCategoryListOptions) ([]*domain.VideoCategory, error) {
	query := `
		SELECT id, name, slug, description, icon, color, display_order,
		       is_active, created_at, updated_at, created_by
		FROM video_categories
		WHERE 1=1`

	args := []interface{}{}
	argCount := 0

	if opts.ActiveOnly {
		argCount++
		query += fmt.Sprintf(" AND is_active = $%d", argCount)
		args = append(args, true)
	}

	// Default order
	orderBy := "display_order"
	orderDir := "ASC"

	if opts.OrderBy != "" {
		switch opts.OrderBy {
		case "name", "slug", "display_order", "created_at":
			orderBy = opts.OrderBy
		}
	}

	if opts.OrderDir != "" {
		if opts.OrderDir == "desc" {
			orderDir = "DESC"
		}
	}

	query += fmt.Sprintf(" ORDER BY %s %s", orderBy, orderDir)

	if opts.Limit > 0 {
		argCount++
		query += fmt.Sprintf(" LIMIT $%d", argCount)
		args = append(args, opts.Limit)
	}

	if opts.Offset > 0 {
		argCount++
		query += fmt.Sprintf(" OFFSET $%d", argCount)
		args = append(args, opts.Offset)
	}

	var categories []*domain.VideoCategory
	err := r.db.SelectContext(ctx, &categories, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list video categories: %w", err)
	}

	return categories, nil
}

func (r *videoCategoryRepository) Update(ctx context.Context, id uuid.UUID, updates *domain.UpdateVideoCategoryRequest) error {
	query := `UPDATE video_categories SET `
	args := []interface{}{}
	argCount := 0
	updateClauses := []string{}

	if updates.Name != nil {
		argCount++
		updateClauses = append(updateClauses, fmt.Sprintf("name = $%d", argCount))
		args = append(args, *updates.Name)
	}

	if updates.Slug != nil {
		argCount++
		updateClauses = append(updateClauses, fmt.Sprintf("slug = $%d", argCount))
		args = append(args, *updates.Slug)
	}

	if updates.Description != nil {
		argCount++
		updateClauses = append(updateClauses, fmt.Sprintf("description = $%d", argCount))
		args = append(args, *updates.Description)
	}

	if updates.Icon != nil {
		argCount++
		updateClauses = append(updateClauses, fmt.Sprintf("icon = $%d", argCount))
		args = append(args, *updates.Icon)
	}

	if updates.Color != nil {
		argCount++
		updateClauses = append(updateClauses, fmt.Sprintf("color = $%d", argCount))
		args = append(args, *updates.Color)
	}

	if updates.DisplayOrder != nil {
		argCount++
		updateClauses = append(updateClauses, fmt.Sprintf("display_order = $%d", argCount))
		args = append(args, *updates.DisplayOrder)
	}

	if updates.IsActive != nil {
		argCount++
		updateClauses = append(updateClauses, fmt.Sprintf("is_active = $%d", argCount))
		args = append(args, *updates.IsActive)
	}

	if len(updateClauses) == 0 {
		return nil // Nothing to update
	}

	argCount++
	args = append(args, id)

	query += fmt.Sprintf("%s WHERE id = $%d",
		joinStrings(updateClauses, ", "), argCount)

	result, err := r.db.ExecContext(ctx, query, args...)
	if err != nil {
		return fmt.Errorf("failed to update video category: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("video category not found")
	}

	return nil
}

func (r *videoCategoryRepository) Delete(ctx context.Context, id uuid.UUID) error {
	query := `DELETE FROM video_categories WHERE id = $1`

	result, err := r.db.ExecContext(ctx, query, id)
	if err != nil {
		return fmt.Errorf("failed to delete video category: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("video category not found")
	}

	return nil
}

func (r *videoCategoryRepository) GetDefault(ctx context.Context) (*domain.VideoCategory, error) {
	var category domain.VideoCategory
	query := `
		SELECT id, name, slug, description, icon, color, display_order,
		       is_active, created_at, updated_at, created_by
		FROM video_categories
		WHERE slug = 'other'
		LIMIT 1`

	err := r.db.GetContext(ctx, &category, query)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, fmt.Errorf("default video category not found")
		}
		return nil, fmt.Errorf("failed to get default video category: %w", err)
	}

	return &category, nil
}

func joinStrings(strs []string, sep string) string {
	result := ""
	for i, s := range strs {
		if i > 0 {
			result += sep
		}
		result += s
	}
	return result
}
