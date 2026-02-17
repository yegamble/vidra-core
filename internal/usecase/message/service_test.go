package message

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"athena/internal/domain"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

type mockMessageRepo struct{ mock.Mock }

func (m *mockMessageRepo) CreateMessage(ctx context.Context, message *domain.Message) error {
	return m.Called(ctx, message).Error(0)
}
func (m *mockMessageRepo) GetMessage(ctx context.Context, messageID, userID string) (*domain.Message, error) {
	args := m.Called(ctx, messageID, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Message), args.Error(1)
}
func (m *mockMessageRepo) GetMessages(ctx context.Context, userID, otherUserID string, limit, offset int) ([]*domain.Message, error) {
	args := m.Called(ctx, userID, otherUserID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Message), args.Error(1)
}
func (m *mockMessageRepo) MarkMessageAsRead(ctx context.Context, messageID, userID string) error {
	return m.Called(ctx, messageID, userID).Error(0)
}
func (m *mockMessageRepo) DeleteMessage(ctx context.Context, messageID, userID string) error {
	return m.Called(ctx, messageID, userID).Error(0)
}
func (m *mockMessageRepo) GetConversations(ctx context.Context, userID string, limit, offset int) ([]*domain.Conversation, error) {
	args := m.Called(ctx, userID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Conversation), args.Error(1)
}
func (m *mockMessageRepo) GetUnreadCount(ctx context.Context, userID string) (int, error) {
	args := m.Called(ctx, userID)
	return args.Int(0), args.Error(1)
}

type mockUserRepo struct{ mock.Mock }

func (m *mockUserRepo) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *mockUserRepo) Create(ctx context.Context, user *domain.User, passwordHash string) error {
	return m.Called(ctx, user, passwordHash).Error(0)
}
func (m *mockUserRepo) GetByEmail(ctx context.Context, email string) (*domain.User, error) {
	args := m.Called(ctx, email)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *mockUserRepo) GetByUsername(ctx context.Context, username string) (*domain.User, error) {
	args := m.Called(ctx, username)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}
func (m *mockUserRepo) Update(ctx context.Context, user *domain.User) error {
	return m.Called(ctx, user).Error(0)
}
func (m *mockUserRepo) Delete(ctx context.Context, id string) error {
	return m.Called(ctx, id).Error(0)
}
func (m *mockUserRepo) GetPasswordHash(ctx context.Context, userID string) (string, error) {
	args := m.Called(ctx, userID)
	return args.String(0), args.Error(1)
}
func (m *mockUserRepo) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	return m.Called(ctx, userID, passwordHash).Error(0)
}
func (m *mockUserRepo) List(ctx context.Context, limit, offset int) ([]*domain.User, error) {
	args := m.Called(ctx, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.User), args.Error(1)
}
func (m *mockUserRepo) Count(ctx context.Context) (int64, error) {
	args := m.Called(ctx)
	return args.Get(0).(int64), args.Error(1)
}
func (m *mockUserRepo) SetAvatarFields(ctx context.Context, userID string, ipfsCID sql.NullString, webpCID sql.NullString) error {
	return m.Called(ctx, userID, ipfsCID, webpCID).Error(0)
}
func (m *mockUserRepo) MarkEmailAsVerified(ctx context.Context, userID string) error {
	return m.Called(ctx, userID).Error(0)
}

func TestSendMessage_Success(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	sender := &domain.User{ID: "user-1", Username: "alice"}
	recipient := &domain.User{ID: "user-2", Username: "bob"}

	userRepo.On("GetByID", mock.Anything, "user-1").Return(sender, nil)
	userRepo.On("GetByID", mock.Anything, "user-2").Return(recipient, nil)
	msgRepo.On("CreateMessage", mock.Anything, mock.AnythingOfType("*domain.Message")).Return(nil)

	req := &domain.SendMessageRequest{
		RecipientID: "user-2",
		Content:     "Hello Bob!",
	}

	msg, err := svc.SendMessage(context.Background(), "user-1", req)
	assert.NoError(t, err)
	assert.NotNil(t, msg)
	assert.Equal(t, "user-1", msg.SenderID)
	assert.Equal(t, "user-2", msg.RecipientID)
	if assert.NotNil(t, msg.Content) {
		assert.Equal(t, "Hello Bob!", *msg.Content)
	}
	assert.False(t, msg.IsRead)
}

