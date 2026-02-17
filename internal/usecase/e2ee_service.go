package usecase

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"athena/internal/domain"
)

type E2EEService struct {
	cryptoRepo       CryptoRepository
	messageRepo      E2EEMessageRepository
	conversationRepo ConversationRepository
	db               *sqlx.DB
}

type CryptoRepository interface {
	CreateKeyExchangeMessage(ctx context.Context, tx *sqlx.Tx, msg *domain.KeyExchangeMessage) error
	GetKeyExchangeMessage(ctx context.Context, messageID string) (*domain.KeyExchangeMessage, error)
	GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error)
	DeleteKeyExchangeMessage(ctx context.Context, tx *sqlx.Tx, messageID string) error

	CreateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error
	GetUserSigningKey(ctx context.Context, userID string) (*domain.UserSigningKey, error)
	UpdateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error

	CreateAuditLog(ctx context.Context, auditLog *domain.CryptoAuditLog) error

	WithTransaction(ctx context.Context, fn func(*sqlx.Tx) error) error
}

type E2EEMessageRepository interface {
	CreateEncryptedMessage(ctx context.Context, message *domain.Message) error
	GetEncryptedMessages(ctx context.Context, participantOneID, participantTwoID string, limit, offset int) ([]*domain.Message, error)
	GetMessage(ctx context.Context, messageID string, userID string) (*domain.Message, error)
}

type ConversationRepository interface {
	GetOrCreateConversation(ctx context.Context, tx *sqlx.Tx, participantOneID, participantTwoID string) (*domain.Conversation, error)
	GetConversation(ctx context.Context, conversationID string) (*domain.Conversation, error)
	UpdateEncryptionStatus(ctx context.Context, tx *sqlx.Tx, conversationID string, status string) error
}

func NewE2EEService(
	cryptoRepo CryptoRepository,
	messageRepo E2EEMessageRepository,
	conversationRepo ConversationRepository,
	db *sqlx.DB,
) *E2EEService {
	return &E2EEService{
		cryptoRepo:       cryptoRepo,
		messageRepo:      messageRepo,
		conversationRepo: conversationRepo,
		db:               db,
	}
}

func (s *E2EEService) RegisterIdentityKey(
	ctx context.Context,
	userID, publicIdentityKey, publicSigningKey string,
	clientIP, userAgent string,
) error {
	err := s.cryptoRepo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
		existing, err := s.cryptoRepo.GetUserSigningKey(ctx, userID)
		if err == nil {
			existing.PublicKey = publicSigningKey
			existing.PublicIdentityKey = &publicIdentityKey
			existing.KeyVersion++
			return s.cryptoRepo.UpdateUserSigningKey(ctx, tx, existing)
		}
		if !errors.Is(err, domain.ErrNotFound) {
			return fmt.Errorf("check existing key: %w", err)
		}

		key := &domain.UserSigningKey{
			UserID:            userID,
			PublicKey:         publicSigningKey,
			PublicIdentityKey: &publicIdentityKey,
			KeyVersion:        1,
			CreatedAt:         time.Now(),
		}
		return s.cryptoRepo.CreateUserSigningKey(ctx, tx, key)
	})
	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return domain.ErrConflict
		}
		s.logAudit(ctx, userID, nil, domain.CryptoOpRegisterKey, false, err.Error(), clientIP, userAgent)
		return fmt.Errorf("register identity key: %w", err)
	}

	s.logAudit(ctx, userID, nil, domain.CryptoOpRegisterKey, true, "", clientIP, userAgent)
	return nil
}

func (s *E2EEService) GetPublicKeys(ctx context.Context, userID string) (*domain.PublicKeyBundle, error) {
	key, err := s.cryptoRepo.GetUserSigningKey(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return nil, domain.ErrNotFound
		}
		return nil, fmt.Errorf("get public keys: %w", err)
	}

	bundle := &domain.PublicKeyBundle{
		PublicSigningKey: key.PublicKey,
		KeyVersion:       key.KeyVersion,
	}
	if key.PublicIdentityKey != nil {
		bundle.PublicIdentityKey = *key.PublicIdentityKey
	}

	return bundle, nil
}

func (s *E2EEService) GetE2EEStatus(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error) {
	key, err := s.cryptoRepo.GetUserSigningKey(ctx, userID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return &domain.E2EEStatusResponse{HasIdentityKey: false}, nil
		}
		return nil, fmt.Errorf("get e2ee status: %w", err)
	}

	hasIdentityKey := key.PublicIdentityKey != nil && *key.PublicIdentityKey != ""
	return &domain.E2EEStatusResponse{
		HasIdentityKey: hasIdentityKey,
		KeyVersion:     key.KeyVersion,
	}, nil
}

func (s *E2EEService) InitiateKeyExchange(
	ctx context.Context,
	senderID, recipientID, senderPublicKey string,
	clientIP, userAgent string,
) (*domain.KeyExchangeMessage, error) {
	var kex *domain.KeyExchangeMessage

	err := s.cryptoRepo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
		conv, err := s.conversationRepo.GetOrCreateConversation(ctx, tx, senderID, recipientID)
		if err != nil {
			return fmt.Errorf("get or create conversation: %w", err)
		}

		now := time.Now()
		kex = &domain.KeyExchangeMessage{
			ID:             uuid.New().String(),
			ConversationID: conv.ID,
			SenderID:       senderID,
			RecipientID:    recipientID,
			ExchangeType:   domain.KeyExchangeTypeOffer,
			PublicKey:      senderPublicKey,
			Nonce:          uuid.New().String(),
			CreatedAt:      now,
			ExpiresAt:      now.Add(1 * time.Hour),
		}

		if err := s.cryptoRepo.CreateKeyExchangeMessage(ctx, tx, kex); err != nil {
			return err
		}

		return s.conversationRepo.UpdateEncryptionStatus(ctx, tx, conv.ID, domain.EncryptionStatusPending)
	})

	if err != nil {
		var pqErr *pq.Error
		if errors.As(err, &pqErr) && pqErr.Code == "23505" {
			return nil, domain.ErrConflict
		}
		s.logAudit(ctx, senderID, nil, domain.CryptoOpKeyExchange, false, err.Error(), clientIP, userAgent)
		return nil, fmt.Errorf("initiate key exchange: %w", err)
	}

	s.logAudit(ctx, senderID, &kex.ConversationID, domain.CryptoOpKeyExchange, true, "", clientIP, userAgent)
	return kex, nil
}

