package notification

import (
	"context"
	"errors"
	"testing"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
)

// Mock implementations

type MockNotificationRepository struct {
	mock.Mock
}

func (m *MockNotificationRepository) Create(ctx context.Context, notification *domain.Notification) error {
	args := m.Called(ctx, notification)
	return args.Error(0)
}

func (m *MockNotificationRepository) CreateBatch(ctx context.Context, notifications []domain.Notification) error {
	args := m.Called(ctx, notifications)
	return args.Error(0)
}

func (m *MockNotificationRepository) GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Notification), args.Error(1)
}

func (m *MockNotificationRepository) ListByUser(ctx context.Context, filter domain.NotificationFilter) ([]domain.Notification, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Notification), args.Error(1)
}

func (m *MockNotificationRepository) MarkAsRead(ctx context.Context, id, userID uuid.UUID) error {
	args := m.Called(ctx, id, userID)
	return args.Error(0)
}

func (m *MockNotificationRepository) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	args := m.Called(ctx, userID)
	return args.Error(0)
}

func (m *MockNotificationRepository) Delete(ctx context.Context, id, userID uuid.UUID) error {
	args := m.Called(ctx, id, userID)
	return args.Error(0)
}

func (m *MockNotificationRepository) DeleteOldRead(ctx context.Context, olderThan any) (int64, error) {
	args := m.Called(ctx, olderThan)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockNotificationRepository) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	args := m.Called(ctx, userID)
	return args.Int(0), args.Error(1)
}

func (m *MockNotificationRepository) GetStats(ctx context.Context, userID uuid.UUID) (*domain.NotificationStats, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.NotificationStats), args.Error(1)
}

type MockSubscriptionRepository struct {
	mock.Mock
}

func (m *MockSubscriptionRepository) GetSubscribers(ctx context.Context, channelID string) ([]*domain.Subscription, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Subscription), args.Error(1)
}

type MockUserRepository struct {
	mock.Mock
}

func (m *MockUserRepository) GetByID(ctx context.Context, id string) (*domain.User, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.User), args.Error(1)
}

// Tests

