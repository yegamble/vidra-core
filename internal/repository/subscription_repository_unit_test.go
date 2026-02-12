package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupSubscriptionMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newSubscriptionRepo(t *testing.T) (usecase_SubscriptionRepository, *sqlx.DB, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupSubscriptionMockDB(t)
	repo := NewSubscriptionRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, db, mock, cleanup
}

// usecase_SubscriptionRepository is a local alias to avoid importing usecase in tests.
// NewSubscriptionRepository returns this interface.
type usecase_SubscriptionRepository interface {
	SubscribeToChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error
	UnsubscribeFromChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error
	IsSubscribed(ctx context.Context, subscriberID, channelID uuid.UUID) (bool, error)
	ListUserSubscriptions(ctx context.Context, subscriberID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error)
	ListChannelSubscribers(ctx context.Context, channelID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error)
	GetSubscriptionVideos(ctx context.Context, subscriberID uuid.UUID, limit, offset int) ([]domain.Video, int, error)
	Subscribe(ctx context.Context, subscriberID, channelID string) error
	Unsubscribe(ctx context.Context, subscriberID, channelID string) error
	ListSubscriptions(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.User, int64, error)
	ListSubscriptionVideos(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.Video, int64, error)
	CountSubscribers(ctx context.Context, channelID string) (int64, error)
	GetSubscribers(ctx context.Context, channelID string) ([]*domain.Subscription, error)
}

