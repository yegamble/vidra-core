package usecase

import (
	"context"
	"crypto/ed25519"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"athena/internal/crypto"
	"athena/internal/domain"
)

// E2EEService provides end-to-end encryption services for messaging
type E2EEService struct {
	cryptoRepo       CryptoRepository
	messageRepo      E2EEMessageRepository
	conversationRepo ConversationRepository
	cryptoService    *crypto.CryptoService
	db               *sqlx.DB
}

// CryptoRepository interface for crypto operations
type CryptoRepository interface {
	CreateUserMasterKey(ctx context.Context, tx *sqlx.Tx, masterKey *domain.UserMasterKey) error
	GetUserMasterKey(ctx context.Context, userID string) (*domain.UserMasterKey, error)
	UpdateUserMasterKey(ctx context.Context, tx *sqlx.Tx, masterKey *domain.UserMasterKey) error
	DeleteUserMasterKey(ctx context.Context, tx *sqlx.Tx, userID string) error

	CreateConversationKey(ctx context.Context, tx *sqlx.Tx, key *domain.ConversationKey) error
	GetActiveConversationKey(ctx context.Context, conversationID, userID string) (*domain.ConversationKey, error)
	ListConversationKeys(ctx context.Context, conversationID string) ([]*domain.ConversationKey, error)
	UpdateConversationKey(ctx context.Context, tx *sqlx.Tx, key *domain.ConversationKey) error
	DeactivateConversationKeys(ctx context.Context, tx *sqlx.Tx, conversationID string, excludeKeyVersion int) error

	CreateKeyExchangeMessage(ctx context.Context, tx *sqlx.Tx, msg *domain.KeyExchangeMessage) error
	GetKeyExchangeMessage(ctx context.Context, messageID string) (*domain.KeyExchangeMessage, error)
	GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error)
	DeleteKeyExchangeMessage(ctx context.Context, tx *sqlx.Tx, messageID string) error

	CreateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error
	GetUserSigningKey(ctx context.Context, userID string) (*domain.UserSigningKey, error)
	GetUserPublicSigningKey(ctx context.Context, userID string) (string, error)
	UpdateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error

	CreateAuditLog(ctx context.Context, auditLog *domain.CryptoAuditLog) error
	WithTransaction(ctx context.Context, fn func(*sqlx.Tx) error) error
}

// E2EEMessageRepository interface for E2EE message operations (separate from main MessageRepository)
type E2EEMessageRepository interface {
	Create(ctx context.Context, tx *sqlx.Tx, message *domain.Message) error
	GetByID(ctx context.Context, messageID string) (*domain.Message, error)
	Update(ctx context.Context, tx *sqlx.Tx, message *domain.Message) error
}

// ConversationRepository interface for conversation operations
type ConversationRepository interface {
	GetByParticipants(ctx context.Context, participantOneID, participantTwoID string) (*domain.Conversation, error)
	Create(ctx context.Context, tx *sqlx.Tx, conversation *domain.Conversation) error
	Update(ctx context.Context, tx *sqlx.Tx, conversation *domain.Conversation) error
}

// NewE2EEService creates a new E2EE service
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
		cryptoService:    crypto.NewCryptoService(),
		db:               db,
	}
}

// UserE2EESession represents a user's E2EE session state
type UserE2EESession struct {
	UserID        string
	MasterKey     []byte
	IsUnlocked    bool
	UnlockedAt    time.Time
	SessionExpiry time.Time
}

// In-memory session cache (in production, use Redis)
var userSessions = make(map[string]*UserE2EESession)

