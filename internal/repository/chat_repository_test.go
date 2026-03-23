package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/domain"
)

func setupChatRepoTest(t *testing.T) (*chatRepository, sqlmock.Sqlmock, func()) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)

	sqlxDB := sqlx.NewDb(db, "sqlmock")
	repo := NewChatRepository(sqlxDB)

	cleanup := func() {
		db.Close()
	}

	return repo.(*chatRepository), mock, cleanup
}

func TestChatRepository_CreateMessage(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	msg := domain.NewChatMessage(
		uuid.New(),
		uuid.New(),
		"testuser",
		"Hello, world!",
	)

	metadataJSON, _ := json.Marshal(msg.Metadata)

	mock.ExpectExec("INSERT INTO chat_messages").
		WithArgs(
			msg.ID,
			msg.StreamID,
			msg.UserID,
			msg.Username,
			msg.Message,
			msg.Type,
			metadataJSON,
			msg.Deleted,
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.CreateMessage(ctx, msg)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_CreateMessage_ValidationError(t *testing.T) {
	repo, _, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	msg := &domain.ChatMessage{
		ID:       uuid.New(),
		StreamID: uuid.Nil,
		UserID:   uuid.New(),
		Message:  "test",
		Type:     domain.ChatMessageTypeRegular,
	}

	err := repo.CreateMessage(ctx, msg)
	assert.Error(t, err)
	assert.ErrorIs(t, err, domain.ErrInvalidStreamID)
}

func TestChatRepository_CreateMessage_DBError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	msg := domain.NewChatMessage(
		uuid.New(),
		uuid.New(),
		"testuser",
		"Hello",
	)

	mock.ExpectExec("INSERT INTO chat_messages").
		WillReturnError(sql.ErrConnDone)

	err := repo.CreateMessage(ctx, msg)
	assert.Error(t, err)
}

func TestChatRepository_GetMessages(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	limit := 50
	offset := 0

	rows := sqlmock.NewRows([]string{
		"id", "stream_id", "user_id", "username", "message",
		"type", "metadata", "deleted", "created_at",
	}).
		AddRow(
			uuid.New(), streamID, uuid.New(), "user1", "Hello",
			"message", `{}`, false, time.Now(),
		).
		AddRow(
			uuid.New(), streamID, uuid.New(), "user2", "Hi",
			"message", `{}`, false, time.Now(),
		)

	mock.ExpectQuery("SELECT (.+) FROM chat_messages").
		WithArgs(streamID, limit, offset).
		WillReturnRows(rows)

	messages, err := repo.GetMessages(ctx, streamID, limit, offset)
	assert.NoError(t, err)
	assert.Len(t, messages, 2)
	assert.Equal(t, "Hello", messages[0].Message)
	assert.Equal(t, "Hi", messages[1].Message)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessages_Empty(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()

	rows := sqlmock.NewRows([]string{
		"id", "stream_id", "user_id", "username", "message",
		"type", "metadata", "deleted", "created_at",
	})

	mock.ExpectQuery("SELECT (.+) FROM chat_messages").
		WithArgs(streamID, 50, 0).
		WillReturnRows(rows)

	messages, err := repo.GetMessages(ctx, streamID, 50, 0)
	assert.NoError(t, err)
	assert.Empty(t, messages)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessagesSince(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	since := time.Now().Add(-1 * time.Hour)

	rows := sqlmock.NewRows([]string{
		"id", "stream_id", "user_id", "username", "message",
		"type", "metadata", "deleted", "created_at",
	}).
		AddRow(
			uuid.New(), streamID, uuid.New(), "user1", "Recent message",
			"message", `{}`, false, time.Now(),
		)

	mock.ExpectQuery("SELECT (.+) FROM chat_messages").
		WithArgs(streamID, since).
		WillReturnRows(rows)

	messages, err := repo.GetMessagesSince(ctx, streamID, since)
	assert.NoError(t, err)
	assert.Len(t, messages, 1)
	assert.Equal(t, "Recent message", messages[0].Message)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_DeleteMessage(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	messageID := uuid.New()

	mock.ExpectExec("UPDATE chat_messages SET deleted").
		WithArgs(messageID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.DeleteMessage(ctx, messageID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_DeleteMessage_NotFound(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	messageID := uuid.New()

	mock.ExpectExec("UPDATE chat_messages SET deleted").
		WithArgs(messageID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.DeleteMessage(ctx, messageID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessageByID(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	messageID := uuid.New()
	streamID := uuid.New()
	userID := uuid.New()

	rows := sqlmock.NewRows([]string{
		"id", "stream_id", "user_id", "username", "message",
		"type", "metadata", "deleted", "created_at",
	}).
		AddRow(
			messageID, streamID, userID, "user1", "Test message",
			"message", `{}`, false, time.Now(),
		)

	mock.ExpectQuery("SELECT (.+) FROM chat_messages WHERE id").
		WithArgs(messageID).
		WillReturnRows(rows)

	msg, err := repo.GetMessageByID(ctx, messageID)
	assert.NoError(t, err)
	assert.Equal(t, messageID, msg.ID)
	assert.Equal(t, "Test message", msg.Message)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessageByID_NotFound(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	messageID := uuid.New()

	mock.ExpectQuery("SELECT (.+) FROM chat_messages WHERE id").
		WithArgs(messageID).
		WillReturnError(sql.ErrNoRows)

	msg, err := repo.GetMessageByID(ctx, messageID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.Nil(t, msg)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_AddModerator(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	mod := domain.NewChatModerator(uuid.New(), uuid.New(), uuid.New())

	mock.ExpectExec("INSERT INTO chat_moderators").
		WithArgs(mod.ID, mod.StreamID, mod.UserID, mod.GrantedBy, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.AddModerator(ctx, mod)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_AddModerator_AlreadyExists(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	mod := domain.NewChatModerator(uuid.New(), uuid.New(), uuid.New())

	mock.ExpectExec("INSERT INTO chat_moderators").
		WillReturnError(sql.ErrNoRows)

	err := repo.AddModerator(ctx, mod)
	assert.Error(t, err)
}

func TestChatRepository_RemoveModerator(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	mock.ExpectExec("DELETE FROM chat_moderators").
		WithArgs(streamID, userID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.RemoveModerator(ctx, streamID, userID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_RemoveModerator_NotFound(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	mock.ExpectExec("DELETE FROM chat_moderators").
		WithArgs(streamID, userID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.RemoveModerator(ctx, streamID, userID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_IsModerator(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	rows := sqlmock.NewRows([]string{"is_mod"}).AddRow(true)

	mock.ExpectQuery("SELECT is_chat_moderator").
		WithArgs(streamID, userID).
		WillReturnRows(rows)

	isMod, err := repo.IsModerator(ctx, streamID, userID)
	assert.NoError(t, err)
	assert.True(t, isMod)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_IsModerator_NotModerator(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	rows := sqlmock.NewRows([]string{"is_mod"}).AddRow(false)

	mock.ExpectQuery("SELECT is_chat_moderator").
		WithArgs(streamID, userID).
		WillReturnRows(rows)

	isMod, err := repo.IsModerator(ctx, streamID, userID)
	assert.NoError(t, err)
	assert.False(t, isMod)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetModerators(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()

	rows := sqlmock.NewRows([]string{
		"id", "stream_id", "user_id", "granted_by", "created_at",
	}).
		AddRow(uuid.New(), streamID, uuid.New(), uuid.New(), time.Now()).
		AddRow(uuid.New(), streamID, uuid.New(), uuid.New(), time.Now())

	mock.ExpectQuery("SELECT (.+) FROM chat_moderators").
		WithArgs(streamID).
		WillReturnRows(rows)

	mods, err := repo.GetModerators(ctx, streamID)
	assert.NoError(t, err)
	assert.Len(t, mods, 2)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_BanUser(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	ban := domain.NewChatBan(uuid.New(), uuid.New(), uuid.New(), "spam", 10*time.Minute)

	mock.ExpectExec("INSERT INTO chat_bans").
		WithArgs(
			ban.ID,
			ban.StreamID,
			ban.UserID,
			ban.BannedBy,
			ban.Reason,
			ban.ExpiresAt,
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.BanUser(ctx, ban)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_BanUser_Permanent(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	ban := domain.NewPermanentBan(uuid.New(), uuid.New(), uuid.New(), "serious violation")

	mock.ExpectExec("INSERT INTO chat_bans").
		WithArgs(
			ban.ID,
			ban.StreamID,
			ban.UserID,
			ban.BannedBy,
			ban.Reason,
			nil,
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.BanUser(ctx, ban)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_UnbanUser(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	mock.ExpectExec("DELETE FROM chat_bans").
		WithArgs(streamID, userID).
		WillReturnResult(sqlmock.NewResult(0, 1))

	err := repo.UnbanUser(ctx, streamID, userID)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_UnbanUser_NotFound(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	mock.ExpectExec("DELETE FROM chat_bans").
		WithArgs(streamID, userID).
		WillReturnResult(sqlmock.NewResult(0, 0))

	err := repo.UnbanUser(ctx, streamID, userID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_IsUserBanned(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	rows := sqlmock.NewRows([]string{"is_banned"}).AddRow(true)

	mock.ExpectQuery("SELECT is_user_banned").
		WithArgs(streamID, userID).
		WillReturnRows(rows)

	isBanned, err := repo.IsUserBanned(ctx, streamID, userID)
	assert.NoError(t, err)
	assert.True(t, isBanned)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_IsUserBanned_NotBanned(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	rows := sqlmock.NewRows([]string{"is_banned"}).AddRow(false)

	mock.ExpectQuery("SELECT is_user_banned").
		WithArgs(streamID, userID).
		WillReturnRows(rows)

	isBanned, err := repo.IsUserBanned(ctx, streamID, userID)
	assert.NoError(t, err)
	assert.False(t, isBanned)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetBans(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	expiresAt := time.Now().Add(1 * time.Hour)

	rows := sqlmock.NewRows([]string{
		"id", "stream_id", "user_id", "banned_by", "reason", "expires_at", "created_at",
	}).
		AddRow(uuid.New(), streamID, uuid.New(), uuid.New(), "spam", expiresAt, time.Now()).
		AddRow(uuid.New(), streamID, uuid.New(), uuid.New(), "abuse", nil, time.Now())

	mock.ExpectQuery("SELECT (.+) FROM chat_bans").
		WithArgs(streamID).
		WillReturnRows(rows)

	bans, err := repo.GetBans(ctx, streamID)
	assert.NoError(t, err)
	assert.Len(t, bans, 2)
	assert.NotNil(t, bans[0].ExpiresAt)
	assert.Nil(t, bans[1].ExpiresAt)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetBanByID(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	banID := uuid.New()

	rows := sqlmock.NewRows([]string{
		"id", "stream_id", "user_id", "banned_by", "reason", "expires_at", "created_at",
	}).
		AddRow(banID, uuid.New(), uuid.New(), uuid.New(), "spam", nil, time.Now())

	mock.ExpectQuery("SELECT (.+) FROM chat_bans WHERE id").
		WithArgs(banID).
		WillReturnRows(rows)

	ban, err := repo.GetBanByID(ctx, banID)
	assert.NoError(t, err)
	assert.Equal(t, banID, ban.ID)
	assert.Equal(t, "spam", ban.Reason)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetBanByID_NotFound(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	banID := uuid.New()

	mock.ExpectQuery("SELECT (.+) FROM chat_bans WHERE id").
		WithArgs(banID).
		WillReturnError(sql.ErrNoRows)

	ban, err := repo.GetBanByID(ctx, banID)
	assert.ErrorIs(t, err, domain.ErrNotFound)
	assert.Nil(t, ban)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_CleanupExpiredBans(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()

	rows := sqlmock.NewRows([]string{"cleanup_expired_bans"}).AddRow(5)

	mock.ExpectQuery("SELECT cleanup_expired_bans").
		WillReturnRows(rows)

	count, err := repo.CleanupExpiredBans(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 5, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_CleanupExpiredBans_None(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()

	rows := sqlmock.NewRows([]string{"cleanup_expired_bans"}).AddRow(0)

	mock.ExpectQuery("SELECT cleanup_expired_bans").
		WillReturnRows(rows)

	count, err := repo.CleanupExpiredBans(ctx)
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetStreamStats(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	lastMessageAt := time.Now()

	rows := sqlmock.NewRows([]string{
		"stream_id", "unique_chatters", "message_count", "moderation_actions",
		"last_message_at", "moderator_count", "active_ban_count",
	}).
		AddRow(streamID, 100, 500, 10, lastMessageAt, 5, 2)

	mock.ExpectQuery("SELECT (.+) FROM chat_stream_stats").
		WithArgs(streamID).
		WillReturnRows(rows)

	stats, err := repo.GetStreamStats(ctx, streamID)
	assert.NoError(t, err)
	assert.Equal(t, streamID, stats.StreamID)
	assert.Equal(t, 100, stats.UniqueChatters)
	assert.Equal(t, 500, stats.MessageCount)
	assert.Equal(t, 10, stats.ModerationActions)
	assert.Equal(t, 5, stats.ModeratorCount)
	assert.Equal(t, 2, stats.ActiveBanCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetStreamStats_NotFound(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()

	mock.ExpectQuery("SELECT (.+) FROM chat_stream_stats").
		WithArgs(streamID).
		WillReturnError(sql.ErrNoRows)

	stats, err := repo.GetStreamStats(ctx, streamID)
	assert.NoError(t, err, "should return empty stats, not error")
	require.NotNil(t, stats)
	assert.Equal(t, streamID, stats.StreamID)
	assert.Equal(t, 0, stats.UniqueChatters)
	assert.Equal(t, 0, stats.MessageCount)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessageCount(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()

	rows := sqlmock.NewRows([]string{"count"}).AddRow(42)

	mock.ExpectQuery("SELECT get_chat_message_count").
		WithArgs(streamID).
		WillReturnRows(rows)

	count, err := repo.GetMessageCount(ctx, streamID)
	assert.NoError(t, err)
	assert.Equal(t, 42, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessageCount_Zero(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()

	rows := sqlmock.NewRows([]string{"count"}).AddRow(0)

	mock.ExpectQuery("SELECT get_chat_message_count").
		WithArgs(streamID).
		WillReturnRows(rows)

	count, err := repo.GetMessageCount(ctx, streamID)
	assert.NoError(t, err)
	assert.Equal(t, 0, count)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessageCount_DatabaseError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()

	mock.ExpectQuery("SELECT get_chat_message_count").
		WithArgs(streamID).
		WillReturnError(sql.ErrConnDone)

	count, err := repo.GetMessageCount(ctx, streamID)
	assert.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "failed to get message count")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_CreateMessage_WithMetadata(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	msg := domain.NewChatMessage(
		uuid.New(),
		uuid.New(),
		"testuser",
		"Hello",
	)
	msg.Metadata = map[string]interface{}{
		"ip":        "127.0.0.1",
		"client":    "web",
		"timestamp": time.Now().Unix(),
	}

	metadataJSON, _ := json.Marshal(msg.Metadata)

	mock.ExpectExec("INSERT INTO chat_messages").
		WithArgs(
			msg.ID,
			msg.StreamID,
			msg.UserID,
			msg.Username,
			msg.Message,
			msg.Type,
			metadataJSON,
			msg.Deleted,
			sqlmock.AnyArg(),
		).
		WillReturnResult(sqlmock.NewResult(1, 1))

	err := repo.CreateMessage(ctx, msg)
	assert.NoError(t, err)
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_BanUser_ValidationError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewChatRepository(sqlxDB)
	ctx := context.Background()

	ban := &domain.ChatBan{
		ID:       uuid.New(),
		StreamID: uuid.Nil,
		UserID:   uuid.New(),
	}

	err = repo.BanUser(ctx, ban)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid ban")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_BanUser_DatabaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewChatRepository(sqlxDB)
	ctx := context.Background()

	ban := &domain.ChatBan{
		ID:        uuid.New(),
		StreamID:  uuid.New(),
		UserID:    uuid.New(),
		BannedBy:  uuid.New(),
		Reason:    "spam",
		ExpiresAt: nil,
		CreatedAt: time.Now(),
	}

	mock.ExpectExec("INSERT INTO chat_bans").
		WithArgs(ban.ID, ban.StreamID, ban.UserID, ban.BannedBy, ban.Reason, ban.ExpiresAt, sqlmock.AnyArg()).
		WillReturnError(sql.ErrConnDone)

	err = repo.BanUser(ctx, ban)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to ban user")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_UnbanUser_DatabaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewChatRepository(sqlxDB)
	ctx := context.Background()

	streamID := uuid.New()
	userID := uuid.New()

	mock.ExpectExec("DELETE FROM chat_bans WHERE stream_id").
		WithArgs(streamID, userID).
		WillReturnError(sql.ErrConnDone)

	err = repo.UnbanUser(ctx, streamID, userID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unban user")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_DeleteMessage_DatabaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewChatRepository(sqlxDB)
	ctx := context.Background()

	messageID := uuid.New()

	mock.ExpectExec("UPDATE chat_messages SET deleted").
		WithArgs(messageID).
		WillReturnError(sql.ErrConnDone)

	err = repo.DeleteMessage(ctx, messageID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to delete message")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_RemoveModerator_DatabaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewChatRepository(sqlxDB)
	ctx := context.Background()

	streamID := uuid.New()
	userID := uuid.New()

	mock.ExpectExec("DELETE FROM chat_moderators WHERE stream_id").
		WithArgs(streamID, userID).
		WillReturnError(sql.ErrConnDone)

	err = repo.RemoveModerator(ctx, streamID, userID)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to remove moderator")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetModerators_DatabaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	sqlxDB := sqlx.NewDb(db, "postgres")
	repo := NewChatRepository(sqlxDB)
	ctx := context.Background()

	streamID := uuid.New()

	mock.ExpectQuery("SELECT id, stream_id, user_id, granted_by, created_at FROM chat_moderators").
		WithArgs(streamID).
		WillReturnError(sql.ErrConnDone)

	mods, err := repo.GetModerators(ctx, streamID)
	assert.Error(t, err)
	assert.Nil(t, mods)
	assert.Contains(t, err.Error(), "failed to get moderators")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessages_DatabaseError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()

	mock.ExpectQuery(`SELECT (.+) FROM chat_messages WHERE stream_id`).
		WithArgs(streamID, 50, 0).
		WillReturnError(sql.ErrConnDone)

	messages, err := repo.GetMessages(ctx, streamID, 50, 0)
	assert.Error(t, err)
	assert.Nil(t, messages)
	assert.Contains(t, err.Error(), "failed to get chat messages")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessagesSince_DatabaseError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	since := time.Now().Add(-1 * time.Hour)

	mock.ExpectQuery(`SELECT (.+) FROM chat_messages WHERE stream_id = (.+) AND created_at`).
		WithArgs(streamID, since).
		WillReturnError(sql.ErrConnDone)

	messages, err := repo.GetMessagesSince(ctx, streamID, since)
	assert.Error(t, err)
	assert.Nil(t, messages)
	assert.Contains(t, err.Error(), "failed to get messages since")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetMessageByID_DatabaseError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	messageID := uuid.New()

	mock.ExpectQuery(`SELECT (.+) FROM chat_messages WHERE id`).
		WithArgs(messageID).
		WillReturnError(sql.ErrConnDone)

	message, err := repo.GetMessageByID(ctx, messageID)
	assert.Error(t, err)
	assert.Nil(t, message)
	assert.Contains(t, err.Error(), "failed to get message")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_AddModerator_DatabaseError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	mod := domain.NewChatModerator(uuid.New(), uuid.New(), uuid.New())

	mock.ExpectExec(`INSERT INTO chat_moderators`).
		WillReturnError(sql.ErrConnDone)

	err := repo.AddModerator(ctx, mod)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to add moderator")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_IsModerator_DatabaseError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	mock.ExpectQuery(`SELECT is_chat_moderator`).
		WithArgs(streamID, userID).
		WillReturnError(sql.ErrConnDone)

	isMod, err := repo.IsModerator(ctx, streamID, userID)
	assert.Error(t, err)
	assert.False(t, isMod)
	assert.Contains(t, err.Error(), "failed to check moderator status")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_IsUserBanned_DatabaseError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()
	userID := uuid.New()

	mock.ExpectQuery(`SELECT is_user_banned`).
		WithArgs(streamID, userID).
		WillReturnError(sql.ErrConnDone)

	isBanned, err := repo.IsUserBanned(ctx, streamID, userID)
	assert.Error(t, err)
	assert.False(t, isBanned)
	assert.Contains(t, err.Error(), "failed to check ban status")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_GetBans_DatabaseError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()
	streamID := uuid.New()

	mock.ExpectQuery(`SELECT .* FROM chat_bans WHERE stream_id`).
		WithArgs(streamID).
		WillReturnError(sql.ErrConnDone)

	bans, err := repo.GetBans(ctx, streamID)
	assert.Error(t, err)
	assert.Nil(t, bans)
	assert.Contains(t, err.Error(), "failed to get bans")
	assert.NoError(t, mock.ExpectationsWereMet())
}

func TestChatRepository_CleanupExpiredBans_DatabaseError(t *testing.T) {
	repo, mock, cleanup := setupChatRepoTest(t)
	defer cleanup()

	ctx := context.Background()

	mock.ExpectQuery(`SELECT cleanup_expired_bans`).
		WillReturnError(sql.ErrConnDone)

	count, err := repo.CleanupExpiredBans(ctx)
	assert.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "failed to cleanup expired bans")
	assert.NoError(t, mock.ExpectationsWereMet())
}
