package repository

import (
	"context"
	"database/sql"
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

func setupMessageMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newMessageRepo(t *testing.T) (*messageRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupMessageMockDB(t)
	repo := NewMessageRepository(db).(*messageRepository)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func TestMessageRepository_Unit_CreateMessage(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		senderID := uuid.New().String()
		recipientID := uuid.New().String()
		now := time.Now()

		message := &domain.Message{
			ID:                   msgID,
			SenderID:             senderID,
			RecipientID:          recipientID,
			Content:              strPtr("test message"),
			MessageType:          domain.MessageTypeText,
			IsRead:               false,
			IsDeletedBySender:    false,
			IsDeletedByRecipient: false,
			ParentMessageID:      nil,
			CreatedAt:            now,
			UpdatedAt:            now,
		}

		mock.ExpectBegin()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO messages (id, sender_id, recipient_id, content, message_type, is_read, is_deleted_by_sender, is_deleted_by_recipient, parent_message_id, created_at, updated_at)`)).
			WithArgs(
				message.ID,
				message.SenderID,
				message.RecipientID,
				message.Content,
				message.MessageType,
				message.IsRead,
				message.IsDeletedBySender,
				message.IsDeletedByRecipient,
				message.ParentMessageID,
				message.CreatedAt,
				message.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO conversations (participant_one_id, participant_two_id, last_message_id, last_message_at, created_at, updated_at)`)).
			WithArgs(
				sqlmock.AnyArg(),
				sqlmock.AnyArg(),
				message.ID,
				message.CreatedAt,
			).
			WillReturnResult(sqlmock.NewResult(1, 1))

		mock.ExpectCommit()

		err := repo.CreateMessage(ctx, message)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("error on begin transaction", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		message := &domain.Message{ID: uuid.New().String()}

		mock.ExpectBegin().WillReturnError(sql.ErrConnDone)

		err := repo.CreateMessage(ctx, message)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to begin transaction")
	})

	t.Run("error on insert message", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		message := &domain.Message{
			ID:          uuid.New().String(),
			SenderID:    uuid.New().String(),
			RecipientID: uuid.New().String(),
			Content:     strPtr("test"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO messages`)).
			WillReturnError(sql.ErrConnDone)
		mock.ExpectRollback()

		err := repo.CreateMessage(ctx, message)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create message")
	})

	t.Run("error on upsert conversation", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		message := &domain.Message{
			ID:          uuid.New().String(),
			SenderID:    uuid.New().String(),
			RecipientID: uuid.New().String(),
			Content:     strPtr("test"),
			MessageType: domain.MessageTypeText,
			CreatedAt:   time.Now(),
			UpdatedAt:   time.Now(),
		}

		mock.ExpectBegin()
		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO messages`)).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO conversations`)).
			WillReturnError(sql.ErrConnDone)
		mock.ExpectRollback()

		err := repo.CreateMessage(ctx, message)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to upsert conversation")
	})
}

func TestMessageRepository_Unit_GetMessage(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		senderID := uuid.New().String()
		recipientID := uuid.New().String()
		userID := recipientID
		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "sender_id", "recipient_id", "content", "message_type",
			"is_read", "is_deleted_by_sender", "is_deleted_by_recipient",
			"parent_message_id", "created_at", "updated_at", "read_at",
			"sender.id", "sender.username", "sender.display_name",
			"recipient.id", "recipient.username", "recipient.display_name",
		}).AddRow(
			msgID, senderID, recipientID, "test content", domain.MessageTypeText,
			true, false, false,
			nil, now, now, &now,
			senderID, "sender_user", "Sender Name",
			recipientID, "recipient_user", "Recipient Name",
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT m.id, m.sender_id, m.recipient_id, m.content, m.message_type`)).
			WithArgs(msgID, userID).
			WillReturnRows(rows)

		message, err := repo.GetMessage(ctx, msgID, userID)
		require.NoError(t, err)
		require.NotNil(t, message)
		assert.Equal(t, msgID, message.ID)
		assert.Equal(t, senderID, message.SenderID)
		assert.Equal(t, recipientID, message.RecipientID)
		require.NotNil(t, message.Content)
		assert.Equal(t, "test content", *message.Content)
		assert.True(t, message.IsRead)
		require.NotNil(t, message.Sender)
		assert.Equal(t, "sender_user", message.Sender.Username)
		assert.Equal(t, "Sender Name", message.Sender.DisplayName)
		require.NotNil(t, message.Recipient)
		assert.Equal(t, "recipient_user", message.Recipient.Username)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		userID := uuid.New().String()

		rows := sqlmock.NewRows([]string{"id"})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT m.id, m.sender_id`)).
			WithArgs(msgID, userID).
			WillReturnRows(rows)

		message, err := repo.GetMessage(ctx, msgID, userID)
		assert.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrMessageNotFound)
		assert.Nil(t, message)
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		userID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT m.id`)).
			WithArgs(msgID, userID).
			WillReturnError(sql.ErrConnDone)

		message, err := repo.GetMessage(ctx, msgID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to query message")
		assert.Nil(t, message)
	})
}