// SetupE2EE initializes E2EE for a user with master key
func (s *E2EEService) SetupE2EE(ctx context.Context, userID, password string, clientIP, userAgent string) error {
	// Check if user already has E2EE setup
	existingKey, err := s.cryptoRepo.GetUserMasterKey(ctx, userID)
	if err != nil {
		s.auditLog(ctx, userID, "", domain.CryptoOpKeyGeneration, false, fmt.Sprintf("Failed to check existing key: %v", err), clientIP, userAgent)
		return fmt.Errorf("failed to check existing E2EE setup: %w", err)
	}

	if existingKey != nil {
		s.auditLog(ctx, userID, "", domain.CryptoOpKeyGeneration, false, "User already has E2EE setup", clientIP, userAgent)
		return fmt.Errorf("user already has E2EE setup")
	}

	return s.cryptoRepo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
		// Generate master key
		masterKey := make([]byte, crypto.ChaCha20KeySize)
		if _, err := crypto.SecureRandom(masterKey); err != nil {
			return fmt.Errorf("failed to generate master key: %w", err)
		}

		// Generate salt and derive key from password
		salt, err := s.cryptoService.GenerateSalt()
		if err != nil {
			return fmt.Errorf("failed to generate salt: %w", err)
		}

		passwordDerivedKey, err := s.cryptoService.DeriveKeyFromPassword(password, salt)
		if err != nil {
			return fmt.Errorf("failed to derive key from password: %w", err)
		}

		// Encrypt master key with password-derived key
		encryptedMasterKeyData, err := s.cryptoService.EncryptWithMasterKey(masterKey, passwordDerivedKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt master key: %w", err)
		}

		// Create user master key record
		userMasterKey := &domain.UserMasterKey{
			UserID:             userID,
			EncryptedMasterKey: s.cryptoService.Base64Encode(encryptedMasterKeyData.Ciphertext),
			Argon2Salt:         s.cryptoService.Base64Encode(salt),
			Argon2Memory:       crypto.Argon2Memory,
			Argon2Time:         crypto.Argon2Time,
			Argon2Parallelism:  crypto.Argon2Parallelism,
			KeyVersion:         1,
		}

		err = s.cryptoRepo.CreateUserMasterKey(ctx, tx, userMasterKey)
		if err != nil {
			return fmt.Errorf("failed to create user master key: %w", err)
		}

		// Generate signing key pair
		signingKeyPair, err := s.cryptoService.GenerateEd25519KeyPair()
		if err != nil {
			return fmt.Errorf("failed to generate signing key pair: %w", err)
		}

		// Encrypt signing private key with master key
		encryptedSigningKey, err := s.cryptoService.EncryptWithMasterKey(signingKeyPair.PrivateKey, masterKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt signing private key: %w", err)
		}

		userSigningKey := &domain.UserSigningKey{
			UserID:              userID,
			EncryptedPrivateKey: s.cryptoService.Base64Encode(encryptedSigningKey.Ciphertext),
			PublicKey:           s.cryptoService.Base64Encode(signingKeyPair.PublicKey),
			KeyVersion:          1,
		}

		err = s.cryptoRepo.CreateUserSigningKey(ctx, tx, userSigningKey)
		if err != nil {
			return fmt.Errorf("failed to create user signing key: %w", err)
		}

		// Clear sensitive data
		s.cryptoService.ZeroMemory(masterKey)
		s.cryptoService.ZeroMemory(passwordDerivedKey)
		s.cryptoService.ZeroMemory(signingKeyPair.PrivateKey)

		s.auditLog(ctx, userID, "", domain.CryptoOpKeyGeneration, true, "", clientIP, userAgent)
		return nil
	})
}

