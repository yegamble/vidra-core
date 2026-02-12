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
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupNotificationMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newNotificationRepo(t *testing.T) (*NotificationRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupNotificationMockDB(t)
	repo := NewNotificationRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func sampleNotification() domain.Notification {
	now := time.Now()
	return domain.Notification{
		ID:      uuid.New(),
		UserID:  uuid.New(),
		Type:    domain.NotificationNewVideo,
		Title:   "New Video",
		Message: "Channel X uploaded a new video",
		Data: map[string]interface{}{
			"video_id":     uuid.New().String(),
			"channel_name": "Channel X",
		},
		Read:      false,
		CreatedAt: now,
		ReadAt:    nil,
	}
}

func makeNotificationScanRows(n domain.Notification) *sqlmock.Rows {
	dataJSON, _ := json.Marshal(n.Data)
	return sqlmock.NewRows([]string{
		"id", "user_id", "type", "title", "message", "data", "read", "created_at", "read_at",
	}).AddRow(
		n.ID, n.UserID, n.Type, n.Title, n.Message, dataJSON, n.Read, n.CreatedAt, n.ReadAt,
	)
}

// ---------- Create ----------

func TestNotificationRepository_Unit_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("success unread", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		n := &domain.Notification{
			UserID:  uuid.New(),
			Type:    domain.NotificationNewVideo,
			Title:   "New Video",
			Message: "A new video was uploaded",
			Data:    map[string]interface{}{"key": "value"},
			Read:    false,
		}

		returnedID := uuid.New()
		now := time.Now()

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`)).
			WithArgs(n.UserID, n.Type, n.Title, n.Message, sqlmock.AnyArg(), false, nil).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(returnedID, now))

		err := repo.Create(ctx, n)
		require.NoError(t, err)
		assert.Equal(t, returnedID, n.ID)
		assert.Equal(t, now, n.CreatedAt)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success read with read_at", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		readAt := time.Now().Add(-time.Hour)
		n := &domain.Notification{
			UserID:  uuid.New(),
			Type:    domain.NotificationSystem,
			Title:   "System Notice",
			Message: "Maintenance scheduled",
			Data:    map[string]interface{}{},
			Read:    true,
			ReadAt:  &readAt,
		}

		returnedID := uuid.New()
		now := time.Now()

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`)).
			WithArgs(n.UserID, n.Type, n.Title, n.Message, sqlmock.AnyArg(), true, readAt).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(returnedID, now))

		err := repo.Create(ctx, n)
		require.NoError(t, err)
		assert.Equal(t, returnedID, n.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success read without read_at defaults to now", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		n := &domain.Notification{
			UserID:  uuid.New(),
			Type:    domain.NotificationSystem,
			Title:   "Notice",
			Message: "msg",
			Data:    map[string]interface{}{},
			Read:    true,
			ReadAt:  nil,
		}

		returnedID := uuid.New()
		now := time.Now()

		// readAt will be set to time.Now() inside Create, so use AnyArg
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`)).
			WithArgs(n.UserID, n.Type, n.Title, n.Message, sqlmock.AnyArg(), true, sqlmock.AnyArg()).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(returnedID, now))

		err := repo.Create(ctx, n)
		require.NoError(t, err)
		assert.Equal(t, returnedID, n.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("marshal error with unmarshalable data", func(t *testing.T) {
		repo, _, cleanup := newNotificationRepo(t)
		defer cleanup()

		n := &domain.Notification{
			UserID: uuid.New(),
			Type:   domain.NotificationNewVideo,
			Title:  "title",
			Data:   map[string]interface{}{"bad": make(chan int)},
		}

		err := repo.Create(ctx, n)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marshaling notification data")
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		n := &domain.Notification{
			UserID:  uuid.New(),
			Type:    domain.NotificationNewVideo,
			Title:   "title",
			Message: "msg",
			Data:    map[string]interface{}{},
			Read:    false,
		}

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`)).
			WithArgs(n.UserID, n.Type, n.Title, n.Message, sqlmock.AnyArg(), false, nil).
			WillReturnError(errors.New("insert failed"))

		err := repo.Create(ctx, n)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating notification")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- CreateBatch ----------