func (s *E2EEService) AcceptKeyExchange(
	ctx context.Context,
	keyExchangeID, userID, recipientPublicKey string,
	clientIP, userAgent string,
) error {
	kex, err := s.cryptoRepo.GetKeyExchangeMessage(ctx, keyExchangeID)
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrNotFound
		}
		return fmt.Errorf("get key exchange: %w", err)
	}

	if kex.RecipientID != userID {
		return domain.ErrForbidden
	}

	if time.Now().After(kex.ExpiresAt) {
		return domain.ErrKeyExchangeExpired
	}

	err = s.cryptoRepo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
		kex.ExchangeType = domain.KeyExchangeTypeAccept
		kex.Signature = recipientPublicKey
		if err := s.cryptoRepo.DeleteKeyExchangeMessage(ctx, tx, keyExchangeID); err != nil {
			return err
		}
		if err := s.cryptoRepo.CreateKeyExchangeMessage(ctx, tx, kex); err != nil {
			return err
		}
		return s.conversationRepo.UpdateEncryptionStatus(ctx, tx, kex.ConversationID, domain.EncryptionStatusActive)
	})

	if err != nil {
		s.logAudit(ctx, userID, &kex.ConversationID, domain.CryptoOpKeyExchange, false, err.Error(), clientIP, userAgent)
		return fmt.Errorf("accept key exchange: %w", err)
	}

	s.logAudit(ctx, userID, &kex.ConversationID, domain.CryptoOpKeyExchange, true, "", clientIP, userAgent)
	return nil
}

func (s *E2EEService) GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error) {
	return s.cryptoRepo.GetPendingKeyExchanges(ctx, userID)
}

func (s *E2EEService) StoreEncryptedMessage(
	ctx context.Context,
	senderID string,
	req *domain.StoreEncryptedMessageRequest,
	clientIP, userAgent string,
) (*domain.Message, error) {
	conv, err := s.conversationRepo.GetOrCreateConversation(ctx, nil, senderID, req.RecipientID)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}
	if conv.EncryptionStatus != domain.EncryptionStatusActive {
		return nil, domain.ErrKeyExchangeNotComplete
	}

	now := time.Now()
	msg := &domain.Message{
		ID:                uuid.New().String(),
		SenderID:          senderID,
		RecipientID:       req.RecipientID,
		Content:           nil,
		MessageType:       domain.MessageTypeSecure,
		IsEncrypted:       true,
		EncryptedContent:  &req.EncryptedContent,
		ContentNonce:      &req.ContentNonce,
		PGPSignature:      &req.Signature,
		EncryptionVersion: 1,
		CreatedAt:         now,
		UpdatedAt:         now,
		ParentMessageID:   req.ParentMessageID,
	}

	if err := s.messageRepo.CreateEncryptedMessage(ctx, msg); err != nil {
		s.logAudit(ctx, senderID, &conv.ID, domain.CryptoOpStoreMessage, false, err.Error(), clientIP, userAgent)
		return nil, fmt.Errorf("store encrypted message: %w", err)
	}

	s.logAudit(ctx, senderID, &conv.ID, domain.CryptoOpStoreMessage, true, "", clientIP, userAgent)
	return msg, nil
}

func (s *E2EEService) GetEncryptedMessages(
	ctx context.Context,
	conversationID, userID string,
	limit, offset int,
) ([]*domain.Message, error) {
	conv, err := s.conversationRepo.GetConversation(ctx, conversationID)
	if err != nil {
		return nil, fmt.Errorf("get conversation: %w", err)
	}

	if conv.ParticipantOneID != userID && conv.ParticipantTwoID != userID {
		return nil, domain.ErrForbidden
	}

	if limit <= 0 || limit > 100 {
		limit = 50
	}

	return s.messageRepo.GetEncryptedMessages(ctx, conv.ParticipantOneID, conv.ParticipantTwoID, limit, offset)
}

func (s *E2EEService) logAudit(
	ctx context.Context,
	userID string,
	conversationID *string,
	operation string,
	success bool,
	errMsg string,
	clientIP, userAgent string,
) {
	entry := &domain.CryptoAuditLog{
		ID:             uuid.New().String(),
		UserID:         userID,
		ConversationID: conversationID,
		Operation:      operation,
		Success:        success,
		CreatedAt:      time.Now(),
	}
	if clientIP != "" {
		entry.ClientIP = &clientIP
	}
	if userAgent != "" {
		entry.UserAgent = &userAgent
	}
	if errMsg != "" {
		entry.ErrorMessage = &errMsg
	}
	if err := s.cryptoRepo.CreateAuditLog(ctx, entry); err != nil {
		log.Printf("WARNING: failed to write crypto audit log for user %s operation %s: %v", userID, operation, err)
	}
}
