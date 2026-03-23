package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"athena/internal/domain"

	"github.com/jmoiron/sqlx"
)

type e2eeMessageRepository struct {
	db *sqlx.DB
}

func NewE2EEMessageRepository(db *sqlx.DB) *e2eeMessageRepository {
	return &e2eeMessageRepository{db: db}
}

func (r *e2eeMessageRepository) CreateEncryptedMessage(ctx context.Context, message *domain.Message) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	const insertMsg = `
		INSERT INTO messages (
			id, sender_id, recipient_id, content, message_type,
			is_read, is_deleted_by_sender, is_deleted_by_recipient,
			parent_message_id, created_at, updated_at,
			encrypted_content, content_nonce, pgp_signature,
			is_encrypted, encryption_version
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`

	_, err = tx.ExecContext(ctx, insertMsg,
		message.ID, message.SenderID, message.RecipientID, message.Content, message.MessageType,
		message.IsRead, message.IsDeletedBySender, message.IsDeletedByRecipient,
		message.ParentMessageID, message.CreatedAt, message.UpdatedAt,
		message.EncryptedContent, message.ContentNonce, message.PGPSignature,
		message.IsEncrypted, message.EncryptionVersion,
	)
	if err != nil {
		return fmt.Errorf("insert encrypted message: %w", err)
	}

	if err := r.upsertConversation(ctx, tx, message.SenderID, message.RecipientID, message.ID, message.CreatedAt); err != nil {
		return fmt.Errorf("upsert conversation: %w", err)
	}

	return tx.Commit()
}

