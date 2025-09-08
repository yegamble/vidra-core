package usecase

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"athena/internal/domain"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	testifymock "github.com/stretchr/testify/mock"
)

// MockMessageRepository is a mock implementation of MessageRepository
type MockMessageRepository struct {
	testifymock.Mock
}

func (m *MockMessageRepository) CreateMessage(ctx context.Context, message *domain.Message) error {
	args := m.Called(ctx, message)
	return args.Error(0)
}

func (m *MockMessageRepository) GetMessage(ctx context.Context, messageID string, userID string) (*domain.Message, error) {
	args := m.Called(ctx, messageID, userID)
	return args.Get(0).(*domain.Message), args.Error(1)
}

func (m *MockMessageRepository) GetMessages(ctx context.Context, userID string, otherUserID string, limit, offset int) ([]*domain.Message, error) {
	args := m.Called(ctx, userID, otherUserID, limit, offset)
	return args.Get(0).([]*domain.Message), args.Error(1)
}

func (m *MockMessageRepository) MarkMessageAsRead(ctx context.Context, messageID string, userID string) error {
	args := m.Called(ctx, messageID, userID)
	return args.Error(0)
}

func (m *MockMessageRepository) DeleteMessage(ctx context.Context, messageID string, userID string) error {
	args := m.Called(ctx, messageID, userID)
	return args.Error(0)
}

func (m *MockMessageRepository) GetConversations(ctx context.Context, userID string, limit, offset int) ([]*domain.Conversation, error) {
	args := m.Called(ctx, userID, limit, offset)
	return args.Get(0).([]*domain.Conversation), args.Error(1)
}

func (m *MockMessageRepository) GetUnreadCount(ctx context.Context, userID string) (int, error) {
	args := m.Called(ctx, userID)
	return args.Int(0), args.Error(1)
}

// MockUserRepository is a mock implementation of UserRepository for testing
type MockUserRepository struct {
	testifymock.Mock
}

func (m *MockUserRepository) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	args := m.Called(ctx, user, passwordHash)
	return args.Error(0)
}

func (m *MockUserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	return args.Get(0).(*domain.User), args.Error(1)
}

func (m *MockUserRepository) Update(ctx context.Context, user *domain.User) error {
	args := m.Called(ctx, user)
	return args.Error(0)
}

func (m *MockUserRepository) Delete(ctx context.Context, id string) error {
	args := m.Called(ctx, id)
	return args.Error(0)
}

func (m *MockUserRepository) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}

func (m *MockUserRepository) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	args := m.Called(ctx, userID, passwordHash)
	return args.Error(0)
}

func (m *MockUserRepository) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	return args.Get(0).([]*domain.User), args.Error(1)
}

func (m *MockUserRepository) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockUserRepository) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	args := m.Called(ctx, userID, ipfsCID, webpCID)
	return args.Error(0)
}