func TestNotificationRepository_Unit_CreateBatch(t *testing.T) {
	ctx := context.Background()

	t.Run("empty slice returns nil", func(t *testing.T) {
		repo, _, cleanup := newNotificationRepo(t)
		defer cleanup()

		err := repo.CreateBatch(ctx, []domain.Notification{})
		require.NoError(t, err)
	})

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		n1 := domain.Notification{
			UserID:  uuid.New(),
			Type:    domain.NotificationNewVideo,
			Title:   "Video 1",
			Message: "msg1",
			Data:    map[string]interface{}{"k": "v1"},
			Read:    false,
		}
		n2 := domain.Notification{
			UserID:  uuid.New(),
			Type:    domain.NotificationComment,
			Title:   "Comment",
			Message: "msg2",
			Data:    map[string]interface{}{"k": "v2"},
			Read:    false,
		}
		batch := []domain.Notification{n1, n2}

		id1 := uuid.New()
		id2 := uuid.New()
		now := time.Now()

		mock.ExpectBegin()
		mock.ExpectPrepare(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`))

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`)).
			WithArgs(n1.UserID, n1.Type, n1.Title, n1.Message, sqlmock.AnyArg(), false, nil).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(id1, now))

		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`)).
			WithArgs(n2.UserID, n2.Type, n2.Title, n2.Message, sqlmock.AnyArg(), false, nil).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(id2, now))

		mock.ExpectCommit()

		err := repo.CreateBatch(ctx, batch)
		require.NoError(t, err)
		assert.Equal(t, id1, batch[0].ID)
		assert.Equal(t, id2, batch[1].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin tx error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		batch := []domain.Notification{{
			UserID: uuid.New(),
			Type:   domain.NotificationSystem,
			Title:  "t",
			Data:   map[string]interface{}{},
		}}

		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

		err := repo.CreateBatch(ctx, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "beginning transaction")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("prepare error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		batch := []domain.Notification{{
			UserID: uuid.New(),
			Type:   domain.NotificationSystem,
			Title:  "t",
			Data:   map[string]interface{}{},
		}}

		mock.ExpectBegin()
		mock.ExpectPrepare(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`)).
			WillReturnError(errors.New("prepare failed"))
		mock.ExpectRollback()

		err := repo.CreateBatch(ctx, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "preparing statement")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("marshal error in batch", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		batch := []domain.Notification{{
			UserID: uuid.New(),
			Type:   domain.NotificationSystem,
			Title:  "t",
			Data:   map[string]interface{}{"bad": make(chan int)},
		}}

		mock.ExpectBegin()
		mock.ExpectPrepare(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`))
		mock.ExpectRollback()

		err := repo.CreateBatch(ctx, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marshaling notification data")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert error in batch", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		batch := []domain.Notification{{
			UserID:  uuid.New(),
			Type:    domain.NotificationSystem,
			Title:   "t",
			Message: "m",
			Data:    map[string]interface{}{},
			Read:    false,
		}}

		mock.ExpectBegin()
		mock.ExpectPrepare(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`))
		mock.ExpectQuery(regexp.QuoteMeta(
			`INSERT INTO notifications (user_id, type, title, message, data, read, read_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)
		RETURNING id, created_at`)).
			WithArgs(batch[0].UserID, batch[0].Type, batch[0].Title, batch[0].Message, sqlmock.AnyArg(), false, nil).
			WillReturnError(errors.New("insert failed"))
		mock.ExpectRollback()

		err := repo.CreateBatch(ctx, batch)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "inserting notification")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetByID ----------

