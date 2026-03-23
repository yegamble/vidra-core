package video

import (
	"context"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
)

type VideoAnalyticsService interface {
	TrackEvent(ctx context.Context, event *domain.AnalyticsEvent) error
	TrackEventsBatch(ctx context.Context, events []*domain.AnalyticsEvent) error
	TrackViewerHeartbeat(ctx context.Context, videoID uuid.UUID, sessionID string, userID *uuid.UUID) error
	GetVideoAnalyticsSummary(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error)
	GetDailyAnalyticsRange(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error)
	GetRetentionCurve(ctx context.Context, videoID uuid.UUID, date time.Time) ([]*domain.RetentionData, error)
	GetActiveViewers(ctx context.Context, videoID uuid.UUID) ([]*domain.ActiveViewer, error)
	GetActiveViewerCount(ctx context.Context, videoID uuid.UUID) (int, error)
	GetChannelDailyAnalyticsRange(ctx context.Context, channelID uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error)
	GetChannelTotalViews(ctx context.Context, channelID uuid.UUID) (int, error)
}
