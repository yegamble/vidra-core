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

func setupCollabMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(db, "sqlmock"), mock
}

var collabColumns = []string{
	"id", "channel_id", "user_id", "invited_by", "role", "status",
	"responded_at", "created_at", "updated_at",
	"account_id", "username", "email", "display_name", "bio",
	"bitcoin_wallet", "account_role", "is_active",
	"email_verified", "email_verified_at", "twofa_enabled", "twofa_secret",
	"twofa_confirmed_at", "account_created_at", "account_updated_at",
}

func TestChannelCollaboratorRepository_ListByChannel(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()

	t.Run("success", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		now := time.Now()
		collabID := uuid.New()
		userID := uuid.New()
		invitedBy := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(channelID).
			WillReturnRows(sqlmock.NewRows(collabColumns).AddRow(
				collabID, channelID, userID, invitedBy, "editor", "accepted",
				nil, now, now,
				userID.String(), "testuser", "test@example.com", "Test User", "bio",
				"", "user", true,
				true, nil, false, nil,
				nil, now, now,
			))

		collabs, err := repo.ListByChannel(ctx, channelID)
		require.NoError(t, err)
		assert.Len(t, collabs, 1)
		assert.Equal(t, collabID, collabs[0].ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(channelID).
			WillReturnError(errors.New("query failed"))

		_, err := repo.ListByChannel(ctx, channelID)
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelCollaboratorRepository_GetByChannelAndID(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()
	collabID := uuid.New()

	t.Run("found", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		now := time.Now()
		userID := uuid.New()
		invitedBy := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(channelID, collabID).
			WillReturnRows(sqlmock.NewRows(collabColumns).AddRow(
				collabID, channelID, userID, invitedBy, "editor", "accepted",
				nil, now, now,
				userID.String(), "testuser", "test@example.com", "Test User", "",
				"", "user", true,
				false, nil, false, nil,
				nil, now, now,
			))

		c, err := repo.GetByChannelAndID(ctx, channelID, collabID)
		require.NoError(t, err)
		assert.Equal(t, collabID, c.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(channelID, collabID).
			WillReturnRows(sqlmock.NewRows(collabColumns))

		_, err := repo.GetByChannelAndID(ctx, channelID, collabID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelCollaboratorRepository_GetByChannelAndUser(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()
	userID := uuid.New()

	t.Run("found", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		now := time.Now()
		collabID := uuid.New()
		invitedBy := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(channelID, userID).
			WillReturnRows(sqlmock.NewRows(collabColumns).AddRow(
				collabID, channelID, userID, invitedBy, "editor", "pending",
				nil, now, now,
				userID.String(), "user1", "u@e.com", "User", "",
				"", "user", true,
				false, nil, false, nil,
				nil, now, now,
			))

		c, err := repo.GetByChannelAndUser(ctx, channelID, userID)
		require.NoError(t, err)
		assert.Equal(t, collabID, c.ID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(channelID, userID).
			WillReturnRows(sqlmock.NewRows(collabColumns))

		_, err := repo.GetByChannelAndUser(ctx, channelID, userID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelCollaboratorRepository_UpdateStatus(t *testing.T) {
	ctx := context.Background()
	collabID := uuid.New()

	t.Run("success", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE channel_collaborators`)).
			WithArgs(collabID, domain.ChannelCollaboratorStatus("accepted")).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateStatus(ctx, collabID, "accepted")
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`UPDATE channel_collaborators`)).
			WithArgs(collabID, domain.ChannelCollaboratorStatus("rejected")).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.UpdateStatus(ctx, collabID, "rejected")
		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelCollaboratorRepository_Delete(t *testing.T) {
	ctx := context.Background()
	collabID := uuid.New()

	t.Run("success", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM channel_collaborators`)).
			WithArgs(collabID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.Delete(ctx, collabID)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM channel_collaborators`)).
			WithArgs(collabID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.Delete(ctx, collabID)
		assert.ErrorIs(t, err, domain.ErrNotFound)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestChannelCollaboratorRepository_UpsertInvite(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		collabID := uuid.New()
		channelID := uuid.New()
		userID := uuid.New()
		invitedBy := uuid.New()
		now := time.Now()

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO channel_collaborators`)).
			WillReturnRows(sqlmock.NewRows([]string{"id", "created_at", "updated_at", "responded_at"}).
				AddRow(collabID, now, now, sql.NullTime{}))

		collab := &domain.ChannelCollaborator{
			ID:        collabID,
			ChannelID: channelID,
			UserID:    userID,
			InvitedBy: invitedBy,
			Role:      "editor",
			Status:    "pending",
		}
		err := repo.UpsertInvite(ctx, collab)
		require.NoError(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error", func(t *testing.T) {
		db, mock := setupCollabMockDB(t)
		defer db.Close()
		repo := NewChannelCollaboratorRepository(db)

		mock.ExpectQuery(regexp.QuoteMeta(`INSERT INTO channel_collaborators`)).
			WillReturnError(errors.New("upsert failed"))

		err := repo.UpsertInvite(ctx, &domain.ChannelCollaborator{
			ChannelID: uuid.New(),
			UserID:    uuid.New(),
			InvitedBy: uuid.New(),
			Role:      "editor",
			Status:    "pending",
		})
		require.Error(t, err)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
