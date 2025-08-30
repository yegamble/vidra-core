package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/jmoiron/sqlx"

	"athena/internal/domain"
)

// CryptoRepository handles E2EE cryptographic data persistence
type CryptoRepository struct {
	db *sqlx.DB
}

// NewCryptoRepository creates a new crypto repository
func NewCryptoRepository(db *sqlx.DB) *CryptoRepository {
	return &CryptoRepository{
		db: db,
	}
}

// UserMasterKey operations

// CreateUserMasterKey creates a new user master key
func (r *CryptoRepository) CreateUserMasterKey(ctx context.Context, tx *sqlx.Tx, masterKey *domain.UserMasterKey) error {
	query := `
		INSERT INTO user_master_keys (
			user_id, encrypted_master_key, argon2_salt, argon2_memory, 
			argon2_time, argon2_parallelism, key_version
		) VALUES (
			:user_id, :encrypted_master_key, :argon2_salt, :argon2_memory,
			:argon2_time, :argon2_parallelism, :key_version
		)`

	var err error
	if tx != nil {
		_, err = tx.NamedExecContext(ctx, query, masterKey)
	} else {
		_, err = r.db.NamedExecContext(ctx, query, masterKey)
	}

	if err != nil {
		return fmt.Errorf("failed to create user master key: %w", err)
	}

	return nil
}

// GetUserMasterKey retrieves a user's master key
func (r *CryptoRepository) GetUserMasterKey(ctx context.Context, userID string) (*domain.UserMasterKey, error) {
	var masterKey domain.UserMasterKey

	query := `
		SELECT user_id, encrypted_master_key, argon2_salt, argon2_memory,
			   argon2_time, argon2_parallelism, key_version, created_at, updated_at
		FROM user_master_keys
		WHERE user_id = $1`

	err := r.db.GetContext(ctx, &masterKey, query, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user master key: %w", err)
	}

	return &masterKey, nil
}

// UpdateUserMasterKey updates a user's master key (for key rotation)
func (r *CryptoRepository) UpdateUserMasterKey(ctx context.Context, tx *sqlx.Tx, masterKey *domain.UserMasterKey) error {
	query := `
		UPDATE user_master_keys 
		SET encrypted_master_key = :encrypted_master_key,
			argon2_salt = :argon2_salt,
			key_version = :key_version,
			updated_at = NOW()
		WHERE user_id = :user_id`

	var err error
	if tx != nil {
		_, err = tx.NamedExecContext(ctx, query, masterKey)
	} else {
		_, err = r.db.NamedExecContext(ctx, query, masterKey)
	}

	if err != nil {
		return fmt.Errorf("failed to update user master key: %w", err)
	}

	return nil
}

// DeleteUserMasterKey deletes a user's master key
func (r *CryptoRepository) DeleteUserMasterKey(ctx context.Context, tx *sqlx.Tx, userID string) error {
	query := `DELETE FROM user_master_keys WHERE user_id = $1`

	var err error
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, userID)
	} else {
		_, err = r.db.ExecContext(ctx, query, userID)
	}

	if err != nil {
		return fmt.Errorf("failed to delete user master key: %w", err)
	}

	return nil
}

// ConversationKey operations

// CreateConversationKey creates a new conversation key
func (r *CryptoRepository) CreateConversationKey(ctx context.Context, tx *sqlx.Tx, key *domain.ConversationKey) error {
	query := `
		INSERT INTO conversation_keys (
			id, conversation_id, user_id, encrypted_private_key, public_key,
			encrypted_shared_secret, key_version, is_active, expires_at
		) VALUES (
			:id, :conversation_id, :user_id, :encrypted_private_key, :public_key,
			:encrypted_shared_secret, :key_version, :is_active, :expires_at
		)`

	var err error
	if tx != nil {
		_, err = tx.NamedExecContext(ctx, query, key)
	} else {
		_, err = r.db.NamedExecContext(ctx, query, key)
	}

	if err != nil {
		return fmt.Errorf("failed to create conversation key: %w", err)
	}

	return nil
}

