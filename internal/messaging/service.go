// Package messaging implements normal (non-E2EE) direct messaging for vidra-core:
// 1:1 conversations and plaintext messages. Encrypted messaging (ciphertext-only)
// is a separate later slice (fix_plan P11.2). It is HTTP-agnostic and testable
// without a server.
package messaging

import (
	"context"
	"errors"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// Sentinel errors the HTTP layer maps to status codes.
var (
	// ErrCannotMessageSelf means a user tried to start a conversation with themselves.
	ErrCannotMessageSelf = errors.New("messaging: cannot message yourself")
	// ErrRecipientNotFound means the target account does not exist.
	ErrRecipientNotFound = errors.New("messaging: recipient not found")
	// ErrNotParticipant means the caller is not a member of the conversation.
	ErrNotParticipant = errors.New("messaging: not a participant")
)

// Repository is the data access the messaging service needs. *sqlcgen.Queries
// satisfies it directly; tests substitute an in-memory fake.
type Repository interface {
	CreateConversation(ctx context.Context, dmKey *string) (sqlcgen.CreateConversationRow, error)
	GetConversationByDMKey(ctx context.Context, dmKey *string) (sqlcgen.GetConversationByDMKeyRow, error)
	AddConversationParticipant(ctx context.Context, arg sqlcgen.AddConversationParticipantParams) error
	IsConversationParticipant(ctx context.Context, arg sqlcgen.IsConversationParticipantParams) (bool, error)
	CreateMessage(ctx context.Context, arg sqlcgen.CreateMessageParams) (sqlcgen.Message, error)
	GetOtherParticipant(ctx context.Context, arg sqlcgen.GetOtherParticipantParams) (uuid.UUID, error)
	TouchConversation(ctx context.Context, id uuid.UUID) error
	ListMessages(ctx context.Context, arg sqlcgen.ListMessagesParams) ([]sqlcgen.ListMessagesRow, error)
	ListConversations(ctx context.Context, arg sqlcgen.ListConversationsParams) ([]sqlcgen.ListConversationsRow, error)
	GetUserByID(ctx context.Context, id uuid.UUID) (sqlcgen.User, error)
}

// Service holds the messaging application logic.
type Service struct{ repo Repository }

// NewService builds the messaging service.
func NewService(repo Repository) *Service { return &Service{repo: repo} }

// Conversation is a 1:1 thread.
type Conversation struct {
	ID        uuid.UUID
	CreatedAt time.Time
	UpdatedAt time.Time
}

// Summary is a conversation as shown in the caller's inbox: the other
// participant and the last message preview.
type Summary struct {
	ID               uuid.UUID
	UpdatedAt        time.Time
	OtherUserID      uuid.UUID
	OtherUsername    string
	OtherDisplayName string
	LastMessageBody  string // "" when there are no messages yet
	LastMessageAt    time.Time
}

// Message is a single message. SenderUsername/SenderDisplayName are populated
// when listing; on send the caller supplies the sender's identity.
type Message struct {
	ID                uuid.UUID
	ConversationID    uuid.UUID
	SenderID          uuid.UUID
	SenderUsername    string
	SenderDisplayName string
	Body              string
	CreatedAt         time.Time
}

// dmKey is the canonical, order-independent key for a 1:1 conversation.
func dmKey(a, b uuid.UUID) string {
	x, y := a.String(), b.String()
	if x > y {
		x, y = y, x
	}
	return x + ":" + y
}

// StartConversation returns the 1:1 conversation between the caller and the
// recipient, creating it (with both participants) if it does not exist.
// Messaging yourself → ErrCannotMessageSelf; an unknown recipient →
// ErrRecipientNotFound.
func (s *Service) StartConversation(ctx context.Context, meID, recipientID uuid.UUID) (Conversation, error) {
	if meID == recipientID {
		return Conversation{}, ErrCannotMessageSelf
	}
	if _, err := s.repo.GetUserByID(ctx, recipientID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return Conversation{}, ErrRecipientNotFound
		}
		return Conversation{}, err
	}

	key := dmKey(meID, recipientID)
	var conv Conversation
	row, err := s.repo.CreateConversation(ctx, &key)
	switch {
	case errors.Is(err, pgx.ErrNoRows): // already exists
		existing, gerr := s.repo.GetConversationByDMKey(ctx, &key)
		if gerr != nil {
			return Conversation{}, gerr
		}
		conv = Conversation{ID: existing.ID, CreatedAt: existing.CreatedAt, UpdatedAt: existing.UpdatedAt}
	case err != nil:
		return Conversation{}, err
	default:
		conv = Conversation{ID: row.ID, CreatedAt: row.CreatedAt, UpdatedAt: row.UpdatedAt}
	}

	for _, uid := range []uuid.UUID{meID, recipientID} {
		if err := s.repo.AddConversationParticipant(ctx, sqlcgen.AddConversationParticipantParams{
			ConversationID: conv.ID,
			UserID:         uid,
		}); err != nil {
			return Conversation{}, err
		}
	}
	return conv, nil
}

