package repository

import (
	"context"
	"fmt"

	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"vidra-core/internal/domain"
)

// ChapterRepository defines data operations for video chapters.
type ChapterRepository interface {
	GetByVideoID(ctx context.Context, videoID string) ([]*domain.VideoChapter, error)
	ReplaceAll(ctx context.Context, videoID string, chapters []*domain.VideoChapter) error
}

type chapterRepository struct {
	db *sqlx.DB
}

// NewChapterRepository creates a new ChapterRepository.
func NewChapterRepository(db *sqlx.DB) ChapterRepository {
	return &chapterRepository{db: db}
}

func (r *chapterRepository) GetByVideoID(ctx context.Context, videoID string) ([]*domain.VideoChapter, error) {
	var chapters []*domain.VideoChapter
	err := r.db.SelectContext(ctx, &chapters,
		`SELECT id, video_id, timecode, title, position
		 FROM video_chapters
		 WHERE video_id = $1
		 ORDER BY position ASC`,
		videoID)
	if err != nil {
		return nil, fmt.Errorf("get chapters: %w", err)
	}
	return chapters, nil
}

func (r *chapterRepository) ReplaceAll(ctx context.Context, videoID string, chapters []*domain.VideoChapter) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Delete existing chapters
	if _, err := tx.ExecContext(ctx, `DELETE FROM video_chapters WHERE video_id = $1`, videoID); err != nil {
		return fmt.Errorf("delete chapters: %w", err)
	}

	// Insert new chapters
	if len(chapters) > 0 {
		timecodes := make([]int, len(chapters))
		titles := make([]string, len(chapters))
		positions := make([]int, len(chapters))

		for i, c := range chapters {
			timecodes[i] = c.Timecode
			titles[i] = c.Title
			positions[i] = c.Position
		}

		query := `
			INSERT INTO video_chapters (video_id, timecode, title, position)
			SELECT $1, t.timecode, t.title, t.position
			FROM UNNEST($2::int[], $3::text[], $4::int[]) AS t(timecode, title, position)
		`
		if _, err := tx.ExecContext(ctx, query, videoID, pq.Array(timecodes), pq.Array(titles), pq.Array(positions)); err != nil {
			return fmt.Errorf("insert chapters: %w", err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit chapters: %w", err)
	}
	return nil
}