// GetConversationKey retrieves a conversation key for a user
func (r *CryptoRepository) GetConversationKey(ctx context.Context, conversationID, userID string, keyVersion int) (*domain.ConversationKey, error) {
	var key domain.ConversationKey

	query := `
		SELECT id, conversation_id, user_id, encrypted_private_key, public_key,
			   encrypted_shared_secret, key_version, is_active, created_at, expires_at
		FROM conversation_keys
		WHERE conversation_id = $1 AND user_id = $2 AND key_version = $3`

	err := r.db.GetContext(ctx, &key, query, conversationID, userID, keyVersion)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get conversation key: %w", err)
	}

	return &key, nil
}

// GetActiveConversationKey retrieves the active conversation key for a user
func (r *CryptoRepository) GetActiveConversationKey(ctx context.Context, conversationID, userID string) (*domain.ConversationKey, error) {
	var key domain.ConversationKey

	query := `
		SELECT id, conversation_id, user_id, encrypted_private_key, public_key,
			   encrypted_shared_secret, key_version, is_active, created_at, expires_at
		FROM conversation_keys
		WHERE conversation_id = $1 AND user_id = $2 AND is_active = true
		ORDER BY key_version DESC
		LIMIT 1`

	err := r.db.GetContext(ctx, &key, query, conversationID, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get active conversation key: %w", err)
	}

	return &key, nil
}

// ListConversationKeys lists all keys for a conversation
func (r *CryptoRepository) ListConversationKeys(ctx context.Context, conversationID string) ([]*domain.ConversationKey, error) {
	var keys []*domain.ConversationKey

	query := `
		SELECT id, conversation_id, user_id, encrypted_private_key, public_key,
			   encrypted_shared_secret, key_version, is_active, created_at, expires_at
		FROM conversation_keys
		WHERE conversation_id = $1
		ORDER BY user_id, key_version DESC`

	err := r.db.SelectContext(ctx, &keys, query, conversationID)
	if err != nil {
		return nil, fmt.Errorf("failed to list conversation keys: %w", err)
	}

	return keys, nil
}

// UpdateConversationKey updates a conversation key
func (r *CryptoRepository) UpdateConversationKey(ctx context.Context, tx *sqlx.Tx, key *domain.ConversationKey) error {
	query := `
		UPDATE conversation_keys 
		SET encrypted_shared_secret = :encrypted_shared_secret,
			is_active = :is_active,
			expires_at = :expires_at
		WHERE id = :id`

	var err error
	if tx != nil {
		_, err = tx.NamedExecContext(ctx, query, key)
	} else {
		_, err = r.db.NamedExecContext(ctx, query, key)
	}

	if err != nil {
		return fmt.Errorf("failed to update conversation key: %w", err)
	}

	return nil
}

// DeactivateConversationKeys deactivates old keys for rotation
func (r *CryptoRepository) DeactivateConversationKeys(ctx context.Context, tx *sqlx.Tx, conversationID string, excludeKeyVersion int) error {
	query := `
		UPDATE conversation_keys 
		SET is_active = false
		WHERE conversation_id = $1 AND key_version != $2`

	var err error
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, conversationID, excludeKeyVersion)
	} else {
		_, err = r.db.ExecContext(ctx, query, conversationID, excludeKeyVersion)
	}

	if err != nil {
		return fmt.Errorf("failed to deactivate conversation keys: %w", err)
	}

	return nil
}

// KeyExchangeMessage operations

// CreateKeyExchangeMessage creates a new key exchange message
func (r *CryptoRepository) CreateKeyExchangeMessage(ctx context.Context, tx *sqlx.Tx, msg *domain.KeyExchangeMessage) error {
	query := `
		INSERT INTO key_exchange_messages (
			id, conversation_id, sender_id, recipient_id, exchange_type,
			public_key, signature, nonce, expires_at
		) VALUES (
			:id, :conversation_id, :sender_id, :recipient_id, :exchange_type,
			:public_key, :signature, :nonce, :expires_at
		)`

	var err error
	if tx != nil {
		_, err = tx.NamedExecContext(ctx, query, msg)
	} else {
		_, err = r.db.NamedExecContext(ctx, query, msg)
	}

	if err != nil {
		return fmt.Errorf("failed to create key exchange message: %w", err)
	}

	return nil
}