// UnlockE2EE unlocks a user's E2EE keys with password
func (s *E2EEService) UnlockE2EE(ctx context.Context, userID, password string, clientIP, userAgent string) error {
	// Get user master key
	userMasterKey, err := s.cryptoRepo.GetUserMasterKey(ctx, userID)
	if err != nil {
		s.auditLog(ctx, userID, "", domain.CryptoOpDecryption, false, fmt.Sprintf("Failed to get master key: %v", err), clientIP, userAgent)
		return fmt.Errorf("failed to get user master key: %w", err)
	}

	if userMasterKey == nil {
		s.auditLog(ctx, userID, "", domain.CryptoOpDecryption, false, "No E2EE setup found", clientIP, userAgent)
		return fmt.Errorf("user has no E2EE setup")
	}

	// Derive key from password
	salt, err := s.cryptoService.Base64Decode(userMasterKey.Argon2Salt)
	if err != nil {
		return fmt.Errorf("failed to decode salt: %w", err)
	}

	passwordDerivedKey, err := s.cryptoService.DeriveKeyFromPassword(password, salt)
	if err != nil {
		return fmt.Errorf("failed to derive key from password: %w", err)
	}

	// Decrypt master key
	encryptedMasterKey, err := s.cryptoService.Base64Decode(userMasterKey.EncryptedMasterKey)
	if err != nil {
		return fmt.Errorf("failed to decode encrypted master key: %w", err)
	}

	encryptedData := &crypto.EncryptedData{
		Ciphertext: encryptedMasterKey,
		Version:    1,
	}

	masterKey, err := s.cryptoService.DecryptWithMasterKey(encryptedData, passwordDerivedKey)
	if err != nil {
		s.auditLog(ctx, userID, "", domain.CryptoOpDecryption, false, "Invalid password", clientIP, userAgent)
		s.cryptoService.ZeroMemory(passwordDerivedKey)
		return fmt.Errorf("invalid password")
	}

	// Create or update user session
	session := &UserE2EESession{
		UserID:        userID,
		MasterKey:     masterKey,
		IsUnlocked:    true,
		UnlockedAt:    time.Now(),
		SessionExpiry: time.Now().Add(24 * time.Hour), // 24 hour session
	}

	userSessions[userID] = session

	// Clear password-derived key
	s.cryptoService.ZeroMemory(passwordDerivedKey)

	s.auditLog(ctx, userID, "", domain.CryptoOpDecryption, true, "", clientIP, userAgent)
	return nil
}

// LockE2EE locks a user's E2EE session
func (s *E2EEService) LockE2EE(ctx context.Context, userID string) {
	if session, exists := userSessions[userID]; exists {
		s.cryptoService.ZeroMemory(session.MasterKey)
		delete(userSessions, userID)
	}
}

// IsUnlocked checks if user's E2EE is unlocked
func (s *E2EEService) IsUnlocked(userID string) bool {
	session, exists := userSessions[userID]
	if !exists {
		return false
	}

	// Check session expiry
	if time.Now().After(session.SessionExpiry) {
		s.LockE2EE(context.Background(), userID)
		return false
	}

	return session.IsUnlocked
}