func TestService_CreateVideoNotificationForSubscribers(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		video       *domain.Video
		channelName string
		setup       func(*MockNotificationRepository, *MockSubscriptionRepository, *MockUserRepository)
		wantErr     bool
	}{
		{
			name: "successful notification creation",
			video: &domain.Video{
				ID:      uuid.New(),
				UserID:  uuid.New().String(),
				Title:   "Test Video",
				Status:  domain.StatusCompleted,
				Privacy: domain.PrivacyPublic,
			},
			channelName: "TestChannel",
			setup: func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {
				subscribers := []*domain.Subscription{
					{SubscriberID: uuid.New()},
					{SubscriberID: uuid.New()},
				}
				sr.On("GetSubscribers", ctx, mock.Anything).Return(subscribers, nil)
				nr.On("CreateBatch", ctx, mock.MatchedBy(func(notifs []domain.Notification) bool {
					return len(notifs) == 2
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "skip private videos",
			video: &domain.Video{
				ID:      uuid.New(),
				UserID:  uuid.New().String(),
				Status:  domain.StatusCompleted,
				Privacy: domain.PrivacyPrivate,
			},
			setup:   func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {},
			wantErr: false,
		},
		{
			name: "skip processing videos",
			video: &domain.Video{
				ID:      uuid.New(),
				UserID:  uuid.New().String(),
				Status:  domain.StatusProcessing,
				Privacy: domain.PrivacyPublic,
			},
			setup:   func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {},
			wantErr: false,
		},
		{
			name: "no subscribers - no error",
			video: &domain.Video{
				ID:      uuid.New(),
				UserID:  uuid.New().String(),
				Status:  domain.StatusCompleted,
				Privacy: domain.PrivacyPublic,
			},
			setup: func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {
				sr.On("GetSubscribers", ctx, mock.Anything).Return([]*domain.Subscription{}, nil)
			},
			wantErr: false,
		},
		{
			name: "fetch channel name when not provided",
			video: &domain.Video{
				ID:      uuid.New(),
				UserID:  uuid.New().String(),
				Title:   "Test Video",
				Status:  domain.StatusCompleted,
				Privacy: domain.PrivacyPublic,
			},
			channelName: "",
			setup: func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {
				subscribers := []*domain.Subscription{
					{SubscriberID: uuid.New()},
				}
				sr.On("GetSubscribers", ctx, mock.Anything).Return(subscribers, nil)
				ur.On("GetByID", ctx, mock.Anything).Return(&domain.User{Username: "FetchedChannel"}, nil)
				nr.On("CreateBatch", ctx, mock.MatchedBy(func(notifs []domain.Notification) bool {
					return len(notifs) == 1 && notifs[0].Title == "New video from FetchedChannel"
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "error getting subscribers",
			video: &domain.Video{
				ID:      uuid.New(),
				UserID:  uuid.New().String(),
				Status:  domain.StatusCompleted,
				Privacy: domain.PrivacyPublic,
			},
			setup: func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {
				sr.On("GetSubscribers", ctx, mock.Anything).Return(nil, errors.New("db error"))
			},
			wantErr: true,
		},
		{
			name: "error creating batch",
			video: &domain.Video{
				ID:      uuid.New(),
				UserID:  uuid.New().String(),
				Title:   "Test Video",
				Status:  domain.StatusCompleted,
				Privacy: domain.PrivacyPublic,
			},
			channelName: "TestChannel",
			setup: func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {
				subscribers := []*domain.Subscription{
					{SubscriberID: uuid.New()},
				}
				sr.On("GetSubscribers", ctx, mock.Anything).Return(subscribers, nil)
				nr.On("CreateBatch", ctx, mock.Anything).Return(errors.New("create error"))
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNotifRepo := new(MockNotificationRepository)
			mockSubRepo := new(MockSubscriptionRepository)
			mockUserRepo := new(MockUserRepository)

			tt.setup(mockNotifRepo, mockSubRepo, mockUserRepo)

			svc := NewService(mockNotifRepo, mockSubRepo, mockUserRepo)
			err := svc.CreateVideoNotificationForSubscribers(ctx, tt.video, tt.channelName)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			mockNotifRepo.AssertExpectations(t)
			mockSubRepo.AssertExpectations(t)
			mockUserRepo.AssertExpectations(t)
		})
	}
}

func TestService_CreateMessageNotification(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name       string
		message    *domain.Message
		senderName string
		setup      func(*MockNotificationRepository, *MockSubscriptionRepository, *MockUserRepository)
		wantErr    bool
	}{
		{
			name: "successful message notification",
			message: &domain.Message{
				ID:          uuid.New().String(),
				SenderID:    uuid.New().String(),
				RecipientID: uuid.New().String(),
				Content:     "Test message content",
				MessageType: "text",
			},
			senderName: "TestSender",
			setup: func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {
				nr.On("Create", ctx, mock.MatchedBy(func(n *domain.Notification) bool {
					return n.Type == domain.NotificationNewMessage &&
						n.Title == "New message from TestSender" &&
						n.Message == "Test message content"
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "skip system messages",
			message: &domain.Message{
				ID:          uuid.New().String(),
				SenderID:    uuid.New().String(),
				RecipientID: uuid.New().String(),
				Content:     "System message",
				MessageType: "system",
			},
			setup:   func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {},
			wantErr: false,
		},
		{
			name: "truncate long messages",
			message: &domain.Message{
				ID:          uuid.New().String(),
				SenderID:    uuid.New().String(),
				RecipientID: uuid.New().String(),
				Content:     "This is a very long message that exceeds 100 characters and should be truncated to show only a preview of the message content with ellipsis",
				MessageType: "text",
			},
			senderName: "TestSender",
			setup: func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {
				nr.On("Create", ctx, mock.MatchedBy(func(n *domain.Notification) bool {
					return len(n.Message) == 100 && n.Message[97:] == "..."
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "fetch sender name when not provided",
			message: &domain.Message{
				ID:          uuid.New().String(),
				SenderID:    uuid.New().String(),
				RecipientID: uuid.New().String(),
				Content:     "Test message",
				MessageType: "text",
			},
			senderName: "",
			setup: func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {
				ur.On("GetByID", ctx, mock.Anything).Return(&domain.User{Username: "FetchedSender"}, nil)
				nr.On("Create", ctx, mock.MatchedBy(func(n *domain.Notification) bool {
					return n.Title == "New message from FetchedSender"
				})).Return(nil)
			},
			wantErr: false,
		},
		{
			name: "handle sender fetch error gracefully",
			message: &domain.Message{
				ID:          uuid.New().String(),
				SenderID:    uuid.New().String(),
				RecipientID: uuid.New().String(),
				Content:     "Test message",
				MessageType: "text",
			},
			senderName: "",
			setup: func(nr *MockNotificationRepository, sr *MockSubscriptionRepository, ur *MockUserRepository) {
				ur.On("GetByID", ctx, mock.Anything).Return(nil, errors.New("user not found"))
				nr.On("Create", ctx, mock.MatchedBy(func(n *domain.Notification) bool {
					return n.Title == "New message from Unknown"
				})).Return(nil)
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockNotifRepo := new(MockNotificationRepository)
			mockSubRepo := new(MockSubscriptionRepository)
			mockUserRepo := new(MockUserRepository)

			tt.setup(mockNotifRepo, mockSubRepo, mockUserRepo)

			svc := NewService(mockNotifRepo, mockSubRepo, mockUserRepo)
			err := svc.CreateMessageNotification(ctx, tt.message, tt.senderName)

			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			mockNotifRepo.AssertExpectations(t)
			mockUserRepo.AssertExpectations(t)
		})
	}
}

func TestService_GetUserNotifications(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	mockNotifRepo := new(MockNotificationRepository)
	mockSubRepo := new(MockSubscriptionRepository)
	mockUserRepo := new(MockUserRepository)

	expectedNotifications := []domain.Notification{
		{ID: uuid.New(), UserID: userID, Type: domain.NotificationNewVideo},
		{ID: uuid.New(), UserID: userID, Type: domain.NotificationComment},
	}

	filter := domain.NotificationFilter{
		Unread: boolPtr(true),
	}

	mockNotifRepo.On("ListByUser", ctx, mock.MatchedBy(func(f domain.NotificationFilter) bool {
		return f.UserID == userID && f.Unread != nil && *f.Unread == true
	})).Return(expectedNotifications, nil)

	svc := NewService(mockNotifRepo, mockSubRepo, mockUserRepo)
	notifications, err := svc.GetUserNotifications(ctx, userID, filter)

	require.NoError(t, err)
	assert.Equal(t, expectedNotifications, notifications)
	mockNotifRepo.AssertExpectations(t)
}

func TestService_MarkAsRead(t *testing.T) {
	ctx := context.Background()
	notifID := uuid.New()
	userID := uuid.New()

	mockNotifRepo := new(MockNotificationRepository)
	mockSubRepo := new(MockSubscriptionRepository)
	mockUserRepo := new(MockUserRepository)

	mockNotifRepo.On("MarkAsRead", ctx, notifID, userID).Return(nil)

	svc := NewService(mockNotifRepo, mockSubRepo, mockUserRepo)
	err := svc.MarkAsRead(ctx, notifID, userID)

	require.NoError(t, err)
	mockNotifRepo.AssertExpectations(t)
}

func TestService_MarkAllAsRead(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	mockNotifRepo := new(MockNotificationRepository)
	mockSubRepo := new(MockSubscriptionRepository)
	mockUserRepo := new(MockUserRepository)

	mockNotifRepo.On("MarkAllAsRead", ctx, userID).Return(nil)

	svc := NewService(mockNotifRepo, mockSubRepo, mockUserRepo)
	err := svc.MarkAllAsRead(ctx, userID)

	require.NoError(t, err)
	mockNotifRepo.AssertExpectations(t)
}

func TestService_DeleteNotification(t *testing.T) {
	ctx := context.Background()
	notifID := uuid.New()
	userID := uuid.New()

	mockNotifRepo := new(MockNotificationRepository)
	mockSubRepo := new(MockSubscriptionRepository)
	mockUserRepo := new(MockUserRepository)

	mockNotifRepo.On("Delete", ctx, notifID, userID).Return(nil)

	svc := NewService(mockNotifRepo, mockSubRepo, mockUserRepo)
	err := svc.DeleteNotification(ctx, notifID, userID)

	require.NoError(t, err)
	mockNotifRepo.AssertExpectations(t)
}

func TestService_GetUnreadCount(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	mockNotifRepo := new(MockNotificationRepository)
	mockSubRepo := new(MockSubscriptionRepository)
	mockUserRepo := new(MockUserRepository)

	mockNotifRepo.On("GetUnreadCount", ctx, userID).Return(5, nil)

	svc := NewService(mockNotifRepo, mockSubRepo, mockUserRepo)
	count, err := svc.GetUnreadCount(ctx, userID)

	require.NoError(t, err)
	assert.Equal(t, 5, count)
	mockNotifRepo.AssertExpectations(t)
}

func TestService_GetStats(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New()

	mockNotifRepo := new(MockNotificationRepository)
	mockSubRepo := new(MockSubscriptionRepository)
	mockUserRepo := new(MockUserRepository)

	expectedStats := &domain.NotificationStats{
		TotalCount:  10,
		UnreadCount: 3,
		ByType: map[domain.NotificationType]int{
			domain.NotificationNewVideo:   5,
			domain.NotificationComment:    3,
			domain.NotificationNewMessage: 2,
		},
	}

	mockNotifRepo.On("GetStats", ctx, userID).Return(expectedStats, nil)

	svc := NewService(mockNotifRepo, mockSubRepo, mockUserRepo)
	stats, err := svc.GetStats(ctx, userID)

	require.NoError(t, err)
	assert.Equal(t, expectedStats, stats)
	mockNotifRepo.AssertExpectations(t)
}

func boolPtr(b bool) *bool {
	return &b
}