func TestSendMessage_CannotMessageSelf(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	req := &domain.SendMessageRequest{
		RecipientID: "user-1",
		Content:     "Talking to myself",
	}

	msg, err := svc.SendMessage(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, msg)
	assert.ErrorIs(t, err, domain.ErrCannotMessageSelf)
}

func TestSendMessage_RecipientNotFound(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	userRepo.On("GetByID", mock.Anything, "user-1").Return(&domain.User{ID: "user-1"}, nil)
	userRepo.On("GetByID", mock.Anything, "user-2").Return(nil, domain.ErrNotFound)

	req := &domain.SendMessageRequest{
		RecipientID: "user-2",
		Content:     "Hello?",
	}

	msg, err := svc.SendMessage(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, msg)
}

func TestSendMessage_TooLong(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	sender := &domain.User{ID: "user-1", Username: "alice"}
	recipient := &domain.User{ID: "user-2", Username: "bob"}

	userRepo.On("GetByID", mock.Anything, "user-1").Return(sender, nil)
	userRepo.On("GetByID", mock.Anything, "user-2").Return(recipient, nil)

	req := &domain.SendMessageRequest{
		RecipientID: "user-2",
		Content:     strings.Repeat("a", 2001),
	}

	msg, err := svc.SendMessage(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, msg)
	assert.ErrorIs(t, err, domain.ErrMessageTooLong)
}

func TestSendMessage_XSSSanitization(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	sender := &domain.User{ID: "user-1", Username: "alice"}
	recipient := &domain.User{ID: "user-2", Username: "bob"}

	userRepo.On("GetByID", mock.Anything, "user-1").Return(sender, nil)
	userRepo.On("GetByID", mock.Anything, "user-2").Return(recipient, nil)
	msgRepo.On("CreateMessage", mock.Anything, mock.MatchedBy(func(m *domain.Message) bool {
		if m.Content == nil {
			return true
		}
		return !strings.Contains(*m.Content, "<script>") && !strings.Contains(*m.Content, "onerror")
	})).Return(nil)

	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Script tag",
			input:    "Hello <script>alert('xss')</script>Bob",
			expected: "Hello Bob",
		},
		{
			name:     "Img with onerror",
			input:    "Check this <img src=x onerror=alert(1)>",
			expected: "Check this ",
		},
		{
			name:     "HTML elements",
			input:    "<b>Bold</b> <i>Italic</i>",
			expected: "Bold Italic",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := &domain.SendMessageRequest{
				RecipientID: "user-2",
				Content:     tc.input,
			}

			msg, err := svc.SendMessage(context.Background(), "user-1", req)
			assert.NoError(t, err)
			assert.NotNil(t, msg)
			if msg != nil && msg.Content != nil {
				assert.Equal(t, tc.expected, *msg.Content)
			}
		})
	}
}

