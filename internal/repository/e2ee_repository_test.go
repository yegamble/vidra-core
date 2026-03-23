package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestE2EEMessageRepository_CreateEncryptedMessage(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupMessageMockDB(t)
		defer func() { _ = db.Close() }()
		repo := NewE2EEMessageRepository(db)

		encContent := "encrypted-blob"
		msg := &domain.Message{
			ID:               uuid.NewString(),
			SenderID:         uuid.NewString(),
			RecipientID:      uuid.NewString(),
			EncryptedContent: &encContent,
			IsEncrypted:      true,
			MessageType:      domain.MessageTypeSecure,
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}

		mock.ExpectBegin()
		mock.ExpectExec(`(?s)INSERT INTO messages`).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectExec(`(?s)INSERT INTO conversations`).
			WillReturnResult(sqlmock.NewResult(1, 1))
		mock.ExpectCommit()

		err := repo.CreateEncryptedMessage(ctx, msg)
		require.NoError(t, err)
	})

	t.Run("insert failure", func(t *testing.T) {
		db, mock := setupMessageMockDB(t)
		defer func() { _ = db.Close() }()
		repo := NewE2EEMessageRepository(db)

		encContent := "encrypted-blob"
		msg := &domain.Message{
			ID:               uuid.NewString(),
			SenderID:         uuid.NewString(),
			RecipientID:      uuid.NewString(),
			EncryptedContent: &encContent,
			IsEncrypted:      true,
			MessageType:      domain.MessageTypeSecure,
			CreatedAt:        time.Now(),
			UpdatedAt:        time.Now(),
		}

		mock.ExpectBegin()
		mock.ExpectExec(`(?s)INSERT INTO messages`).
			WillReturnError(errors.New("db error"))
		mock.ExpectRollback()

		err := repo.CreateEncryptedMessage(ctx, msg)
		require.Error(t, err)
	})
}

func TestE2EEConversationRepository_GetConversation(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupMessageMockDB(t)
		defer func() { _ = db.Close() }()
		repo := NewE2EEConversationRepository(db)

		convID := uuid.NewString()
		now := time.Now()

		rows := sqlmock.NewRows([]string{
			"id", "participant_one_id", "participant_two_id",
			"encryption_status", "last_message_at", "created_at", "updated_at",
		}).AddRow(convID, uuid.NewString(), uuid.NewString(), "active", now, now, now)

		mock.ExpectQuery(`(?s)SELECT.*FROM conversations WHERE id`).
			WithArgs(convID).
			WillReturnRows(rows)

		conv, err := repo.GetConversation(ctx, convID)
		require.NoError(t, err)
		assert.Equal(t, convID, conv.ID)
	})

	t.Run("not found returns ErrNotFound", func(t *testing.T) {
		db, mock := setupMessageMockDB(t)
		defer func() { _ = db.Close() }()
		repo := NewE2EEConversationRepository(db)

		mock.ExpectQuery(`(?s)SELECT.*FROM conversations WHERE id`).
			WillReturnError(sql.ErrNoRows)

		_, err := repo.GetConversation(ctx, uuid.NewString())
		require.ErrorIs(t, err, domain.ErrNotFound)
	})

	t.Run("database error", func(t *testing.T) {
		db, mock := setupMessageMockDB(t)
		defer func() { _ = db.Close() }()
		repo := NewE2EEConversationRepository(db)

		mock.ExpectQuery(`(?s)SELECT.*FROM conversations WHERE id`).
			WillReturnError(errors.New("connection error"))

		_, err := repo.GetConversation(ctx, uuid.NewString())
		require.Error(t, err)
		assert.NotErrorIs(t, err, domain.ErrNotFound)
	})
}

func TestE2EEConversationRepository_UpdateEncryptionStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupMessageMockDB(t)
		defer func() { _ = db.Close() }()
		repo := NewE2EEConversationRepository(db)

		convID := uuid.NewString()

		mock.ExpectExec(`(?s)UPDATE conversations SET encryption_status`).
			WithArgs(domain.EncryptionStatusActive, convID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateEncryptionStatus(ctx, nil, convID, domain.EncryptionStatusActive)
		require.NoError(t, err)
	})
}
