package analytics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"athena/internal/domain"
	"athena/internal/repository"

	"github.com/google/uuid"
	"github.com/mssola/user_agent"
)

// Service provides video analytics operations
type Service struct {
	repo *repository.VideoAnalyticsRepository
}

// NewService creates a new analytics service
func NewService(repo *repository.VideoAnalyticsRepository) *Service {
	return &Service{
		repo: repo,
	}
}

// ======================================================================
// Event Collection
// ======================================================================

// TrackEvent records a new analytics event
func (s *Service) TrackEvent(ctx context.Context, event *domain.AnalyticsEvent) error {
	// Enrich event with parsed data
	s.enrichEvent(event)

	// Validate the event
	if err := event.Validate(); err != nil {
		return fmt.Errorf("invalid event: %w", err)
	}

	// Store the event
	if err := s.repo.CreateEvent(ctx, event); err != nil {
		return fmt.Errorf("failed to create event: %w", err)
	}

	// If this is a view event, also update active viewers
	if event.EventType == domain.EventTypeView {
		viewer := &domain.ActiveViewer{
			VideoID:   event.VideoID,
			SessionID: event.SessionID,
			UserID:    event.UserID,
		}
		_ = s.repo.UpsertActiveViewer(ctx, viewer) // Non-critical, so don't fail on error
	}

	return nil
}

// TrackEventsBatch records multiple analytics events
func (s *Service) TrackEventsBatch(ctx context.Context, events []*domain.AnalyticsEvent) error {
	if len(events) == 0 {
		return nil
	}

	// Enrich and validate all events
	for _, event := range events {
		s.enrichEvent(event)
		if err := event.Validate(); err != nil {
			return fmt.Errorf("invalid event in batch: %w", err)
		}
	}

	// Store events in batch
	if err := s.repo.CreateEventsBatch(ctx, events); err != nil {
		return fmt.Errorf("failed to create events batch: %w", err)
	}

	// Update active viewers for view events
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

// enrichEvent parses user agent and extracts device/browser/OS information
func (s *Service) enrichEvent(event *domain.AnalyticsEvent) {
	if event.UserAgent == "" {
		return
	}

	ua := user_agent.New(event.UserAgent)

	// Extract browser
	browserName, _ := ua.Browser()
	event.Browser = browserName

	// Extract OS
	event.OS = ua.OS()

	// Determine device type
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

// TrackViewerHeartbeat updates the heartbeat for an active viewer
func (s *Service) TrackViewerHeartbeat(ctx context.Context, videoID uuid.UUID, sessionID string, userID *uuid.UUID) error {
	viewer := &domain.ActiveViewer{
		VideoID:   videoID,
		SessionID: sessionID,
		UserID:    userID,
	}

	return s.repo.UpsertActiveViewer(ctx, viewer)
}

// GetActiveViewerCount returns the current number of active viewers for a video
func (s *Service) GetActiveViewerCount(ctx context.Context, videoID uuid.UUID) (int, error) {
	return s.repo.GetActiveViewerCount(ctx, videoID)
}

// GetActiveViewers returns the list of active viewers for a video
func (s *Service) GetActiveViewers(ctx context.Context, videoID uuid.UUID) ([]*domain.ActiveViewer, error) {
	return s.repo.GetActiveViewersForVideo(ctx, videoID)
}

// ======================================================================
// Aggregation
// ======================================================================

// AggregateDailyAnalytics aggregates raw events into daily analytics for a specific date
func (s *Service) AggregateDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) error {
	// Aggregate daily stats
	if err := s.repo.AggregateDailyAnalytics(ctx, videoID, date); err != nil {
		return fmt.Errorf("failed to aggregate daily analytics: %w", err)
	}

	// Calculate retention curve
	if err := s.repo.CalculateRetentionCurve(ctx, videoID, date); err != nil {
		return fmt.Errorf("failed to calculate retention curve: %w", err)
	}

	return nil
}

// AggregateAllVideosForDate aggregates analytics for all videos on a specific date
func (s *Service) AggregateAllVideosForDate(ctx context.Context, date time.Time) error {
	// This would ideally fetch all video IDs and aggregate them
	// For now, this is a placeholder that would be called by a scheduled job
	// TODO: Implement video list retrieval and batch processing
	return nil
}

// GetDailyAnalytics retrieves daily analytics for a video on a specific date
func (s *Service) GetDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) (*domain.DailyAnalytics, error) {
	analytics, err := s.repo.GetDailyAnalytics(ctx, videoID, date)
	if err != nil {
		return nil, fmt.Errorf("failed to get daily analytics: %w", err)
	}

	return analytics, nil
}