func TestGetMessages_Success(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	userRepo.On("GetByID", mock.Anything, "user-2").Return(&domain.User{ID: "user-2"}, nil)
	helloMsg := "Hello"
	msgRepo.On("GetMessages", mock.Anything, "user-1", "user-2", 51, 0).Return([]*domain.Message{
		{ID: "msg-1", Content: &helloMsg},
	}, nil)

	req := &domain.GetMessagesRequest{ConversationWith: "user-2"}
	resp, err := svc.GetMessages(context.Background(), "user-1", req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.Len(t, resp.Messages, 1)
	assert.False(t, resp.HasMore)
}

func TestGetMessages_DefaultLimit(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	userRepo.On("GetByID", mock.Anything, "user-2").Return(&domain.User{ID: "user-2"}, nil)
	msgRepo.On("GetMessages", mock.Anything, "user-1", "user-2", 51, 0).Return([]*domain.Message{}, nil)

	req := &domain.GetMessagesRequest{ConversationWith: "user-2", Limit: 0}
	_, err := svc.GetMessages(context.Background(), "user-1", req)
	assert.NoError(t, err)
	msgRepo.AssertExpectations(t)
}

func TestGetMessages_OverMaxLimit(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	userRepo.On("GetByID", mock.Anything, "user-2").Return(&domain.User{ID: "user-2"}, nil)
	msgRepo.On("GetMessages", mock.Anything, "user-1", "user-2", 51, 0).Return([]*domain.Message{}, nil)

	req := &domain.GetMessagesRequest{ConversationWith: "user-2", Limit: 200}
	_, err := svc.GetMessages(context.Background(), "user-1", req)
	assert.NoError(t, err)
}

func TestMarkMessageAsRead_Success(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	msgRepo.On("MarkMessageAsRead", mock.Anything, "msg-1", "user-1").Return(nil)

	err := svc.MarkMessageAsRead(context.Background(), "user-1", &domain.MarkMessageReadRequest{MessageID: "msg-1"})
	assert.NoError(t, err)
}

func TestDeleteMessage_Success(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	msgRepo.On("DeleteMessage", mock.Anything, "msg-1", "user-1").Return(nil)

	err := svc.DeleteMessage(context.Background(), "user-1", &domain.DeleteMessageRequest{MessageID: "msg-1"})
	assert.NoError(t, err)
}

func TestDeleteMessage_Error(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	msgRepo.On("DeleteMessage", mock.Anything, "msg-1", "user-1").Return(errors.New("db error"))

	err := svc.DeleteMessage(context.Background(), "user-1", &domain.DeleteMessageRequest{MessageID: "msg-1"})
	assert.Error(t, err)
}

func TestGetConversations_HasMore(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	convs := make([]*domain.Conversation, 21)
	for i := range convs {
		convs[i] = &domain.Conversation{ParticipantOneID: "user-1"}
	}
	msgRepo.On("GetConversations", mock.Anything, "user-1", 21, 0).Return(convs, nil)

	req := &domain.GetConversationsRequest{Limit: 0}
	resp, err := svc.GetConversations(context.Background(), "user-1", req)
	assert.NoError(t, err)
	assert.True(t, resp.HasMore)
	assert.Len(t, resp.Conversations, 20)
}

func TestGetUnreadCount_Success(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	msgRepo.On("GetUnreadCount", mock.Anything, "user-1").Return(5, nil)

	count, err := svc.GetUnreadCount(context.Background(), "user-1")
	assert.NoError(t, err)
	assert.Equal(t, 5, count)
}

func TestGetUnreadCount_Error(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	msgRepo.On("GetUnreadCount", mock.Anything, "user-1").Return(0, errors.New("db error"))

	count, err := svc.GetUnreadCount(context.Background(), "user-1")
	assert.Error(t, err)
	assert.Equal(t, 0, count)
	assert.Contains(t, err.Error(), "failed to get unread count")
}

func TestSendMessage_SenderNotFound(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	userRepo.On("GetByID", mock.Anything, "user-1").Return(nil, errors.New("not found"))

	req := &domain.SendMessageRequest{
		RecipientID: "user-2",
		Content:     "Hello",
	}

	msg, err := svc.SendMessage(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, msg)
	assert.Contains(t, err.Error(), "failed to get sender")
}

func TestSendMessage_CreateMessageError(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	sender := &domain.User{ID: "user-1"}
	recipient := &domain.User{ID: "user-2"}

	userRepo.On("GetByID", mock.Anything, "user-1").Return(sender, nil)
	userRepo.On("GetByID", mock.Anything, "user-2").Return(recipient, nil)
	msgRepo.On("CreateMessage", mock.Anything, mock.AnythingOfType("*domain.Message")).Return(errors.New("db error"))

	req := &domain.SendMessageRequest{RecipientID: "user-2", Content: "Hello"}

	msg, err := svc.SendMessage(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, msg)
	assert.Contains(t, err.Error(), "failed to create message")
}

func TestSendMessage_WithParentMessage(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	sender := &domain.User{ID: "user-1"}
	recipient := &domain.User{ID: "user-2"}
	parentID := "parent-msg-1"

	userRepo.On("GetByID", mock.Anything, "user-1").Return(sender, nil)
	userRepo.On("GetByID", mock.Anything, "user-2").Return(recipient, nil)
	msgRepo.On("GetMessage", mock.Anything, parentID, "user-1").Return(&domain.Message{
		ID: parentID, SenderID: "user-1", RecipientID: "user-2",
	}, nil)
	msgRepo.On("CreateMessage", mock.Anything, mock.AnythingOfType("*domain.Message")).Return(nil)

	req := &domain.SendMessageRequest{
		RecipientID:     "user-2",
		Content:         "Reply",
		ParentMessageID: &parentID,
	}

	msg, err := svc.SendMessage(context.Background(), "user-1", req)
	assert.NoError(t, err)
	assert.NotNil(t, msg)
}

func TestSendMessage_ParentMessageNotFound(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	sender := &domain.User{ID: "user-1"}
	recipient := &domain.User{ID: "user-2"}
	parentID := "bad-parent"

	userRepo.On("GetByID", mock.Anything, "user-1").Return(sender, nil)
	userRepo.On("GetByID", mock.Anything, "user-2").Return(recipient, nil)
	msgRepo.On("GetMessage", mock.Anything, parentID, "user-1").Return(nil, errors.New("not found"))

	req := &domain.SendMessageRequest{
		RecipientID:     "user-2",
		Content:         "Reply",
		ParentMessageID: &parentID,
	}

	msg, err := svc.SendMessage(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, msg)
	assert.Contains(t, err.Error(), "failed to get parent message")
}

func TestSendMessage_ParentMessageWrongConversation(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	sender := &domain.User{ID: "user-1"}
	recipient := &domain.User{ID: "user-2"}
	parentID := "wrong-convo-msg"

	userRepo.On("GetByID", mock.Anything, "user-1").Return(sender, nil)
	userRepo.On("GetByID", mock.Anything, "user-2").Return(recipient, nil)
	msgRepo.On("GetMessage", mock.Anything, parentID, "user-1").Return(&domain.Message{
		ID: parentID, SenderID: "user-3", RecipientID: "user-4",
	}, nil)

	req := &domain.SendMessageRequest{
		RecipientID:     "user-2",
		Content:         "Reply",
		ParentMessageID: &parentID,
	}

	msg, err := svc.SendMessage(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, msg)
	assert.ErrorIs(t, err, domain.ErrMessageNotFound)
}

func TestMarkMessageAsRead_Error(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	msgRepo.On("MarkMessageAsRead", mock.Anything, "msg-1", "user-1").Return(errors.New("db error"))

	err := svc.MarkMessageAsRead(context.Background(), "user-1", &domain.MarkMessageReadRequest{MessageID: "msg-1"})
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to mark message as read")
}

func TestGetMessages_UserNotFound(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	userRepo.On("GetByID", mock.Anything, "user-2").Return(nil, errors.New("not found"))

	req := &domain.GetMessagesRequest{ConversationWith: "user-2"}
	resp, err := svc.GetMessages(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
	assert.Contains(t, err.Error(), "failed to get conversation partner")
}

func TestGetMessages_RepoError(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	userRepo.On("GetByID", mock.Anything, "user-2").Return(&domain.User{ID: "user-2"}, nil)
	msgRepo.On("GetMessages", mock.Anything, "user-1", "user-2", 51, 0).Return(nil, errors.New("db error"))

	req := &domain.GetMessagesRequest{ConversationWith: "user-2"}
	resp, err := svc.GetMessages(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestGetMessages_HasMore(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	userRepo.On("GetByID", mock.Anything, "user-2").Return(&domain.User{ID: "user-2"}, nil)

	msgs := make([]*domain.Message, 11)
	for i := range msgs {
		msgs[i] = &domain.Message{ID: "msg"}
	}
	msgRepo.On("GetMessages", mock.Anything, "user-1", "user-2", 11, 0).Return(msgs, nil)

	req := &domain.GetMessagesRequest{ConversationWith: "user-2", Limit: 10}
	resp, err := svc.GetMessages(context.Background(), "user-1", req)
	assert.NoError(t, err)
	assert.True(t, resp.HasMore)
	assert.Len(t, resp.Messages, 10)
}

func TestGetConversations_Error(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	msgRepo.On("GetConversations", mock.Anything, "user-1", 21, 0).Return(nil, errors.New("db error"))

	req := &domain.GetConversationsRequest{}
	resp, err := svc.GetConversations(context.Background(), "user-1", req)
	assert.Error(t, err)
	assert.Nil(t, resp)
}

func TestGetConversations_OverMaxLimit(t *testing.T) {
	msgRepo := new(mockMessageRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(msgRepo, userRepo)

	msgRepo.On("GetConversations", mock.Anything, "user-1", 21, 0).Return([]*domain.Conversation{}, nil)

	req := &domain.GetConversationsRequest{Limit: 100}
	resp, err := svc.GetConversations(context.Background(), "user-1", req)
	assert.NoError(t, err)
	assert.NotNil(t, resp)
	assert.False(t, resp.HasMore)
}