func (r *e2eeMessageRepository) GetEncryptedMessages(ctx context.Context, participantOneID, participantTwoID string, limit, offset int) ([]*domain.Message, error) {
	const query = `
		SELECT id, sender_id, recipient_id, content, message_type,
			is_read, is_deleted_by_sender, is_deleted_by_recipient,
			parent_message_id, created_at, updated_at, read_at,
			encrypted_content, content_nonce, pgp_signature,
			is_encrypted, encryption_version
		FROM messages
		WHERE ((sender_id = $1 AND recipient_id = $2) OR (sender_id = $2 AND recipient_id = $1))
			AND is_encrypted = true
		ORDER BY created_at DESC
		LIMIT $3 OFFSET $4`

	rows, err := r.db.QueryContext(ctx, query, participantOneID, participantTwoID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("query encrypted messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var messages []*domain.Message
	for rows.Next() {
		var m domain.Message
		if err := rows.Scan(
			&m.ID, &m.SenderID, &m.RecipientID, &m.Content, &m.MessageType,
			&m.IsRead, &m.IsDeletedBySender, &m.IsDeletedByRecipient,
			&m.ParentMessageID, &m.CreatedAt, &m.UpdatedAt, &m.ReadAt,
			&m.EncryptedContent, &m.ContentNonce, &m.PGPSignature,
			&m.IsEncrypted, &m.EncryptionVersion,
		); err != nil {
			return nil, fmt.Errorf("scan encrypted message: %w", err)
		}
		messages = append(messages, &m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate encrypted messages: %w", err)
	}
	return messages, nil
}

func (r *e2eeMessageRepository) GetMessage(ctx context.Context, messageID string, userID string) (*domain.Message, error) {
	const query = `
		SELECT id, sender_id, recipient_id, content, message_type,
			is_read, is_deleted_by_sender, is_deleted_by_recipient,
			parent_message_id, created_at, updated_at, read_at,
			encrypted_content, content_nonce, pgp_signature,
			is_encrypted, encryption_version
		FROM messages
		WHERE id = $1 AND (sender_id = $2 OR recipient_id = $2)`

	rows, err := r.db.QueryContext(ctx, query, messageID, userID)
	if err != nil {
		return nil, fmt.Errorf("query message: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return nil, domain.ErrMessageNotFound
	}

	var m domain.Message
	if err := rows.Scan(
		&m.ID, &m.SenderID, &m.RecipientID, &m.Content, &m.MessageType,
		&m.IsRead, &m.IsDeletedBySender, &m.IsDeletedByRecipient,
		&m.ParentMessageID, &m.CreatedAt, &m.UpdatedAt, &m.ReadAt,
		&m.EncryptedContent, &m.ContentNonce, &m.PGPSignature,
		&m.IsEncrypted, &m.EncryptionVersion,
	); err != nil {
		return nil, fmt.Errorf("scan message: %w", err)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("message rows error: %w", err)
	}
	return &m, nil
}

func (r *e2eeMessageRepository) upsertConversation(ctx context.Context, tx *sqlx.Tx, userID1, userID2, lastMessageID string, lastMessageAt time.Time) error {
	p1, p2 := userID1, userID2
	if userID1 > userID2 {
		p1, p2 = userID2, userID1
	}
	const query = `
		INSERT INTO conversations (participant_one_id, participant_two_id, last_message_id, last_message_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (participant_one_id, participant_two_id)
		DO UPDATE SET last_message_id = $3, last_message_at = $4, updated_at = NOW()`

	_, err := tx.ExecContext(ctx, query, p1, p2, lastMessageID, lastMessageAt)
	if err != nil {
		return fmt.Errorf("upsert conversation: %w", err)
	}
	return nil
}

type e2eeConversationRepository struct {
	db *sqlx.DB
}

func NewE2EEConversationRepository(db *sqlx.DB) *e2eeConversationRepository {
	return &e2eeConversationRepository{db: db}
}

func (r *e2eeConversationRepository) GetOrCreateConversation(ctx context.Context, tx *sqlx.Tx, participantOneID, participantTwoID string) (*domain.Conversation, error) {
	p1, p2 := participantOneID, participantTwoID
	if participantOneID > participantTwoID {
		p1, p2 = participantTwoID, participantOneID
	}

	const upsert = `
		INSERT INTO conversations (participant_one_id, participant_two_id, last_message_at, created_at, updated_at)
		VALUES ($1, $2, NOW(), NOW(), NOW())
		ON CONFLICT (participant_one_id, participant_two_id) DO UPDATE SET updated_at = NOW()
		RETURNING id, participant_one_id, participant_two_id, encryption_status,
			last_message_at, created_at, updated_at`

	var row *sql.Row
	if tx != nil {
		row = tx.QueryRowContext(ctx, upsert, p1, p2)
	} else {
		row = r.db.QueryRowContext(ctx, upsert, p1, p2)
	}

	var conv domain.Conversation
	if err := row.Scan(
		&conv.ID, &conv.ParticipantOneID, &conv.ParticipantTwoID,
		&conv.EncryptionStatus, &conv.LastMessageAt, &conv.CreatedAt, &conv.UpdatedAt,
	); err != nil {
		return nil, fmt.Errorf("get or create conversation: %w", err)
	}
	return &conv, nil
}

func (r *e2eeConversationRepository) GetConversation(ctx context.Context, conversationID string) (*domain.Conversation, error) {
	const query = `
		SELECT id, participant_one_id, participant_two_id,
			encryption_status, last_message_at, created_at, updated_at
		FROM conversations WHERE id = $1`

	row := r.db.QueryRowContext(ctx, query, conversationID)
	var conv domain.Conversation
	if err := row.Scan(
		&conv.ID, &conv.ParticipantOneID, &conv.ParticipantTwoID,
		&conv.EncryptionStatus, &conv.LastMessageAt, &conv.CreatedAt, &conv.UpdatedAt,
	); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	return &conv, nil
}

func (r *e2eeConversationRepository) UpdateEncryptionStatus(ctx context.Context, tx *sqlx.Tx, conversationID string, status string) error {
	const query = `UPDATE conversations SET encryption_status = $1, updated_at = NOW() WHERE id = $2`

	var err error
	if tx != nil {
		_, err = tx.ExecContext(ctx, query, status, conversationID)
	} else {
		_, err = r.db.ExecContext(ctx, query, status, conversationID)
	}
	if err != nil {
		return fmt.Errorf("update encryption status: %w", err)
	}
	return nil
}
