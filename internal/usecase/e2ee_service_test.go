package usecase

import (
	"context"
	"crypto/ed25519"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"athena/internal/crypto"
	"athena/internal/domain"
)

// Mock repositories for testing
type MockCryptoRepository struct {
	mock.Mock
}

func (m *MockCryptoRepository) CreateUserMasterKey(ctx context.Context, tx *sqlx.Tx, masterKey *domain.UserMasterKey) error {
	args := m.Called(ctx, tx, masterKey)
	return args.Error(0)
}

func (m *MockCryptoRepository) GetUserMasterKey(ctx context.Context, userID string) (*domain.UserMasterKey, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.UserMasterKey), args.Error(1)
}

func (m *MockCryptoRepository) UpdateUserMasterKey(ctx context.Context, tx *sqlx.Tx, masterKey *domain.UserMasterKey) error {
	args := m.Called(ctx, tx, masterKey)
	return args.Error(0)
}

func (m *MockCryptoRepository) DeleteUserMasterKey(ctx context.Context, tx *sqlx.Tx, userID string) error {
	args := m.Called(ctx, tx, userID)
	return args.Error(0)
}

func (m *MockCryptoRepository) CreateConversationKey(ctx context.Context, tx *sqlx.Tx, key *domain.ConversationKey) error {
	args := m.Called(ctx, tx, key)
	return args.Error(0)
}

func (m *MockCryptoRepository) GetActiveConversationKey(ctx context.Context, conversationID, userID string) (*domain.ConversationKey, error) {
	args := m.Called(ctx, conversationID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.ConversationKey), args.Error(1)
}

func (m *MockCryptoRepository) ListConversationKeys(ctx context.Context, conversationID string) ([]*domain.ConversationKey, error) {
	args := m.Called(ctx, conversationID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.ConversationKey), args.Error(1)
}

func (m *MockCryptoRepository) UpdateConversationKey(ctx context.Context, tx *sqlx.Tx, key *domain.ConversationKey) error {
	args := m.Called(ctx, tx, key)
	return args.Error(0)
}

func (m *MockCryptoRepository) DeactivateConversationKeys(ctx context.Context, tx *sqlx.Tx, conversationID string, excludeKeyVersion int) error {
	args := m.Called(ctx, tx, conversationID, excludeKeyVersion)
	return args.Error(0)
}

func (m *MockCryptoRepository) CreateKeyExchangeMessage(ctx context.Context, tx *sqlx.Tx, msg *domain.KeyExchangeMessage) error {
	args := m.Called(ctx, tx, msg)
	return args.Error(0)
}

func (m *MockCryptoRepository) GetKeyExchangeMessage(ctx context.Context, messageID string) (*domain.KeyExchangeMessage, error) {
	args := m.Called(ctx, messageID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.KeyExchangeMessage), args.Error(1)
}

func (m *MockCryptoRepository) GetPendingKeyExchanges(ctx context.Context, userID string) ([]*domain.KeyExchangeMessage, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.KeyExchangeMessage), args.Error(1)
}

func (m *MockCryptoRepository) DeleteKeyExchangeMessage(ctx context.Context, tx *sqlx.Tx, messageID string) error {
	args := m.Called(ctx, tx, messageID)
	return args.Error(0)
}

func (m *MockCryptoRepository) CreateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error {
	args := m.Called(ctx, tx, key)
	return args.Error(0)
}

func (m *MockCryptoRepository) GetUserSigningKey(ctx context.Context, userID string) (*domain.UserSigningKey, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.UserSigningKey), args.Error(1)
}

func (m *MockCryptoRepository) GetUserPublicSigningKey(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}

func (m *MockCryptoRepository) UpdateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error {
	args := m.Called(ctx, tx, key)
	return args.Error(0)
}

func (m *MockCryptoRepository) CreateAuditLog(ctx context.Context, auditLog *domain.CryptoAuditLog) error {
	args := m.Called(ctx, auditLog)
	return args.Error(0)
}