// GetKeyExchangeMessage retrieves a key exchange message
func (r *CryptoRepository) GetKeyExchangeMessage(ctx context.Context, messageID string) (*domain.KeyExchangeMessage, error) {
	var msg domain.KeyExchangeMessage

	query := `
		SELECT id, conversation_id, sender_id, recipient_id, exchange_type,
			   public_key, signature, nonce, created_at, expires_at
		FROM key_exchange_messages
		WHERE id = $1 AND expires_at > NOW()`

	err := r.db.GetContext(ctx, &msg, query, messageID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get key exchange message: %w", err)
	}

	return &msg, nil
}

// GetPendingKeyExchanges retrieves pending key exchanges for a user
func (r *CryptoRepository) GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error) {
	var messages []*domain.KeyExchangeMessage

	query := `
		SELECT id, conversation_id, sender_id, recipient_id, exchange_type,
			   public_key, signature, nonce, created_at, expires_at
		FROM key_exchange_messages
		WHERE recipient_id = $1 AND expires_at > NOW()
		ORDER BY created_at DESC`

	err := r.db.SelectContext(ctx, &messages, query, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get pending key exchanges: %w", err)
	}

	return messages, nil
}

// DeleteKeyExchangeMessage deletes a key exchange message
func (r *CryptoRepository) DeleteKeyExchangeMessage(ctx context.Context, tx *sqlx.Tx, messageID string) error {
	query := `DELETE FROM key_exchange_messages WHERE id = $1`

	var err error
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, messageID)
	} else {
		_, err = r.db.ExecContext(ctx, query, messageID)
	}

	if err != nil {
		return fmt.Errorf("failed to delete key exchange message: %w", err)
	}

	return nil
}

// CleanupExpiredKeyExchanges removes expired key exchange messages
func (r *CryptoRepository) CleanupExpiredKeyExchanges(ctx context.Context) (int64, error) {
	query := `DELETE FROM key_exchange_messages WHERE expires_at <= NOW()`

	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup expired key exchanges: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}

// UserSigningKey operations

// CreateUserSigningKey creates a new user signing key
func (r *CryptoRepository) CreateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error {
	query := `
		INSERT INTO user_signing_keys (
			user_id, encrypted_private_key, public_key, key_version
		) VALUES (
			:user_id, :encrypted_private_key, :public_key, :key_version
		)`

	var err error
	if tx != nil {
		_, err = tx.NamedExecContext(ctx, query, key)
	} else {
		_, err = r.db.NamedExecContext(ctx, query, key)
	}

	if err != nil {
		return fmt.Errorf("failed to create user signing key: %w", err)
	}

	return nil
}

// GetUserSigningKey retrieves a user's signing key
func (r *CryptoRepository) GetUserSigningKey(ctx context.Context, userID string) (*domain.UserSigningKey, error) {
	var key domain.UserSigningKey

	query := `
		SELECT user_id, encrypted_private_key, public_key, key_version, created_at
		FROM user_signing_keys
		WHERE user_id = $1
		ORDER BY key_version DESC
		LIMIT 1`

	err := r.db.GetContext(ctx, &key, query, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get user signing key: %w", err)
	}

	return &key, nil
}

// GetUserPublicSigningKey retrieves a user's public signing key
func (r *CryptoRepository) GetUserPublicSigningKey(ctx context.Context, userID string) (string, error) {
	var publicKey string

	query := `
		SELECT public_key
		FROM user_signing_keys
		WHERE user_id = $1
		ORDER BY key_version DESC
		LIMIT 1`

	err := r.db.GetContext(ctx, &publicKey, query, userID)
	if err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("failed to get user public signing key: %w", err)
	}

	return publicKey, nil
}