// InitiateKeyExchange initiates E2EE key exchange for a conversation
func (s *E2EEService) InitiateKeyExchange(ctx context.Context, senderID, recipientID string, clientIP, userAgent string) (*domain.KeyExchangeMessage, error) {
	// Verify sender is unlocked
	if !s.IsUnlocked(senderID) {
		return nil, fmt.Errorf("sender E2EE session not unlocked")
	}

	// Get or create conversation
	conversation, err := s.getOrCreateConversation(ctx, senderID, recipientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	// Check if key exchange already in progress
	if conversation.IsEncrypted && conversation.KeyExchangeComplete {
		return nil, fmt.Errorf("conversation already has E2EE enabled")
	}

	var keyExchange *domain.KeyExchangeMessage
	err = s.cryptoRepo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
		// Generate key pair for this conversation
		keyPair, err := s.cryptoService.GenerateX25519KeyPair()
		if err != nil {
			return fmt.Errorf("failed to generate key pair: %w", err)
		}

		// Encrypt private key with user's master key
		session := userSessions[senderID]
		encryptedPrivateKey, err := s.cryptoService.EncryptWithMasterKey(keyPair.PrivateKey, session.MasterKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt private key: %w", err)
		}

		// Create conversation key record
		conversationKey := &domain.ConversationKey{
			ID:                  uuid.New().String(),
			ConversationID:      conversation.ID,
			UserID:              senderID,
			EncryptedPrivateKey: s.cryptoService.Base64Encode(encryptedPrivateKey.Ciphertext),
			PublicKey:           s.cryptoService.Base64Encode(keyPair.PublicKey),
			KeyVersion:          1,
			IsActive:            true,
		}

		err = s.cryptoRepo.CreateConversationKey(ctx, tx, conversationKey)
		if err != nil {
			return fmt.Errorf("failed to create conversation key: %w", err)
		}

		// Get sender's signing key
		signingKey, err := s.getUserSigningKey(ctx, senderID)
		if err != nil {
			return fmt.Errorf("failed to get signing key: %w", err)
		}

		// Create key exchange message
		keyExchangeMsg := &domain.KeyExchangeMessage{
			ID:             uuid.New().String(),
			ConversationID: conversation.ID,
			SenderID:       senderID,
			RecipientID:    recipientID,
			ExchangeType:   domain.KeyExchangeTypeOffer,
			PublicKey:      s.cryptoService.Base64Encode(keyPair.PublicKey),
			Nonce:          s.cryptoService.Base64Encode([]byte(fmt.Sprintf("%s:%s:%d", senderID, recipientID, time.Now().Unix()))),
			ExpiresAt:      time.Now().Add(1 * time.Hour),
		}

		// Sign the key exchange message
		signatureData := fmt.Sprintf("%s:%s:%s:%s", keyExchangeMsg.ConversationID, keyExchangeMsg.ExchangeType, keyExchangeMsg.PublicKey, keyExchangeMsg.Nonce)
		signature, err := s.cryptoService.SignMessage([]byte(signatureData), signingKey)
		if err != nil {
			return fmt.Errorf("failed to sign key exchange: %w", err)
		}

		keyExchangeMsg.Signature = s.cryptoService.Base64Encode(signature)

		err = s.cryptoRepo.CreateKeyExchangeMessage(ctx, tx, keyExchangeMsg)
		if err != nil {
			return fmt.Errorf("failed to create key exchange message: %w", err)
		}

		// Update conversation to indicate E2EE initiation
		conversation.IsEncrypted = true
		conversation.KeyExchangeComplete = false
		conversation.EncryptionVersion = 1
		err = s.conversationRepo.Update(ctx, tx, conversation)
		if err != nil {
			return fmt.Errorf("failed to update conversation: %w", err)
		}

		// Set the key exchange for return
		keyExchange = keyExchangeMsg

		// Clear sensitive data
		s.cryptoService.ZeroMemory(keyPair.PrivateKey)
		s.cryptoService.ZeroMemory(signingKey)

		s.auditLog(ctx, senderID, conversation.ID, domain.CryptoOpKeyExchange, true, "", clientIP, userAgent)
		return nil
	})

	if err != nil {
		return nil, err
	}

	return keyExchange, nil
}