// ---------------------------------------------------------------------------
// SubscribeToChannel
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_SubscribeToChannel(t *testing.T) {
	ctx := context.Background()
	subscriberID := uuid.New()
	channelID := uuid.New()
	ownerID := uuid.New() // channel owner, different from subscriber

	t.Run("success", func(t *testing.T) {
		_, db, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		// SubscribeToChannel goes through WithTransaction, so we need to
		// supply the tx via context to avoid the BeginTx/Commit dance.
		// We inject a *sqlx.Tx in the context so subscribeWithExecutor is
		// called directly on that tx.
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(ownerID))
		mock.ExpectExec(`(?s)INSERT INTO subscriptions`).
			WithArgs(subscriberID, channelID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		repo := NewSubscriptionRepository(db)
		err := repo.SubscribeToChannel(ctx, subscriberID, channelID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("channel not found", func(t *testing.T) {
		_, db, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnError(sql.ErrNoRows)
		mock.ExpectRollback()

		repo := NewSubscriptionRepository(db)
		err := repo.SubscribeToChannel(ctx, subscriberID, channelID)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cannot subscribe to own channel", func(t *testing.T) {
		_, db, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(subscriberID)) // owner == subscriber
		mock.ExpectRollback()

		repo := NewSubscriptionRepository(db)
		err := repo.SubscribeToChannel(ctx, subscriberID, channelID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cannot subscribe to your own channel")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("channel ownership check failure", func(t *testing.T) {
		_, db, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnError(errors.New("db error"))
		mock.ExpectRollback()

		repo := NewSubscriptionRepository(db)
		err := repo.SubscribeToChannel(ctx, subscriberID, channelID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to check channel ownership")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert failure", func(t *testing.T) {
		_, db, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(ownerID))
		mock.ExpectExec(`(?s)INSERT INTO subscriptions`).
			WithArgs(subscriberID, channelID).
			WillReturnError(errors.New("insert error"))
		mock.ExpectRollback()

		repo := NewSubscriptionRepository(db)
		err := repo.SubscribeToChannel(ctx, subscriberID, channelID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to subscribe")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// UnsubscribeFromChannel
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_UnsubscribeFromChannel(t *testing.T) {
	ctx := context.Background()
	subscriberID := uuid.New()
	channelID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2`)).
			WithArgs(subscriberID, channelID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UnsubscribeFromChannel(ctx, subscriberID, channelID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2`)).
			WithArgs(subscriberID, channelID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UnsubscribeFromChannel(ctx, subscriberID, channelID)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2`)).
			WithArgs(subscriberID, channelID).
			WillReturnError(errors.New("delete error"))

		err := repo.UnsubscribeFromChannel(ctx, subscriberID, channelID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unsubscribe")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2`)).
			WithArgs(subscriberID, channelID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.UnsubscribeFromChannel(ctx, subscriberID, channelID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// IsSubscribed
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_IsSubscribed(t *testing.T) {
	ctx := context.Background()
	subscriberID := uuid.New()
	channelID := uuid.New()

	t.Run("subscribed", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2)`)).
			WithArgs(subscriberID, channelID).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))

		ok, err := repo.IsSubscribed(ctx, subscriberID, channelID)
		require.NoError(t, err)
		assert.True(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not subscribed", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2)`)).
			WithArgs(subscriberID, channelID).
			WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(false))

		ok, err := repo.IsSubscribed(ctx, subscriberID, channelID)
		require.NoError(t, err)
		assert.False(t, ok)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT EXISTS(SELECT 1 FROM subscriptions WHERE subscriber_id = $1 AND channel_id = $2)`)).
			WithArgs(subscriberID, channelID).
			WillReturnError(errors.New("query failed"))

		ok, err := repo.IsSubscribed(ctx, subscriberID, channelID)
		require.Error(t, err)
		assert.False(t, ok)
		assert.Contains(t, err.Error(), "failed to check subscription")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// ListUserSubscriptions
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_ListUserSubscriptions(t *testing.T) {
	ctx := context.Background()
	subscriberID := uuid.New()
	now := time.Now()

	t.Run("success with results", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		subID := uuid.New()
		channelID := uuid.New()
		accountID := uuid.New()
		desc := "test channel"

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE subscriber_id = $1`)).
			WithArgs(subscriberID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows([]string{
			"id", "subscriber_id", "channel_id", "created_at",
			"channel.id", "channel.account_id", "channel.handle", "channel.display_name",
			"channel.description", "channel.is_local", "channel.followers_count",
			"channel.videos_count", "channel.created_at", "channel.updated_at",
		}).AddRow(
			subID, subscriberID, channelID, now,
			channelID, accountID, "test-channel", "Test Channel",
			&desc, true, 10,
			5, now, now,
		)

		mock.ExpectQuery(`(?s)SELECT.*FROM subscriptions s.*JOIN channels c`).
			WithArgs(subscriberID, 10, 0).
			WillReturnRows(rows)

		resp, err := repo.ListUserSubscriptions(ctx, subscriberID, 10, 0)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, 1, resp.Total)
		require.Len(t, resp.Data, 1)
		assert.Equal(t, subID, resp.Data[0].ID)
		require.NotNil(t, resp.Data[0].Channel)
		assert.Equal(t, "test-channel", resp.Data[0].Channel.Handle)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success empty results", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE subscriber_id = $1`)).
			WithArgs(subscriberID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		rows := sqlmock.NewRows([]string{
			"id", "subscriber_id", "channel_id", "created_at",
			"channel.id", "channel.account_id", "channel.handle", "channel.display_name",
			"channel.description", "channel.is_local", "channel.followers_count",
			"channel.videos_count", "channel.created_at", "channel.updated_at",
		})

		mock.ExpectQuery(`(?s)SELECT.*FROM subscriptions s.*JOIN channels c`).
			WithArgs(subscriberID, 10, 0).
			WillReturnRows(rows)

		resp, err := repo.ListUserSubscriptions(ctx, subscriberID, 10, 0)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, 0, resp.Total)
		assert.Empty(t, resp.Data)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count query failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE subscriber_id = $1`)).
			WithArgs(subscriberID).
			WillReturnError(errors.New("count failed"))

		resp, err := repo.ListUserSubscriptions(ctx, subscriberID, 10, 0)
		require.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to count subscriptions")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list query failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE subscriber_id = $1`)).
			WithArgs(subscriberID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		mock.ExpectQuery(`(?s)SELECT.*FROM subscriptions s.*JOIN channels c`).
			WithArgs(subscriberID, 10, 0).
			WillReturnError(errors.New("query failed"))

		resp, err := repo.ListUserSubscriptions(ctx, subscriberID, 10, 0)
		require.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list subscriptions")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// ListChannelSubscribers
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_ListChannelSubscribers(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()
	now := time.Now()

	t.Run("success with results", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		subID := uuid.New()
		subscriberID := uuid.New()
		userIDStr := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE channel_id = $1`)).
			WithArgs(channelID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows([]string{
			"id", "subscriber_id", "channel_id", "created_at",
			"subscriber.id", "subscriber.username", "subscriber.email",
			"subscriber.display_name", "subscriber.bio", "subscriber.role",
			"subscriber.is_active", "subscriber.created_at", "subscriber.updated_at",
		}).AddRow(
			subID, subscriberID, channelID, now,
			userIDStr, "testuser", "test@example.com",
			"Test User", "bio", domain.RoleUser,
			true, now, now,
		)

		mock.ExpectQuery(`(?s)SELECT.*FROM subscriptions s.*JOIN users u`).
			WithArgs(channelID, 10, 0).
			WillReturnRows(rows)

		resp, err := repo.ListChannelSubscribers(ctx, channelID, 10, 0)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, 1, resp.Total)
		require.Len(t, resp.Data, 1)
		assert.Equal(t, subID, resp.Data[0].ID)
		require.NotNil(t, resp.Data[0].Subscriber)
		assert.Equal(t, "testuser", resp.Data[0].Subscriber.Username)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success empty results", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE channel_id = $1`)).
			WithArgs(channelID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		rows := sqlmock.NewRows([]string{
			"id", "subscriber_id", "channel_id", "created_at",
			"subscriber.id", "subscriber.username", "subscriber.email",
			"subscriber.display_name", "subscriber.bio", "subscriber.role",
			"subscriber.is_active", "subscriber.created_at", "subscriber.updated_at",
		})

		mock.ExpectQuery(`(?s)SELECT.*FROM subscriptions s.*JOIN users u`).
			WithArgs(channelID, 10, 0).
			WillReturnRows(rows)

		resp, err := repo.ListChannelSubscribers(ctx, channelID, 10, 0)
		require.NoError(t, err)
		require.NotNil(t, resp)
		assert.Equal(t, 0, resp.Total)
		assert.Empty(t, resp.Data)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count query failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE channel_id = $1`)).
			WithArgs(channelID).
			WillReturnError(errors.New("count failed"))

		resp, err := repo.ListChannelSubscribers(ctx, channelID, 10, 0)
		require.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to count subscribers")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("list query failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE channel_id = $1`)).
			WithArgs(channelID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		mock.ExpectQuery(`(?s)SELECT.*FROM subscriptions s.*JOIN users u`).
			WithArgs(channelID, 10, 0).
			WillReturnError(errors.New("query failed"))

		resp, err := repo.ListChannelSubscribers(ctx, channelID, 10, 0)
		require.Nil(t, resp)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list subscribers")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetSubscriptionVideos
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_GetSubscriptionVideos(t *testing.T) {
	ctx := context.Background()
	subscriberID := uuid.New()
	now := time.Now()

	t.Run("success with results", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		videoID := "video-123"
		channelIDVideo := uuid.New()
		categoryID := uuid.New()
		processedCIDs, _ := json.Marshal(map[string]string{"720p": "cid-720"})
		metadata, _ := json.Marshal(domain.VideoMetadata{Width: 1920, Height: 1080})
		outputPaths, _ := json.Marshal(map[string]string{"720p": "/out/720p.mp4"})

		mock.ExpectQuery(`(?s)SELECT COUNT\(\*\).*FROM videos v.*JOIN channels c.*JOIN subscriptions s`).
			WithArgs(subscriberID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows([]string{
			"id", "title", "description", "duration", "views",
			"privacy", "status", "upload_date", "user_id", "channel_id",
			"original_cid", "processed_cids", "thumbnail_cid",
			"tags", "category_id", "language", "file_size",
			"mime_type", "metadata", "created_at", "updated_at",
			"output_paths", "thumbnail_path", "preview_path",
		}).AddRow(
			videoID, "Test Video", "desc", 120, int64(1000),
			domain.PrivacyPublic, domain.StatusCompleted, now, "user-1", channelIDVideo,
			"orig-cid", processedCIDs, "thumb-cid",
			pq.StringArray{"tag1", "tag2"}, &categoryID, "en", int64(1024000),
			"video/mp4", metadata, now, now,
			outputPaths, "/thumb.jpg", "/preview.jpg",
		)

		mock.ExpectQuery(`(?s)SELECT.*FROM videos v.*JOIN channels c.*JOIN subscriptions s`).
			WithArgs(subscriberID, 10, 0).
			WillReturnRows(rows)

		videos, total, err := repo.GetSubscriptionVideos(ctx, subscriberID, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 1, total)
		require.Len(t, videos, 1)
		assert.Equal(t, videoID, videos[0].ID)
		assert.Equal(t, "Test Video", videos[0].Title)
		assert.Equal(t, []string{"tag1", "tag2"}, videos[0].Tags)
		assert.Equal(t, "cid-720", videos[0].ProcessedCIDs["720p"])
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success empty results", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT COUNT\(\*\).*FROM videos v.*JOIN channels c.*JOIN subscriptions s`).
			WithArgs(subscriberID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		rows := sqlmock.NewRows([]string{
			"id", "title", "description", "duration", "views",
			"privacy", "status", "upload_date", "user_id", "channel_id",
			"original_cid", "processed_cids", "thumbnail_cid",
			"tags", "category_id", "language", "file_size",
			"mime_type", "metadata", "created_at", "updated_at",
			"output_paths", "thumbnail_path", "preview_path",
		})

		mock.ExpectQuery(`(?s)SELECT.*FROM videos v.*JOIN channels c.*JOIN subscriptions s`).
			WithArgs(subscriberID, 10, 0).
			WillReturnRows(rows)

		videos, total, err := repo.GetSubscriptionVideos(ctx, subscriberID, 10, 0)
		require.NoError(t, err)
		assert.Equal(t, 0, total)
		assert.Empty(t, videos)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("count query failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT COUNT\(\*\).*FROM videos v.*JOIN channels c.*JOIN subscriptions s`).
			WithArgs(subscriberID).
			WillReturnError(errors.New("count failed"))

		videos, total, err := repo.GetSubscriptionVideos(ctx, subscriberID, 10, 0)
		require.Error(t, err)
		assert.Nil(t, videos)
		assert.Equal(t, 0, total)
		assert.Contains(t, err.Error(), "failed to count subscription videos")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("select query failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT COUNT\(\*\).*FROM videos v.*JOIN channels c.*JOIN subscriptions s`).
			WithArgs(subscriberID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		mock.ExpectQuery(`(?s)SELECT.*FROM videos v.*JOIN channels c.*JOIN subscriptions s`).
			WithArgs(subscriberID, 10, 0).
			WillReturnError(errors.New("query failed"))

		videos, total, err := repo.GetSubscriptionVideos(ctx, subscriberID, 10, 0)
		require.Error(t, err)
		assert.Nil(t, videos)
		assert.Equal(t, 0, total)
		assert.Contains(t, err.Error(), "failed to get subscription videos")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// Subscribe (deprecated)
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_Subscribe(t *testing.T) {
	ctx := context.Background()
	subscriberID := uuid.New()
	targetUserID := uuid.New()
	channelID := uuid.New()

	t.Run("success", func(t *testing.T) {
		_, db, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		// First query: find default channel for target user
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM channels
		WHERE account_id = $1::uuid
		ORDER BY created_at ASC
		LIMIT 1`)).
			WithArgs(targetUserID.String()).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(channelID))

		// Then SubscribeToChannel is called -> transaction
		mock.ExpectBegin()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT account_id FROM channels WHERE id = $1`)).
			WithArgs(channelID).
			WillReturnRows(sqlmock.NewRows([]string{"account_id"}).AddRow(targetUserID))
		mock.ExpectExec(`(?s)INSERT INTO subscriptions`).
			WithArgs(subscriberID, channelID).
			WillReturnResult(sqlmock.NewResult(0, 1))
		mock.ExpectCommit()

		repo := NewSubscriptionRepository(db)
		err := repo.Subscribe(ctx, subscriberID.String(), targetUserID.String())
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("user has no channels", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM channels
		WHERE account_id = $1::uuid
		ORDER BY created_at ASC
		LIMIT 1`)).
			WithArgs(targetUserID.String()).
			WillReturnError(sql.ErrNoRows)

		err := repo.Subscribe(ctx, subscriberID.String(), targetUserID.String())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "user has no channels")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("channel lookup failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM channels
		WHERE account_id = $1::uuid
		ORDER BY created_at ASC
		LIMIT 1`)).
			WithArgs(targetUserID.String()).
			WillReturnError(errors.New("db failure"))

		err := repo.Subscribe(ctx, subscriberID.String(), targetUserID.String())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to find user channel")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid subscriber ID", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		// The code queries the DB for the channel first, then parses subscriberID.
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id FROM channels
		WHERE account_id = $1::uuid
		ORDER BY created_at ASC
		LIMIT 1`)).
			WithArgs(targetUserID.String()).
			WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(channelID))

		err := repo.Subscribe(ctx, "not-a-uuid", targetUserID.String())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid subscriber ID")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// Unsubscribe (deprecated)
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_Unsubscribe(t *testing.T) {
	ctx := context.Background()
	subscriberID := uuid.New()
	targetUserID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)DELETE FROM subscriptions.*WHERE subscriber_id = \$1::uuid.*AND channel_id IN`).
			WithArgs(subscriberID.String(), targetUserID.String()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Unsubscribe(ctx, subscriberID.String(), targetUserID.String())
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)DELETE FROM subscriptions.*WHERE subscriber_id = \$1::uuid.*AND channel_id IN`).
			WithArgs(subscriberID.String(), targetUserID.String()).
			WillReturnError(errors.New("delete error"))

		err := repo.Unsubscribe(ctx, subscriberID.String(), targetUserID.String())
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to unsubscribe")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CountSubscribers (deprecated)
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_CountSubscribers(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE channel_id = $1::uuid`)).
			WithArgs(channelID.String()).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(int64(42)))

		count, err := repo.CountSubscribers(ctx, channelID.String())
		require.NoError(t, err)
		assert.Equal(t, int64(42), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE channel_id = $1::uuid`)).
			WithArgs(channelID.String()).
			WillReturnError(errors.New("count failed"))

		count, err := repo.CountSubscribers(ctx, channelID.String())
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		assert.Contains(t, err.Error(), "failed to count subscribers")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// ListSubscriptionVideos (deprecated)
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_ListSubscriptionVideos(t *testing.T) {
	ctx := context.Background()
	subscriberID := uuid.New()
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		videoID := "video-456"
		channelIDVideo := uuid.New()
		processedCIDs, _ := json.Marshal(map[string]string{})
		metadata, _ := json.Marshal(domain.VideoMetadata{})
		outputPaths, _ := json.Marshal(map[string]string{})

		mock.ExpectQuery(`(?s)SELECT COUNT\(\*\).*FROM videos v.*JOIN channels c.*JOIN subscriptions s`).
			WithArgs(subscriberID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows([]string{
			"id", "title", "description", "duration", "views",
			"privacy", "status", "upload_date", "user_id", "channel_id",
			"original_cid", "processed_cids", "thumbnail_cid",
			"tags", "category_id", "language", "file_size",
			"mime_type", "metadata", "created_at", "updated_at",
			"output_paths", "thumbnail_path", "preview_path",
		}).AddRow(
			videoID, "Video", "desc", 60, int64(100),
			domain.PrivacyPublic, domain.StatusCompleted, now, "user-2", channelIDVideo,
			"cid", processedCIDs, "thumb",
			pq.StringArray{}, nil, "en", int64(512000),
			"video/mp4", metadata, now, now,
			outputPaths, "/thumb.jpg", "/preview.jpg",
		)

		mock.ExpectQuery(`(?s)SELECT.*FROM videos v.*JOIN channels c.*JOIN subscriptions s`).
			WithArgs(subscriberID, 10, 0).
			WillReturnRows(rows)

		videos, total, err := repo.ListSubscriptionVideos(ctx, subscriberID.String(), 10, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total)
		require.Len(t, videos, 1)
		assert.Equal(t, videoID, videos[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid subscriber ID", func(t *testing.T) {
		repo, _, _, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		videos, total, err := repo.ListSubscriptionVideos(ctx, "bad-uuid", 10, 0)
		require.Error(t, err)
		assert.Nil(t, videos)
		assert.Equal(t, int64(0), total)
		assert.Contains(t, err.Error(), "invalid subscriber ID")
	})
}

// ---------------------------------------------------------------------------
// GetSubscribers (deprecated)
// ---------------------------------------------------------------------------

func TestSubscriptionRepository_GetSubscribers(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, _, mock, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		subID := uuid.New()
		subscriberID := uuid.New()
		userIDStr := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM subscriptions WHERE channel_id = $1`)).
			WithArgs(channelID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))

		rows := sqlmock.NewRows([]string{
			"id", "subscriber_id", "channel_id", "created_at",
			"subscriber.id", "subscriber.username", "subscriber.email",
			"subscriber.display_name", "subscriber.bio", "subscriber.role",
			"subscriber.is_active", "subscriber.created_at", "subscriber.updated_at",
		}).AddRow(
			subID, subscriberID, channelID, now,
			userIDStr, "testuser", "test@example.com",
			"Test User", "bio", domain.RoleUser,
			true, now, now,
		)

		mock.ExpectQuery(`(?s)SELECT.*FROM subscriptions s.*JOIN users u`).
			WithArgs(channelID, 1000, 0).
			WillReturnRows(rows)

		subs, err := repo.GetSubscribers(ctx, channelID.String())
		require.NoError(t, err)
		require.Len(t, subs, 1)
		assert.Equal(t, subID, subs[0].ID)
		require.NotNil(t, subs[0].Subscriber)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("invalid channel ID", func(t *testing.T) {
		repo, _, _, cleanup := newSubscriptionRepo(t)
		defer cleanup()

		subs, err := repo.GetSubscribers(ctx, "not-a-uuid")
		require.Error(t, err)
		assert.Nil(t, subs)
		assert.Contains(t, err.Error(), "invalid channel ID")
	})
}
