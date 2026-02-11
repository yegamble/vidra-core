package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupPlaylistMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newPlaylistRepo(t *testing.T) (*playlistRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupPlaylistMockDB(t)
	repo := NewPlaylistRepository(db).(*playlistRepository)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func makePlaylistRows(id, userID uuid.UUID, privacy domain.Privacy, now time.Time) *sqlmock.Rows {
	desc := "playlist description"
	thumb := "https://example/thumb.jpg"
	return sqlmock.NewRows([]string{
		"id", "user_id", "name", "description", "privacy",
		"thumbnail_url", "is_watch_later", "created_at", "updated_at", "item_count",
	}).AddRow(id, userID, "Playlist", desc, string(privacy), thumb, false, now, now, 2)
}

func makePlaylistItemRows(itemID, playlistID, videoID uuid.UUID, now time.Time) *sqlmock.Rows {
	return sqlmock.NewRows([]string{
		"id", "playlist_id", "video_id", "position", "added_at",
		"video.id", "video.title", "video.description", "video.duration",
		"video.views", "video.privacy", "video.created_at", "video.updated_at",
	}).AddRow(
		itemID, playlistID, videoID, 0, now,
		videoID, "Video Title", "Video Description", 123,
		int64(10), string(domain.PrivacyPublic), now, now,
	)
}

func TestPlaylistRepository_Unit_Create(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		desc := "playlist description"
		thumb := "https://example/thumb.jpg"
		playlist := &domain.Playlist{
			UserID:       userID,
			Name:         "My Playlist",
			Description:  &desc,
			Privacy:      domain.PrivacyPrivate,
			ThumbnailURL: &thumb,
			IsWatchLater: false,
		}

		mock.ExpectExec(`(?s)INSERT INTO playlists`).
			WithArgs(
				sqlmock.AnyArg(), // id
				playlist.UserID,
				playlist.Name,
				playlist.Description,
				playlist.Privacy,
				playlist.ThumbnailURL,
				playlist.IsWatchLater,
				sqlmock.AnyArg(), // created_at
				sqlmock.AnyArg(), // updated_at
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.Create(ctx, playlist)
		require.NoError(t, err)
		assert.NotEqual(t, uuid.Nil, playlist.ID)
		assert.WithinDuration(t, now, playlist.CreatedAt, 2*time.Second)
		assert.WithinDuration(t, now, playlist.UpdatedAt, 2*time.Second)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		playlist := &domain.Playlist{UserID: userID, Name: "x", Privacy: domain.PrivacyPublic}
		mock.ExpectExec(`(?s)INSERT INTO playlists`).
			WillReturnError(errors.New("insert failed"))

		err := repo.Create(ctx, playlist)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create playlist")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPlaylistRepository_Unit_GetByID(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	playlistID := uuid.New()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT p.id, p.user_id, p.name, p.description, p.privacy`).
			WithArgs(playlistID).
			WillReturnRows(makePlaylistRows(playlistID, userID, domain.PrivacyPrivate, now))

		got, err := repo.GetByID(ctx, playlistID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, playlistID, got.ID)
		assert.Equal(t, 2, got.ItemCount)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT p.id, p.user_id, p.name, p.description, p.privacy`).
			WithArgs(playlistID).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetByID(ctx, playlistID)
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT p.id, p.user_id, p.name, p.description, p.privacy`).
			WithArgs(playlistID).
			WillReturnError(errors.New("query failed"))

		got, err := repo.GetByID(ctx, playlistID)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get playlist")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPlaylistRepository_Unit_Update(t *testing.T) {
	ctx := context.Background()
	playlistID := uuid.New()

	t.Run("update all fields success", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		name := "Updated Playlist"
		desc := "Updated Description"
		privacy := domain.PrivacyUnlisted
		thumb := "https://example/updated.jpg"
		updates := domain.UpdatePlaylistRequest{
			Name:         &name,
			Description:  &desc,
			Privacy:      &privacy,
			ThumbnailURL: &thumb,
		}

		mock.ExpectExec(`(?s)UPDATE playlists\s+SET updated_at = \$1`).
			WithArgs(sqlmock.AnyArg(), name, desc, privacy, thumb, playlistID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Update(ctx, playlistID, updates)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update empty payload still updates timestamp", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE playlists\s+SET updated_at = \$1`).
			WithArgs(sqlmock.AnyArg(), playlistID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Update(ctx, playlistID, domain.UpdatePlaylistRequest{})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE playlists\s+SET updated_at = \$1`).
			WillReturnError(errors.New("update failed"))

		err := repo.Update(ctx, playlistID, domain.UpdatePlaylistRequest{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update playlist")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE playlists\s+SET updated_at = \$1`).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Update(ctx, playlistID, domain.UpdatePlaylistRequest{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE playlists\s+SET updated_at = \$1`).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Update(ctx, playlistID, domain.UpdatePlaylistRequest{})
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPlaylistRepository_Unit_Delete(t *testing.T) {
	ctx := context.Background()
	playlistID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM playlists WHERE id = $1`)).
			WithArgs(playlistID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Delete(ctx, playlistID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM playlists WHERE id = $1`)).
			WithArgs(playlistID).
			WillReturnError(errors.New("delete failed"))

		err := repo.Delete(ctx, playlistID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete playlist")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM playlists WHERE id = $1`)).
			WithArgs(playlistID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Delete(ctx, playlistID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM playlists WHERE id = $1`)).
			WithArgs(playlistID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, playlistID)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPlaylistRepository_Unit_List(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	playlistID := uuid.New()
	userID := uuid.New()

	t.Run("success with filters", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		privacy := domain.PrivacyPrivate
		opts := domain.PlaylistListOptions{
			UserID:  &userID,
			Privacy: &privacy,
			Limit:   10,
			Offset:  2,
			OrderBy: "name",
		}

		mock.ExpectQuery(`(?s)SELECT p.id, p.user_id, p.name, p.description, p.privacy`).
			WithArgs(userID, privacy, opts.Limit, opts.Offset).
			WillReturnRows(makePlaylistRows(playlistID, userID, privacy, now))

		mock.ExpectQuery(`(?s)SELECT COUNT\(DISTINCT p.id\) FROM playlists p WHERE 1=1`).
			WithArgs(userID, privacy).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		playlists, total, err := repo.List(ctx, opts)
		require.NoError(t, err)
		require.Len(t, playlists, 1)
		assert.Equal(t, 1, total)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("select failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT p.id, p.user_id, p.name, p.description, p.privacy`).
			WithArgs(20, 0).
			WillReturnError(errors.New("select failed"))

		playlists, total, err := repo.List(ctx, domain.PlaylistListOptions{Limit: 20, Offset: 0})
		require.Nil(t, playlists)
		assert.Equal(t, 0, total)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list playlists")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT p.id, p.user_id, p.name, p.description, p.privacy`).
			WithArgs(20, 0).
			WillReturnRows(makePlaylistRows(playlistID, userID, domain.PrivacyPublic, now))
		mock.ExpectQuery(`(?s)SELECT COUNT\(DISTINCT p.id\) FROM playlists p WHERE 1=1`).
			WillReturnError(errors.New("count failed"))

		playlists, total, err := repo.List(ctx, domain.PlaylistListOptions{Limit: 20, Offset: 0})
		require.Nil(t, playlists)
		assert.Equal(t, 0, total)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get playlist count")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPlaylistRepository_Unit_AddRemoveItems(t *testing.T) {
	ctx := context.Background()
	playlistID := uuid.New()
	videoID := uuid.New()
	itemID := uuid.New()

	t.Run("add item with explicit position", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		pos := 7
		mock.ExpectExec(`(?s)INSERT INTO playlist_items`).
			WithArgs(sqlmock.AnyArg(), playlistID, videoID, pos, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.AddItem(ctx, playlistID, videoID, &pos)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("add item with implicit append", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(MAX(position), -1) FROM playlist_items WHERE playlist_id = $1`)).
			WithArgs(playlistID).
			WillReturnRows(sqlmock.NewRows([]string{"max"}).AddRow(2))
		mock.ExpectExec(`(?s)INSERT INTO playlist_items`).
			WithArgs(sqlmock.AnyArg(), playlistID, videoID, 3, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.AddItem(ctx, playlistID, videoID, nil)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("add item max-position lookup error", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(MAX(position), -1) FROM playlist_items WHERE playlist_id = $1`)).
			WithArgs(playlistID).
			WillReturnError(errors.New("lookup failed"))

		err := repo.AddItem(ctx, playlistID, videoID, nil)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get max position")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("add item insert error", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		pos := 0
		mock.ExpectExec(`(?s)INSERT INTO playlist_items`).
			WithArgs(sqlmock.AnyArg(), playlistID, videoID, pos, sqlmock.AnyArg()).
			WillReturnError(errors.New("insert failed"))

		err := repo.AddItem(ctx, playlistID, videoID, &pos)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to add item to playlist")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("remove item success and branches", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		require.NoError(t, repo.RemoveItem(ctx, playlistID, itemID))

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnError(errors.New("delete failed"))
		err := repo.RemoveItem(ctx, playlistID, itemID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to remove item from playlist")

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))
		err = repo.RemoveItem(ctx, playlistID, itemID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnResult(sqlmock.NewResult(0, 0))
		err = repo.RemoveItem(ctx, playlistID, itemID)
		require.ErrorIs(t, err, domain.ErrNotFound)

		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPlaylistRepository_Unit_GetItems(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	playlistID := uuid.New()
	itemID := uuid.New()
	videoID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT pi.id, pi.playlist_id, pi.video_id, pi.position, pi.added_at`).
			WithArgs(playlistID, 10, 0).
			WillReturnRows(makePlaylistItemRows(itemID, playlistID, videoID, now))

		items, err := repo.GetItems(ctx, playlistID, 10, 0)
		require.NoError(t, err)
		require.Len(t, items, 1)
		assert.Equal(t, "Video Title", items[0].Video.Title)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT pi.id, pi.playlist_id, pi.video_id, pi.position, pi.added_at`).
			WithArgs(playlistID, 10, 0).
			WillReturnError(errors.New("query failed"))

		items, err := repo.GetItems(ctx, playlistID, 10, 0)
		require.Nil(t, items)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get playlist items")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan error", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "playlist_id", "video_id", "position", "added_at",
			"video.id", "video.title", "video.description", "video.duration",
			"video.views", "video.privacy", "video.created_at", "video.updated_at",
		}).AddRow(
			itemID, playlistID, videoID, 0, now,
			videoID, "Title", "Desc", "bad-duration",
			int64(1), string(domain.PrivacyPublic), now, now,
		)

		mock.ExpectQuery(`(?s)SELECT pi.id, pi.playlist_id, pi.video_id, pi.position, pi.added_at`).
			WithArgs(playlistID, 10, 0).
			WillReturnRows(rows)

		items, err := repo.GetItems(ctx, playlistID, 10, 0)
		require.Nil(t, items)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to scan playlist item")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPlaylistRepository_Unit_ReorderItem(t *testing.T) {
	ctx := context.Background()
	playlistID := uuid.New()
	itemID := uuid.New()

	t.Run("begin tx failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

		err := repo.ReorderItem(ctx, playlistID, itemID, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to begin transaction")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("item not found", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT position FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()

		err := repo.ReorderItem(ctx, playlistID, itemID, 1)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("current equals new position no-op", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT position FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnRows(sqlmock.NewRows([]string{"position"}).AddRow(3))
		mock.ExpectRollback()

		err := repo.ReorderItem(ctx, playlistID, itemID, 3)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("move up success", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT position FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnRows(sqlmock.NewRows([]string{"position"}).AddRow(5))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE playlist_items SET position = $1 WHERE id = $2`)).
			WithArgs(sqlmock.AnyArg(), itemID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`(?s)UPDATE playlist_items\s+SET position = position \+ 1`).
			WithArgs(playlistID, 2, 5).
			WillReturnResult(sqlmock.NewResult(0, 3))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE playlist_items SET position = $1 WHERE id = $2`)).
			WithArgs(2, itemID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		err := repo.ReorderItem(ctx, playlistID, itemID, 2)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("move down success", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT position FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnRows(sqlmock.NewRows([]string{"position"}).AddRow(2))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE playlist_items SET position = $1 WHERE id = $2`)).
			WithArgs(sqlmock.AnyArg(), itemID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`(?s)UPDATE playlist_items\s+SET position = position - 1`).
			WithArgs(playlistID, 2, 6).
			WillReturnResult(sqlmock.NewResult(0, 4))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE playlist_items SET position = $1 WHERE id = $2`)).
			WithArgs(6, itemID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		err := repo.ReorderItem(ctx, playlistID, itemID, 6)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("shift failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT position FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnRows(sqlmock.NewRows([]string{"position"}).AddRow(4))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE playlist_items SET position = $1 WHERE id = $2`)).
			WithArgs(sqlmock.AnyArg(), itemID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`(?s)UPDATE playlist_items\s+SET position = position \+ 1`).
			WithArgs(playlistID, 1, 4).
			WillReturnError(errors.New("shift failed"))
		mock.ExpectRollback()

		err := repo.ReorderItem(ctx, playlistID, itemID, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to shift items")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update position failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT position FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnRows(sqlmock.NewRows([]string{"position"}).AddRow(4))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE playlist_items SET position = $1 WHERE id = $2`)).
			WithArgs(sqlmock.AnyArg(), itemID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectExec(`(?s)UPDATE playlist_items\s+SET position = position \+ 1`).
			WithArgs(playlistID, 1, 4).
			WillReturnResult(sqlmock.NewResult(0, 3))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE playlist_items SET position = $1 WHERE id = $2`)).
			WithArgs(1, itemID).
			WillReturnError(errors.New("update position failed"))
		mock.ExpectRollback()

		err := repo.ReorderItem(ctx, playlistID, itemID, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update item position")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("temporary position reservation failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT position FROM playlist_items WHERE playlist_id = $1 AND id = $2`)).
			WithArgs(playlistID, itemID).
			WillReturnRows(sqlmock.NewRows([]string{"position"}).AddRow(4))
		mock.ExpectExec(regexp.QuoteMeta(`UPDATE playlist_items SET position = $1 WHERE id = $2`)).
			WithArgs(sqlmock.AnyArg(), itemID).
			WillReturnError(errors.New("reserve failed"))
		mock.ExpectRollback()

		err := repo.ReorderItem(ctx, playlistID, itemID, 1)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to reserve temporary item position")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestPlaylistRepository_Unit_OwnershipAndWatchLater(t *testing.T) {
	ctx := context.Background()
	now := time.Now()
	playlistID := uuid.New()
	userID := uuid.New()

	t.Run("is owner success and query failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM playlists WHERE id = $1 AND user_id = $2)`)).
			WithArgs(playlistID, userID).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
		ok, err := repo.IsOwner(ctx, playlistID, userID)
		require.NoError(t, err)
		assert.True(t, ok)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM playlists WHERE id = $1 AND user_id = $2)`)).
			WithArgs(playlistID, userID).
			WillReturnError(errors.New("query failed"))
		ok, err = repo.IsOwner(ctx, playlistID, userID)
		require.Error(t, err)
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "failed to check playlist ownership")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get existing watch later", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		desc := "Videos to watch later"
		rows := sqlmock.NewRows([]string{
			"id", "user_id", "name", "description", "privacy",
			"thumbnail_url", "is_watch_later", "created_at", "updated_at",
		}).AddRow(playlistID, userID, "Watch Later", desc, string(domain.PrivacyPrivate), nil, true, now, now)

		mock.ExpectQuery(`(?s)SELECT id, user_id, name, description, privacy`).
			WithArgs(userID).
			WillReturnRows(rows)

		playlist, err := repo.GetOrCreateWatchLater(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, playlist)
		assert.Equal(t, playlistID, playlist.ID)
		assert.True(t, playlist.IsWatchLater)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("watch later get failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, user_id, name, description, privacy`).
			WithArgs(userID).
			WillReturnError(errors.New("select failed"))

		playlist, err := repo.GetOrCreateWatchLater(ctx, userID)
		require.Nil(t, playlist)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get watch later playlist")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("create watch later when missing", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, user_id, name, description, privacy`).
			WithArgs(userID).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectExec(`(?s)INSERT INTO playlists`).
			WithArgs(
				sqlmock.AnyArg(),
				userID,
				"Watch Later",
				sqlmock.AnyArg(),
				domain.PrivacyPrivate,
				nil,
				true,
				sqlmock.AnyArg(),
				sqlmock.AnyArg(),
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		playlist, err := repo.GetOrCreateWatchLater(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, playlist)
		assert.Equal(t, "Watch Later", playlist.Name)
		assert.True(t, playlist.IsWatchLater)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("create watch later failure", func(t *testing.T) {
		repo, mock, cleanup := newPlaylistRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, user_id, name, description, privacy`).
			WithArgs(userID).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectExec(`(?s)INSERT INTO playlists`).
			WillReturnError(errors.New("insert failed"))

		playlist, err := repo.GetOrCreateWatchLater(ctx, userID)
		require.Nil(t, playlist)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create watch later playlist")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