func (m *MockUserRepository) MarkEmailAsVerified(ctx context.Context, userID string) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func TestMessageService_SendMessage(t *testing.T) {
	mockMessageRepo := new(MockMessageRepository)
	mockUserRepo := new(MockUserRepository)
	service := NewMessageService(mockMessageRepo, mockUserRepo)

	ctx := context.Background()
	senderID := uuid.New().String()
	recipientID := uuid.New().String()

	sender := &domain.User{
		ID:       senderID,
		Username: "sender",
		Email:    "sender@test.com",
	}

	recipient := &domain.User{
		ID:       recipientID,
		Username: "recipient",
		Email:    "recipient@test.com",
	}

	t.Run("successful message send", func(t *testing.T) {
		req := &domain.SendMessageRequest{
			RecipientID: recipientID,
			Content:     "Hello, this is a test message!",
		}

		mockUserRepo.On("GetByID", ctx, senderID).Return(sender, nil)
		mockUserRepo.On("GetByID", ctx, recipientID).Return(recipient, nil)
		mockMessageRepo.On("CreateMessage", ctx, testifymock.AnythingOfType("*domain.Message")).Return(nil)

		message, err := service.SendMessage(ctx, senderID, req)

		assert.NoError(t, err)
		assert.NotNil(t, message)
		assert.Equal(t, senderID, message.SenderID)
		assert.Equal(t, recipientID, message.RecipientID)
		assert.Equal(t, req.Content, message.Content)
		assert.Equal(t, domain.MessageTypeText, message.MessageType)
		assert.False(t, message.IsRead)

		mockUserRepo.AssertExpectations(t)
		mockMessageRepo.AssertExpectations(t)
	})

	t.Run("cannot send message to self", func(t *testing.T) {
		req := &domain.SendMessageRequest{
			RecipientID: senderID,
			Content:     "Hello, myself!",
		}

		_, err := service.SendMessage(ctx, senderID, req)

		assert.Error(t, err)
		assert.Equal(t, domain.ErrCannotMessageSelf, err)
	})

	t.Run("message too long", func(t *testing.T) {
		req := &domain.SendMessageRequest{
			RecipientID: recipientID,
			Content:     string(make([]byte, 2001)), // Exceeds 2000 character limit
		}

		mockUserRepo.On("GetByID", ctx, senderID).Return(sender, nil)
		mockUserRepo.On("GetByID", ctx, recipientID).Return(recipient, nil)

		_, err := service.SendMessage(ctx, senderID, req)

		assert.Error(t, err)
		assert.Equal(t, domain.ErrMessageTooLong, err)

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("sender not found", func(t *testing.T) {
		// Reset mocks for this test
		mockUserRepo = new(MockUserRepository)
		mockMessageRepo = new(MockMessageRepository)
		service = NewMessageService(mockMessageRepo, mockUserRepo)

		req := &domain.SendMessageRequest{
			RecipientID: recipientID,
			Content:     "Hello!",
		}

		mockUserRepo.On("GetByID", ctx, senderID).Return((*domain.User)(nil), domain.ErrUserNotFound)

		_, err := service.SendMessage(ctx, senderID, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get sender")

		mockUserRepo.AssertExpectations(t)
	})

	t.Run("recipient not found", func(t *testing.T) {
		// Reset mocks for this test
		mockUserRepo = new(MockUserRepository)
		mockMessageRepo = new(MockMessageRepository)
		service = NewMessageService(mockMessageRepo, mockUserRepo)

		req := &domain.SendMessageRequest{
			RecipientID: recipientID,
			Content:     "Hello!",
		}

		mockUserRepo.On("GetByID", ctx, senderID).Return(sender, nil)
		mockUserRepo.On("GetByID", ctx, recipientID).Return((*domain.User)(nil), domain.ErrUserNotFound)

		_, err := service.SendMessage(ctx, senderID, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get recipient")

		mockUserRepo.AssertExpectations(t)
	})
}

func TestMessageService_GetMessages(t *testing.T) {
	mockMessageRepo := new(MockMessageRepository)
	mockUserRepo := new(MockUserRepository)
	service := NewMessageService(mockMessageRepo, mockUserRepo)

	ctx := context.Background()
	userID := uuid.New().String()
	otherUserID := uuid.New().String()

	otherUser := &domain.User{
		ID:       otherUserID,
		Username: "other",
		Email:    "other@test.com",
	}

	t.Run("successful get messages", func(t *testing.T) {
		req := &domain.GetMessagesRequest{
			ConversationWith: otherUserID,
			Limit:            10,
			Offset:           0,
		}

		messages := []*domain.Message{
			{
				ID:          uuid.New().String(),
				SenderID:    userID,
				RecipientID: otherUserID,
				Content:     "Hello!",
				CreatedAt:   time.Now(),
			},
		}

		mockUserRepo.On("GetByID", ctx, otherUserID).Return(otherUser, nil)
		mockMessageRepo.On("GetMessages", ctx, userID, otherUserID, 11, 0).Return(messages, nil)

		response, err := service.GetMessages(ctx, userID, req)

		assert.NoError(t, err)
		assert.NotNil(t, response)
		assert.Len(t, response.Messages, 1)
		assert.Equal(t, messages[0].Content, response.Messages[0].Content)
		assert.False(t, response.HasMore)

		mockUserRepo.AssertExpectations(t)
		mockMessageRepo.AssertExpectations(t)
	})

	t.Run("conversation partner not found", func(t *testing.T) {
		// Reset mocks for this test
		mockUserRepo = new(MockUserRepository)
		mockMessageRepo = new(MockMessageRepository)
		service = NewMessageService(mockMessageRepo, mockUserRepo)

		req := &domain.GetMessagesRequest{
			ConversationWith: otherUserID,
		}

		mockUserRepo.On("GetByID", ctx, otherUserID).Return((*domain.User)(nil), domain.ErrUserNotFound)

		_, err := service.GetMessages(ctx, userID, req)

		assert.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get conversation partner")

		mockUserRepo.AssertExpectations(t)
	})
}

func TestMessageService_GetUnreadCount(t *testing.T) {
	mockMessageRepo := new(MockMessageRepository)
	mockUserRepo := new(MockUserRepository)
	service := NewMessageService(mockMessageRepo, mockUserRepo)

	ctx := context.Background()
	userID := uuid.New().String()

	t.Run("successful get unread count", func(t *testing.T) {
		expectedCount := 5

		mockMessageRepo.On("GetUnreadCount", ctx, userID).Return(expectedCount, nil)

		count, err := service.GetUnreadCount(ctx, userID)

		assert.NoError(t, err)
		assert.Equal(t, expectedCount, count)

		mockMessageRepo.AssertExpectations(t)
	})
}
