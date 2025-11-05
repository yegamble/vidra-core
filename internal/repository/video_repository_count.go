package repository

import (
	"context"
	"fmt"
)

// Count returns the total number of videos
func (r *VideoRepository) Count(ctx context.Context) (int64, error) {
	query := `SELECT COUNT(*) FROM videos WHERE deleted_at IS NULL`

	var count int64
	err := r.db.QueryRowContext(ctx, query).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("failed to count videos: %w", err)
	}

	return count, nil
}