func TestMessageRepository_Unit_GetMessages(t *testing.T) {
	ctx := context.Background()

	t.Run("success with results", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		userID := uuid.New().String()
		otherUserID := uuid.New().String()
		msg1ID := uuid.New().String()
		msg2ID := uuid.New().String()
		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "sender_id", "recipient_id", "content", "message_type",
			"is_read", "is_deleted_by_sender", "is_deleted_by_recipient",
			"parent_message_id", "created_at", "updated_at", "read_at",
			"sender.id", "sender.username", "sender.display_name",
			"recipient.id", "recipient.username", "recipient.display_name",
		}).
			AddRow(msg1ID, userID, otherUserID, "msg1", domain.MessageTypeText, false, false, false, nil, now, now, nil, userID, "user", "User", otherUserID, "other", "Other").
			AddRow(msg2ID, otherUserID, userID, "msg2", domain.MessageTypeText, true, false, false, nil, now, now, &now, otherUserID, "other", "Other", userID, "user", "User")

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT m.id, m.sender_id`)).
			WithArgs(userID, otherUserID, 10, 0).
			WillReturnRows(rows)

		messages, err := repo.GetMessages(ctx, userID, otherUserID, 10, 0)
		require.NoError(t, err)
		require.Len(t, messages, 2)
		assert.Equal(t, msg1ID, messages[0].ID)
		assert.Equal(t, msg2ID, messages[1].ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty results", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		userID := uuid.New().String()
		otherUserID := uuid.New().String()

		rows := sqlmock.NewRows([]string{"id"})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT m.id`)).
			WithArgs(userID, otherUserID, 10, 0).
			WillReturnRows(rows)

		messages, err := repo.GetMessages(ctx, userID, otherUserID, 10, 0)
		require.NoError(t, err)
		assert.Empty(t, messages)
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		userID := uuid.New().String()
		otherUserID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT m.id`)).
			WithArgs(userID, otherUserID, 10, 0).
			WillReturnError(sql.ErrConnDone)

		messages, err := repo.GetMessages(ctx, userID, otherUserID, 10, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to query messages")
		assert.Nil(t, messages)
	})
}

func TestMessageRepository_Unit_MarkMessageAsRead(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE messages SET is_read = true, read_at = NOW(), updated_at = NOW()`)).
			WithArgs(msgID, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.MarkMessageAsRead(ctx, msgID, userID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found (0 rows affected)", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE messages SET is_read = true`)).
			WithArgs(msgID, userID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.MarkMessageAsRead(ctx, msgID, userID)
		assert.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrMessageNotFound)
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE messages`)).
			WithArgs(msgID, userID).
			WillReturnError(sql.ErrConnDone)

		err := repo.MarkMessageAsRead(ctx, msgID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to mark message as read")
	})
}

func TestMessageRepository_Unit_DeleteMessage(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE messages SET is_deleted_by_sender = CASE WHEN sender_id = $2 THEN true ELSE is_deleted_by_sender END`)).
			WithArgs(msgID, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteMessage(ctx, msgID, userID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found (0 rows affected)", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE messages SET is_deleted_by_sender`)).
			WithArgs(msgID, userID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.DeleteMessage(ctx, msgID, userID)
		assert.Error(t, err)
		assert.ErrorIs(t, err, domain.ErrMessageNotFound)
	})

	t.Run("exec error", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		msgID := uuid.New().String()
		userID := uuid.New().String()

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE messages`)).
			WithArgs(msgID, userID).
			WillReturnError(sql.ErrConnDone)

		err := repo.DeleteMessage(ctx, msgID, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete message")
	})
}

func TestMessageRepository_Unit_GetConversations(t *testing.T) {
	ctx := context.Background()

	t.Run("success with results", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		userID := uuid.New().String()
		convID := uuid.New().String()
		p1ID := uuid.New().String()
		p2ID := uuid.New().String()
		lastMsgID := uuid.New().String()
		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "participant_one_id", "participant_two_id", "last_message_id",
			"last_message_at", "created_at", "updated_at",
			"p1.id", "p1.username", "p1.display_name",
			"p2.id", "p2.username", "p2.display_name",
			"last_message.id", "last_message.content", "last_message.sender_id", "last_message.created_at",
			"unread_count",
		}).AddRow(
			convID, p1ID, p2ID, &lastMsgID,
			now, now, now,
			p1ID, "user1", "User One",
			p2ID, "user2", "User Two",
			&lastMsgID, "last message", p1ID, &now,
			3,
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT c.id, c.participant_one_id`)).
			WithArgs(userID, 10, 0).
			WillReturnRows(rows)

		conversations, err := repo.GetConversations(ctx, userID, 10, 0)
		require.NoError(t, err)
		require.Len(t, conversations, 1)
		conv := conversations[0]
		assert.Equal(t, convID, conv.ID)
		assert.Equal(t, p1ID, conv.ParticipantOneID)
		assert.Equal(t, p2ID, conv.ParticipantTwoID)
		assert.Equal(t, 3, conv.UnreadCount)
		require.NotNil(t, conv.LastMessage)
		require.NotNil(t, conv.LastMessage.Content)
		assert.Equal(t, "last message", *conv.LastMessage.Content)
		require.NotNil(t, conv.ParticipantOne)
		assert.Equal(t, "user1", conv.ParticipantOne.Username)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty results", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		rows := sqlmock.NewRows([]string{"id"})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT c.id`)).
			WithArgs(userID, 10, 0).
			WillReturnRows(rows)

		conversations, err := repo.GetConversations(ctx, userID, 10, 0)
		require.NoError(t, err)
		assert.Empty(t, conversations)
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT c.id`)).
			WithArgs(userID, 10, 0).
			WillReturnError(sql.ErrConnDone)

		conversations, err := repo.GetConversations(ctx, userID, 10, 0)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to query conversations")
		assert.Nil(t, conversations)
	})
}

func TestMessageRepository_Unit_GetUnreadCount(t *testing.T) {
	ctx := context.Background()

	t.Run("success with count", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		rows := sqlmock.NewRows([]string{"count"}).AddRow(5)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM messages WHERE recipient_id = $1 AND is_read = false AND is_deleted_by_recipient = false`)).
			WithArgs(userID).
			WillReturnRows(rows)

		count, err := repo.GetUnreadCount(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, 5, count)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("zero count", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		rows := sqlmock.NewRows([]string{"count"}).AddRow(0)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*)`)).
			WithArgs(userID).
			WillReturnRows(rows)

		count, err := repo.GetUnreadCount(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, 0, count)
	})

	t.Run("query error", func(t *testing.T) {
		repo, mock, cleanup := newMessageRepo(t)
		defer cleanup()

		userID := uuid.New().String()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*)`)).
			WithArgs(userID).
			WillReturnError(sql.ErrConnDone)

		count, err := repo.GetUnreadCount(ctx, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get unread count")
		assert.Equal(t, 0, count)
	})
}
