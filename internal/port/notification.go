package port

import (
	"context"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type NotificationRepository interface {
	Create(ctx context.Context, notification *domain.Notification) error
	CreateBatch(ctx context.Context, notifications []domain.Notification) error
	GetByID(ctx context.Context, id uuid.UUID) (*domain.Notification, error)
	ListByUser(ctx context.Context, filter domain.NotificationFilter) ([]domain.Notification, error)
	MarkAsRead(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	MarkAllAsRead(ctx context.Context, userID uuid.UUID) error
	Delete(ctx context.Context, id uuid.UUID, userID uuid.UUID) error
	DeleteOldRead(ctx context.Context, olderThan time.Duration) (int64, error)
	GetUnreadCount(ctx context.Context, userID uuid.UUID) (int, error)
	GetStats(ctx context.Context, userID uuid.UUID) (*domain.NotificationStats, error)
}
