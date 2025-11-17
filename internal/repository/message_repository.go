package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"athena/internal/domain"
	"athena/internal/usecase"

	"github.com/jmoiron/sqlx"
)

type messageRepository struct {
	db *sqlx.DB
}

func NewMessageRepository(db *sqlx.DB) usecase.MessageRepository {
	return &messageRepository{db: db}
}

func (r *messageRepository) CreateMessage(ctx context.Context, message *domain.Message) error {
	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	// Insert the message
	query := `
		INSERT INTO messages (id, sender_id, recipient_id, content, message_type,
			is_read, is_deleted_by_sender, is_deleted_by_recipient, parent_message_id,
			created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err = tx.ExecContext(ctx, query,
		message.ID, message.SenderID, message.RecipientID, message.Content, message.MessageType,
		message.IsRead, message.IsDeletedBySender, message.IsDeletedByRecipient, message.ParentMessageID,
		message.CreatedAt, message.UpdatedAt)

	if err != nil {
		return fmt.Errorf("failed to create message: %w", err)
	}

	// Upsert conversation record
	err = r.upsertConversation(ctx, tx, message.SenderID, message.RecipientID, message.ID, message.CreatedAt)
	if err != nil {
		return fmt.Errorf("failed to upsert conversation: %w", err)
	}

	return tx.Commit()
}

func (r *messageRepository) GetMessage(ctx context.Context, messageID string, userID string) (*domain.Message, error) {
	query := `
		SELECT m.id, m.sender_id, m.recipient_id, m.content, m.message_type,
			m.is_read, m.is_deleted_by_sender, m.is_deleted_by_recipient,
			m.parent_message_id, m.created_at, m.updated_at, m.read_at,
			s.id as "sender.id", s.username as "sender.username", s.display_name as "sender.display_name",
			r.id as "recipient.id", r.username as "recipient.username", r.display_name as "recipient.display_name"
		FROM messages m
		JOIN users s ON s.id = m.sender_id
		JOIN users r ON r.id = m.recipient_id
		WHERE m.id = $1 AND (m.sender_id = $2 OR m.recipient_id = $2)
			AND ((m.sender_id = $2 AND m.is_deleted_by_sender = false) OR
				 (m.recipient_id = $2 AND m.is_deleted_by_recipient = false))`

	rows, err := r.db.QueryContext(ctx, query, messageID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to query message: %w", err)
	}
	defer func() { _ = rows.Close() }()

	if !rows.Next() {
		return nil, domain.ErrMessageNotFound
	}

	var message domain.Message
	var sender domain.User
	var recipient domain.User

	err = rows.Scan(
		&message.ID, &message.SenderID, &message.RecipientID, &message.Content, &message.MessageType,
		&message.IsRead, &message.IsDeletedBySender, &message.IsDeletedByRecipient,
		&message.ParentMessageID, &message.CreatedAt, &message.UpdatedAt, &message.ReadAt,
		&sender.ID, &sender.Username, &sender.DisplayName,
		&recipient.ID, &recipient.Username, &recipient.DisplayName,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to scan message: %w", err)
	}

	message.Sender = &sender
	message.Recipient = &recipient

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return &message, nil
}

func (r *messageRepository) GetMessages(ctx context.Context, userID string, otherUserID string, limit, offset int) ([]*domain.Message, error) {
	query := `
		SELECT m.id, m.sender_id, m.recipient_id, m.content, m.message_type,
			m.is_read, m.is_deleted_by_sender, m.is_deleted_by_recipient,
			m.parent_message_id, m.created_at, m.updated_at, m.read_at,
			s.id as "sender.id", s.username as "sender.username", s.display_name as "sender.display_name",
			r.id as "recipient.id", r.username as "recipient.username", r.display_name as "recipient.display_name"
		FROM messages m
		JOIN users s ON s.id = m.sender_id
		JOIN users r ON r.id = m.recipient_id
		WHERE ((m.sender_id = $1 AND m.recipient_id = $2) OR (m.sender_id = $2 AND m.recipient_id = $1))
			AND ((m.sender_id = $1 AND m.is_deleted_by_sender = false) OR
				 (m.recipient_id = $1 AND m.is_deleted_by_recipient = false))
		ORDER BY m.created_at DESC
		LIMIT $3 OFFSET $4`

	rows, err := r.db.QueryContext(ctx, query, userID, otherUserID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query messages: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var messages []*domain.Message
	for rows.Next() {
		var message domain.Message
		var sender domain.User
		var recipient domain.User

		err = rows.Scan(
			&message.ID, &message.SenderID, &message.RecipientID, &message.Content, &message.MessageType,
			&message.IsRead, &message.IsDeletedBySender, &message.IsDeletedByRecipient,
			&message.ParentMessageID, &message.CreatedAt, &message.UpdatedAt, &message.ReadAt,
			&sender.ID, &sender.Username, &sender.DisplayName,
			&recipient.ID, &recipient.Username, &recipient.DisplayName,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}

		message.Sender = &sender
		message.Recipient = &recipient
		messages = append(messages, &message)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return messages, nil
}

func (r *messageRepository) MarkMessageAsRead(ctx context.Context, messageID string, userID string) error {
	query := `
		UPDATE messages
		SET is_read = true, read_at = NOW(), updated_at = NOW()
		WHERE id = $1 AND recipient_id = $2 AND is_read = false`

	result, err := r.db.ExecContext(ctx, query, messageID, userID)
	if err != nil {
		return fmt.Errorf("failed to mark message as read: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrMessageNotFound
	}

	return nil
}

func (r *messageRepository) DeleteMessage(ctx context.Context, messageID string, userID string) error {
	// Soft delete by marking as deleted for the user
	query := `
		UPDATE messages
		SET is_deleted_by_sender = CASE WHEN sender_id = $2 THEN true ELSE is_deleted_by_sender END,
			is_deleted_by_recipient = CASE WHEN recipient_id = $2 THEN true ELSE is_deleted_by_recipient END,
			updated_at = NOW()
		WHERE id = $1 AND (sender_id = $2 OR recipient_id = $2)`

	result, err := r.db.ExecContext(ctx, query, messageID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return domain.ErrMessageNotFound
	}

	return nil
}

func (r *messageRepository) GetConversations(ctx context.Context, userID string, limit, offset int) ([]*domain.Conversation, error) {
	query := `
		SELECT c.id, c.participant_one_id, c.participant_two_id, c.last_message_id,
			c.last_message_at, c.created_at, c.updated_at,
			p1.id as "p1.id", p1.username as "p1.username", p1.display_name as "p1.display_name",
			p2.id as "p2.id", p2.username as "p2.username", p2.display_name as "p2.display_name",
			lm.id as "last_message.id", lm.content as "last_message.content",
			lm.sender_id as "last_message.sender_id", lm.created_at as "last_message.created_at",
			COALESCE(unread.count, 0) as unread_count
		FROM conversations c
		JOIN users p1 ON p1.id = c.participant_one_id
		JOIN users p2 ON p2.id = c.participant_two_id
		LEFT JOIN messages lm ON lm.id = c.last_message_id
		LEFT JOIN (
			SELECT
				CASE
					WHEN sender_id = $1 THEN recipient_id
					ELSE sender_id
				END as other_user_id,
				COUNT(*) as count
			FROM messages
			WHERE (sender_id = $1 OR recipient_id = $1)
				AND recipient_id = $1
				AND is_read = false
				AND is_deleted_by_recipient = false
			GROUP BY other_user_id
		) unread ON unread.other_user_id = CASE WHEN c.participant_one_id = $1 THEN c.participant_two_id ELSE c.participant_one_id END
		WHERE c.participant_one_id = $1 OR c.participant_two_id = $1
		ORDER BY c.last_message_at DESC
		LIMIT $2 OFFSET $3`

	rows, err := r.db.QueryContext(ctx, query, userID, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to query conversations: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var conversations []*domain.Conversation
	for rows.Next() {
		var conv domain.Conversation
		var p1, p2 domain.User
		var lastMessage domain.Message
		var lastMessageID sql.NullString
		var lastMessageContent sql.NullString
		var lastMessageSenderID sql.NullString
		var lastMessageCreatedAt sql.NullTime

		err = rows.Scan(
			&conv.ID, &conv.ParticipantOneID, &conv.ParticipantTwoID, &conv.LastMessageID,
			&conv.LastMessageAt, &conv.CreatedAt, &conv.UpdatedAt,
			&p1.ID, &p1.Username, &p1.DisplayName,
			&p2.ID, &p2.Username, &p2.DisplayName,
			&lastMessageID, &lastMessageContent, &lastMessageSenderID, &lastMessageCreatedAt,
			&conv.UnreadCount,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan conversation: %w", err)
		}

		conv.ParticipantOne = &p1
		conv.ParticipantTwo = &p2

		if lastMessageID.Valid {
			lastMessage.ID = lastMessageID.String
			lastMessage.Content = lastMessageContent.String
			lastMessage.SenderID = lastMessageSenderID.String
			lastMessage.CreatedAt = lastMessageCreatedAt.Time
			conv.LastMessage = &lastMessage
		}

		conversations = append(conversations, &conv)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return conversations, nil
}

func (r *messageRepository) GetUnreadCount(ctx context.Context, userID string) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM messages
		WHERE recipient_id = $1 AND is_read = false AND is_deleted_by_recipient = false`

	var count int
	err := r.db.GetContext(ctx, &count, query, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to get unread count: %w", err)
	}

	return count, nil
}

func (r *messageRepository) upsertConversation(ctx context.Context, tx *sqlx.Tx, userID1, userID2, lastMessageID string, lastMessageAt time.Time) error {
	// Ensure consistent ordering of participant IDs
	participantOne := userID1
	participantTwo := userID2
	if userID1 > userID2 {
		participantOne = userID2
		participantTwo = userID1
	}

	query := `
		INSERT INTO conversations (participant_one_id, participant_two_id, last_message_id, last_message_at, created_at, updated_at)
		VALUES ($1, $2, $3, $4, NOW(), NOW())
		ON CONFLICT (participant_one_id, participant_two_id)
		DO UPDATE SET
			last_message_id = $3,
			last_message_at = $4,
			updated_at = NOW()`

	_, err := tx.ExecContext(ctx, query, participantOne, participantTwo, lastMessageID, lastMessageAt)
	if err != nil {
		return fmt.Errorf("failed to upsert conversation: %w", err)
	}

	return nil
}