func TestNotificationRepository_Unit_GetByID(t *testing.T) {
	ctx := context.Background()
	n := sampleNotification()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE id = $1`)).
			WithArgs(n.ID).
			WillReturnRows(makeNotificationScanRows(n))

		got, err := repo.GetByID(ctx, n.ID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, n.ID, got.ID)
		assert.Equal(t, n.UserID, got.UserID)
		assert.Equal(t, n.Type, got.Type)
		assert.Equal(t, n.Title, got.Title)
		assert.Equal(t, n.Message, got.Message)
		assert.Equal(t, n.Read, got.Read)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		id := uuid.New()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE id = $1`)).
			WithArgs(id).
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetByID(ctx, id)
		require.Nil(t, got)
		require.ErrorIs(t, err, domain.ErrNotificationNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		id := uuid.New()
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE id = $1`)).
			WithArgs(id).
			WillReturnError(errors.New("db error"))

		got, err := repo.GetByID(ctx, id)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting notification")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("unmarshal error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		id := uuid.New()
		rows := sqlmock.NewRows([]string{
			"id", "user_id", "type", "title", "message", "data", "read", "created_at", "read_at",
		}).AddRow(
			id, uuid.New(), domain.NotificationSystem, "title", "msg",
			[]byte(`{invalid json`), false, time.Now(), nil,
		)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE id = $1`)).
			WithArgs(id).
			WillReturnRows(rows)

		got, err := repo.GetByID(ctx, id)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "unmarshaling notification data")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- ListByUser ----------

func TestNotificationRepository_Unit_ListByUser(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()
	n := sampleNotification()
	n.UserID = userID

	t.Run("success minimal filter", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		filter := domain.NotificationFilter{
			UserID: userID,
		}

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE user_id = $1 ORDER BY created_at DESC`)).
			WithArgs(userID).
			WillReturnRows(makeNotificationScanRows(n))

		got, err := repo.ListByUser(ctx, filter)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, n.ID, got[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with unread filter and limit/offset", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		unread := true
		filter := domain.NotificationFilter{
			UserID: userID,
			Unread: &unread,
			Limit:  10,
			Offset: 5,
		}

		// When Unread=true, the query adds "AND read = $2" with value !*filter.Unread = false
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE user_id = $1 AND read = $2 ORDER BY created_at DESC LIMIT $3 OFFSET $4`)).
			WithArgs(userID, false, 10, 5).
			WillReturnRows(makeNotificationScanRows(n))

		got, err := repo.ListByUser(ctx, filter)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	// Note: The Types filter path uses ANY($N) with a raw []NotificationType
	// slice. This requires the pq driver's array handling at the SQL driver
	// level, which sqlmock cannot simulate. The Types filter path is exercised
	// in integration tests with a real PostgreSQL connection instead.

	t.Run("success with date range", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		start := time.Now().Add(-24 * time.Hour)
		end := time.Now()
		filter := domain.NotificationFilter{
			UserID:    userID,
			StartDate: &start,
			EndDate:   &end,
		}

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE user_id = $1 AND created_at >= $2 AND created_at <= $3 ORDER BY created_at DESC`)).
			WithArgs(userID, start, end).
			WillReturnRows(makeNotificationScanRows(n))

		got, err := repo.ListByUser(ctx, filter)
		require.NoError(t, err)
		require.Len(t, got, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		filter := domain.NotificationFilter{UserID: userID}

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE user_id = $1 ORDER BY created_at DESC`)).
			WithArgs(userID).
			WillReturnError(errors.New("query failed"))

		got, err := repo.ListByUser(ctx, filter)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "querying notifications")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		filter := domain.NotificationFilter{UserID: userID}

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE user_id = $1 ORDER BY created_at DESC`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{
				"id", "user_id", "type", "title", "message", "data", "read", "created_at", "read_at",
			}))

		got, err := repo.ListByUser(ctx, filter)
		require.NoError(t, err)
		assert.Empty(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		filter := domain.NotificationFilter{UserID: userID}

		// Return a row with wrong number of columns to trigger scan error
		badRows := sqlmock.NewRows([]string{
			"id", "user_id", "type", "title", "message", "data", "read", "created_at", "read_at",
		}).AddRow(
			"not-a-uuid", uuid.New(), domain.NotificationSystem, "t", "m",
			[]byte(`{}`), false, time.Now(), nil,
		)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT id, user_id, type, title, message, data, read, created_at, read_at
		FROM notifications
		WHERE user_id = $1 ORDER BY created_at DESC`)).
			WithArgs(userID).
			WillReturnRows(badRows)

		got, err := repo.ListByUser(ctx, filter)
		require.Nil(t, got)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scanning notification")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- MarkAsRead ----------

