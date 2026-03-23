package usecase

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/domain"
)

type MockCryptoRepository struct {
	mock.Mock
}

func (m *MockCryptoRepository) CreateKeyExchangeMessage(ctx context.Context, tx *sqlx.Tx, msg *domain.KeyExchangeMessage) error {
	return m.Called(ctx, tx, msg).Error(0)
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
	return m.Called(ctx, tx, messageID).Error(0)
}

func (m *MockCryptoRepository) CreateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error {
	return m.Called(ctx, tx, key).Error(0)
}

func (m *MockCryptoRepository) GetUserSigningKey(ctx context.Context, userID string) (*domain.UserSigningKey, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.UserSigningKey), args.Error(1)
}

func (m *MockCryptoRepository) UpdateUserSigningKey(ctx context.Context, tx *sqlx.Tx, key *domain.UserSigningKey) error {
	return m.Called(ctx, tx, key).Error(0)
}

func (m *MockCryptoRepository) CreateAuditLog(ctx context.Context, auditLog *domain.CryptoAuditLog) error {
	return m.Called(ctx, auditLog).Error(0)
}

func (m *MockCryptoRepository) WithTransaction(ctx context.Context, fn func(*sqlx.Tx) error) error {
	return fn(nil)
}

type MockE2EEMessageRepository struct {
	mock.Mock
}

func (m *MockE2EEMessageRepository) CreateEncryptedMessage(ctx context.Context, message *domain.Message) error {
	return m.Called(ctx, message).Error(0)
}

func (m *MockE2EEMessageRepository) GetEncryptedMessages(ctx context.Context, p1, p2 string, limit, offset int) ([]*domain.Message, error) {
	args := m.Called(ctx, p1, p2, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Message), args.Error(1)
}

func (m *MockE2EEMessageRepository) GetMessage(ctx context.Context, messageID string, userID string) (*domain.Message, error) {
	args := m.Called(ctx, messageID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Message), args.Error(1)
}

type MockConversationRepository struct {
	mock.Mock
}

func (m *MockConversationRepository) GetOrCreateConversation(ctx context.Context, tx *sqlx.Tx, p1, p2 string) (*domain.Conversation, error) {
	args := m.Called(ctx, tx, p1, p2)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Conversation), args.Error(1)
}

func (m *MockConversationRepository) GetConversation(ctx context.Context, conversationID string) (*domain.Conversation, error) {
	args := m.Called(ctx, conversationID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Conversation), args.Error(1)
}

func (m *MockConversationRepository) UpdateEncryptionStatus(ctx context.Context, tx *sqlx.Tx, conversationID string, status string) error {
	return m.Called(ctx, tx, conversationID, status).Error(0)
}

func newTestE2EEService() (*E2EEService, *MockCryptoRepository, *MockE2EEMessageRepository, *MockConversationRepository) {
	cryptoRepo := &MockCryptoRepository{}
	msgRepo := &MockE2EEMessageRepository{}
	convRepo := &MockConversationRepository{}
	svc := NewE2EEService(cryptoRepo, msgRepo, convRepo, nil)
	return svc, cryptoRepo, msgRepo, convRepo
}

func strPtrE2EE(s string) *string { return &s }