func (m *MockCryptoRepository) WithTransaction(ctx context.Context, fn func(*sqlx.Tx) error) error {
	args := m.Called(ctx, mock.AnythingOfType("func(*sqlx.Tx) error"))
	if args.Get(0) == nil {
		return fn(nil) // Execute function with nil tx for testing
	}
	return args.Error(0)
}

type MockMessageRepository struct {
	mock.Mock
}

func (m *MockMessageRepository) Create(ctx context.Context, tx *sqlx.Tx, message *domain.Message) error {
	args := m.Called(ctx, tx, message)
	return args.Error(0)
}

func (m *MockMessageRepository) GetByID(ctx context.Context, messageID string) (*domain.Message, error) {
	args := m.Called(ctx, messageID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Message), args.Error(1)
}

func (m *MockMessageRepository) Update(ctx context.Context, tx *sqlx.Tx, message *domain.Message) error {
	args := m.Called(ctx, tx, message)
	return args.Error(0)
}

type MockConversationRepository struct {
	mock.Mock
}

func (m *MockConversationRepository) GetByParticipants(ctx context.Context, participantOneID, participantTwoID string) (*domain.Conversation, error) {
	args := m.Called(ctx, participantOneID, participantTwoID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Conversation), args.Error(1)
}

func (m *MockConversationRepository) Create(ctx context.Context, tx *sqlx.Tx, conversation *domain.Conversation) error {
	args := m.Called(ctx, tx, conversation)
	return args.Error(0)
}

func (m *MockConversationRepository) Update(ctx context.Context, tx *sqlx.Tx, conversation *domain.Conversation) error {
	args := m.Called(ctx, tx, conversation)
	return args.Error(0)
}

// Test helper functions
func setupE2EEService() (*E2EEService, *MockCryptoRepository, *MockMessageRepository, *MockConversationRepository) {
	mockCryptoRepo := new(MockCryptoRepository)
	mockMessageRepo := new(MockMessageRepository)
	mockConversationRepo := new(MockConversationRepository)

	service := &E2EEService{
		cryptoRepo:       mockCryptoRepo,
		messageRepo:      mockMessageRepo,
		conversationRepo: mockConversationRepo,
		cryptoService:    crypto.NewCryptoService(),
		db:               nil, // Not needed for unit tests
	}

	return service, mockCryptoRepo, mockMessageRepo, mockConversationRepo
}

