package notification

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
)

func strPtr(s string) *string { return &s }

type mockNotificationRepo struct{ mock.Mock }

func (m *mockNotificationRepo) Create(ctx context.Context, notification *domain.Notification) error {
	return m.Called(ctx, notification).Error(0)
}
func (m *mockNotificationRepo) CreateBatch(ctx context.Context, notifications []domain.Notification) error {
	return m.Called(ctx, notifications).Error(0)
}
func (m *mockNotificationRepo) GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error) {
	args := m.Called(ctx, id)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.Notification), args.Error(1)
}
func (m *mockNotificationRepo) ListByUser(ctx context.Context, filter domain.NotificationFilter) ([]domain.Notification, error) {
	args := m.Called(ctx, filter)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]domain.Notification), args.Error(1)
}
func (m *mockNotificationRepo) MarkAsRead(ctx context.Context, id, userID uuid.UUID) error {
	return m.Called(ctx, id, userID).Error(0)
}
func (m *mockNotificationRepo) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	return m.Called(ctx, userID).Error(0)
}
func (m *mockNotificationRepo) Delete(ctx context.Context, id, userID uuid.UUID) error {
	return m.Called(ctx, id, userID).Error(0)
}
func (m *mockNotificationRepo) DeleteOldRead(ctx context.Context, olderThan time.Duration) (int64, error) {
	args := m.Called(ctx, olderThan)
	return args.Get(0).(int64), args.Error(1)
}
func (m *mockNotificationRepo) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	args := m.Called(ctx, userID)
	return args.Int(0), args.Error(1)
}
func (m *mockNotificationRepo) GetStats(ctx context.Context, userID uuid.UUID) (*domain.NotificationStats, error) {
	args := m.Called(ctx, userID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.NotificationStats), args.Error(1)
}

type mockSubscriptionRepo struct{ mock.Mock }