// AcceptKeyExchange accepts an E2EE key exchange
func (s *E2EEService) AcceptKeyExchange(ctx context.Context, keyExchangeID, userID string, clientIP, userAgent string) error {
	// Verify user is unlocked
	if !s.IsUnlocked(userID) {
		return fmt.Errorf("user E2EE session not unlocked")
	}

	// Get key exchange message
	keyExchange, err := s.cryptoRepo.GetKeyExchangeMessage(ctx, keyExchangeID)
	if err != nil {
		return fmt.Errorf("failed to get key exchange: %w", err)
	}

	if keyExchange == nil {
		return fmt.Errorf("key exchange not found or expired")
	}

	if keyExchange.RecipientID != userID {
		return fmt.Errorf("unauthorized to accept this key exchange")
	}

	if keyExchange.ExchangeType != domain.KeyExchangeTypeOffer {
		return fmt.Errorf("invalid key exchange type for acceptance")
	}

	return s.cryptoRepo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
		// Verify sender's signature
		senderPublicKey, err := s.cryptoRepo.GetUserPublicSigningKey(ctx, keyExchange.SenderID)
		if err != nil {
			return fmt.Errorf("failed to get sender's public key: %w", err)
		}

		senderPubKeyBytes, err := s.cryptoService.Base64Decode(senderPublicKey)
		if err != nil {
			return fmt.Errorf("failed to decode sender public key: %w", err)
		}

		signatureData := fmt.Sprintf("%s:%s:%s:%s", keyExchange.ConversationID, keyExchange.ExchangeType, keyExchange.PublicKey, keyExchange.Nonce)
		signature, err := s.cryptoService.Base64Decode(keyExchange.Signature)
		if err != nil {
			return fmt.Errorf("failed to decode signature: %w", err)
		}

		if !s.cryptoService.VerifySignature([]byte(signatureData), signature, ed25519.PublicKey(senderPubKeyBytes)) {
			s.auditLog(ctx, userID, keyExchange.ConversationID, domain.CryptoOpKeyExchange, false, "Invalid signature", clientIP, userAgent)
			return fmt.Errorf("invalid key exchange signature")
		}

		// Generate recipient's key pair
		recipientKeyPair, err := s.cryptoService.GenerateX25519KeyPair()
		if err != nil {
			return fmt.Errorf("failed to generate recipient key pair: %w", err)
		}

		// Compute shared secret
		senderPublicKeyBytes, err := s.cryptoService.Base64Decode(keyExchange.PublicKey)
		if err != nil {
			return fmt.Errorf("failed to decode sender public key: %w", err)
		}

		sharedSecret, err := s.cryptoService.ComputeSharedSecret(recipientKeyPair.PrivateKey, senderPublicKeyBytes)
		if err != nil {
			return fmt.Errorf("failed to compute shared secret: %w", err)
		}

		// Encrypt keys with user's master key
		session := userSessions[userID]
		encryptedPrivateKey, err := s.cryptoService.EncryptWithMasterKey(recipientKeyPair.PrivateKey, session.MasterKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt private key: %w", err)
		}

		encryptedSharedSecret, err := s.cryptoService.EncryptWithMasterKey(sharedSecret, session.MasterKey)
		if err != nil {
			return fmt.Errorf("failed to encrypt shared secret: %w", err)
		}

		// Create recipient's conversation key
		recipientConversationKey := &domain.ConversationKey{
			ID:                    uuid.New().String(),
			ConversationID:        keyExchange.ConversationID,
			UserID:                userID,
			EncryptedPrivateKey:   s.cryptoService.Base64Encode(encryptedPrivateKey.Ciphertext),
			PublicKey:             s.cryptoService.Base64Encode(recipientKeyPair.PublicKey),
			EncryptedSharedSecret: &[]string{s.cryptoService.Base64Encode(encryptedSharedSecret.Ciphertext)}[0],
			KeyVersion:            1,
			IsActive:              true,
		}

		err = s.cryptoRepo.CreateConversationKey(ctx, tx, recipientConversationKey)
		if err != nil {
			return fmt.Errorf("failed to create recipient conversation key: %w", err)
		}

		// Update sender's conversation key with shared secret
		senderKey, err := s.cryptoRepo.GetActiveConversationKey(ctx, keyExchange.ConversationID, keyExchange.SenderID)
		if err != nil {
			return fmt.Errorf("failed to get sender's conversation key: %w", err)
		}

		senderKey.EncryptedSharedSecret = &[]string{s.cryptoService.Base64Encode(encryptedSharedSecret.Ciphertext)}[0]
		err = s.cryptoRepo.UpdateConversationKey(ctx, tx, senderKey)
		if err != nil {
			return fmt.Errorf("failed to update sender's conversation key: %w", err)
		}

		// Mark key exchange as complete
		conversation, err := s.conversationRepo.GetByParticipants(ctx, keyExchange.SenderID, keyExchange.RecipientID)
		if err != nil {
			return fmt.Errorf("failed to get conversation: %w", err)
		}

		conversation.KeyExchangeComplete = true
		err = s.conversationRepo.Update(ctx, tx, conversation)
		if err != nil {
			return fmt.Errorf("failed to update conversation: %w", err)
		}

		// Delete key exchange message
		err = s.cryptoRepo.DeleteKeyExchangeMessage(ctx, tx, keyExchangeID)
		if err != nil {
			return fmt.Errorf("failed to delete key exchange message: %w", err)
		}

		// Clear sensitive data
		s.cryptoService.ZeroMemory(recipientKeyPair.PrivateKey)
		s.cryptoService.ZeroMemory(sharedSecret)

		s.auditLog(ctx, userID, keyExchange.ConversationID, domain.CryptoOpKeyExchange, true, "", clientIP, userAgent)
		return nil
	})
}