func TestE2EEService_RegisterIdentityKey_CreatesNew(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()

	cryptoRepo.On("GetUserSigningKey", ctx, userID).Return(nil, domain.ErrNotFound)
	cryptoRepo.On("CreateUserSigningKey", ctx, mock.Anything, mock.MatchedBy(func(k *domain.UserSigningKey) bool {
		return k.UserID == userID && k.KeyVersion == 1
	})).Return(nil)
	cryptoRepo.On("CreateAuditLog", ctx, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

	err := svc.RegisterIdentityKey(ctx, userID, "pub-identity-key", "pub-signing-key", "127.0.0.1", "test-agent")
	require.NoError(t, err)
	cryptoRepo.AssertExpectations(t)
}

func TestE2EEService_RegisterIdentityKey_UpdatesExisting(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()
	identityKey := "old-identity-key"

	existing := &domain.UserSigningKey{
		UserID:            userID,
		PublicKey:         "old-signing-key",
		PublicIdentityKey: &identityKey,
		KeyVersion:        1,
	}
	cryptoRepo.On("GetUserSigningKey", ctx, userID).Return(existing, nil)
	cryptoRepo.On("UpdateUserSigningKey", ctx, mock.Anything, mock.MatchedBy(func(k *domain.UserSigningKey) bool {
		return k.KeyVersion == 2
	})).Return(nil)
	cryptoRepo.On("CreateAuditLog", ctx, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

	err := svc.RegisterIdentityKey(ctx, userID, "new-identity-key", "new-signing-key", "127.0.0.1", "agent")
	require.NoError(t, err)
	cryptoRepo.AssertExpectations(t)
}

func TestE2EEService_RegisterIdentityKey_RepoError(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()

	cryptoRepo.On("GetUserSigningKey", ctx, userID).Return(nil, errors.New("connection error"))
	cryptoRepo.On("CreateUserSigningKey", ctx, mock.Anything, mock.Anything).Return(errors.New("db error"))
	cryptoRepo.On("CreateAuditLog", ctx, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

	err := svc.RegisterIdentityKey(ctx, userID, "pub-identity-key", "pub-signing-key", "127.0.0.1", "agent")
	assert.Error(t, err)
}

func TestE2EEService_GetPublicKeys_Success(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()
	identityKey := "identity-pub"

	sigKey := &domain.UserSigningKey{
		UserID:            userID,
		PublicKey:         "signing-pub",
		PublicIdentityKey: &identityKey,
		KeyVersion:        2,
		CreatedAt:         time.Now(),
	}
	cryptoRepo.On("GetUserSigningKey", ctx, userID).Return(sigKey, nil)

	bundle, err := svc.GetPublicKeys(ctx, userID)
	require.NoError(t, err)
	assert.Equal(t, "identity-pub", bundle.PublicIdentityKey)
	assert.Equal(t, "signing-pub", bundle.PublicSigningKey)
	assert.Equal(t, 2, bundle.KeyVersion)
}

func TestE2EEService_GetPublicKeys_NotFound(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()

	cryptoRepo.On("GetUserSigningKey", ctx, userID).Return(nil, domain.ErrNotFound)

	_, err := svc.GetPublicKeys(ctx, userID)
	assert.Error(t, err)
}

func TestE2EEService_GetE2EEStatus_KeysRegistered(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()
	identityKey := "identity-pub"

	sigKey := &domain.UserSigningKey{
		UserID:            userID,
		PublicIdentityKey: &identityKey,
		KeyVersion:        1,
		CreatedAt:         time.Now(),
	}
	cryptoRepo.On("GetUserSigningKey", ctx, userID).Return(sigKey, nil)

	status, err := svc.GetE2EEStatus(ctx, userID)
	require.NoError(t, err)
	assert.True(t, status.HasIdentityKey)
	assert.Equal(t, 1, status.KeyVersion)
}

func TestE2EEService_GetE2EEStatus_NoKeys(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()

	cryptoRepo.On("GetUserSigningKey", ctx, userID).Return(nil, domain.ErrNotFound)

	status, err := svc.GetE2EEStatus(ctx, userID)
	require.NoError(t, err)
	assert.False(t, status.HasIdentityKey)
}

func TestE2EEService_InitiateKeyExchange_Success(t *testing.T) {
	svc, cryptoRepo, _, convRepo := newTestE2EEService()
	ctx := context.Background()
	senderID := uuid.New().String()
	recipientID := uuid.New().String()
	convID := uuid.New().String()

	conv := &domain.Conversation{ID: convID}
	convRepo.On("GetOrCreateConversation", ctx, mock.Anything, senderID, recipientID).Return(conv, nil)
	convRepo.On("UpdateEncryptionStatus", ctx, mock.Anything, convID, domain.EncryptionStatusPending).Return(nil)
	cryptoRepo.On("CreateKeyExchangeMessage", ctx, mock.Anything, mock.MatchedBy(func(km *domain.KeyExchangeMessage) bool {
		return km.SenderID == senderID &&
			km.RecipientID == recipientID &&
			km.ConversationID == convID &&
			km.ExchangeType == domain.KeyExchangeTypeOffer
	})).Return(nil)
	cryptoRepo.On("CreateAuditLog", ctx, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

	km, err := svc.InitiateKeyExchange(ctx, senderID, recipientID, "sender-pub-key", "127.0.0.1", "agent")
	require.NoError(t, err)
	assert.Equal(t, senderID, km.SenderID)
	assert.Equal(t, recipientID, km.RecipientID)
	assert.Equal(t, "sender-pub-key", km.PublicKey)
}

func TestE2EEService_InitiateKeyExchange_ConversationError(t *testing.T) {
	svc, cryptoRepo, _, convRepo := newTestE2EEService()
	ctx := context.Background()

	convRepo.On("GetOrCreateConversation", ctx, mock.Anything, mock.Anything, mock.Anything).
		Return(nil, errors.New("db error"))
	cryptoRepo.On("CreateAuditLog", ctx, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

	_, err := svc.InitiateKeyExchange(ctx, uuid.New().String(), uuid.New().String(), "pub-key", "127.0.0.1", "agent")
	assert.Error(t, err)
}

func TestE2EEService_AcceptKeyExchange_Success(t *testing.T) {
	svc, cryptoRepo, _, convRepo := newTestE2EEService()
	ctx := context.Background()
	recipientID := uuid.New().String()
	exchangeID := uuid.New().String()
	convID := uuid.New().String()

	km := &domain.KeyExchangeMessage{
		ID:             exchangeID,
		RecipientID:    recipientID,
		ConversationID: convID,
		ExchangeType:   domain.KeyExchangeTypeOffer,
		ExpiresAt:      time.Now().Add(time.Hour),
	}
	cryptoRepo.On("GetKeyExchangeMessage", ctx, exchangeID).Return(km, nil)
	cryptoRepo.On("DeleteKeyExchangeMessage", ctx, mock.Anything, exchangeID).Return(nil)
	cryptoRepo.On("CreateKeyExchangeMessage", ctx, mock.Anything, mock.Anything).Return(nil)
	convRepo.On("UpdateEncryptionStatus", ctx, mock.Anything, convID, domain.EncryptionStatusActive).Return(nil)
	cryptoRepo.On("CreateAuditLog", ctx, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

	err := svc.AcceptKeyExchange(ctx, exchangeID, recipientID, "recipient-pub-key", "127.0.0.1", "agent")
	require.NoError(t, err)
}

func TestE2EEService_AcceptKeyExchange_NotFound(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()

	cryptoRepo.On("GetKeyExchangeMessage", ctx, "bad-id").Return(nil, domain.ErrNotFound)

	err := svc.AcceptKeyExchange(ctx, "bad-id", uuid.New().String(), "key", "127.0.0.1", "agent")
	assert.Error(t, err)
}

func TestE2EEService_AcceptKeyExchange_WrongRecipient_Forbidden(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	exchangeID := uuid.New().String()

	km := &domain.KeyExchangeMessage{
		ID:          exchangeID,
		RecipientID: uuid.New().String(),
		ExpiresAt:   time.Now().Add(time.Hour),
	}
	cryptoRepo.On("GetKeyExchangeMessage", ctx, exchangeID).Return(km, nil)

	err := svc.AcceptKeyExchange(ctx, exchangeID, "not-the-recipient", "key", "127.0.0.1", "agent")
	assert.ErrorIs(t, err, domain.ErrForbidden)
}

func TestE2EEService_AcceptKeyExchange_Expired(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	recipientID := uuid.New().String()
	exchangeID := uuid.New().String()

	km := &domain.KeyExchangeMessage{
		ID:          exchangeID,
		RecipientID: recipientID,
		ExpiresAt:   time.Now().Add(-time.Hour),
	}
	cryptoRepo.On("GetKeyExchangeMessage", ctx, exchangeID).Return(km, nil)

	err := svc.AcceptKeyExchange(ctx, exchangeID, recipientID, "key", "127.0.0.1", "agent")
	assert.Error(t, err)
}

func TestE2EEService_GetPendingKeyExchanges_ReturnsList(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()

	exchanges := []*domain.KeyExchangeMessage{
		{ID: uuid.New().String(), RecipientID: userID},
		{ID: uuid.New().String(), RecipientID: userID},
	}
	cryptoRepo.On("GetPendingKeyExchanges", ctx, userID).Return(exchanges, nil)

	result, err := svc.GetPendingKeyExchanges(ctx, userID)
	require.NoError(t, err)
	assert.Len(t, result, 2)
}

func TestE2EEService_GetPendingKeyExchanges_RepoError(t *testing.T) {
	svc, cryptoRepo, _, _ := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()

	cryptoRepo.On("GetPendingKeyExchanges", ctx, userID).Return(nil, errors.New("db error"))

	_, err := svc.GetPendingKeyExchanges(ctx, userID)
	assert.Error(t, err)
}

func TestE2EEService_StoreEncryptedMessage_Success(t *testing.T) {
	svc, cryptoRepo, msgRepo, convRepo := newTestE2EEService()
	ctx := context.Background()
	senderID := uuid.New().String()
	recipientID := uuid.New().String()
	convID := uuid.New().String()

	conv := &domain.Conversation{
		ID:               convID,
		EncryptionStatus: domain.EncryptionStatusActive,
	}
	convRepo.On("GetOrCreateConversation", ctx, (*sqlx.Tx)(nil), senderID, recipientID).Return(conv, nil)
	msgRepo.On("CreateEncryptedMessage", ctx, mock.AnythingOfType("*domain.Message")).Return(nil)
	cryptoRepo.On("CreateAuditLog", ctx, mock.AnythingOfType("*domain.CryptoAuditLog")).Return(nil)

	req := &domain.StoreEncryptedMessageRequest{
		RecipientID:      recipientID,
		EncryptedContent: "base64-ciphertext",
		ContentNonce:     "nonce123",
		Signature:        "sig456",
	}

	msg, err := svc.StoreEncryptedMessage(ctx, senderID, req, "127.0.0.1", "agent")
	require.NoError(t, err)
	assert.Equal(t, senderID, msg.SenderID)
	assert.Equal(t, recipientID, msg.RecipientID)
	assert.True(t, msg.IsEncrypted)
	assert.Equal(t, "base64-ciphertext", *msg.EncryptedContent)
}

func TestE2EEService_StoreEncryptedMessage_ConversationNotEncrypted(t *testing.T) {
	svc, _, _, convRepo := newTestE2EEService()
	ctx := context.Background()
	recipientID := uuid.New().String()

	conv := &domain.Conversation{
		ID:               uuid.New().String(),
		EncryptionStatus: domain.EncryptionStatusPending,
	}
	convRepo.On("GetOrCreateConversation", ctx, (*sqlx.Tx)(nil), mock.Anything, recipientID).Return(conv, nil)

	req := &domain.StoreEncryptedMessageRequest{
		RecipientID:      recipientID,
		EncryptedContent: "ciphertext",
		ContentNonce:     "nonce",
		Signature:        "sig",
	}

	_, err := svc.StoreEncryptedMessage(ctx, uuid.New().String(), req, "127.0.0.1", "agent")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "key exchange not complete")
}

func TestE2EEService_GetEncryptedMessages_Success(t *testing.T) {
	svc, _, msgRepo, convRepo := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()
	otherID := uuid.New().String()
	convID := uuid.New().String()

	conv := &domain.Conversation{
		ID:               convID,
		ParticipantOneID: userID,
		ParticipantTwoID: otherID,
		EncryptionStatus: domain.EncryptionStatusActive,
	}
	convRepo.On("GetConversation", ctx, convID).Return(conv, nil)

	msgs := []*domain.Message{
		{
			ID:               uuid.New().String(),
			SenderID:         otherID,
			RecipientID:      userID,
			EncryptedContent: strPtrE2EE("cipher1"),
		},
	}
	msgRepo.On("GetEncryptedMessages", ctx, userID, otherID, 50, 0).Return(msgs, nil)

	result, err := svc.GetEncryptedMessages(ctx, convID, userID, 50, 0)
	require.NoError(t, err)
	assert.Len(t, result, 1)
}

func TestE2EEService_GetEncryptedMessages_NotParticipant(t *testing.T) {
	svc, _, _, convRepo := newTestE2EEService()
	ctx := context.Background()
	outsiderID := uuid.New().String()
	convID := uuid.New().String()

	conv := &domain.Conversation{
		ID:               convID,
		ParticipantOneID: uuid.New().String(),
		ParticipantTwoID: uuid.New().String(),
	}
	convRepo.On("GetConversation", ctx, convID).Return(conv, nil)

	_, err := svc.GetEncryptedMessages(ctx, convID, outsiderID, 50, 0)
	assert.ErrorIs(t, err, domain.ErrForbidden)
}

func TestE2EEService_GetEncryptedMessages_ConversationNotFound(t *testing.T) {
	svc, _, _, convRepo := newTestE2EEService()
	ctx := context.Background()

	convRepo.On("GetConversation", ctx, "bad-id").Return(nil, domain.ErrNotFound)

	_, err := svc.GetEncryptedMessages(ctx, "bad-id", uuid.New().String(), 50, 0)
	assert.Error(t, err)
}

func TestE2EEService_GetEncryptedMessages_LimitClamping(t *testing.T) {
	svc, _, msgRepo, convRepo := newTestE2EEService()
	ctx := context.Background()
	userID := uuid.New().String()
	otherID := uuid.New().String()
	convID := uuid.New().String()

	conv := &domain.Conversation{
		ID:               convID,
		ParticipantOneID: userID,
		ParticipantTwoID: otherID,
	}
	convRepo.On("GetConversation", ctx, convID).Return(conv, nil)
	msgRepo.On("GetEncryptedMessages", ctx, userID, otherID, 50, 0).Return([]*domain.Message{}, nil)

	_, err := svc.GetEncryptedMessages(ctx, convID, userID, 0, 0)
	require.NoError(t, err)
	msgRepo.AssertExpectations(t)
}