func TestE2EEService_SetupE2EE(t *testing.T) {
	ctx := context.Background()
	userID := "test-user-id"
	password := "test-password-123"
	clientIP := "127.0.0.1"
	userAgent := "test-agent"

	t.Run("successful setup", func(t *testing.T) {
		service, mockCryptoRepo, _, _ := setupE2EEService()

		// Mock no existing key
		mockCryptoRepo.On("GetUserMasterKey", ctx, userID).Return(nil, nil)

		// Mock successful transaction execution
		mockCryptoRepo.On("WithTransaction", ctx, mock.AnythingOfType("func(*sqlx.Tx) error")).Return(nil)

		// Mock audit log creation
		mockCryptoRepo.On("CreateAuditLog", mock.Anything, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

		// Mock key creation calls within transaction
		mockCryptoRepo.On("CreateUserMasterKey", ctx, (*sqlx.Tx)(nil), mock.AnythingOfType("*domain.UserMasterKey")).Return(nil)
		mockCryptoRepo.On("CreateUserSigningKey", ctx, (*sqlx.Tx)(nil), mock.AnythingOfType("*domain.UserSigningKey")).Return(nil)

		err := service.SetupE2EE(ctx, userID, password, clientIP, userAgent)
		assert.NoError(t, err)

		mockCryptoRepo.AssertExpectations(t)
	})

	t.Run("user already has E2EE setup", func(t *testing.T) {
		service, mockCryptoRepo, _, _ := setupE2EEService()

		existingKey := &domain.UserMasterKey{
			UserID:     userID,
			KeyVersion: 1,
		}

		mockCryptoRepo.On("GetUserMasterKey", ctx, userID).Return(existingKey, nil)
		mockCryptoRepo.On("CreateAuditLog", mock.Anything, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

		err := service.SetupE2EE(ctx, userID, password, clientIP, userAgent)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user already has E2EE setup")

		mockCryptoRepo.AssertExpectations(t)
	})
}

func TestE2EEService_UnlockE2EE(t *testing.T) {
	ctx := context.Background()
	userID := "test-user-id"
	password := "test-password-123"
	clientIP := "127.0.0.1"
	userAgent := "test-agent"

	t.Run("successful unlock", func(t *testing.T) {
		service, mockCryptoRepo, _, _ := setupE2EEService()

		// Create test master key with known salt and encrypted key
		cryptoService := crypto.NewCryptoService()
		salt, _ := cryptoService.GenerateSalt()
		passwordDerivedKey, _ := cryptoService.DeriveKeyFromPassword(password, salt)
		
		masterKey := make([]byte, crypto.ChaCha20KeySize)
		crypto.SecureRandom(masterKey)
		
		encryptedMasterKey, _ := cryptoService.EncryptWithMasterKey(masterKey, passwordDerivedKey)

		userMasterKey := &domain.UserMasterKey{
			UserID:             userID,
			EncryptedMasterKey: cryptoService.Base64Encode(encryptedMasterKey.Ciphertext),
			Argon2Salt:         cryptoService.Base64Encode(salt),
			Argon2Memory:       crypto.Argon2Memory,
			Argon2Time:         crypto.Argon2Time,
			Argon2Parallelism:  crypto.Argon2Parallelism,
			KeyVersion:         1,
		}

		mockCryptoRepo.On("GetUserMasterKey", ctx, userID).Return(userMasterKey, nil)
		mockCryptoRepo.On("CreateAuditLog", mock.Anything, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

		err := service.UnlockE2EE(ctx, userID, password, clientIP, userAgent)
		assert.NoError(t, err)
		assert.True(t, service.IsUnlocked(userID))

		mockCryptoRepo.AssertExpectations(t)
	})

	t.Run("user has no E2EE setup", func(t *testing.T) {
		service, mockCryptoRepo, _, _ := setupE2EEService()

		mockCryptoRepo.On("GetUserMasterKey", ctx, userID).Return(nil, nil)
		mockCryptoRepo.On("CreateAuditLog", mock.Anything, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

		err := service.UnlockE2EE(ctx, userID, password, clientIP, userAgent)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "user has no E2EE setup")

		mockCryptoRepo.AssertExpectations(t)
	})

	t.Run("invalid password", func(t *testing.T) {
		service, mockCryptoRepo, _, _ := setupE2EEService()

		// Create test master key with different password
		cryptoService := crypto.NewCryptoService()
		salt, _ := cryptoService.GenerateSalt()
		correctPassword := "correct-password"
		wrongPassword := "wrong-password"
		
		passwordDerivedKey, _ := cryptoService.DeriveKeyFromPassword(correctPassword, salt)
		masterKey := make([]byte, crypto.ChaCha20KeySize)
		crypto.SecureRandom(masterKey)
		encryptedMasterKey, _ := cryptoService.EncryptWithMasterKey(masterKey, passwordDerivedKey)

		userMasterKey := &domain.UserMasterKey{
			UserID:             userID,
			EncryptedMasterKey: cryptoService.Base64Encode(encryptedMasterKey.Ciphertext),
			Argon2Salt:         cryptoService.Base64Encode(salt),
			KeyVersion:         1,
		}

		mockCryptoRepo.On("GetUserMasterKey", ctx, userID).Return(userMasterKey, nil)
		mockCryptoRepo.On("CreateAuditLog", mock.Anything, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

		err := service.UnlockE2EE(ctx, userID, wrongPassword, clientIP, userAgent)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "invalid password")

		mockCryptoRepo.AssertExpectations(t)
	})
}

func TestE2EEService_LockE2EE(t *testing.T) {
	ctx := context.Background()
	userID := "test-user-id"

	service, _, _, _ := setupE2EEService()

	// Setup session
	masterKey := make([]byte, crypto.ChaCha20KeySize)
	crypto.SecureRandom(masterKey)
	
	session := &UserE2EESession{
		UserID:        userID,
		MasterKey:     masterKey,
		IsUnlocked:    true,
		UnlockedAt:    time.Now(),
		SessionExpiry: time.Now().Add(24 * time.Hour),
	}
	
	userSessions[userID] = session

	assert.True(t, service.IsUnlocked(userID))

	service.LockE2EE(ctx, userID)

	assert.False(t, service.IsUnlocked(userID))
}

func TestE2EEService_InitiateKeyExchange(t *testing.T) {
	ctx := context.Background()
	senderID := "sender-user-id"
	recipientID := "recipient-user-id"
	clientIP := "127.0.0.1"
	userAgent := "test-agent"

	t.Run("successful key exchange initiation", func(t *testing.T) {
		service, mockCryptoRepo, _, mockConversationRepo := setupE2EEService()

		// Setup unlocked session
		masterKey := make([]byte, crypto.ChaCha20KeySize)
		crypto.SecureRandom(masterKey)
		
		session := &UserE2EESession{
			UserID:        senderID,
			MasterKey:     masterKey,
			IsUnlocked:    true,
			UnlockedAt:    time.Now(),
			SessionExpiry: time.Now().Add(24 * time.Hour),
		}
		userSessions[senderID] = session

		// Mock conversation
		conversation := &domain.Conversation{
			ID:                  uuid.New().String(),
			ParticipantOneID:    senderID,
			ParticipantTwoID:    recipientID,
			IsEncrypted:         false,
			KeyExchangeComplete: false,
		}

		mockConversationRepo.On("GetByParticipants", ctx, senderID, recipientID).Return(conversation, nil)

		// Mock transaction and operations
		mockCryptoRepo.On("WithTransaction", ctx, mock.AnythingOfType("func(*sqlx.Tx) error")).Return(nil)
		
		// Setup signing key for sender
		cryptoService := crypto.NewCryptoService()
		signingKeyPair, _ := cryptoService.GenerateEd25519KeyPair()
		encryptedSigningKey, _ := cryptoService.EncryptWithMasterKey(signingKeyPair.PrivateKey, masterKey)
		
		userSigningKey := &domain.UserSigningKey{
			UserID:              senderID,
			EncryptedPrivateKey: cryptoService.Base64Encode(encryptedSigningKey.Ciphertext),
			PublicKey:           cryptoService.Base64Encode(signingKeyPair.PublicKey),
			KeyVersion:          1,
		}

		mockCryptoRepo.On("GetUserSigningKey", ctx, senderID).Return(userSigningKey, nil)
		mockCryptoRepo.On("CreateConversationKey", ctx, (*sqlx.Tx)(nil), mock.AnythingOfType("*domain.ConversationKey")).Return(nil)
		mockCryptoRepo.On("CreateKeyExchangeMessage", ctx, (*sqlx.Tx)(nil), mock.AnythingOfType("*domain.KeyExchangeMessage")).Return(nil)
		mockConversationRepo.On("Update", ctx, (*sqlx.Tx)(nil), mock.AnythingOfType("*domain.Conversation")).Return(nil)
		mockCryptoRepo.On("CreateAuditLog", mock.Anything, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

		keyExchange, err := service.InitiateKeyExchange(ctx, senderID, recipientID, clientIP, userAgent)
		assert.NoError(t, err)
		assert.NotNil(t, keyExchange)

		mockCryptoRepo.AssertExpectations(t)
		mockConversationRepo.AssertExpectations(t)
	})

	t.Run("sender not unlocked", func(t *testing.T) {
		service, _, _, _ := setupE2EEService()

		// No session setup - user not unlocked
		keyExchange, err := service.InitiateKeyExchange(ctx, senderID, recipientID, clientIP, userAgent)
		assert.Error(t, err)
		assert.Nil(t, keyExchange)
		assert.Contains(t, err.Error(), "sender E2EE session not unlocked")
	})
}

func TestE2EEService_EncryptDecryptMessage(t *testing.T) {
	ctx := context.Background()
	senderID := "sender-user-id"
	recipientID := "recipient-user-id"
	plaintext := "This is a secret message for testing encryption"
	clientIP := "127.0.0.1"
	userAgent := "test-agent"

	t.Run("successful encrypt and decrypt", func(t *testing.T) {
		service, mockCryptoRepo, _, mockConversationRepo := setupE2EEService()

		// Setup crypto service and shared secret
		cryptoService := crypto.NewCryptoService()
		sharedSecret := make([]byte, crypto.ChaCha20KeySize)
		crypto.SecureRandom(sharedSecret)

		// Setup sender session
		senderMasterKey := make([]byte, crypto.ChaCha20KeySize)
		crypto.SecureRandom(senderMasterKey)
		
		senderSession := &UserE2EESession{
			UserID:        senderID,
			MasterKey:     senderMasterKey,
			IsUnlocked:    true,
			UnlockedAt:    time.Now(),
			SessionExpiry: time.Now().Add(24 * time.Hour),
		}
		userSessions[senderID] = senderSession

		// Setup recipient session
		recipientMasterKey := make([]byte, crypto.ChaCha20KeySize)
		crypto.SecureRandom(recipientMasterKey)
		
		recipientSession := &UserE2EESession{
			UserID:        recipientID,
			MasterKey:     recipientMasterKey,
			IsUnlocked:    true,
			UnlockedAt:    time.Now(),
			SessionExpiry: time.Now().Add(24 * time.Hour),
		}
		userSessions[recipientID] = recipientSession

		// Setup conversation
		conversation := &domain.Conversation{
			ID:                  uuid.New().String(),
			ParticipantOneID:    senderID,
			ParticipantTwoID:    recipientID,
			IsEncrypted:         true,
			KeyExchangeComplete: true,
		}

		mockConversationRepo.On("GetByParticipants", ctx, senderID, recipientID).Return(conversation, nil)

		// Setup conversation keys with encrypted shared secret
		encryptedSharedSecretSender, _ := cryptoService.EncryptWithMasterKey(sharedSecret, senderMasterKey)
		encryptedSharedSecretRecipient, _ := cryptoService.EncryptWithMasterKey(sharedSecret, recipientMasterKey)
		
		senderConversationKey := &domain.ConversationKey{
			ID:                    uuid.New().String(),
			ConversationID:        conversation.ID,
			UserID:                senderID,
			EncryptedSharedSecret: &[]string{cryptoService.Base64Encode(encryptedSharedSecretSender.Ciphertext)}[0],
			KeyVersion:            1,
			IsActive:              true,
		}
		
		recipientConversationKey := &domain.ConversationKey{
			ID:                    uuid.New().String(),
			ConversationID:        conversation.ID,
			UserID:                recipientID,
			EncryptedSharedSecret: &[]string{cryptoService.Base64Encode(encryptedSharedSecretRecipient.Ciphertext)}[0],
			KeyVersion:            1,
			IsActive:              true,
		}

		mockCryptoRepo.On("GetActiveConversationKey", ctx, conversation.ID, senderID).Return(senderConversationKey, nil)
		mockCryptoRepo.On("GetActiveConversationKey", ctx, conversation.ID, recipientID).Return(recipientConversationKey, nil)

		// Setup sender signing key
		signingKeyPair, _ := cryptoService.GenerateEd25519KeyPair()
		encryptedSigningKey, _ := cryptoService.EncryptWithMasterKey(signingKeyPair.PrivateKey, senderMasterKey)
		
		senderSigningKey := &domain.UserSigningKey{
			UserID:              senderID,
			EncryptedPrivateKey: cryptoService.Base64Encode(encryptedSigningKey.Ciphertext),
			PublicKey:           cryptoService.Base64Encode(signingKeyPair.PublicKey),
			KeyVersion:          1,
		}

		mockCryptoRepo.On("GetUserSigningKey", ctx, senderID).Return(senderSigningKey, nil)
		mockCryptoRepo.On("GetUserPublicSigningKey", ctx, senderID).Return(cryptoService.Base64Encode(signingKeyPair.PublicKey), nil)
		mockCryptoRepo.On("CreateAuditLog", mock.Anything, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

		// Test encryption
		encryptedMessage, err := service.EncryptMessage(ctx, senderID, recipientID, plaintext, clientIP, userAgent)
		require.NoError(t, err)
		require.NotNil(t, encryptedMessage)
		assert.True(t, encryptedMessage.IsEncrypted)
		assert.NotNil(t, encryptedMessage.EncryptedContent)
		assert.NotNil(t, encryptedMessage.ContentNonce)
		assert.NotNil(t, encryptedMessage.PGPSignature)

		// Test decryption
		decryptedText, err := service.DecryptMessage(ctx, encryptedMessage, recipientID, clientIP, userAgent)
		require.NoError(t, err)
		assert.Equal(t, plaintext, decryptedText)

		mockCryptoRepo.AssertExpectations(t)
		mockConversationRepo.AssertExpectations(t)
	})

	t.Run("encrypt with unlocked session", func(t *testing.T) {
		service, _, _, _ := setupE2EEService()

		// No session setup - user not unlocked
		encryptedMessage, err := service.EncryptMessage(ctx, senderID, recipientID, plaintext, clientIP, userAgent)
		assert.Error(t, err)
		assert.Nil(t, encryptedMessage)
		assert.Contains(t, err.Error(), "sender E2EE session not unlocked")
	})
}

func TestE2EEService_GetE2EEStatus(t *testing.T) {
	ctx := context.Background()
	userID := "test-user-id"

	t.Run("user with E2EE setup and unlocked", func(t *testing.T) {
		service, mockCryptoRepo, _, _ := setupE2EEService()

		// Setup master key
		userMasterKey := &domain.UserMasterKey{
			UserID:     userID,
			KeyVersion: 2,
		}

		mockCryptoRepo.On("GetUserMasterKey", ctx, userID).Return(userMasterKey, nil)

		// Setup session
		masterKey := make([]byte, crypto.ChaCha20KeySize)
		crypto.SecureRandom(masterKey)
		
		session := &UserE2EESession{
			UserID:        userID,
			MasterKey:     masterKey,
			IsUnlocked:    true,
			UnlockedAt:    time.Now(),
			SessionExpiry: time.Now().Add(24 * time.Hour),
		}
		userSessions[userID] = session

		status, err := service.GetE2EEStatus(ctx, userID)
		require.NoError(t, err)
		assert.True(t, status.HasMasterKey)
		assert.True(t, status.IsUnlocked)
		assert.Equal(t, 2, status.KeyVersion)

		mockCryptoRepo.AssertExpectations(t)
	})

	t.Run("user without E2EE setup", func(t *testing.T) {
		service, mockCryptoRepo, _, _ := setupE2EEService()

		mockCryptoRepo.On("GetUserMasterKey", ctx, userID).Return(nil, nil)

		status, err := service.GetE2EEStatus(ctx, userID)
		require.NoError(t, err)
		assert.False(t, status.HasMasterKey)
		assert.False(t, status.IsUnlocked)
		assert.Equal(t, 0, status.KeyVersion)

		mockCryptoRepo.AssertExpectations(t)
	})
}

func TestE2EEService_SessionExpiry(t *testing.T) {
	userID := "test-user-id"
	service, _, _, _ := setupE2EEService()

	// Setup expired session
	masterKey := make([]byte, crypto.ChaCha20KeySize)
	crypto.SecureRandom(masterKey)
	
	expiredSession := &UserE2EESession{
		UserID:        userID,
		MasterKey:     masterKey,
		IsUnlocked:    true,
		UnlockedAt:    time.Now().Add(-25 * time.Hour), // 25 hours ago
		SessionExpiry: time.Now().Add(-1 * time.Hour),  // Expired 1 hour ago
	}
	userSessions[userID] = expiredSession

	// Session should be considered expired and automatically locked
	isUnlocked := service.IsUnlocked(userID)
	assert.False(t, isUnlocked)

	// Session should be removed from memory
	_, exists := userSessions[userID]
	assert.False(t, exists)
}

// Security-focused tests

func TestE2EEService_SecurityValidation(t *testing.T) {
	service, _, _, _ := setupE2EEService()
	cryptoService := crypto.NewCryptoService()

	t.Run("key validation", func(t *testing.T) {
		// Test weak key rejection
		weakKey := make([]byte, crypto.X25519PublicKeySize) // All zeros
		err := cryptoService.ValidatePublicKey(weakKey)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "weak public key")

		// Test valid key acceptance
		keyPair, _ := cryptoService.GenerateX25519KeyPair()
		err = cryptoService.ValidatePublicKey(keyPair.PublicKey)
		assert.NoError(t, err)
	})

	t.Run("signature validation", func(t *testing.T) {
		// Generate key pair and test message
		keyPair, err := cryptoService.GenerateEd25519KeyPair()
		require.NoError(t, err)

		message := []byte("test message for signature validation")

		// Test valid signature
		signature, err := cryptoService.SignMessage(message, keyPair.PrivateKey)
		require.NoError(t, err)

		valid := cryptoService.VerifySignature(message, signature, keyPair.PublicKey)
		assert.True(t, valid)

		// Test invalid signature with wrong key
		wrongKeyPair, _ := cryptoService.GenerateEd25519KeyPair()
		valid = cryptoService.VerifySignature(message, signature, wrongKeyPair.PublicKey)
		assert.False(t, valid)

		// Test tampered message
		tamperedMessage := []byte("tampered message for signature validation")
		valid = cryptoService.VerifySignature(tamperedMessage, signature, keyPair.PublicKey)
		assert.False(t, valid)

		// Test tampered signature
		tamperedSignature := make([]byte, len(signature))
		copy(tamperedSignature, signature)
		tamperedSignature[0] ^= 1 // Flip a bit
		valid = cryptoService.VerifySignature(message, tamperedSignature, keyPair.PublicKey)
		assert.False(t, valid)
	})

	t.Run("encryption parameter validation", func(t *testing.T) {
		plaintext := []byte("test plaintext")
		key := make([]byte, crypto.ChaCha20KeySize)
		crypto.SecureRandom(key)
		nonce, _ := cryptoService.GenerateNonce()

		// Valid encryption
		ciphertext, err := cryptoService.Encrypt(plaintext, key, nonce)
		assert.NoError(t, err)

		// Test invalid key size
		invalidKey := make([]byte, 16) // Wrong size
		_, err = cryptoService.Encrypt(plaintext, invalidKey, nonce)
		assert.Error(t, err)

		// Test invalid nonce size
		invalidNonce := make([]byte, 12) // Wrong size
		_, err = cryptoService.Encrypt(plaintext, key, invalidNonce)
		assert.Error(t, err)

		// Test successful decryption
		decryptedText, err := cryptoService.Decrypt(ciphertext, key, nonce)
		assert.NoError(t, err)
		assert.Equal(t, plaintext, decryptedText)

		// Test decryption with wrong key
		wrongKey := make([]byte, crypto.ChaCha20KeySize)
		crypto.SecureRandom(wrongKey)
		_, err = cryptoService.Decrypt(ciphertext, wrongKey, nonce)
		assert.Error(t, err)

		// Test decryption with wrong nonce
		wrongNonce, _ := cryptoService.GenerateNonce()
		_, err = cryptoService.Decrypt(ciphertext, key, wrongNonce)
		assert.Error(t, err)
	})

	t.Run("memory security", func(t *testing.T) {
		// Test secure memory zeroing
		sensitiveData := []byte("very sensitive data that must be cleared")
		originalData := make([]byte, len(sensitiveData))
		copy(originalData, sensitiveData)

		cryptoService.ZeroMemory(sensitiveData)

		// All bytes should be zero
		for i, b := range sensitiveData {
			assert.Equal(t, byte(0), b, "Byte at index %d should be zero", i)
		}

		// Should be different from original
		assert.NotEqual(t, originalData, sensitiveData)
	})

	t.Run("constant time comparison", func(t *testing.T) {
		data1 := []byte("same data")
		data2 := []byte("same data")
		data3 := []byte("different data")

		// Same data should compare equal
		assert.True(t, cryptoService.SecureCompare(data1, data2))

		// Different data should not compare equal
		assert.False(t, cryptoService.SecureCompare(data1, data3))

		// Different lengths should not compare equal
		data4 := []byte("same")
		assert.False(t, cryptoService.SecureCompare(data1, data4))
	})
}