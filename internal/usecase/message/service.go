package message

import (
	"context"
	"fmt"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"
	"vidra-core/internal/security"

	"github.com/google/uuid"
)

// publisher is the subset of *WSHub that Service depends on. Defined as an interface so unit
// tests can drop in a fake; production wiring passes the concrete hub.
type publisher interface {
	PublishMessageReceived(senderID, recipientID uuid.UUID, payload MessageReceivedPayload)
	PublishMessageRead(senderID uuid.UUID, payload MessageReadPayload)
}

type Service struct {
	messageRepo port.MessageRepository
	userRepo    port.UserRepository
	pub         publisher
}

func NewService(messageRepo port.MessageRepository, userRepo port.UserRepository) *Service {
	return &Service{
		messageRepo: messageRepo,
		userRepo:    userRepo,
	}
}

// SetPublisher wires the realtime hub. Optional; when nil, Service is realtime-blind (used in
// unit tests that don't exercise the WS path).
func (s *Service) SetPublisher(p publisher) { s.pub = p }

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

	if s.pub != nil {
		senderUUID, _ := uuid.Parse(senderID)
		recipUUID, _ := uuid.Parse(req.RecipientID)
		body := ""
		if message.Content != nil {
			body = *message.Content
		}
		s.pub.PublishMessageReceived(senderUUID, recipUUID, MessageReceivedPayload{
			ID:              message.ID,
			ConversationID:  conversationID(senderID, req.RecipientID),
			SenderID:        senderID,
			RecipientID:     req.RecipientID,
			Body:            body,
			CreatedAt:       message.CreatedAt,
			ClientMessageID: req.ClientMessageID,
		})
	}

	return message, nil
}

// conversationID builds a stable, order-independent id for a 1:1 conversation. Sorting the
// two endpoints means SendMessage(A→B) and SendMessage(B→A) target the same conversation.
func conversationID(a, b string) string {
	if a < b {
		return a + ":" + b
	}
	return b + ":" + a
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
	// Notify the original sender that their message has been read. We have to fetch it to
	// determine sender_id and conversation; the repo already handles auth (user must be the
	// recipient to mark-read).
	if s.pub != nil {
		msg, getErr := s.messageRepo.GetMessage(ctx, req.MessageID, userID)
		if getErr == nil && msg.SenderID != userID {
			if senderUUID, parseErr := uuid.Parse(msg.SenderID); parseErr == nil {
				s.pub.PublishMessageRead(senderUUID, MessageReadPayload{
					MessageID:      msg.ID,
					ConversationID: conversationID(msg.SenderID, msg.RecipientID),
					ReaderID:       userID,
				})
			}
		}
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
