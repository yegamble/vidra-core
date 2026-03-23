package message

import (
	"context"
	"fmt"
	"time"

	"athena/internal/domain"
	"athena/internal/port"
	"athena/internal/security"

	"github.com/google/uuid"
)

type Service struct {
	messageRepo port.MessageRepository
	userRepo    port.UserRepository
}

func NewService(messageRepo port.MessageRepository, userRepo port.UserRepository) *Service {
	return &Service{
		messageRepo: messageRepo,
		userRepo:    userRepo,
	}
}

func (s *Service) SendMessage(ctx context.Context, senderID string, req *domain.SendMessageRequest) (*domain.Message, error) {
	if senderID == req.RecipientID {
		return nil, domain.ErrCannotMessageSelf
	}

	sender, err := s.userRepo.GetByID(ctx, senderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sender: %w", err)
	}

	recipient, err := s.userRepo.GetByID(ctx, req.RecipientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipient: %w", err)
	}

	if len(req.Content) > 2000 {
		return nil, domain.ErrMessageTooLong
	}

	if req.ParentMessageID != nil {
		parentMessage, err := s.messageRepo.GetMessage(ctx, *req.ParentMessageID, senderID)
		if err != nil {
			return nil, fmt.Errorf("failed to get parent message: %w", err)
		}

		if (parentMessage.SenderID != senderID && parentMessage.SenderID != req.RecipientID) ||
			(parentMessage.RecipientID != senderID && parentMessage.RecipientID != req.RecipientID) {
			return nil, domain.ErrMessageNotFound
		}
	}

	sanitizedContent := security.SanitizeStrictText(req.Content)

	now := time.Now()
	message := &domain.Message{
		ID:                   uuid.New().String(),
		SenderID:             senderID,
		RecipientID:          req.RecipientID,
		Content:              &sanitizedContent,
		MessageType:          domain.MessageTypeText,
		IsRead:               false,
		IsDeletedBySender:    false,
		IsDeletedByRecipient: false,
		ParentMessageID:      req.ParentMessageID,
		CreatedAt:            now,
		UpdatedAt:            now,
		Sender:               sender,
		Recipient:            recipient,
	}

	err = s.messageRepo.CreateMessage(ctx, message)
	if err != nil {
		return nil, fmt.Errorf("failed to create message: %w", err)
	}

	return message, nil
}

func (s *Service) GetMessages(ctx context.Context, userID string, req *domain.GetMessagesRequest) (*domain.MessagesResponse, error) {
	_, err := s.userRepo.GetByID(ctx, req.ConversationWith)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation partner: %w", err)
	}

	limit := req.Limit
	if limit == 0 || limit > 100 {
		limit = 50
	}

	messages, err := s.messageRepo.GetMessages(ctx, userID, req.ConversationWith, limit+1, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	messageValues := make([]domain.Message, len(messages))
	for i, msg := range messages {
		messageValues[i] = *msg
	}

	return &domain.MessagesResponse{
		Messages: messageValues,
		Total:    len(messageValues),
		HasMore:  hasMore,
	}, nil
}

func (s *Service) MarkMessageAsRead(ctx context.Context, userID string, req *domain.MarkMessageReadRequest) error {
	err := s.messageRepo.MarkMessageAsRead(ctx, req.MessageID, userID)
	if err != nil {
		return fmt.Errorf("failed to mark message as read: %w", err)
	}
	return nil
}

func (s *Service) DeleteMessage(ctx context.Context, userID string, req *domain.DeleteMessageRequest) error {
	err := s.messageRepo.DeleteMessage(ctx, req.MessageID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}
	return nil
}

func (s *Service) GetConversations(ctx context.Context, userID string, req *domain.GetConversationsRequest) (*domain.ConversationsResponse, error) {
	limit := req.Limit
	if limit == 0 || limit > 50 {
		limit = 20
	}

	conversations, err := s.messageRepo.GetConversations(ctx, userID, limit+1, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversations: %w", err)
	}

	hasMore := len(conversations) > limit
	if hasMore {
		conversations = conversations[:limit]
	}

	conversationValues := make([]domain.Conversation, len(conversations))
	for i, conv := range conversations {
		conversationValues[i] = *conv
	}

	return &domain.ConversationsResponse{
		Conversations: conversationValues,
		Total:         len(conversationValues),
		HasMore:       hasMore,
	}, nil
}

func (s *Service) GetUnreadCount(ctx context.Context, userID string) (int, error) {
	count, err := s.messageRepo.GetUnreadCount(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to get unread count: %w", err)
	}
	return count, nil
}
