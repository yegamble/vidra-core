package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
)

func setupChapterMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	return sqlxDB, mock
}

func TestChapterRepository_GetByVideoID(t *testing.T) {
	db, mock := setupChapterMockDB(t)
	defer db.Close()
	repo := NewChapterRepository(db)

	ctx := context.Background()
	videoID := "test-video-id"

	t.Run("success", func(t *testing.T) {
		rows := sqlmock.NewRows([]string{"id", "video_id", "timecode", "title", "position"}).
			AddRow("chap1", videoID, 0, "Intro", 0).
			AddRow("chap2", videoID, 60, "Part 1", 1)

		mock.ExpectQuery(`SELECT id, video_id, timecode, title, position FROM video_chapters WHERE video_id = \$1 ORDER BY position ASC`).
			WithArgs(videoID).
			WillReturnRows(rows)

		chapters, err := repo.GetByVideoID(ctx, videoID)
		assert.NoError(t, err)
		assert.Len(t, chapters, 2)
		assert.Equal(t, "Intro", chapters[0].Title)
		assert.Equal(t, "Part 1", chapters[1].Title)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db_error", func(t *testing.T) {
		mock.ExpectQuery(`SELECT id, video_id, timecode, title, position FROM video_chapters WHERE video_id = \$1 ORDER BY position ASC`).
			WithArgs(videoID).
			WillReturnError(fmt.Errorf("db error"))

		chapters, err := repo.GetByVideoID(ctx, videoID)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "get chapters: db error")
		assert.Nil(t, chapters)

		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChapterRepository_ReplaceAll(t *testing.T) {
	db, mock := setupChapterMockDB(t)
	defer db.Close()
	repo := NewChapterRepository(db)

	ctx := context.Background()
	videoID := "test-video-id"

	chapters := []*domain.VideoChapter{
		{Timecode: 0, Title: "Intro", Position: 0},
		{Timecode: 60, Title: "Part 1", Position: 1},
		{Timecode: 120, Title: "Part 2", Position: 2},
	}

	t.Run("success_with_chapters", func(t *testing.T) {
		mock.ExpectBegin()

		mock.ExpectExec(`DELETE FROM video_chapters WHERE video_id = \$1`).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 2)) // Assuming 2 deleted

		timecodes := []int{0, 60, 120}
		titles := []string{"Intro", "Part 1", "Part 2"}
		positions := []int{0, 1, 2}

		mock.ExpectExec(`(?s)INSERT INTO video_chapters.*SELECT.*FROM UNNEST.*`).
			WithArgs(videoID, pq.Array(timecodes), pq.Array(titles), pq.Array(positions)).
			WillReturnResult(sqlmock.NewResult(3, 3))

		mock.ExpectCommit()

		err := repo.ReplaceAll(ctx, videoID, chapters)
		assert.NoError(t, err)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success_no_chapters", func(t *testing.T) {
		mock.ExpectBegin()

		mock.ExpectExec(`DELETE FROM video_chapters WHERE video_id = \$1`).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 2))

		mock.ExpectCommit()

		err := repo.ReplaceAll(ctx, videoID, []*domain.VideoChapter{})
		assert.NoError(t, err)

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete_error", func(t *testing.T) {
		mock.ExpectBegin()

		mock.ExpectExec(`DELETE FROM video_chapters WHERE video_id = \$1`).
			WithArgs(videoID).
			WillReturnError(fmt.Errorf("delete error"))

		mock.ExpectRollback()

		err := repo.ReplaceAll(ctx, videoID, chapters)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "delete chapters: delete error")

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert_error", func(t *testing.T) {
		mock.ExpectBegin()

		mock.ExpectExec(`DELETE FROM video_chapters WHERE video_id = \$1`).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		timecodes := []int{0, 60, 120}
		titles := []string{"Intro", "Part 1", "Part 2"}
		positions := []int{0, 1, 2}

		mock.ExpectExec(`(?s)INSERT INTO video_chapters.*SELECT.*FROM UNNEST.*`).
			WithArgs(videoID, pq.Array(timecodes), pq.Array(titles), pq.Array(positions)).
			WillReturnError(fmt.Errorf("insert error"))

		mock.ExpectRollback()

		err := repo.ReplaceAll(ctx, videoID, chapters)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "insert chapters: insert error")

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin_tx_error", func(t *testing.T) {
		mock.ExpectBegin().WillReturnError(fmt.Errorf("begin error"))

		err := repo.ReplaceAll(ctx, videoID, chapters)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "begin transaction: begin error")

		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("commit_error", func(t *testing.T) {
		mock.ExpectBegin()

		mock.ExpectExec(`DELETE FROM video_chapters WHERE video_id = \$1`).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 2))

		timecodes := []int{0, 60, 120}
		titles := []string{"Intro", "Part 1", "Part 2"}
		positions := []int{0, 1, 2}

		mock.ExpectExec(`(?s)INSERT INTO video_chapters.*SELECT.*FROM UNNEST.*`).
			WithArgs(videoID, pq.Array(timecodes), pq.Array(titles), pq.Array(positions)).
			WillReturnResult(sqlmock.NewResult(3, 3))

		mock.ExpectCommit().WillReturnError(fmt.Errorf("commit error"))

		err := repo.ReplaceAll(ctx, videoID, chapters)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "commit chapters: commit error")

		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
