package notification

import (
	"context"
	"fmt"

	"athena/internal/domain"
	"athena/internal/port"

	"github.com/google/uuid"
)

type Service interface {
	CreateVideoNotificationForSubscribers(ctx context.Context, video *domain.Video, channelName string) error
	CreateMessageNotification(ctx context.Context, message *domain.Message, senderName string) error
	CreateMessageReadNotification(ctx context.Context, messageID uuid.UUID, readerID uuid.UUID, readerName string) error
	GetUserNotifications(ctx context.Context, userID uuid.UUID, filter domain.NotificationFilter) ([]domain.Notification, error)
	MarkAsRead(ctx context.Context, notificationID, userID uuid.UUID) error
	MarkAllAsRead(ctx context.Context, userID uuid.UUID) error
	DeleteNotification(ctx context.Context, notificationID, userID uuid.UUID) error
	GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error)
	GetStats(ctx context.Context, userID uuid.UUID) (*domain.NotificationStats, error)
}

type service struct {
	notificationRepo port.NotificationRepository
	subscriptionRepo port.SubscriptionRepository
	userRepo         port.UserRepository
}

func NewService(notificationRepo port.NotificationRepository, subscriptionRepo port.SubscriptionRepository, userRepo port.UserRepository) Service {
	return &service{notificationRepo: notificationRepo, subscriptionRepo: subscriptionRepo, userRepo: userRepo}
}

func (s *service) CreateVideoNotificationForSubscribers(ctx context.Context, video *domain.Video, channelName string) error {
	// Only create notifications for public completed videos
	if video.Status != domain.StatusCompleted || video.Privacy != domain.PrivacyPublic {
		return nil
	}
	// Get all subscribers for this channel
	subscribers, err := s.subscriptionRepo.GetSubscribers(ctx, video.UserID)
	if err != nil {
		return fmt.Errorf("failed to get subscribers: %w", err)
	}
	if len(subscribers) == 0 {
		return nil
	}
	// If channel name not provided, fetch it
	if channelName == "" {
		user, err := s.userRepo.GetByID(ctx, video.UserID)
		if err != nil {
			return fmt.Errorf("failed to get channel user: %w", err)
		}
		channelName = user.Username
	}
	// Create notifications for all subscribers
	notifications := make([]domain.Notification, len(subscribers))
	for i, subscriber := range subscribers {
		notifications[i] = domain.Notification{
			UserID:  subscriber.SubscriberID,
			Type:    domain.NotificationNewVideo,
			Title:   fmt.Sprintf("New video from %s", channelName),
			Message: fmt.Sprintf("%s uploaded: %s", channelName, video.Title),
			Data: map[string]interface{}{
				"video_id":      video.ID,
				"channel_id":    video.UserID,
				"channel_name":  channelName,
				"video_title":   video.Title,
				"thumbnail_cid": video.ThumbnailCID,
			},
			Read: false,
		}
	}
	if err := s.notificationRepo.CreateBatch(ctx, notifications); err != nil {
		return fmt.Errorf("failed to create notifications: %w", err)
	}
	return nil
}

func (s *service) GetUserNotifications(ctx context.Context, userID uuid.UUID, filter domain.NotificationFilter) ([]domain.Notification, error) {
	filter.UserID = userID
	return s.notificationRepo.ListByUser(ctx, filter)
}

func (s *service) MarkAsRead(ctx context.Context, notificationID, userID uuid.UUID) error {
	return s.notificationRepo.MarkAsRead(ctx, notificationID, userID)
}

func (s *service) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	return s.notificationRepo.MarkAllAsRead(ctx, userID)
}

func (s *service) DeleteNotification(ctx context.Context, notificationID, userID uuid.UUID) error {
	return s.notificationRepo.Delete(ctx, notificationID, userID)
}

func (s *service) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	return s.notificationRepo.GetUnreadCount(ctx, userID)
}

func (s *service) GetStats(ctx context.Context, userID uuid.UUID) (*domain.NotificationStats, error) {
	return s.notificationRepo.GetStats(ctx, userID)
}

func (s *service) CreateMessageNotification(ctx context.Context, message *domain.Message, senderName string) error {
	// Don't create notification for system messages
	if message.MessageType == "system" {
		return nil
	}
	// Parse IDs
	recipientID, err := uuid.Parse(message.RecipientID)
	if err != nil {
		return fmt.Errorf("invalid recipient ID: %w", err)
	}
	senderID, err := uuid.Parse(message.SenderID)
	if err != nil {
		return fmt.Errorf("invalid sender ID: %w", err)
	}
	messageID, err := uuid.Parse(message.ID)
	if err != nil {
		return fmt.Errorf("invalid message ID: %w", err)
	}
	if senderName == "" {
		user, err := s.userRepo.GetByID(ctx, message.SenderID)
		if err != nil {
			senderName = "Unknown"
		} else {
			senderName = user.Username
		}
	}
	messagePreview := message.Content
	if len(messagePreview) > 100 {
		messagePreview = messagePreview[:97] + "..."
	}
	notification := domain.Notification{
		UserID:  recipientID,
		Type:    domain.NotificationNewMessage,
		Title:   fmt.Sprintf("New message from %s", senderName),
		Message: messagePreview,
		Data: map[string]interface{}{
			"message_id":      messageID.String(),
			"sender_id":       senderID.String(),
			"sender_name":     senderName,
			"message_preview": messagePreview,
		},
		Read: false,
	}
	if err := s.notificationRepo.Create(ctx, &notification); err != nil {
		return fmt.Errorf("failed to create message notification: %w", err)
	}
	return nil
}

func (s *service) CreateMessageReadNotification(ctx context.Context, messageID uuid.UUID, readerID uuid.UUID, readerName string) error {
	// No-op (optional feature)
	return nil
}
