package port

import (
	"athena/internal/domain"
	"context"
)

type MessageRepository interface {
	CreateMessage(ctx context.Context, message *domain.Message) error
	GetMessage(ctx context.Context, messageID string, userID string) (*domain.Message, error)
	GetMessages(ctx context.Context, userID string, otherUserID string, limit, offset int) ([]*domain.Message, error)
	MarkMessageAsRead(ctx context.Context, messageID string, userID string) error
	DeleteMessage(ctx context.Context, messageID string, userID string) error
	GetConversations(ctx context.Context, userID string, limit, offset int) ([]*domain.Conversation, error)
	GetUnreadCount(ctx context.Context, userID string) (int, error)
}