// EncryptMessage encrypts a message for secure transmission
func (s *E2EEService) EncryptMessage(ctx context.Context, senderID, recipientID, plaintext string, clientIP, userAgent string) (*domain.Message, error) {
	// Verify sender is unlocked
	if !s.IsUnlocked(senderID) {
		return nil, fmt.Errorf("sender E2EE session not unlocked")
	}

	// Get conversation
	conversation, err := s.conversationRepo.GetByParticipants(ctx, senderID, recipientID)
	if err != nil {
		return nil, fmt.Errorf("failed to get conversation: %w", err)
	}

	if conversation == nil || !conversation.IsEncrypted || !conversation.KeyExchangeComplete {
		return nil, fmt.Errorf("conversation not ready for E2EE")
	}

	// Get sender's conversation key
	senderKey, err := s.cryptoRepo.GetActiveConversationKey(ctx, conversation.ID, senderID)
	if err != nil {
		return nil, fmt.Errorf("failed to get sender's conversation key: %w", err)
	}

	if senderKey == nil || senderKey.EncryptedSharedSecret == nil {
		return nil, fmt.Errorf("no shared secret available")
	}

	// Decrypt shared secret
	session := userSessions[senderID]
	encryptedSharedSecret, err := s.cryptoService.Base64Decode(*senderKey.EncryptedSharedSecret)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted shared secret: %w", err)
	}

	encryptedData := &crypto.EncryptedData{
		Ciphertext: encryptedSharedSecret,
		Version:    1,
	}

	sharedSecret, err := s.cryptoService.DecryptWithMasterKey(encryptedData, session.MasterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt shared secret: %w", err)
	}

	// Generate nonce and encrypt message
	nonce, err := s.cryptoService.GenerateNonce()
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		return nil, fmt.Errorf("failed to generate nonce: %w", err)
	}

	ciphertext, err := s.cryptoService.Encrypt([]byte(plaintext), sharedSecret, nonce)
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		return nil, fmt.Errorf("failed to encrypt message: %w", err)
	}

	// Sign the encrypted message
	signingKey, err := s.getUserSigningKey(ctx, senderID)
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		return nil, fmt.Errorf("failed to get signing key: %w", err)
	}

	messageToSign := fmt.Sprintf("%s:%s:%s", s.cryptoService.Base64Encode(ciphertext), s.cryptoService.Base64Encode(nonce), recipientID)
	signature, err := s.cryptoService.SignMessage([]byte(messageToSign), signingKey)
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		s.cryptoService.ZeroMemory(signingKey)
		return nil, fmt.Errorf("failed to sign message: %w", err)
	}

	// Create encrypted message
	message := &domain.Message{
		ID:                uuid.New().String(),
		SenderID:          senderID,
		RecipientID:       recipientID,
		EncryptedContent:  &[]string{s.cryptoService.Base64Encode(ciphertext)}[0],
		ContentNonce:      &[]string{s.cryptoService.Base64Encode(nonce)}[0],
		PGPSignature:      &[]string{s.cryptoService.Base64Encode(signature)}[0],
		IsEncrypted:       true,
		EncryptionVersion: 1,
		MessageType:       domain.MessageTypeSecure,
		CreatedAt:         time.Now(),
		UpdatedAt:         time.Now(),
	}

	// Clear sensitive data
	s.cryptoService.ZeroMemory(sharedSecret)
	s.cryptoService.ZeroMemory(signingKey)

	s.auditLog(ctx, senderID, conversation.ID, domain.CryptoOpEncryption, true, "", clientIP, userAgent)
	return message, nil
}