func (m *mockSubscriptionRepo) GetSubscribers(ctx context.Context, channelID string) ([]*domain.Subscription, error) {
	args := m.Called(ctx, channelID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).([]*domain.Subscription), args.Error(1)
}
func (m *mockSubscriptionRepo) SubscribeToChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error {
	return m.Called(ctx, subscriberID, channelID).Error(0)
}
func (m *mockSubscriptionRepo) UnsubscribeFromChannel(ctx context.Context, subscriberID, channelID uuid.UUID) error {
	return m.Called(ctx, subscriberID, channelID).Error(0)
}
func (m *mockSubscriptionRepo) IsSubscribed(ctx context.Context, subscriberID, channelID uuid.UUID) (bool, error) {
	args := m.Called(ctx, subscriberID, channelID)
	return args.Bool(0), args.Error(1)
}
func (m *mockSubscriptionRepo) ListUserSubscriptions(ctx context.Context, subscriberID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error) {
	args := m.Called(ctx, subscriberID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.SubscriptionResponse), args.Error(1)
}
func (m *mockSubscriptionRepo) ListChannelSubscribers(ctx context.Context, channelID uuid.UUID, limit, offset int) (*domain.SubscriptionResponse, error) {
	args := m.Called(ctx, channelID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*domain.SubscriptionResponse), args.Error(1)
}
func (m *mockSubscriptionRepo) GetSubscriptionVideos(ctx context.Context, subscriberID uuid.UUID, limit, offset int) ([]domain.Video, int, error) {
	args := m.Called(ctx, subscriberID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Int(1), args.Error(2)
	}
	return args.Get(0).([]domain.Video), args.Int(1), args.Error(2)
}
func (m *mockSubscriptionRepo) Subscribe(ctx context.Context, subscriberID, channelID string) error {
	return m.Called(ctx, subscriberID, channelID).Error(0)
}
func (m *mockSubscriptionRepo) Unsubscribe(ctx context.Context, subscriberID, channelID string) error {
	return m.Called(ctx, subscriberID, channelID).Error(0)
}
func (m *mockSubscriptionRepo) ListSubscriptions(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.User, int64, error) {
	args := m.Called(ctx, subscriberID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.User), args.Get(1).(int64), args.Error(2)
}
func (m *mockSubscriptionRepo) ListSubscriptionVideos(ctx context.Context, subscriberID string, limit, offset int) ([]*domain.Video, int64, error) {
	args := m.Called(ctx, subscriberID, limit, offset)
	if args.Get(0) == nil {
		return nil, args.Get(1).(int64), args.Error(2)
	}
	return args.Get(0).([]*domain.Video), args.Get(1).(int64), args.Error(2)
}
func (m *mockSubscriptionRepo) CountSubscribers(ctx context.Context, channelID string) (int64, error) {
	args := m.Called(ctx, channelID)
	return args.Get(0).(int64), args.Error(1)
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
func (m *mockUserRepo) Anonymize(_ context.Context, _ string) error { return nil }

func TestCreateVideoNotification_SkipsNonPublic(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	video := &domain.Video{Status: domain.StatusProcessing, Privacy: domain.PrivacyPublic}
	err := svc.CreateVideoNotificationForSubscribers(context.Background(), video, "TestChannel")
	assert.NoError(t, err)
	notifRepo.AssertNotCalled(t, "CreateBatch")
}

func TestCreateVideoNotification_SkipsPrivate(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	video := &domain.Video{Status: domain.StatusCompleted, Privacy: domain.PrivacyPrivate}
	err := svc.CreateVideoNotificationForSubscribers(context.Background(), video, "TestChannel")
	assert.NoError(t, err)
	notifRepo.AssertNotCalled(t, "CreateBatch")
}

func TestCreateVideoNotification_NoSubscribers(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	video := &domain.Video{
		ID:      uuid.New().String(),
		UserID:  uuid.New().String(),
		Title:   "Test Video",
		Status:  domain.StatusCompleted,
		Privacy: domain.PrivacyPublic,
	}

	subRepo.On("GetSubscribers", mock.Anything, video.UserID).Return([]*domain.Subscription{}, nil)

	err := svc.CreateVideoNotificationForSubscribers(context.Background(), video, "TestChannel")
	assert.NoError(t, err)
	notifRepo.AssertNotCalled(t, "CreateBatch")
}

func TestCreateVideoNotification_Success(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	video := &domain.Video{
		ID:      uuid.New().String(),
		UserID:  uuid.New().String(),
		Title:   "New Video",
		Status:  domain.StatusCompleted,
		Privacy: domain.PrivacyPublic,
	}

	subscriberID := uuid.New()
	subRepo.On("GetSubscribers", mock.Anything, video.UserID).Return([]*domain.Subscription{
		{SubscriberID: subscriberID},
	}, nil)
	notifRepo.On("CreateBatch", mock.Anything, mock.MatchedBy(func(notifs []domain.Notification) bool {
		return len(notifs) == 1 && notifs[0].UserID == subscriberID && notifs[0].Type == domain.NotificationNewVideo
	})).Return(nil)

	err := svc.CreateVideoNotificationForSubscribers(context.Background(), video, "TestChannel")
	assert.NoError(t, err)
	notifRepo.AssertExpectations(t)
}

func TestGetUserNotifications_Success(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	userID := uuid.New()
	expected := []domain.Notification{{Title: "Test"}}

	notifRepo.On("ListByUser", mock.Anything, mock.MatchedBy(func(f domain.NotificationFilter) bool {
		return f.UserID == userID
	})).Return(expected, nil)

	notifs, err := svc.GetUserNotifications(context.Background(), userID, domain.NotificationFilter{})
	assert.NoError(t, err)
	assert.Equal(t, expected, notifs)
}

func TestMarkAsRead_Success(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	notifID := uuid.New()
	userID := uuid.New()

	notifRepo.On("MarkAsRead", mock.Anything, notifID, userID).Return(nil)

	err := svc.MarkAsRead(context.Background(), notifID, userID)
	assert.NoError(t, err)
}

func TestMarkAllAsRead_Success(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	userID := uuid.New()
	notifRepo.On("MarkAllAsRead", mock.Anything, userID).Return(nil)

	err := svc.MarkAllAsRead(context.Background(), userID)
	assert.NoError(t, err)
}

func TestDeleteNotification_Success(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	notifID := uuid.New()
	userID := uuid.New()

	notifRepo.On("Delete", mock.Anything, notifID, userID).Return(nil)

	err := svc.DeleteNotification(context.Background(), notifID, userID)
	assert.NoError(t, err)
}

func TestGetUnreadCount_Success(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	userID := uuid.New()
	notifRepo.On("GetUnreadCount", mock.Anything, userID).Return(7, nil)

	count, err := svc.GetUnreadCount(context.Background(), userID)
	assert.NoError(t, err)
	assert.Equal(t, 7, count)
}

func TestGetStats_Success(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	userID := uuid.New()
	expected := &domain.NotificationStats{UnreadCount: 3, TotalCount: 10}
	notifRepo.On("GetStats", mock.Anything, userID).Return(expected, nil)

	stats, err := svc.GetStats(context.Background(), userID)
	assert.NoError(t, err)
	assert.Equal(t, expected, stats)
}

func TestCreateMessageNotification_SkipsSystem(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	msg := &domain.Message{MessageType: "system"}
	err := svc.CreateMessageNotification(context.Background(), msg, "")
	assert.NoError(t, err)
	notifRepo.AssertNotCalled(t, "Create")
}

func TestCreateMessageNotification_Success(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	senderID := uuid.New()
	recipientID := uuid.New()
	msgID := uuid.New()

	msg := &domain.Message{
		ID:          msgID.String(),
		SenderID:    senderID.String(),
		RecipientID: recipientID.String(),
		Content:     strPtr("Hello there"),
		MessageType: "text",
	}

	notifRepo.On("Create", mock.Anything, mock.MatchedBy(func(n *domain.Notification) bool {
		return n.UserID == recipientID && n.Type == domain.NotificationNewMessage
	})).Return(nil)

	err := svc.CreateMessageNotification(context.Background(), msg, "Alice")
	assert.NoError(t, err)
}

func TestCreateMessageNotification_InvalidRecipientID(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	msg := &domain.Message{
		ID:          uuid.New().String(),
		SenderID:    uuid.New().String(),
		RecipientID: "not-a-uuid",
		Content:     strPtr("Hello"),
		MessageType: "text",
	}

	err := svc.CreateMessageNotification(context.Background(), msg, "Alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid recipient ID")
}

func TestCreateMessageReadNotification_NoOp(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	err := svc.CreateMessageReadNotification(context.Background(), uuid.New(), uuid.New(), "Alice")
	assert.NoError(t, err)
}

func TestCreateVideoNotification_FetchesChannelName(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	userIDStr := uuid.New().String()
	subscriberID := uuid.New()

	video := &domain.Video{
		ID:      uuid.New().String(),
		UserID:  userIDStr,
		Title:   "Test",
		Status:  domain.StatusCompleted,
		Privacy: domain.PrivacyPublic,
	}

	subRepo.On("GetSubscribers", mock.Anything, userIDStr).Return([]*domain.Subscription{
		{SubscriberID: subscriberID},
	}, nil)
	userRepo.On("GetByID", mock.Anything, userIDStr).Return(&domain.User{Username: "alice"}, nil)
	notifRepo.On("CreateBatch", mock.Anything, mock.Anything).Return(nil)

	err := svc.CreateVideoNotificationForSubscribers(context.Background(), video, "")
	assert.NoError(t, err)
	userRepo.AssertCalled(t, "GetByID", mock.Anything, userIDStr)
}

func TestCreateVideoNotification_SubscriberFetchError(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	video := &domain.Video{
		ID:      uuid.New().String(),
		UserID:  uuid.New().String(),
		Title:   "Test",
		Status:  domain.StatusCompleted,
		Privacy: domain.PrivacyPublic,
	}

	subRepo.On("GetSubscribers", mock.Anything, video.UserID).Return(nil, errors.New("db error"))

	err := svc.CreateVideoNotificationForSubscribers(context.Background(), video, "Channel")
	assert.Error(t, err)
}

func TestCreateMessageNotification_InvalidSenderID(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	msg := &domain.Message{
		ID:          uuid.New().String(),
		SenderID:    "not-a-uuid",
		RecipientID: uuid.New().String(),
		Content:     strPtr("Hello"),
		MessageType: "text",
	}

	err := svc.CreateMessageNotification(context.Background(), msg, "Alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid sender ID")
}

func TestCreateMessageNotification_InvalidMessageID(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	msg := &domain.Message{
		ID:          "not-a-uuid",
		SenderID:    uuid.New().String(),
		RecipientID: uuid.New().String(),
		Content:     strPtr("Hello"),
		MessageType: "text",
	}

	err := svc.CreateMessageNotification(context.Background(), msg, "Alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid message ID")
}

func TestCreateMessageNotification_LookupSenderName(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	senderID := uuid.New()
	msg := &domain.Message{
		ID:          uuid.New().String(),
		SenderID:    senderID.String(),
		RecipientID: uuid.New().String(),
		Content:     strPtr("Hello"),
		MessageType: "text",
	}

	userRepo.On("GetByID", mock.Anything, senderID.String()).Return(&domain.User{Username: "alice"}, nil)
	notifRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Notification")).Return(nil)

	err := svc.CreateMessageNotification(context.Background(), msg, "")
	assert.NoError(t, err)
	userRepo.AssertCalled(t, "GetByID", mock.Anything, senderID.String())
}

func TestCreateMessageNotification_LookupSenderNameError(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	senderID := uuid.New()
	msg := &domain.Message{
		ID:          uuid.New().String(),
		SenderID:    senderID.String(),
		RecipientID: uuid.New().String(),
		Content:     strPtr("Hello"),
		MessageType: "text",
	}

	userRepo.On("GetByID", mock.Anything, senderID.String()).Return(nil, errors.New("not found"))
	notifRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Notification")).Return(nil)

	err := svc.CreateMessageNotification(context.Background(), msg, "")
	assert.NoError(t, err)
}

func TestCreateMessageNotification_LongContent(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	longContent := strings.Repeat("a", 200)
	msg := &domain.Message{
		ID:          uuid.New().String(),
		SenderID:    uuid.New().String(),
		RecipientID: uuid.New().String(),
		Content:     &longContent,
		MessageType: "text",
	}

	notifRepo.On("Create", mock.Anything, mock.MatchedBy(func(n *domain.Notification) bool {
		return len(n.Message) == 100 && strings.HasSuffix(n.Message, "...")
	})).Return(nil)

	err := svc.CreateMessageNotification(context.Background(), msg, "Alice")
	assert.NoError(t, err)
}

func TestCreateMessageNotification_CreateError(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	msg := &domain.Message{
		ID:          uuid.New().String(),
		SenderID:    uuid.New().String(),
		RecipientID: uuid.New().String(),
		Content:     strPtr("Hello"),
		MessageType: "text",
	}

	notifRepo.On("Create", mock.Anything, mock.AnythingOfType("*domain.Notification")).Return(errors.New("db error"))

	err := svc.CreateMessageNotification(context.Background(), msg, "Alice")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create message notification")
}

func TestCreateVideoNotification_UsesChannelID(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	channelID := uuid.New()
	subscriberID := uuid.New()

	video := &domain.Video{
		ID:        uuid.New().String(),
		UserID:    uuid.New().String(),
		ChannelID: channelID,
		Title:     "Test",
		Status:    domain.StatusCompleted,
		Privacy:   domain.PrivacyPublic,
	}

	subRepo.On("GetSubscribers", mock.Anything, channelID.String()).Return([]*domain.Subscription{
		{SubscriberID: subscriberID},
	}, nil)
	notifRepo.On("CreateBatch", mock.Anything, mock.Anything).Return(nil)

	err := svc.CreateVideoNotificationForSubscribers(context.Background(), video, "TestChannel")
	assert.NoError(t, err)
	subRepo.AssertCalled(t, "GetSubscribers", mock.Anything, channelID.String())
}

func TestCreateVideoNotification_CreateBatchError(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	video := &domain.Video{
		ID:      uuid.New().String(),
		UserID:  uuid.New().String(),
		Title:   "Test",
		Status:  domain.StatusCompleted,
		Privacy: domain.PrivacyPublic,
	}

	subRepo.On("GetSubscribers", mock.Anything, video.UserID).Return([]*domain.Subscription{
		{SubscriberID: uuid.New()},
	}, nil)
	notifRepo.On("CreateBatch", mock.Anything, mock.Anything).Return(errors.New("db error"))

	err := svc.CreateVideoNotificationForSubscribers(context.Background(), video, "Channel")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to create notifications")
}

func TestCreateVideoNotification_FetchChannelNameError(t *testing.T) {
	notifRepo := new(mockNotificationRepo)
	subRepo := new(mockSubscriptionRepo)
	userRepo := new(mockUserRepo)
	svc := NewService(notifRepo, subRepo, userRepo)

	video := &domain.Video{
		ID:      uuid.New().String(),
		UserID:  uuid.New().String(),
		Title:   "Test",
		Status:  domain.StatusCompleted,
		Privacy: domain.PrivacyPublic,
	}

	subRepo.On("GetSubscribers", mock.Anything, video.UserID).Return([]*domain.Subscription{
		{SubscriberID: uuid.New()},
	}, nil)
	userRepo.On("GetByID", mock.Anything, video.UserID).Return(nil, errors.New("not found"))

	err := svc.CreateVideoNotificationForSubscribers(context.Background(), video, "")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "failed to get channel user")
}
