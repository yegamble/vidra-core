package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupAbuseMsgMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(db, "sqlmock"), mock
}

func TestAbuseMessageRepository_GetAbuseReportOwner(t *testing.T) {
	ctx := context.Background()
	reportID := uuid.New()

	t.Run("success", func(t *testing.T) {
		db, mock := setupAbuseMsgMockDB(t)
		defer db.Close()
		repo := NewAbuseMessageRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT reporter_id FROM abuse_reports`)).
			WithArgs(reportID).
			WillReturnRows(sqlmock.NewRows([]string{"reporter_id"}).AddRow("user-1"))

		ownerID, err := repo.GetAbuseReportOwner(ctx, reportID)
		require.NoError(t, err)
		assert.Equal(t, "user-1", ownerID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupAbuseMsgMockDB(t)
		defer db.Close()
		repo := NewAbuseMessageRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT reporter_id FROM abuse_reports`)).
			WithArgs(reportID).
			WillReturnError(sql.ErrNoRows)

		_, err := repo.GetAbuseReportOwner(ctx, reportID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		db, mock := setupAbuseMsgMockDB(t)
		defer db.Close()
		repo := NewAbuseMessageRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT reporter_id FROM abuse_reports`)).
			WithArgs(reportID).
			WillReturnError(errors.New("db error"))

		_, err := repo.GetAbuseReportOwner(ctx, reportID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "getting abuse report owner")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAbuseMessageRepository_ListAbuseMessages(t *testing.T) {
	ctx := context.Background()
	reportID := uuid.New()

	t.Run("success", func(t *testing.T) {
		db, mock := setupAbuseMsgMockDB(t)
		defer db.Close()
		repo := NewAbuseMessageRepository(db)

		msgID := uuid.New()
		now := time.Now()
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, abuse_report_id, sender_id, message, created_at`)).
			WithArgs(reportID).
			WillReturnRows(sqlmock.NewRows([]string{"id", "abuse_report_id", "sender_id", "message", "created_at"}).
				AddRow(msgID, reportID, "user-1", "test message", now))

		msgs, err := repo.ListAbuseMessages(ctx, reportID)
		require.NoError(t, err)
		assert.Len(t, msgs, 1)
		assert.Equal(t, msgID, msgs[0].ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		db, mock := setupAbuseMsgMockDB(t)
		defer db.Close()
		repo := NewAbuseMessageRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, abuse_report_id`)).
			WithArgs(reportID).
			WillReturnError(errors.New("query failed"))

		_, err := repo.ListAbuseMessages(ctx, reportID)
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAbuseMessageRepository_CreateAbuseMessage(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupAbuseMsgMockDB(t)
		defer db.Close()
		repo := NewAbuseMessageRepository(db)

		reportID := uuid.New()
		msgID := uuid.New()
		now := time.Now()

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO abuse_report_messages`)).
			WithArgs(reportID, "sender-1", "hello").
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at"}).AddRow(msgID, now))

		msg := &domain.AbuseMessage{
			AbuseReportID: reportID,
			SenderID:      "sender-1",
			Message:       "hello",
		}
		err := repo.CreateAbuseMessage(ctx, msg)
		require.NoError(t, err)
		assert.Equal(t, msgID, msg.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		db, mock := setupAbuseMsgMockDB(t)
		defer db.Close()
		repo := NewAbuseMessageRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO abuse_report_messages`)).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateAbuseMessage(ctx, &domain.AbuseMessage{})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "creating abuse message")
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestAbuseMessageRepository_DeleteAbuseMessage(t *testing.T) {
	ctx := context.Background()
	reportID := uuid.New()
	msgID := uuid.New()

	t.Run("success", func(t *testing.T) {
		db, mock := setupAbuseMsgMockDB(t)
		defer db.Close()
		repo := NewAbuseMessageRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM abuse_report_messages`)).
			WithArgs(msgID, reportID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteAbuseMessage(ctx, reportID, msgID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupAbuseMsgMockDB(t)
		defer db.Close()
		repo := NewAbuseMessageRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM abuse_report_messages`)).
			WithArgs(msgID, reportID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteAbuseMessage(ctx, reportID, msgID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