// DecryptMessage decrypts a secure message
func (s *E2EEService) DecryptMessage(ctx context.Context, message *domain.Message, userID string, clientIP, userAgent string) (string, error) {
	// Verify user is unlocked
	if !s.IsUnlocked(userID) {
		return "", fmt.Errorf("user E2EE session not unlocked")
	}

	if !message.IsEncrypted || message.EncryptedContent == nil || message.ContentNonce == nil || message.PGPSignature == nil {
		return "", fmt.Errorf("message is not encrypted")
	}

	// Verify user is authorized to decrypt (sender or recipient)
	if message.SenderID != userID && message.RecipientID != userID {
		return "", fmt.Errorf("unauthorized to decrypt message")
	}

	// Get conversation
	conversation, err := s.conversationRepo.GetByParticipants(ctx, message.SenderID, message.RecipientID)
	if err != nil {
		return "", fmt.Errorf("failed to get conversation: %w", err)
	}

	// Get user's conversation key
	userKey, err := s.cryptoRepo.GetActiveConversationKey(ctx, conversation.ID, userID)
	if err != nil {
		return "", fmt.Errorf("failed to get user's conversation key: %w", err)
	}

	if userKey == nil || userKey.EncryptedSharedSecret == nil {
		return "", fmt.Errorf("no shared secret available for decryption")
	}

	// Decrypt shared secret
	session := userSessions[userID]
	encryptedSharedSecret, err := s.cryptoService.Base64Decode(*userKey.EncryptedSharedSecret)
	if err != nil {
		return "", fmt.Errorf("failed to decode encrypted shared secret: %w", err)
	}

	encryptedData := &crypto.EncryptedData{
		Ciphertext: encryptedSharedSecret,
		Version:    1,
	}

	sharedSecret, err := s.cryptoService.DecryptWithMasterKey(encryptedData, session.MasterKey)
	if err != nil {
		return "", fmt.Errorf("failed to decrypt shared secret: %w", err)
	}

	// Verify message signature
	senderPublicKey, err := s.cryptoRepo.GetUserPublicSigningKey(ctx, message.SenderID)
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		return "", fmt.Errorf("failed to get sender's public key: %w", err)
	}

	senderPubKeyBytes, err := s.cryptoService.Base64Decode(senderPublicKey)
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		return "", fmt.Errorf("failed to decode sender public key: %w", err)
	}

	messageToVerify := fmt.Sprintf("%s:%s:%s", *message.EncryptedContent, *message.ContentNonce, message.RecipientID)
	signature, err := s.cryptoService.Base64Decode(*message.PGPSignature)
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		return "", fmt.Errorf("failed to decode signature: %w", err)
	}

	if !s.cryptoService.VerifySignature([]byte(messageToVerify), signature, ed25519.PublicKey(senderPubKeyBytes)) {
		s.cryptoService.ZeroMemory(sharedSecret)
		s.auditLog(ctx, userID, conversation.ID, domain.CryptoOpDecryption, false, "Invalid message signature", clientIP, userAgent)
		return "", fmt.Errorf("invalid message signature")
	}

	// Decrypt message content
	ciphertext, err := s.cryptoService.Base64Decode(*message.EncryptedContent)
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		return "", fmt.Errorf("failed to decode ciphertext: %w", err)
	}

	nonce, err := s.cryptoService.Base64Decode(*message.ContentNonce)
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		return "", fmt.Errorf("failed to decode nonce: %w", err)
	}

	plaintext, err := s.cryptoService.Decrypt(ciphertext, sharedSecret, nonce)
	if err != nil {
		s.cryptoService.ZeroMemory(sharedSecret)
		s.auditLog(ctx, userID, conversation.ID, domain.CryptoOpDecryption, false, "Decryption failed", clientIP, userAgent)
		return "", fmt.Errorf("failed to decrypt message: %w", err)
	}

	// Clear sensitive data
	s.cryptoService.ZeroMemory(sharedSecret)

	s.auditLog(ctx, userID, conversation.ID, domain.CryptoOpDecryption, true, "", clientIP, userAgent)
	return string(plaintext), nil
}

