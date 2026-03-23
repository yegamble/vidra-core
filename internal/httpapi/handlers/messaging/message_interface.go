package messaging

import (
	"context"

	"vidra-core/internal/domain"
)

type MessageServiceInterface interface {
	SendMessage(ctx context.Context, senderID string, req *domain.SendMessageRequest) (*domain.Message, error)
	GetMessages(ctx context.Context, userID string, req *domain.GetMessagesRequest) (*domain.MessagesResponse, error)
}
