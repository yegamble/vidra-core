package analytics

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"athena/internal/domain"
	"athena/internal/port"

	"github.com/google/uuid"
	"github.com/mssola/user_agent"
)

const aggregateBatchSize = 100

type Service struct {
	repo      port.VideoAnalyticsRepository
	videoRepo port.VideoRepository
}

func NewService(repo port.VideoAnalyticsRepository, videoRepo port.VideoRepository) *Service {
	return &Service{
		repo:      repo,
		videoRepo: videoRepo,
	}
}

func (s *Service) TrackEvent(ctx context.Context, event *domain.AnalyticsEvent) error {
	s.enrichEvent(event)

	if err := event.Validate(); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}

	if err := s.repo.CreateEvent(ctx, event); err != nil {
		return fmt.Errorf("failed to create event: %w", err)
	}

	if event.EventType == domain.EventTypeView {
		viewer := &domain.ActiveViewer{
			VideoID:   event.VideoID,
			SessionID: event.SessionID,
			UserID:    event.UserID,
		}
		_ = s.repo.UpsertActiveViewer(ctx, viewer)
	}

	return nil
}

func (s *Service) TrackEventsBatch(ctx context.Context, events []*domain.AnalyticsEvent) error {
	if len(events) == 0 {
		return nil
	}

	for _, event := range events {
		s.enrichEvent(event)
		if err := event.Validate(); err != nil {
			return fmt.Errorf("invalid event in batch: %w", err)
		}
	}

	if err := s.repo.CreateEventsBatch(ctx, events); err != nil {
		return fmt.Errorf("failed to create events batch: %w", err)
	}

	var viewers []*domain.ActiveViewer
	for _, event := range events {
		if event.EventType == domain.EventTypeView {
			viewer := &domain.ActiveViewer{
				VideoID:   event.VideoID,
				SessionID: event.SessionID,
				UserID:    event.UserID,
			}
			viewers = append(viewers, viewer)
		}
	}

	if len(viewers) > 0 {
		_ = s.repo.UpsertActiveViewersBatch(ctx, viewers)
	}

	return nil
}

func (s *Service) enrichEvent(event *domain.AnalyticsEvent) {
	if event.UserAgent == "" {
		return
	}

	ua := user_agent.New(event.UserAgent)

	browserName, _ := ua.Browser()
	event.Browser = browserName

	event.OS = ua.OS()

	if ua.Mobile() {
		event.DeviceType = domain.VideoDeviceTypeMobile
	} else if strings.Contains(strings.ToLower(event.UserAgent), "tablet") || strings.Contains(strings.ToLower(event.UserAgent), "ipad") {
		event.DeviceType = domain.VideoDeviceTypeTablet
	} else if strings.Contains(strings.ToLower(event.UserAgent), "tv") || strings.Contains(strings.ToLower(event.UserAgent), "smart-tv") {
		event.DeviceType = domain.VideoDeviceTypeTV
	} else {
		event.DeviceType = domain.VideoDeviceTypeDesktop
	}
}

func (s *Service) TrackViewerHeartbeat(ctx context.Context, videoID uuid.UUID, sessionID string, userID *uuid.UUID) error {
	viewer := &domain.ActiveViewer{
		VideoID:   videoID,
		SessionID: sessionID,
		UserID:    userID,
	}

	return s.repo.UpsertActiveViewer(ctx, viewer)
}

func (s *Service) GetActiveViewerCount(ctx context.Context, videoID uuid.UUID) (int, error) {
	return s.repo.GetActiveViewerCount(ctx, videoID)
}

func (s *Service) GetActiveViewers(ctx context.Context, videoID uuid.UUID) ([]*domain.ActiveViewer, error) {
	return s.repo.GetActiveViewersForVideo(ctx, videoID)
}

func (s *Service) AggregateDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) error {
	if err := s.repo.AggregateDailyAnalytics(ctx, videoID, date); err != nil {
		return fmt.Errorf("failed to aggregate daily analytics: %w", err)
	}

	if err := s.repo.CalculateRetentionCurve(ctx, videoID, date); err != nil {
		return fmt.Errorf("failed to calculate retention curve: %w", err)
	}

	return nil
}