// Helper methods

func (s *E2EEService) getOrCreateConversation(ctx context.Context, participantOneID, participantTwoID string) (*domain.Conversation, error) {
	conversation, err := s.conversationRepo.GetByParticipants(ctx, participantOneID, participantTwoID)
	if err != nil {
		return nil, err
	}

	if conversation != nil {
		return conversation, nil
	}

	// Create new conversation
	newConversation := &domain.Conversation{
		ID:               uuid.New().String(),
		ParticipantOneID: participantOneID,
		ParticipantTwoID: participantTwoID,
		LastMessageAt:    time.Now(),
		CreatedAt:        time.Now(),
		UpdatedAt:        time.Now(),
	}

	err = s.cryptoRepo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
		return s.conversationRepo.Create(ctx, tx, newConversation)
	})

	if err != nil {
		return nil, err
	}

	return newConversation, nil
}

func (s *E2EEService) getUserSigningKey(ctx context.Context, userID string) (ed25519.PrivateKey, error) {
	session, exists := userSessions[userID]
	if !exists {
		return nil, fmt.Errorf("user session not found")
	}

	userSigningKey, err := s.cryptoRepo.GetUserSigningKey(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get user signing key: %w", err)
	}

	if userSigningKey == nil {
		return nil, fmt.Errorf("user signing key not found")
	}

	encryptedPrivateKey, err := s.cryptoService.Base64Decode(userSigningKey.EncryptedPrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decode encrypted private key: %w", err)
	}

	encryptedData := &crypto.EncryptedData{
		Ciphertext: encryptedPrivateKey,
		Version:    1,
	}

	privateKey, err := s.cryptoService.DecryptWithMasterKey(encryptedData, session.MasterKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt private key: %w", err)
	}

	return ed25519.PrivateKey(privateKey), nil
}

func (s *E2EEService) auditLog(ctx context.Context, userID, conversationID, operation string, success bool, errorMsg, clientIP, userAgent string) {
	auditLog := &domain.CryptoAuditLog{
		ID:        uuid.New().String(),
		UserID:    userID,
		Operation: operation,
		Success:   success,
		CreatedAt: time.Now(),
	}

	if conversationID != "" {
		auditLog.ConversationID = &conversationID
	}

	if errorMsg != "" {
		auditLog.ErrorMessage = &errorMsg
	}

	if clientIP != "" {
		auditLog.ClientIP = &clientIP
	}

	if userAgent != "" {
		auditLog.UserAgent = &userAgent
	}

	// Fire and forget audit logging
	go func() {
		ctx := context.Background()
		s.cryptoRepo.CreateAuditLog(ctx, auditLog)
	}()
}

// GetE2EEStatus returns the E2EE status for a user
func (s *E2EEService) GetE2EEStatus(ctx context.Context, userID string) (*domain.E2EEStatusResponse, error) {
	masterKey, err := s.cryptoRepo.GetUserMasterKey(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("failed to get master key: %w", err)
	}

	status := &domain.E2EEStatusResponse{
		HasMasterKey: masterKey != nil,
		IsUnlocked:   s.IsUnlocked(userID),
	}

	if masterKey != nil {
		status.KeyVersion = masterKey.KeyVersion
	}

	return status, nil
}

// GetPendingKeyExchanges returns pending key exchanges for a user
func (s *E2EEService) GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error) {
	return s.cryptoRepo.GetPendingKeyExchanges(ctx, userID)
}

// SaveSecureMessage saves an encrypted message to the database
func (s *E2EEService) SaveSecureMessage(ctx context.Context, message *domain.Message) error {
	return s.cryptoRepo.WithTransaction(ctx, func(tx *sqlx.Tx) error {
		return s.messageRepo.Create(ctx, tx, message)
	})
}

// GetMessage retrieves a message by ID
func (s *E2EEService) GetMessage(ctx context.Context, messageID string) (*domain.Message, error) {
	return s.messageRepo.GetByID(ctx, messageID)
}
