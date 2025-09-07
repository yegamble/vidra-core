package usecase

import (
	"context"
	"fmt"

	"athena/internal/domain"
	"github.com/google/uuid"
)

type NotificationService interface {
	// CreateVideoNotificationForSubscribers creates notifications for all subscribers when a video is uploaded
	CreateVideoNotificationForSubscribers(ctx context.Context, video *domain.Video, channelName string) error
	// GetUserNotifications retrieves notifications for a user
	GetUserNotifications(ctx context.Context, userID uuid.UUID, filter domain.NotificationFilter) ([]domain.Notification, error)
	// MarkAsRead marks a notification as read
	MarkAsRead(ctx context.Context, notificationID, userID uuid.UUID) error
	// MarkAllAsRead marks all notifications for a user as read
	MarkAllAsRead(ctx context.Context, userID uuid.UUID) error
	// DeleteNotification deletes a notification
	DeleteNotification(ctx context.Context, notificationID, userID uuid.UUID) error
	// GetUnreadCount gets the count of unread notifications for a user
	GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error)
	// GetStats gets notification statistics for a user
	GetStats(ctx context.Context, userID uuid.UUID) (*domain.NotificationStats, error)
}

type notificationService struct {
	notificationRepo NotificationRepository
	subscriptionRepo SubscriptionRepository
	userRepo         UserRepository
}

func NewNotificationService(
	notificationRepo NotificationRepository,
	subscriptionRepo SubscriptionRepository,
	userRepo UserRepository,
) NotificationService {
	return &notificationService{
		notificationRepo: notificationRepo,
		subscriptionRepo: subscriptionRepo,
		userRepo:         userRepo,
	}
}

func (s *notificationService) CreateVideoNotificationForSubscribers(ctx context.Context, video *domain.Video, channelName string) error {
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
		return nil // No subscribers to notify
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

	// Batch create notifications
	if err := s.notificationRepo.CreateBatch(ctx, notifications); err != nil {
		return fmt.Errorf("failed to create notifications: %w", err)
	}

	return nil
}

func (s *notificationService) GetUserNotifications(ctx context.Context, userID uuid.UUID, filter domain.NotificationFilter) ([]domain.Notification, error) {
	filter.UserID = userID
	return s.notificationRepo.ListByUser(ctx, filter)
}

func (s *notificationService) MarkAsRead(ctx context.Context, notificationID, userID uuid.UUID) error {
	return s.notificationRepo.MarkAsRead(ctx, notificationID, userID)
}

func (s *notificationService) MarkAllAsRead(ctx context.Context, userID uuid.UUID) error {
	return s.notificationRepo.MarkAllAsRead(ctx, userID)
}

func (s *notificationService) DeleteNotification(ctx context.Context, notificationID, userID uuid.UUID) error {
	return s.notificationRepo.Delete(ctx, notificationID, userID)
}

func (s *notificationService) GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error) {
	return s.notificationRepo.GetUnreadCount(ctx, userID)
}

func (s *notificationService) GetStats(ctx context.Context, userID uuid.UUID) (*domain.NotificationStats, error) {
	return s.notificationRepo.GetStats(ctx, userID)
}