// UpdateUserSigningKey updates a user's signing key
func (r *CryptoRepository) UpdateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error {
	query := `
		UPDATE user_signing_keys 
		SET encrypted_private_key = :encrypted_private_key,
			public_key = :public_key,
			key_version = :key_version
		WHERE user_id = :user_id`

	var err error
	if tx != nil {
		_, err = tx.NamedExecContext(ctx, query, key)
	} else {
		_, err = r.db.NamedExecContext(ctx, query, key)
	}

	if err != nil {
		return fmt.Errorf("failed to update user signing key: %w", err)
	}

	return nil
}

// DeleteUserSigningKey deletes a user's signing key
func (r *CryptoRepository) DeleteUserSigningKey(ctx context.Context, tx *sqlx.Tx, userID string) error {
	query := `DELETE FROM user_signing_keys WHERE user_id = $1`

	var err error
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, userID)
	} else {
		_, err = r.db.ExecContext(ctx, query, userID)
	}

	if err != nil {
		return fmt.Errorf("failed to delete user signing key: %w", err)
	}

	return nil
}

// CryptoAuditLog operations

// CreateAuditLog creates a new crypto audit log entry
func (r *CryptoRepository) CreateAuditLog(ctx context.Context, auditLog *domain.CryptoAuditLog) error {
	query := `
		INSERT INTO crypto_audit_log (
			id, user_id, conversation_id, operation, success,
			error_message, client_ip, user_agent
		) VALUES (
			:id, :user_id, :conversation_id, :operation, :success,
			:error_message, :client_ip, :user_agent
		)`

	_, err := r.db.NamedExecContext(ctx, query, auditLog)
	if err != nil {
		return fmt.Errorf("failed to create audit log: %w", err)
	}

	return nil
}

// GetAuditLogs retrieves audit logs for a user
func (r *CryptoRepository) GetAuditLogs(ctx context.Context, userID string, limit, offset int) ([]*domain.CryptoAuditLog, error) {
	var logs []*domain.CryptoAuditLog

	query := `
		SELECT id, user_id, conversation_id, operation, success,
			   error_message, client_ip, user_agent, created_at
		FROM crypto_audit_log
		WHERE user_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	err := r.db.SelectContext(ctx, &logs, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get audit logs: %w", err)
	}

	return logs, nil
}

// GetAuditLogsByConversation retrieves audit logs for a conversation
func (r *CryptoRepository) GetAuditLogsByConversation(ctx context.Context, conversationID string, limit, offset int) ([]*domain.CryptoAuditLog, error) {
	var logs []*domain.CryptoAuditLog

	query := `
		SELECT id, user_id, conversation_id, operation, success,
			   error_message, client_ip, user_agent, created_at
		FROM crypto_audit_log
		WHERE conversation_id = $1
		ORDER BY created_at DESC
		LIMIT $2 OFFSET $3`

	err := r.db.SelectContext(ctx, &logs, query, conversationID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get audit logs by conversation: %w", err)
	}

	return logs, nil
}

// CleanupOldAuditLogs removes audit logs older than specified duration
func (r *CryptoRepository) CleanupOldAuditLogs(ctx context.Context, olderThan time.Duration) (int64, error) {
	query := `DELETE FROM crypto_audit_log WHERE created_at < NOW() - $1::INTERVAL`

	result, err := r.db.ExecContext(ctx, query, olderThan.String())
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old audit logs: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}

// Helper methods for transactions

// WithTransaction executes a function within a database transaction
func (r *CryptoRepository) WithTransaction(ctx context.Context, fn func(*sqlx.Tx) error) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	defer func() {
		if p := recover(); p != nil {
			_ = tx.Rollback()
			panic(p)
		} else if err != nil {
			_ = tx.Rollback()
		} else {
			err = tx.Commit()
		}
	}()

	err = fn(tx)
	return err
}