func (s *Service) AggregateAllVideosForDate(ctx context.Context, date time.Time) error {
	if s.videoRepo == nil {
		return nil
	}

	offset := 0
	for {
		videos, _, err := s.videoRepo.List(ctx, &domain.VideoSearchRequest{
			Limit:  aggregateBatchSize,
			Offset: offset,
		})
		if err != nil {
			return fmt.Errorf("failed to list videos for aggregation: %w", err)
		}
		if len(videos) == 0 {
			break
		}

		for _, v := range videos {
			videoID, err := uuid.Parse(v.ID)
			if err != nil {
				slog.Warn("skipping video with invalid ID during aggregation", "video_id", v.ID, "error", err)
				continue
			}
			if err := s.AggregateDailyAnalytics(ctx, videoID, date); err != nil {
				slog.Error("failed to aggregate video, continuing batch", "video_id", v.ID, "error", err)
				continue
			}
		}

		offset += aggregateBatchSize
	}

	return nil
}

func (s *Service) GetDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) (*domain.DailyAnalytics, error) {
	analytics, err := s.repo.GetDailyAnalytics(ctx, videoID, date)
	if err != nil {
		return nil, fmt.Errorf("failed to get daily analytics: %w", err)
	}

	return analytics, nil
}

func (s *Service) GetDailyAnalyticsRange(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error) {
	analytics, err := s.repo.GetDailyAnalyticsRange(ctx, videoID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get daily analytics range: %w", err)
	}

	return analytics, nil
}

func (s *Service) GetRetentionCurve(ctx context.Context, videoID uuid.UUID, date time.Time) ([]*domain.RetentionData, error) {
	retention, err := s.repo.GetRetentionData(ctx, videoID, date)
	if err != nil {
		return nil, fmt.Errorf("failed to get retention data: %w", err)
	}

	return retention, nil
}

func (s *Service) GetVideoAnalyticsSummary(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error) {
	summary, err := s.repo.GetVideoAnalyticsSummary(ctx, videoID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get video analytics summary: %w", err)
	}

	retention, err := s.repo.GetRetentionData(ctx, videoID, endDate)
	if err == nil && len(retention) > 0 {
		summary.RetentionCurve = make([]domain.RetentionPoint, len(retention))
		for i, r := range retention {
			summary.RetentionCurve[i] = domain.RetentionPoint{
				Timestamp: r.TimestampSeconds,
				Viewers:   r.ViewerCount,
			}
		}
	}

	return summary, nil
}

func (s *Service) GetTotalViews(ctx context.Context, videoID uuid.UUID) (int, error) {
	return s.repo.GetTotalViewsForVideo(ctx, videoID)
}

func (s *Service) GetChannelDailyAnalytics(ctx context.Context, channelID uuid.UUID, date time.Time) (*domain.ChannelDailyAnalytics, error) {
	analytics, err := s.repo.GetChannelDailyAnalytics(ctx, channelID, date)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel daily analytics: %w", err)
	}

	return analytics, nil
}

func (s *Service) GetChannelDailyAnalyticsRange(ctx context.Context, channelID uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error) {
	analytics, err := s.repo.GetChannelDailyAnalyticsRange(ctx, channelID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel daily analytics range: %w", err)
	}

	return analytics, nil
}

func (s *Service) GetChannelTotalViews(ctx context.Context, channelID uuid.UUID) (int, error) {
	return s.repo.GetTotalViewsForChannel(ctx, channelID)
}

func (s *Service) CleanupOldEvents(ctx context.Context, retentionDays int) (int64, error) {
	count, err := s.repo.DeleteOldEvents(ctx, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old events: %w", err)
	}

	return count, nil
}

func (s *Service) CleanupInactiveViewers(ctx context.Context) (int64, error) {
	count, err := s.repo.CleanupInactiveViewers(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup inactive viewers: %w", err)
	}

	return count, nil
}

func (s *Service) GetEventsBySession(ctx context.Context, sessionID string) ([]*domain.AnalyticsEvent, error) {
	events, err := s.repo.GetEventsBySessionID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get events by session: %w", err)
	}

	return events, nil
}

func (s *Service) GetEventsByVideo(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time, limit, offset int) ([]*domain.AnalyticsEvent, error) {
	events, err := s.repo.GetEventsByVideoID(ctx, videoID, startDate, endDate, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get events by video: %w", err)
	}

	return events, nil
}
