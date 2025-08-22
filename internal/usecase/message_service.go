package usecase

import (
	"context"
	"fmt"
	"time"

	"athena/internal/config"
	"athena/internal/crypto"
	"athena/internal/domain"
	"github.com/google/uuid"
)

type MessageService struct {
	messageRepo MessageRepository
	userRepo    UserRepository
	pgpService  *crypto.PGPService
	cfg         *config.Config
}

func NewMessageServiceWithPGPConfig(messageRepo MessageRepository, userRepo UserRepository, pgpService *crypto.PGPService, cfg *config.Config) *MessageService {
	return &MessageService{
		messageRepo: messageRepo,
		userRepo:    userRepo,
		pgpService:  pgpService,
		cfg:         cfg,
	}
}

// NewMessageService creates a message service with a default PGP service.
// Backward-compatible constructor used by existing tests and routes.
func NewMessageService(messageRepo MessageRepository, userRepo UserRepository) *MessageService {
	return NewMessageServiceWithPGPConfig(messageRepo, userRepo, crypto.NewPGPService(), nil)
}

// NewMessageServiceWithConfig creates a message service with default PGP service and app config.
func NewMessageServiceWithConfig(messageRepo MessageRepository, userRepo UserRepository, cfg *config.Config) *MessageService {
	return NewMessageServiceWithPGPConfig(messageRepo, userRepo, crypto.NewPGPService(), cfg)
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

	// Handle secure messaging
	if req.IsSecure || req.EncryptedContent != nil {
		// For secure messages, validate that both users have PGP keys
		if sender.PGPPublicKey == nil || recipient.PGPPublicKey == nil {
			return nil, domain.ErrSecureModeNotAllowed
		}

		// Validate encrypted content and signature are provided
		if req.EncryptedContent == nil || req.PGPSignature == nil {
			return nil, fmt.Errorf("encrypted_content and pgp_signature required for secure messages: %w", domain.ErrBadRequest)
		}

		// Verify the signature using sender's public key
		if err := s.pgpService.VerifySignature(req.Content, *req.PGPSignature, *sender.PGPPublicKey); err != nil {
			return nil, fmt.Errorf("signature verification failed: %w", domain.ErrPGPVerificationFailed)
		}
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
		EncryptedContent:     req.EncryptedContent,
		PGPSignature:         req.PGPSignature,
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

	// Set IsSecure flag based on whether encrypted content exists
	message.IsSecure = req.EncryptedContent != nil

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

// SendSecureMessage sends an encrypted message using PGP
func (s *MessageService) SendSecureMessage(ctx context.Context, senderID string, req *domain.SendSecureMessageRequest) (*domain.Message, error) {
	// Validate that sender and recipient are different
	if senderID == req.RecipientID {
		return nil, domain.ErrCannotMessageSelf
	}

	// Validate that both users exist and have PGP keys
	sender, err := s.userRepo.GetByID(ctx, senderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sender: %w", err)
	}

	recipient, err := s.userRepo.GetByID(ctx, req.RecipientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipient: %w", err)
	}

	// Verify both users have PGP keys
	if sender.PGPPublicKey == nil || recipient.PGPPublicKey == nil {
		return nil, domain.ErrSecureModeNotAllowed
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

	// Optional server-side signature verification (no plaintext required: verify against encrypted content)
	if s.pgpService != nil && sender.PGPPublicKey != nil && (s.cfg == nil || s.cfg.PGPVerifySignatures) {
		if err := s.pgpService.VerifySignature(req.EncryptedContent, req.PGPSignature, *sender.PGPPublicKey); err != nil {
			return nil, domain.ErrPGPVerificationFailed
		}
	}

	// Create the secure message
	now := time.Now()
	message := &domain.Message{
		ID:                   uuid.New().String(),
		SenderID:             senderID,
		RecipientID:          req.RecipientID,
		Content:              "[Encrypted Message]", // Placeholder content for non-encrypted view
		EncryptedContent:     &req.EncryptedContent,
		PGPSignature:         &req.PGPSignature,
		MessageType:          domain.MessageTypeText,
		IsRead:               false,
		IsDeletedBySender:    false,
		IsDeletedByRecipient: false,
		ParentMessageID:      req.ParentMessageID,
		CreatedAt:            now,
		UpdatedAt:            now,
		Sender:               sender,
		Recipient:            recipient,
		IsSecure:             true,
	}

	err = s.messageRepo.CreateMessage(ctx, message)
	if err != nil {
		return nil, fmt.Errorf("failed to create secure message: %w", err)
	}

	return message, nil
}

// StartSecureConversation initiates a secure conversation between two users
func (s *MessageService) StartSecureConversation(ctx context.Context, userID string, req *domain.StartSecureConversationRequest) (*domain.Conversation, error) {
	// Validate that user and recipient are different
	if userID == req.RecipientID {
		return nil, domain.ErrCannotMessageSelf
	}

	// Validate that both users exist and have PGP keys
	user, err := s.userRepo.GetByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user: %w", err)
	}

	recipient, err := s.userRepo.GetByID(ctx, req.RecipientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get recipient: %w", err)
	}

	// Verify both users have PGP keys
	if user.PGPPublicKey == nil || recipient.PGPPublicKey == nil {
		return nil, domain.ErrSecureModeNotAllowed
	}

	// Create secure conversation
	err = s.messageRepo.CreateSecureConversation(ctx, userID, req.RecipientID)
	if err != nil {
		return nil, fmt.Errorf("failed to create secure conversation: %w", err)
	}

	// Get the created conversation
	conversation, err := s.messageRepo.GetConversation(ctx, userID, req.RecipientID, true)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	return conversation, nil
}

// SetPGPPublicKey sets or updates a user's PGP public key
func (s *MessageService) SetPGPPublicKey(ctx context.Context, userID string, req *domain.SetPGPKeyRequest) error {
	// Validate the PGP key format
	err := s.pgpService.ValidatePGPPublicKey(req.PGPPublicKey)
	if err != nil {
		return fmt.Errorf("%w: %v", domain.ErrInvalidPGPKey, err)
	}

	// Compute fingerprint if available
	fingerprint := ""
	if s.pgpService != nil {
		if fp, ferr := s.pgpService.GetKeyFingerprint(req.PGPPublicKey); ferr == nil {
			fingerprint = fp
		}
	}

	// Set the PGP key (and fingerprint if computed)
	if fingerprint != "" {
		err = s.userRepo.SetPGPPublicKeyWithFingerprint(ctx, userID, req.PGPPublicKey, fingerprint)
	} else {
		err = s.userRepo.SetPGPPublicKey(ctx, userID, req.PGPPublicKey)
	}
	if err != nil {
		return fmt.Errorf("failed to set PGP public key: %w", err)
	}

	return nil
}

// RemovePGPPublicKey removes a user's PGP public key
func (s *MessageService) RemovePGPPublicKey(ctx context.Context, userID string) error {
	err := s.userRepo.RemovePGPPublicKey(ctx, userID)
	if err != nil {
		return fmt.Errorf("failed to remove PGP public key: %w", err)
	}

	return nil
}

// GetPGPPublicKey gets a user's PGP public key status
func (s *MessageService) GetPGPPublicKey(ctx context.Context, userID string) (*domain.PGPKeyResponse, error) {
	pgpKey, err := s.userRepo.GetPGPPublicKey(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get PGP public key: %w", err)
	}

	response := &domain.PGPKeyResponse{
		HasPGPKey:    pgpKey != nil,
		PGPPublicKey: pgpKey,
	}

	return response, nil
}

// GetUserPGPPublicKey gets another user's PGP public key (for encryption)
func (s *MessageService) GetUserPGPPublicKey(ctx context.Context, userID string) (*domain.PGPKeyResponse, error) {
	key, err := s.userRepo.GetPGPPublicKey(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user pgp key: %w", err)
	}
	fp, err := s.userRepo.GetPGPFingerprint(ctx, userID)
	if err != nil && err != domain.ErrUserNotFound {
		return nil, fmt.Errorf("failed to get pgp fingerprint: %w", err)
	}
	response := &domain.PGPKeyResponse{HasPGPKey: key != nil, PGPPublicKey: key, PGPFingerprint: fp}

	return response, nil
}

// GeneratePGPKey generates a PGP keypair for the user, stores the public key + fingerprint, and returns the private key to the client.
func (s *MessageService) GeneratePGPKey(ctx context.Context, userID, name, email string) (publicKey string, privateKey string, fingerprint string, err error) {
	if s.pgpService == nil {
		return "", "", "", domain.ErrInternalServer
	}
	// Generate keypair using service
	pub, priv, fp, gerr := s.pgpService.GenerateKeyPair(name, email)
	if gerr != nil {
		return "", "", "", fmt.Errorf("failed to generate pgp key: %w", gerr)
	}
	// Store public key + fingerprint
	if err = s.userRepo.SetPGPPublicKeyWithFingerprint(ctx, userID, pub, fp); err != nil {
		return "", "", "", fmt.Errorf("failed to store public key: %w", err)
	}
	return pub, priv, fp, nil
}

// GetMessageThread retrieves messages in a specific conversation thread
func (s *MessageService) GetMessageThread(ctx context.Context, userID string, req *domain.GetMessageThreadRequest) (*domain.MessagesResponse, error) {
	// For now, we'll treat threadId as conversationId/otherUserID
	// In a proper implementation, you'd have a proper thread/conversation mapping

	// Set default limit if not provided
	if req.Limit == 0 {
		req.Limit = 50
	}

	messages, err := s.messageRepo.GetMessages(ctx, userID, req.ThreadID, req.Limit, req.Offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get thread messages: %w", err)
	}

	// TODO: Get total count for HasMore calculation
	total := len(messages)
	hasMore := len(messages) == req.Limit

	// Convert []*domain.Message to []domain.Message
	messageList := make([]domain.Message, len(messages))
	for i, msg := range messages {
		messageList[i] = *msg
	}

	return &domain.MessagesResponse{
		Messages: messageList,
		Total:    total,
		HasMore:  hasMore,
	}, nil
}

// UpdateMessage edits a message if it's within 5 minutes of creation and user owns it
func (s *MessageService) UpdateMessage(ctx context.Context, userID string, req *domain.UpdateMessageRequest) (*domain.Message, error) {
	// Get the original message
	message, err := s.messageRepo.GetMessage(ctx, req.MessageID, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get message: %w", err)
	}

	// Check if user owns the message
	if message.SenderID != userID {
		return nil, domain.ErrForbidden
	}

	// Check if message is within 5-minute edit window
	editWindow := 5 * time.Minute
	if time.Since(message.CreatedAt) > editWindow {
		return nil, fmt.Errorf("message edit window expired (5 minutes): %w", domain.ErrBadRequest)
	}

	// Update the message content
	now := time.Now()
	message.Content = req.Content
	message.UpdatedAt = now

	// For secure messages, we'd need to re-encrypt the content here
	// For now, just update regular messages
	if message.EncryptedContent != nil {
		return nil, fmt.Errorf("editing encrypted messages not supported yet: %w", domain.ErrBadRequest)
	}

	// Update in database (need to add this method to repository)
	// For now, return the updated message - repository implementation needed
	return message, nil
}
