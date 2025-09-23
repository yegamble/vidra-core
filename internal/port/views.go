package port

import (
	"athena/internal/domain"
	"context"
	"time"
)

type ViewsRepository interface {
	CreateUserView(ctx context.Context, view *domain.UserView) error
	UpdateUserView(ctx context.Context, view *domain.UserView) error
	GetUserViewBySessionAndVideo(ctx context.Context, sessionID, videoID string) (*domain.UserView, error)
	GetVideoAnalytics(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error)
	GetDailyVideoStats(ctx context.Context, videoID string, startDate, endDate time.Time) ([]domain.DailyVideoStats, error)
	GetUserEngagementStats(ctx context.Context, userID string, startDate, endDate time.Time) ([]domain.UserEngagementStats, error)
	GetTrendingVideos(ctx context.Context, limit int) ([]domain.TrendingVideo, error)
	UpdateTrendingVideo(ctx context.Context, trending *domain.TrendingVideo) error
	IncrementVideoViews(ctx context.Context, videoID string) error
	GetUniqueViews(ctx context.Context, videoID string, startDate, endDate time.Time) (int64, error)
	CalculateEngagementScore(ctx context.Context, videoID string, hoursBack int) (float64, error)
	AggregateDailyStats(ctx context.Context, date time.Time) error
	CleanupOldViews(ctx context.Context, daysToKeep int) error
	GetViewsByDateRange(ctx context.Context, filter *domain.ViewAnalyticsFilter) ([]domain.UserView, error)
	GetTopVideos(ctx context.Context, startDate, endDate time.Time, limit int) ([]struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}, error)
}