// GetDailyAnalyticsRange retrieves daily analytics for a video within a date range
func (s *Service) GetDailyAnalyticsRange(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error) {
	analytics, err := s.repo.GetDailyAnalyticsRange(ctx, videoID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get daily analytics range: %w", err)
	}

	return analytics, nil
}

// GetRetentionCurve retrieves the retention curve for a video
func (s *Service) GetRetentionCurve(ctx context.Context, videoID uuid.UUID, date time.Time) ([]*domain.RetentionData, error) {
	retention, err := s.repo.GetRetentionData(ctx, videoID, date)
	if err != nil {
		return nil, fmt.Errorf("failed to get retention data: %w", err)
	}

	return retention, nil
}

// ======================================================================
// Summary and Reports
// ======================================================================

// GetVideoAnalyticsSummary retrieves a comprehensive analytics summary for a video
func (s *Service) GetVideoAnalyticsSummary(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error) {
	summary, err := s.repo.GetVideoAnalyticsSummary(ctx, videoID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get video analytics summary: %w", err)
	}

	// Get retention curve for the date range (use end date)
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

// GetTotalViews returns the total view count for a video across all time
func (s *Service) GetTotalViews(ctx context.Context, videoID uuid.UUID) (int, error) {
	return s.repo.GetTotalViewsForVideo(ctx, videoID)
}

// ======================================================================
// Channel Analytics
// ======================================================================

// GetChannelDailyAnalytics retrieves daily analytics for a channel
func (s *Service) GetChannelDailyAnalytics(ctx context.Context, channelID uuid.UUID, date time.Time) (*domain.ChannelDailyAnalytics, error) {
	analytics, err := s.repo.GetChannelDailyAnalytics(ctx, channelID, date)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel daily analytics: %w", err)
	}

	return analytics, nil
}

// GetChannelDailyAnalyticsRange retrieves daily analytics for a channel within a date range
func (s *Service) GetChannelDailyAnalyticsRange(ctx context.Context, channelID uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error) {
	analytics, err := s.repo.GetChannelDailyAnalyticsRange(ctx, channelID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get channel daily analytics range: %w", err)
	}

	return analytics, nil
}

// GetChannelTotalViews returns the total view count for a channel across all time
func (s *Service) GetChannelTotalViews(ctx context.Context, channelID uuid.UUID) (int, error) {
	return s.repo.GetTotalViewsForChannel(ctx, channelID)
}

// ======================================================================
// Maintenance
// ======================================================================

// CleanupOldEvents removes analytics events older than the retention period
func (s *Service) CleanupOldEvents(ctx context.Context, retentionDays int) (int64, error) {
	count, err := s.repo.DeleteOldEvents(ctx, retentionDays)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup old events: %w", err)
	}

	return count, nil
}

// CleanupInactiveViewers removes viewer sessions with no recent heartbeat
func (s *Service) CleanupInactiveViewers(ctx context.Context) (int64, error) {
	count, err := s.repo.CleanupInactiveViewers(ctx)
	if err != nil {
		return 0, fmt.Errorf("failed to cleanup inactive viewers: %w", err)
	}

	return count, nil
}

// ======================================================================
// Analytics Queries
// ======================================================================

// GetEventsBySession retrieves all events for a specific session
func (s *Service) GetEventsBySession(ctx context.Context, sessionID string) ([]*domain.AnalyticsEvent, error) {
	events, err := s.repo.GetEventsBySessionID(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("failed to get events by session: %w", err)
	}

	return events, nil
}

// GetEventsByVideo retrieves events for a video within a date range
func (s *Service) GetEventsByVideo(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time, limit, offset int) ([]*domain.AnalyticsEvent, error) {
	events, err := s.repo.GetEventsByVideoID(ctx, videoID, startDate, endDate, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("failed to get events by video: %w", err)
	}

	return events, nil
}
