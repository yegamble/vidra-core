package usecase

import (
	"context"
	"fmt"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
)

type MessageService struct {
	messageRepo MessageRepository
	userRepo    UserRepository
}

func NewMessageService(messageRepo MessageRepository, userRepo UserRepository) *MessageService {
	return &MessageService{
		messageRepo: messageRepo,
		userRepo:    userRepo,
	}
}

func (s *MessageService) SendMessage(ctx context.Context, senderID string, req *domain.SendMessageRequest) (*domain.Message, error) {
	// Validate that sender and recipient are different
	if senderID == req.RecipientID {
		return nil, domain.ErrCannotMessageSelf
	}

	// Validate that both users exist
	sender, err := s.userRepo.GetByID(ctx, senderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sender: %w", err)
	}

	recipient, err := s.userRepo.GetByID(ctx, req.RecipientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipient: %w", err)
	}

	// Validate content length
	if len(req.Content) > 2000 {
		return nil, domain.ErrMessageTooLong
	}

	// Validate parent message if specified
	if req.ParentMessageID != nil {
		parentMessage, err := s.messageRepo.GetMessage(ctx, *req.ParentMessageID, senderID)
		if err != nil {
			return nil, fmt.Errorf("failed to get parent message: %w", err)
		}

		// Ensure parent message is part of the same conversation
		if (parentMessage.SenderID != senderID && parentMessage.SenderID != req.RecipientID) ||
			(parentMessage.RecipientID != senderID && parentMessage.RecipientID != req.RecipientID) {
			return nil, domain.ErrMessageNotFound
		}
	}

	// Create the message
	now := time.Now()
	message := &domain.Message{
		ID:                   uuid.New().String(),
		SenderID:             senderID,
		RecipientID:          req.RecipientID,
		Content:              req.Content,
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

func (s *MessageService) GetMessages(ctx context.Context, userID string, req *domain.GetMessagesRequest) (*domain.MessagesResponse, error) {
	// Validate that the other user exists
	_, err := s.userRepo.GetByID(ctx, req.ConversationWith)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation partner: %w", err)
	}

	// Set default limit if not specified
	limit := req.Limit
	if limit == 0 || limit > 100 {
		limit = 50
	}

	// Get messages
	messages, err := s.messageRepo.GetMessages(ctx, userID, req.ConversationWith, limit+1, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get messages: %w", err)
	}

	// Check if there are more messages
	hasMore := len(messages) > limit
	if hasMore {
		messages = messages[:limit]
	}

	// Convert to slice of values for response
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

func (s *MessageService) MarkMessageAsRead(ctx context.Context, userID string, req *domain.MarkMessageReadRequest) error {
	err := s.messageRepo.MarkMessageAsRead(ctx, req.MessageID, userID)
	if err != nil {
		return fmt.Errorf("failed to mark message as read: %w", err)
	}
	return nil
}

func (s *MessageService) DeleteMessage(ctx context.Context, userID string, req *domain.DeleteMessageRequest) error {
	err := s.messageRepo.DeleteMessage(ctx, req.MessageID, userID)
	if err != nil {
		return fmt.Errorf("failed to delete message: %w", err)
	}
	return nil
}

func (s *MessageService) GetConversations(ctx context.Context, userID string, req *domain.GetConversationsRequest) (*domain.ConversationsResponse, error) {
	// Set default limit if not specified
	limit := req.Limit
	if limit == 0 || limit > 50 {
		limit = 20
	}

	// Get conversations
	conversations, err := s.messageRepo.GetConversations(ctx, userID, limit+1, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversations: %w", err)
	}

	// Check if there are more conversations
	hasMore := len(conversations) > limit
	if hasMore {
		conversations = conversations[:limit]
	}

	// Convert to slice of values for response
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

func (s *MessageService) GetUnreadCount(ctx context.Context, userID string) (int, error) {
	count, err := s.messageRepo.GetUnreadCount(ctx, userID)
	if err != nil {
		return 0, fmt.Errorf("failed to get unread count: %w", err)
	}
	return count, nil
}