// ListConversations returns the caller's conversations, most-recently-active
// first. The caller clamps limit/offset.
func (s *Service) ListConversations(ctx context.Context, meID uuid.UUID, limit, offset int32) ([]Summary, error) {
	rows, err := s.repo.ListConversations(ctx, sqlcgen.ListConversationsParams{
		UserID:       meID,
		ResultLimit:  limit,
		ResultOffset: offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Summary, 0, len(rows))
	for _, r := range rows {
		out = append(out, Summary{
			ID: r.ID, UpdatedAt: r.UpdatedAt, OtherUserID: r.OtherUserID,
			OtherUsername: r.OtherUsername, OtherDisplayName: r.OtherDisplayName,
			LastMessageBody: r.LastMessageBody, LastMessageAt: r.LastMessageAt,
		})
	}
	return out, nil
}

// SendMessage posts a message to a conversation. The caller must be a
// participant, else ErrNotParticipant.
func (s *Service) SendMessage(ctx context.Context, meID, conversationID uuid.UUID, body string) (Message, error) {
	member, err := s.repo.IsConversationParticipant(ctx, sqlcgen.IsConversationParticipantParams{
		ConversationID: conversationID,
		UserID:         meID,
	})
	if err != nil {
		return Message{}, err
	}
	if !member {
		return Message{}, ErrNotParticipant
	}
	m, err := s.repo.CreateMessage(ctx, sqlcgen.CreateMessageParams{
		ConversationID: conversationID,
		SenderID:       meID,
		Body:           body,
	})
	if err != nil {
		return Message{}, err
	}
	// Bump the conversation so it sorts to the top of both inboxes (best-effort).
	_ = s.repo.TouchConversation(ctx, conversationID)
	return Message{
		ID: m.ID, ConversationID: m.ConversationID, SenderID: m.SenderID,
		Body: m.Body, CreatedAt: m.CreatedAt,
	}, nil
}

// OtherParticipant returns the other member of a 1:1 conversation (whoever isn't
// meID). Used to address a new-message notification to the recipient. Returns
// pgx.ErrNoRows if there is no other participant.
func (s *Service) OtherParticipant(ctx context.Context, conversationID, meID uuid.UUID) (uuid.UUID, error) {
	return s.repo.GetOtherParticipant(ctx, sqlcgen.GetOtherParticipantParams{
		ConversationID: conversationID,
		UserID:         meID,
	})
}

// ListMessages returns a conversation's messages, newest first. The caller must
// be a participant, else ErrNotParticipant.
func (s *Service) ListMessages(ctx context.Context, meID, conversationID uuid.UUID, limit, offset int32) ([]Message, error) {
	member, err := s.repo.IsConversationParticipant(ctx, sqlcgen.IsConversationParticipantParams{
		ConversationID: conversationID,
		UserID:         meID,
	})
	if err != nil {
		return nil, err
	}
	if !member {
		return nil, ErrNotParticipant
	}
	rows, err := s.repo.ListMessages(ctx, sqlcgen.ListMessagesParams{
		ConversationID: conversationID,
		ResultLimit:    limit,
		ResultOffset:   offset,
	})
	if err != nil {
		return nil, err
	}
	out := make([]Message, 0, len(rows))
	for _, r := range rows {
		out = append(out, Message{
			ID: r.ID, ConversationID: r.ConversationID, SenderID: r.SenderID,
			SenderUsername: r.SenderUsername, SenderDisplayName: r.SenderDisplayName,
			Body: r.Body, CreatedAt: r.CreatedAt,
		})
	}
	return out, nil
}