func TestNotificationRepository_Unit_MarkAsRead(t *testing.T) {
	ctx := context.Background()
	notifID := uuid.New()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE notifications
		SET read = true, read_at = NOW()
		WHERE id = $1 AND user_id = $2 AND read = false`)).
			WithArgs(notifID, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.MarkAsRead(ctx, notifID, userID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found (zero rows affected)", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE notifications
		SET read = true, read_at = NOW()
		WHERE id = $1 AND user_id = $2 AND read = false`)).
			WithArgs(notifID, userID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.MarkAsRead(ctx, notifID, userID)
		require.ErrorIs(t, err, domain.ErrNotificationNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE notifications
		SET read = true, read_at = NOW()
		WHERE id = $1 AND user_id = $2 AND read = false`)).
			WithArgs(notifID, userID).
			WillReturnError(errors.New("exec failed"))

		err := repo.MarkAsRead(ctx, notifID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marking notification as read")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE notifications
		SET read = true, read_at = NOW()
		WHERE id = $1 AND user_id = $2 AND read = false`)).
			WithArgs(notifID, userID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		err := repo.MarkAsRead(ctx, notifID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- MarkAllAsRead ----------

func TestNotificationRepository_Unit_MarkAllAsRead(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE notifications
		SET read = true, read_at = NOW()
		WHERE user_id = $1 AND read = false`)).
			WithArgs(userID).
			WillReturnResult(sqlmock.NewResult(0, 5))

		err := repo.MarkAllAsRead(ctx, userID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with zero rows (no unread)", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE notifications
		SET read = true, read_at = NOW()
		WHERE user_id = $1 AND read = false`)).
			WithArgs(userID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.MarkAllAsRead(ctx, userID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE notifications
		SET read = true, read_at = NOW()
		WHERE user_id = $1 AND read = false`)).
			WithArgs(userID).
			WillReturnError(errors.New("exec failed"))

		err := repo.MarkAllAsRead(ctx, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "marking all notifications as read")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- Delete ----------

func TestNotificationRepository_Unit_Delete(t *testing.T) {
	ctx := context.Background()
	notifID := uuid.New()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM notifications WHERE id = $1 AND user_id = $2`)).
			WithArgs(notifID, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Delete(ctx, notifID, userID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM notifications WHERE id = $1 AND user_id = $2`)).
			WithArgs(notifID, userID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, notifID, userID)
		require.ErrorIs(t, err, domain.ErrNotificationNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM notifications WHERE id = $1 AND user_id = $2`)).
			WithArgs(notifID, userID).
			WillReturnError(errors.New("delete failed"))

		err := repo.Delete(ctx, notifID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "deleting notification")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM notifications WHERE id = $1 AND user_id = $2`)).
			WithArgs(notifID, userID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows failed")))

		err := repo.Delete(ctx, notifID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- DeleteOldRead ----------

func TestNotificationRepository_Unit_DeleteOldRead(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM notifications
		WHERE read = true AND read_at < $1`)).
			WithArgs(sqlmock.AnyArg()). // cutoffTime is computed internally
			WillReturnResult(sqlmock.NewResult(0, 42))

		count, err := repo.DeleteOldRead(ctx, 30*24*time.Hour)
		require.NoError(t, err)
		assert.Equal(t, int64(42), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero deleted", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM notifications
		WHERE read = true AND read_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 0))

		count, err := repo.DeleteOldRead(ctx, time.Hour)
		require.NoError(t, err)
		assert.Equal(t, int64(0), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM notifications
		WHERE read = true AND read_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnError(errors.New("delete failed"))

		count, err := repo.DeleteOldRead(ctx, time.Hour)
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		assert.Contains(t, err.Error(), "deleting old notifications")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetUnreadCount ----------

func TestNotificationRepository_Unit_GetUnreadCount(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read = false`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(7))

		count, err := repo.GetUnreadCount(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, 7, count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero count", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read = false`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(0))

		count, err := repo.GetUnreadCount(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT COUNT(*) FROM notifications WHERE user_id = $1 AND read = false`)).
			WithArgs(userID).
			WillReturnError(errors.New("query failed"))

		count, err := repo.GetUnreadCount(ctx, userID)
		require.Error(t, err)
		assert.Equal(t, 0, count)
		assert.Contains(t, err.Error(), "getting unread count")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------- GetStats ----------

func TestNotificationRepository_Unit_GetStats(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		// First query: total + unread counts
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN read = false THEN 1 ELSE 0 END), 0) as unread
		FROM notifications
		WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"total", "unread"}).AddRow(15, 3))

		// Second query: counts by type
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT type, COUNT(*) as count
		FROM notifications
		WHERE user_id = $1
		GROUP BY type`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"type", "count"}).
				AddRow(string(domain.NotificationNewVideo), 10).
				AddRow(string(domain.NotificationComment), 5))

		stats, err := repo.GetStats(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, stats)
		assert.Equal(t, 15, stats.TotalCount)
		assert.Equal(t, 3, stats.UnreadCount)
		assert.Equal(t, 10, stats.ByType[domain.NotificationNewVideo])
		assert.Equal(t, 5, stats.ByType[domain.NotificationComment])
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("counts query error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN read = false THEN 1 ELSE 0 END), 0) as unread
		FROM notifications
		WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnError(errors.New("counts query failed"))

		stats, err := repo.GetStats(ctx, userID)
		require.Nil(t, stats)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting notification counts")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("type query error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN read = false THEN 1 ELSE 0 END), 0) as unread
		FROM notifications
		WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"total", "unread"}).AddRow(10, 2))

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT type, COUNT(*) as count
		FROM notifications
		WHERE user_id = $1
		GROUP BY type`)).
			WithArgs(userID).
			WillReturnError(errors.New("type query failed"))

		stats, err := repo.GetStats(ctx, userID)
		require.Nil(t, stats)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting type counts")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("type scan error", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN read = false THEN 1 ELSE 0 END), 0) as unread
		FROM notifications
		WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"total", "unread"}).AddRow(10, 2))

		// Return mismatched column types to cause scan error
		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT type, COUNT(*) as count
		FROM notifications
		WHERE user_id = $1
		GROUP BY type`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"type", "count"}).
				AddRow(string(domain.NotificationNewVideo), "not-an-int"))

		stats, err := repo.GetStats(ctx, userID)
		require.Nil(t, stats)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "scanning type count")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty stats", func(t *testing.T) {
		repo, mock, cleanup := newNotificationRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT
			COUNT(*) as total,
			COALESCE(SUM(CASE WHEN read = false THEN 1 ELSE 0 END), 0) as unread
		FROM notifications
		WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"total", "unread"}).AddRow(0, 0))

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT type, COUNT(*) as count
		FROM notifications
		WHERE user_id = $1
		GROUP BY type`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"type", "count"}))

		stats, err := repo.GetStats(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, stats)
		assert.Equal(t, 0, stats.TotalCount)
		assert.Equal(t, 0, stats.UnreadCount)
		assert.Empty(t, stats.ByType)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
