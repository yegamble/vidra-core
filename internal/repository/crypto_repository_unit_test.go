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

func setupCryptoMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()

	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)

	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newCryptoRepo(t *testing.T) (*CryptoRepository, sqlmock.Sqlmock, func()) {
	t.Helper()

	db, mock := setupCryptoMockDB(t)
	repo := NewCryptoRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func TestCryptoRepository_Unit_ConversationKey_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("success without tx", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		key := &domain.ConversationKey{
			ID:                  uuid.NewString(),
			ConversationID:      uuid.NewString(),
			UserID:              uuid.NewString(),
			EncryptedPrivateKey: "enc_priv",
			PublicKey:           "pub_key",
			KeyVersion:          1,
			IsActive:            true,
		}

		mock.ExpectExec(`(?s)INSERT INTO conversation_keys`).
			WithArgs(key.ID, key.ConversationID, key.UserID, "enc_priv", "pub_key", nil, 1, true, sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateConversationKey(ctx, nil, key)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		key := &domain.ConversationKey{ID: uuid.NewString()}

		mock.ExpectExec(`(?s)INSERT INTO conversation_keys`).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateConversationKey(ctx, nil, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create conversation key")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_ConversationKey_Get(t *testing.T) {
	ctx := context.Background()
	conversationID := uuid.NewString()
	userID := uuid.NewString()
	keyVersion := 1
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "conversation_id", "user_id", "encrypted_private_key", "public_key",
			"encrypted_shared_secret", "key_version", "is_active", "created_at", "expires_at",
		}).AddRow(
			uuid.NewString(), conversationID, userID, "enc_priv", "pub_key", nil, keyVersion, true, now, nil,
		)

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, user_id`).
			WithArgs(conversationID, userID, keyVersion).
			WillReturnRows(rows)

		key, err := repo.GetConversationKey(ctx, conversationID, userID, keyVersion)
		require.NoError(t, err)
		require.NotNil(t, key)
		assert.Equal(t, conversationID, key.ConversationID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, user_id`).
			WithArgs(conversationID, userID, keyVersion).
			WillReturnError(sql.ErrNoRows)

		key, err := repo.GetConversationKey(ctx, conversationID, userID, keyVersion)
		require.Nil(t, key)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, user_id`).
			WithArgs(conversationID, userID, keyVersion).
			WillReturnError(errors.New("query failed"))

		key, err := repo.GetConversationKey(ctx, conversationID, userID, keyVersion)
		require.Nil(t, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get conversation key")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_ConversationKey_GetActive(t *testing.T) {
	ctx := context.Background()
	conversationID := uuid.NewString()
	userID := uuid.NewString()
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "conversation_id", "user_id", "encrypted_private_key", "public_key",
			"encrypted_shared_secret", "key_version", "is_active", "created_at", "expires_at",
		}).AddRow(
			uuid.NewString(), conversationID, userID, "enc_priv", "pub_key", nil, 1, true, now, nil,
		)

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, user_id.*WHERE conversation_id.*AND user_id.*AND is_active = true`).
			WithArgs(conversationID, userID).
			WillReturnRows(rows)

		key, err := repo.GetActiveConversationKey(ctx, conversationID, userID)
		require.NoError(t, err)
		require.NotNil(t, key)
		assert.True(t, key.IsActive)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, user_id.*WHERE conversation_id.*AND user_id.*AND is_active = true`).
			WithArgs(conversationID, userID).
			WillReturnError(sql.ErrNoRows)

		key, err := repo.GetActiveConversationKey(ctx, conversationID, userID)
		require.Nil(t, key)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_ConversationKey_List(t *testing.T) {
	ctx := context.Background()
	conversationID := uuid.NewString()
	now := time.Now()

	t.Run("success with multiple keys", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "conversation_id", "user_id", "encrypted_private_key", "public_key",
			"encrypted_shared_secret", "key_version", "is_active", "created_at", "expires_at",
		}).AddRow(
			uuid.NewString(), conversationID, uuid.NewString(), "enc1", "pub1", nil, 1, true, now, nil,
		).AddRow(
			uuid.NewString(), conversationID, uuid.NewString(), "enc2", "pub2", nil, 1, false, now, nil,
		)

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, user_id.*WHERE conversation_id`).
			WithArgs(conversationID).
			WillReturnRows(rows)

		keys, err := repo.ListConversationKeys(ctx, conversationID)
		require.NoError(t, err)
		require.Len(t, keys, 2)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, user_id.*WHERE conversation_id`).
			WithArgs(conversationID).
			WillReturnError(errors.New("query failed"))

		keys, err := repo.ListConversationKeys(ctx, conversationID)
		require.Nil(t, keys)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to list conversation keys")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_ConversationKey_UpdateAndDeactivate(t *testing.T) {
	ctx := context.Background()
	conversationID := uuid.NewString()

	t.Run("update success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		sharedSecret := "shared_secret"
		expiresAt := time.Now().Add(1 * time.Hour)
		key := &domain.ConversationKey{
			ID:                    uuid.NewString(),
			ConversationID:        conversationID,
			UserID:                uuid.NewString(),
			EncryptedPrivateKey:   "new_enc",
			PublicKey:             "new_pub",
			EncryptedSharedSecret: &sharedSecret,
			KeyVersion:            2,
			IsActive:              true,
			ExpiresAt:             &expiresAt,
		}

		mock.ExpectExec(`(?s)UPDATE conversation_keys\s+SET encrypted_shared_secret`).
			WithArgs(&sharedSecret, true, &expiresAt, key.ID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateConversationKey(ctx, nil, key)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		key := &domain.ConversationKey{
			ID:        uuid.NewString(),
			IsActive:  false,
			ExpiresAt: nil,
		}

		mock.ExpectExec(`(?s)UPDATE conversation_keys\s+SET encrypted_shared_secret`).
			WillReturnError(errors.New("update failed"))

		err := repo.UpdateConversationKey(ctx, nil, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update conversation key")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("deactivate keys success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE conversation_keys\s+SET is_active = false`).
			WithArgs(conversationID, 2).
			WillReturnResult(sqlmock.NewResult(0, 3))

		err := repo.DeactivateConversationKeys(ctx, nil, conversationID, 2)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("deactivate failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)UPDATE conversation_keys\s+SET is_active = false`).
			WithArgs(conversationID, 2).
			WillReturnError(errors.New("update failed"))

		err := repo.DeactivateConversationKeys(ctx, nil, conversationID, 2)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to deactivate conversation keys")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_KeyExchangeMessage_Create(t *testing.T) {
	ctx := context.Background()

	t.Run("success without tx", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		msg := &domain.KeyExchangeMessage{
			ID:             uuid.NewString(),
			SenderID:       uuid.NewString(),
			RecipientID:    uuid.NewString(),
			ConversationID: uuid.NewString(),
			ExchangeType:   "dh_key_exchange",
			PublicKey:      "pub_key",
			Signature:      "sig",
			Nonce:          "nonce",
		}

		mock.ExpectExec(`(?s)INSERT INTO key_exchange_messages`).
			WithArgs(msg.ID, msg.ConversationID, msg.SenderID, msg.RecipientID, "dh_key_exchange", "pub_key", "sig", "nonce", sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateKeyExchangeMessage(ctx, nil, msg)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("insert failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		msg := &domain.KeyExchangeMessage{ID: uuid.NewString()}

		mock.ExpectExec(`(?s)INSERT INTO key_exchange_messages`).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateKeyExchangeMessage(ctx, nil, msg)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create key exchange message")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_KeyExchangeMessage_Get(t *testing.T) {
	ctx := context.Background()
	messageID := uuid.NewString()
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "conversation_id", "sender_id", "recipient_id", "exchange_type", "public_key", "signature", "nonce", "created_at", "expires_at",
		}).AddRow(messageID, uuid.NewString(), uuid.NewString(), uuid.NewString(), "dh_key_exchange", "pub_key", "sig", "nonce", now, now.Add(1*time.Hour))

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, sender_id, recipient_id, exchange_type`).
			WithArgs(messageID).
			WillReturnRows(rows)

		msg, err := repo.GetKeyExchangeMessage(ctx, messageID)
		require.NoError(t, err)
		require.NotNil(t, msg)
		assert.Equal(t, messageID, msg.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, sender_id, recipient_id, exchange_type`).
			WithArgs(messageID).
			WillReturnError(sql.ErrNoRows)

		msg, err := repo.GetKeyExchangeMessage(ctx, messageID)
		require.Nil(t, msg)
		require.ErrorIs(t, err, domain.ErrNotFound)
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_KeyExchangeMessage_GetPending(t *testing.T) {
	ctx := context.Background()
	userID := uuid.NewString()
	now := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "conversation_id", "sender_id", "recipient_id", "exchange_type", "public_key", "signature", "nonce", "created_at", "expires_at",
		}).AddRow(uuid.NewString(), uuid.NewString(), uuid.NewString(), userID, "dh_key_exchange", "pub_key", "sig", "nonce", now, now.Add(1*time.Hour))

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, sender_id, recipient_id, exchange_type.*WHERE recipient_id`).
			WithArgs(userID).
			WillReturnRows(rows)

		msgs, err := repo.GetPendingKeyExchanges(ctx, userID)
		require.NoError(t, err)
		require.Len(t, msgs, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT id, conversation_id, sender_id, recipient_id, exchange_type.*WHERE recipient_id`).
			WithArgs(userID).
			WillReturnError(errors.New("query failed"))

		msgs, err := repo.GetPendingKeyExchanges(ctx, userID)
		require.Nil(t, msgs)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get pending key exchanges")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_KeyExchangeMessage_Delete(t *testing.T) {
	ctx := context.Background()
	messageID := uuid.NewString()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM key_exchange_messages WHERE id = $1`)).
			WithArgs(messageID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteKeyExchangeMessage(ctx, nil, messageID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM key_exchange_messages WHERE id = $1`)).
			WithArgs(messageID).
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteKeyExchangeMessage(ctx, nil, messageID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete key exchange message")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_KeyExchangeMessage_Cleanup(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM key_exchange_messages WHERE expires_at <= NOW()`)).
			WillReturnResult(sqlmock.NewResult(0, 42))

		count, err := repo.CleanupExpiredKeyExchanges(ctx)
		require.NoError(t, err)
		assert.Equal(t, int64(42), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM key_exchange_messages WHERE expires_at <= NOW()`)).
			WillReturnError(errors.New("delete failed"))

		count, err := repo.CleanupExpiredKeyExchanges(ctx)
		assert.Equal(t, int64(0), count)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to cleanup expired key exchanges")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_UserSigningKey_CRUD(t *testing.T) {
	ctx := context.Background()
	userID := uuid.NewString()

	t.Run("create success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		encPriv := "enc_priv"
		identityKey := "identity_pub"
		key := &domain.UserSigningKey{
			UserID:              userID,
			EncryptedPrivateKey: &encPriv,
			PublicKey:           "pub_key",
			PublicIdentityKey:   &identityKey,
			KeyVersion:          1,
		}

		mock.ExpectExec(`(?s)INSERT INTO user_signing_keys`).
			WithArgs(userID, "enc_priv", "pub_key", &identityKey, 1).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateUserSigningKey(ctx, nil, key)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("create failure", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		key := &domain.UserSigningKey{UserID: userID}

		mock.ExpectExec(`(?s)INSERT INTO user_signing_keys`).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateUserSigningKey(ctx, nil, key)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create user signing key")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		identityKey := "identity_pub_key"
		rows := sqlmock.NewRows([]string{
			"user_id", "encrypted_private_key", "public_key", "public_identity_key", "key_version", "created_at",
		}).AddRow(userID, "enc_priv", "pub_key", &identityKey, 1, time.Now())

		mock.ExpectQuery(`(?s)SELECT user_id, encrypted_private_key, public_key, public_identity_key, key_version, created_at`).
			WithArgs(userID).
			WillReturnRows(rows)

		key, err := repo.GetUserSigningKey(ctx, userID)
		require.NoError(t, err)
		require.NotNil(t, key)
		assert.Equal(t, userID, key.UserID)
		assert.Equal(t, "identity_pub_key", *key.PublicIdentityKey)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get not found", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT user_id, encrypted_private_key, public_key, public_identity_key, key_version, created_at`).
			WithArgs(userID).
			WillReturnError(sql.ErrNoRows)

		key, err := repo.GetUserSigningKey(ctx, userID)
		require.ErrorIs(t, err, domain.ErrNotFound)
		assert.Nil(t, key)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get database error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT user_id, encrypted_private_key, public_key, public_identity_key, key_version, created_at`).
			WithArgs(userID).
			WillReturnError(sql.ErrConnDone)

		key, err := repo.GetUserSigningKey(ctx, userID)
		require.Error(t, err)
		assert.Nil(t, key)
		assert.Contains(t, err.Error(), "failed to get user signing key")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get public key success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT public_key FROM user_signing_keys WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnRows(sqlmock.NewRows([]string{"public_key"}).AddRow("pub_key"))

		pubKey, err := repo.GetUserPublicSigningKey(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, "pub_key", pubKey)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get public key not found", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT public_key FROM user_signing_keys WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnError(sql.ErrNoRows)

		pubKey, err := repo.GetUserPublicSigningKey(ctx, userID)
		require.NoError(t, err)
		assert.Equal(t, "", pubKey)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get public key database error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT public_key FROM user_signing_keys WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnError(sql.ErrConnDone)

		pubKey, err := repo.GetUserPublicSigningKey(ctx, userID)
		require.Error(t, err)
		assert.Equal(t, "", pubKey)
		assert.Contains(t, err.Error(), "failed to get user public signing key")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update success - without tx", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		newEncPriv := "new_enc_priv"
		newIdentity := "new_identity_key"
		key := &domain.UserSigningKey{
			UserID:              userID,
			EncryptedPrivateKey: &newEncPriv,
			PublicKey:           "new_pub_key",
			PublicIdentityKey:   &newIdentity,
			KeyVersion:          2,
		}

		mock.ExpectExec(`(?s)UPDATE user_signing_keys`).
			WithArgs("new_enc_priv", "new_pub_key", &newIdentity, 2, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateUserSigningKey(ctx, nil, key)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update success - with tx", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		newEncPriv := "new_enc_priv"
		newIdentity := "new_identity_key"
		key := &domain.UserSigningKey{
			UserID:              userID,
			EncryptedPrivateKey: &newEncPriv,
			PublicKey:           "new_pub_key",
			PublicIdentityKey:   &newIdentity,
			KeyVersion:          2,
		}

		mock.ExpectBegin()
		tx, _ := repo.db.BeginTxx(ctx, nil)

		mock.ExpectExec(`(?s)UPDATE user_signing_keys`).
			WithArgs("new_enc_priv", "new_pub_key", &newIdentity, 2, userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateUserSigningKey(ctx, tx, key)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("update error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		newEncPriv := "new_enc_priv"
		newIdentity := "new_identity_key"
		key := &domain.UserSigningKey{
			UserID:              userID,
			EncryptedPrivateKey: &newEncPriv,
			PublicKey:           "new_pub_key",
			PublicIdentityKey:   &newIdentity,
			KeyVersion:          2,
		}

		mock.ExpectExec(`(?s)UPDATE user_signing_keys`).
			WithArgs("new_enc_priv", "new_pub_key", &newIdentity, 2, userID).
			WillReturnError(sql.ErrConnDone)

		err := repo.UpdateUserSigningKey(ctx, nil, key)
		require.Error(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM user_signing_keys WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.DeleteUserSigningKey(ctx, nil, userID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("delete error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM user_signing_keys WHERE user_id = $1`)).
			WithArgs(userID).
			WillReturnError(errors.New("delete failed"))

		err := repo.DeleteUserSigningKey(ctx, nil, userID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to delete user signing key")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_AuditLog(t *testing.T) {
	ctx := context.Background()
	userID := uuid.NewString()

	t.Run("create audit log success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		clientIP := "192.168.1.1"
		userAgent := "test-agent"
		auditLog := &domain.CryptoAuditLog{
			ID:             uuid.NewString(),
			UserID:         userID,
			Operation:      "key_created",
			Success:        true,
			ConversationID: nil,
			ClientIP:       &clientIP,
			UserAgent:      &userAgent,
		}

		mock.ExpectExec(`(?s)INSERT INTO crypto_audit_log`).
			WithArgs(auditLog.ID, userID, nil, "key_created", true, nil, &clientIP, &userAgent).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateAuditLog(ctx, auditLog)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("create audit log error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		auditLog := &domain.CryptoAuditLog{
			ID:        uuid.NewString(),
			UserID:    userID,
			Operation: "key_created",
			Success:   true,
		}

		mock.ExpectExec(`(?s)INSERT INTO crypto_audit_log`).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateAuditLog(ctx, auditLog)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to create audit log")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get audit logs success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		now := time.Now()
		clientIP := "192.168.1.1"
		userAgent := "agent"
		rows := sqlmock.NewRows([]string{
			"id", "user_id", "conversation_id", "operation", "success", "error_message", "client_ip", "user_agent", "created_at",
		}).AddRow(uuid.NewString(), userID, nil, "key_created", true, nil, &clientIP, &userAgent, now)

		mock.ExpectQuery(`(?s)SELECT id, user_id, conversation_id, operation, success.*WHERE user_id.*ORDER BY created_at DESC`).
			WithArgs(userID, 10, 0).
			WillReturnRows(rows)

		logs, err := repo.GetAuditLogs(ctx, userID, 10, 0)
		require.NoError(t, err)
		require.Len(t, logs, 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get audit logs by conversation success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		conversationID := uuid.NewString()
		now := time.Now()
		clientIP := "10.0.0.1"
		userAgent := "browser"
		rows := sqlmock.NewRows([]string{
			"id", "user_id", "conversation_id", "operation", "success", "error_message", "client_ip", "user_agent", "created_at",
		}).
			AddRow(uuid.NewString(), userID, &conversationID, "encrypt", true, nil, &clientIP, &userAgent, now).
			AddRow(uuid.NewString(), userID, &conversationID, "decrypt", true, nil, &clientIP, &userAgent, now)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, conversation_id, operation, success, error_message, client_ip, user_agent, created_at FROM crypto_audit_log WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`)).
			WithArgs(conversationID, 20, 0).
			WillReturnRows(rows)

		logs, err := repo.GetAuditLogsByConversation(ctx, conversationID, 20, 0)
		require.NoError(t, err)
		require.Len(t, logs, 2)
		assert.Equal(t, conversationID, *logs[0].ConversationID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get audit logs by conversation - empty result", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		conversationID := uuid.NewString()
		rows := sqlmock.NewRows([]string{
			"id", "user_id", "conversation_id", "operation", "success", "error_message", "client_ip", "user_agent", "created_at",
		})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, conversation_id, operation, success, error_message, client_ip, user_agent, created_at FROM crypto_audit_log WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`)).
			WithArgs(conversationID, 10, 0).
			WillReturnRows(rows)

		logs, err := repo.GetAuditLogsByConversation(ctx, conversationID, 10, 0)
		require.NoError(t, err)
		require.Len(t, logs, 0)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("get audit logs by conversation - database error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		conversationID := uuid.NewString()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, user_id, conversation_id, operation, success, error_message, client_ip, user_agent, created_at FROM crypto_audit_log WHERE conversation_id = $1 ORDER BY created_at DESC LIMIT $2 OFFSET $3`)).
			WithArgs(conversationID, 10, 0).
			WillReturnError(sql.ErrConnDone)

		logs, err := repo.GetAuditLogsByConversation(ctx, conversationID, 10, 0)
		require.Error(t, err)
		require.Nil(t, logs)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cleanup old audit logs success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)DELETE FROM crypto_audit_log WHERE created_at < NOW\(\) - \$1::INTERVAL`).
			WithArgs((30 * 24 * time.Hour).String()).
			WillReturnResult(sqlmock.NewResult(0, 100))

		count, err := repo.CleanupOldAuditLogs(ctx, 30*24*time.Hour)
		require.NoError(t, err)
		assert.Equal(t, int64(100), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cleanup old audit logs exec error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)DELETE FROM crypto_audit_log WHERE created_at < NOW\(\) - \$1::INTERVAL`).
			WithArgs((30 * 24 * time.Hour).String()).
			WillReturnError(errors.New("delete failed"))

		count, err := repo.CleanupOldAuditLogs(ctx, 30*24*time.Hour)
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		assert.Contains(t, err.Error(), "failed to cleanup old audit logs")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("cleanup old audit logs rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)DELETE FROM crypto_audit_log WHERE created_at < NOW\(\) - \$1::INTERVAL`).
			WithArgs((30 * 24 * time.Hour).String()).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected failed")))

		count, err := repo.CleanupOldAuditLogs(ctx, 30*24*time.Hour)
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		assert.Contains(t, err.Error(), "failed to get rows affected")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestCryptoRepository_Unit_WithTransaction(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectCommit()

		err := repo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
			return nil
		})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rollback on error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectBegin()
		mock.ExpectRollback()

		err := repo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
			return errors.New("test error")
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "test error")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("begin error", func(t *testing.T) {
		repo, mock, cleanup := newCryptoRepo(t)
		defer cleanup()

		mock.ExpectBegin().WillReturnError(errors.New("begin failed"))

		err := repo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
			return nil
		})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to begin transaction")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
