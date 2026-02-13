package port

import (
	"context"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
)

// VideoAnalyticsRepository defines the interface for video analytics data persistence.
type VideoAnalyticsRepository interface {
	// Event operations
	CreateEvent(ctx context.Context, event *domain.AnalyticsEvent) error
	CreateEventsBatch(ctx context.Context, events []*domain.AnalyticsEvent) error
	GetEventsByVideoID(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time, limit, offset int) ([]*domain.AnalyticsEvent, error)
	GetEventsBySessionID(ctx context.Context, sessionID string) ([]*domain.AnalyticsEvent, error)
	DeleteOldEvents(ctx context.Context, retentionDays int) (int64, error)

	// Active viewer operations
	UpsertActiveViewer(ctx context.Context, viewer *domain.ActiveViewer) error
	UpsertActiveViewersBatch(ctx context.Context, viewers []*domain.ActiveViewer) error
	GetActiveViewerCount(ctx context.Context, videoID uuid.UUID) (int, error)
	GetActiveViewersForVideo(ctx context.Context, videoID uuid.UUID) ([]*domain.ActiveViewer, error)
	CleanupInactiveViewers(ctx context.Context) (int64, error)

	// Daily analytics aggregation
	AggregateDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) error
	GetDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) (*domain.DailyAnalytics, error)
	GetDailyAnalyticsRange(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error)

	// Retention
	CalculateRetentionCurve(ctx context.Context, videoID uuid.UUID, date time.Time) error
	GetRetentionData(ctx context.Context, videoID uuid.UUID, date time.Time) ([]*domain.RetentionData, error)

	// Summary
	GetVideoAnalyticsSummary(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error)
	GetTotalViewsForVideo(ctx context.Context, videoID uuid.UUID) (int, error)

	// Channel analytics
	GetChannelDailyAnalytics(ctx context.Context, channelID uuid.UUID, date time.Time) (*domain.ChannelDailyAnalytics, error)
	GetChannelDailyAnalyticsRange(ctx context.Context, channelID uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error)
	GetTotalViewsForChannel(ctx context.Context, channelID uuid.UUID) (int, error)
}
