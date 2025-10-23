package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// VideoAnalyticsRepository handles video analytics data persistence
type VideoAnalyticsRepository struct {
	db *sqlx.DB
}

// NewVideoAnalyticsRepository creates a new video analytics repository
func NewVideoAnalyticsRepository(db *sqlx.DB) *VideoAnalyticsRepository {
	return &VideoAnalyticsRepository{db: db}
}

// ======================================================================
// Analytics Events (Raw Data)
// ======================================================================

// CreateEvent inserts a new analytics event
func (r *VideoAnalyticsRepository) CreateEvent(ctx context.Context, event *domain.AnalyticsEvent) error {
	query := `
		INSERT INTO video_analytics_events (
			id, video_id, event_type, user_id, session_id, timestamp_seconds,
			watch_duration_seconds, ip_address, user_agent, country_code, region,
			city, device_type, browser, os, referrer, quality, player_version, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19
		)`

	event.ID = uuid.New()
	event.CreatedAt = time.Now()

	_, err := r.db.ExecContext(ctx, query,
		event.ID, event.VideoID, event.EventType, event.UserID, event.SessionID,
		event.TimestampSeconds, event.WatchDurationSecs, event.IPAddress, event.UserAgent,
		event.CountryCode, event.Region, event.City, event.DeviceType, event.Browser,
		event.OS, event.Referrer, event.Quality, event.PlayerVersion, event.CreatedAt,
	)

	return err
}

// CreateEventsBatch inserts multiple analytics events in a single transaction
func (r *VideoAnalyticsRepository) CreateEventsBatch(ctx context.Context, events []*domain.AnalyticsEvent) error {
	if len(events) == 0 {
		return nil
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	query := `
		INSERT INTO video_analytics_events (
			id, video_id, event_type, user_id, session_id, timestamp_seconds,
			watch_duration_seconds, ip_address, user_agent, country_code, region,
			city, device_type, browser, os, referrer, quality, player_version, created_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19
		)`

	stmt, err := tx.PreparexContext(ctx, query)
	if err != nil {
		return err
	}
	defer stmt.Close()

	for _, event := range events {
		event.ID = uuid.New()
		event.CreatedAt = time.Now()

		_, err = stmt.ExecContext(ctx,
			event.ID, event.VideoID, event.EventType, event.UserID, event.SessionID,
			event.TimestampSeconds, event.WatchDurationSecs, event.IPAddress, event.UserAgent,
			event.CountryCode, event.Region, event.City, event.DeviceType, event.Browser,
			event.OS, event.Referrer, event.Quality, event.PlayerVersion, event.CreatedAt,
		)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

// GetEventsByVideoID retrieves analytics events for a video within a date range
func (r *VideoAnalyticsRepository) GetEventsByVideoID(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time, limit, offset int) ([]*domain.AnalyticsEvent, error) {
	query := `
		SELECT id, video_id, event_type, user_id, session_id, timestamp_seconds,
			   watch_duration_seconds, ip_address, user_agent, country_code, region,
			   city, device_type, browser, os, referrer, quality, player_version, created_at
		FROM video_analytics_events
		WHERE video_id = $1 AND created_at >= $2 AND created_at <= $3
		ORDER BY created_at DESC
		LIMIT $4 OFFSET $5`

	var events []*domain.AnalyticsEvent
	err := r.db.SelectContext(ctx, &events, query, videoID, startDate, endDate, limit, offset)
	if err != nil {
		return nil, err
	}

	return events, nil
}

// GetEventsBySessionID retrieves all events for a specific session
func (r *VideoAnalyticsRepository) GetEventsBySessionID(ctx context.Context, sessionID string) ([]*domain.AnalyticsEvent, error) {
	query := `
		SELECT id, video_id, event_type, user_id, session_id, timestamp_seconds,
			   watch_duration_seconds, ip_address, user_agent, country_code, region,
			   city, device_type, browser, os, referrer, quality, player_version, created_at
		FROM video_analytics_events
		WHERE session_id = $1
		ORDER BY created_at ASC`

	var events []*domain.AnalyticsEvent
	err := r.db.SelectContext(ctx, &events, query, sessionID)
	if err != nil {
		return nil, err
	}

	return events, nil
}

// DeleteOldEvents deletes events older than the retention period
func (r *VideoAnalyticsRepository) DeleteOldEvents(ctx context.Context, retentionDays int) (int64, error) {
	query := `DELETE FROM video_analytics_events WHERE created_at < $1`
	cutoffDate := time.Now().AddDate(0, 0, -retentionDays)

	result, err := r.db.ExecContext(ctx, query, cutoffDate)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// ======================================================================
// Daily Analytics (Aggregated Data)
// ======================================================================

// GetDailyAnalytics retrieves daily analytics for a video on a specific date
func (r *VideoAnalyticsRepository) GetDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) (*domain.DailyAnalytics, error) {
	query := `
		SELECT id, video_id, date, views, unique_viewers, watch_time_seconds,
			   avg_watch_percentage, completion_rate, likes, dislikes, comments,
			   shares, downloads, countries, devices, browsers, traffic_sources,
			   qualities, peak_concurrent_viewers, errors, buffering_events,
			   avg_buffering_duration_seconds, created_at, updated_at
		FROM video_analytics_daily
		WHERE video_id = $1 AND date = $2`

	var analytics domain.DailyAnalytics
	err := r.db.GetContext(ctx, &analytics, query, videoID, date.Format("2006-01-02"))
	if err == sql.ErrNoRows {
		return nil, domain.ErrAnalyticsDailyNotFound
	}
	if err != nil {
		return nil, err
	}

	return &analytics, nil
}

// GetDailyAnalyticsRange retrieves daily analytics for a video within a date range
func (r *VideoAnalyticsRepository) GetDailyAnalyticsRange(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) ([]*domain.DailyAnalytics, error) {
	query := `
		SELECT id, video_id, date, views, unique_viewers, watch_time_seconds,
			   avg_watch_percentage, completion_rate, likes, dislikes, comments,
			   shares, downloads, countries, devices, browsers, traffic_sources,
			   qualities, peak_concurrent_viewers, errors, buffering_events,
			   avg_buffering_duration_seconds, created_at, updated_at
		FROM video_analytics_daily
		WHERE video_id = $1 AND date >= $2 AND date <= $3
		ORDER BY date ASC`

	var analytics []*domain.DailyAnalytics
	err := r.db.SelectContext(ctx, &analytics, query, videoID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}

	return analytics, nil
}

// UpsertDailyAnalytics creates or updates daily analytics
func (r *VideoAnalyticsRepository) UpsertDailyAnalytics(ctx context.Context, analytics *domain.DailyAnalytics) error {
	query := `
		INSERT INTO video_analytics_daily (
			id, video_id, date, views, unique_viewers, watch_time_seconds,
			avg_watch_percentage, completion_rate, likes, dislikes, comments,
			shares, downloads, countries, devices, browsers, traffic_sources,
			qualities, peak_concurrent_viewers, errors, buffering_events,
			avg_buffering_duration_seconds, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15,
			$16, $17, $18, $19, $20, $21, $22, $23, $24
		)
		ON CONFLICT (video_id, date) DO UPDATE SET
			views = EXCLUDED.views,
			unique_viewers = EXCLUDED.unique_viewers,
			watch_time_seconds = EXCLUDED.watch_time_seconds,
			avg_watch_percentage = EXCLUDED.avg_watch_percentage,
			completion_rate = EXCLUDED.completion_rate,
			likes = EXCLUDED.likes,
			dislikes = EXCLUDED.dislikes,
			comments = EXCLUDED.comments,
			shares = EXCLUDED.shares,
			downloads = EXCLUDED.downloads,
			countries = EXCLUDED.countries,
			devices = EXCLUDED.devices,
			browsers = EXCLUDED.browsers,
			traffic_sources = EXCLUDED.traffic_sources,
			qualities = EXCLUDED.qualities,
			peak_concurrent_viewers = EXCLUDED.peak_concurrent_viewers,
			errors = EXCLUDED.errors,
			buffering_events = EXCLUDED.buffering_events,
			avg_buffering_duration_seconds = EXCLUDED.avg_buffering_duration_seconds,
			updated_at = CURRENT_TIMESTAMP`

	if analytics.ID == uuid.Nil {
		analytics.ID = uuid.New()
	}
	if analytics.CreatedAt.IsZero() {
		analytics.CreatedAt = time.Now()
	}
	analytics.UpdatedAt = time.Now()

	// Ensure empty JSON objects for null fields
	if analytics.Countries == nil {
		analytics.Countries = json.RawMessage("{}")
	}
	if analytics.Devices == nil {
		analytics.Devices = json.RawMessage("{}")
	}
	if analytics.Browsers == nil {
		analytics.Browsers = json.RawMessage("{}")
	}
	if analytics.TrafficSources == nil {
		analytics.TrafficSources = json.RawMessage("{}")
	}
	if analytics.Qualities == nil {
		analytics.Qualities = json.RawMessage("{}")
	}

	_, err := r.db.ExecContext(ctx, query,
		analytics.ID, analytics.VideoID, analytics.Date, analytics.Views,
		analytics.UniqueViewers, analytics.WatchTimeSeconds, analytics.AvgWatchPercentage,
		analytics.CompletionRate, analytics.Likes, analytics.Dislikes, analytics.Comments,
		analytics.Shares, analytics.Downloads, analytics.Countries, analytics.Devices,
		analytics.Browsers, analytics.TrafficSources, analytics.Qualities,
		analytics.PeakConcurrentViewers, analytics.Errors, analytics.BufferingEvents,
		analytics.AvgBufferingDurationSecs, analytics.CreatedAt, analytics.UpdatedAt,
	)

	return err
}

// AggregateDailyAnalytics aggregates raw events into daily analytics for a specific date
func (r *VideoAnalyticsRepository) AggregateDailyAnalytics(ctx context.Context, videoID uuid.UUID, date time.Time) error {
	query := `
		WITH event_stats AS (
			SELECT
				video_id,
				COUNT(DISTINCT CASE WHEN event_type = 'view' THEN session_id END) AS views,
				COUNT(DISTINCT session_id) AS unique_viewers,
				COALESCE(SUM(watch_duration_seconds), 0) AS total_watch_time,
				COUNT(CASE WHEN event_type = 'complete' THEN 1 END) AS completions,
				COUNT(CASE WHEN event_type = 'buffer' THEN 1 END) AS buffering_events,
				COUNT(CASE WHEN event_type = 'error' THEN 1 END) AS errors
			FROM video_analytics_events
			WHERE video_id = $1
			  AND created_at >= $2::date
			  AND created_at < ($2::date + INTERVAL '1 day')
			GROUP BY video_id
		),
		aggregated_json AS (
			SELECT
				video_id,
				jsonb_object_agg(COALESCE(country_code, 'unknown'), country_count) AS countries,
				jsonb_object_agg(COALESCE(device_type::text, 'unknown'), device_count) AS devices,
				jsonb_object_agg(COALESCE(browser, 'unknown'), browser_count) AS browsers,
				jsonb_object_agg(COALESCE(quality, 'unknown'), quality_count) AS qualities
			FROM (
				SELECT
					video_id,
					country_code,
					COUNT(*) AS country_count,
					device_type,
					COUNT(*) AS device_count,
					browser,
					COUNT(*) AS browser_count,
					quality,
					COUNT(*) AS quality_count
				FROM video_analytics_events
				WHERE video_id = $1
				  AND created_at >= $2::date
				  AND created_at < ($2::date + INTERVAL '1 day')
				GROUP BY video_id, country_code, device_type, browser, quality
			) sub
			GROUP BY video_id
		)
		INSERT INTO video_analytics_daily (
			id, video_id, date, views, unique_viewers, watch_time_seconds,
			completion_rate, countries, devices, browsers,
			qualities, buffering_events, errors, created_at, updated_at
		)
		SELECT
			gen_random_uuid(),
			es.video_id,
			$2::date,
			es.views,
			es.unique_viewers,
			es.total_watch_time,
			CASE WHEN es.views > 0 THEN (es.completions::float / es.views::float * 100) ELSE 0 END,
			COALESCE(aj.countries, '{}'::jsonb),
			COALESCE(aj.devices, '{}'::jsonb),
			COALESCE(aj.browsers, '{}'::jsonb),
			COALESCE(aj.qualities, '{}'::jsonb),
			es.buffering_events,
			es.errors,
			CURRENT_TIMESTAMP,
			CURRENT_TIMESTAMP
		FROM event_stats es
		LEFT JOIN aggregated_json aj ON es.video_id = aj.video_id
		ON CONFLICT (video_id, date) DO UPDATE SET
			views = EXCLUDED.views,
			unique_viewers = EXCLUDED.unique_viewers,
			watch_time_seconds = EXCLUDED.watch_time_seconds,
			completion_rate = EXCLUDED.completion_rate,
			countries = EXCLUDED.countries,
			devices = EXCLUDED.devices,
			browsers = EXCLUDED.browsers,
			qualities = EXCLUDED.qualities,
			buffering_events = EXCLUDED.buffering_events,
			errors = EXCLUDED.errors,
			updated_at = CURRENT_TIMESTAMP`

	_, err := r.db.ExecContext(ctx, query, videoID, date.Format("2006-01-02"))
	return err
}

// ======================================================================
// Retention Data
// ======================================================================

// GetRetentionData retrieves retention curve data for a video on a specific date
func (r *VideoAnalyticsRepository) GetRetentionData(ctx context.Context, videoID uuid.UUID, date time.Time) ([]*domain.RetentionData, error) {
	query := `
		SELECT id, video_id, timestamp_seconds, viewer_count, date, created_at, updated_at
		FROM video_analytics_retention
		WHERE video_id = $1 AND date = $2
		ORDER BY timestamp_seconds ASC`

	var retention []*domain.RetentionData
	err := r.db.SelectContext(ctx, &retention, query, videoID, date.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}

	return retention, nil
}

// UpsertRetentionData creates or updates retention data for a specific timestamp
func (r *VideoAnalyticsRepository) UpsertRetentionData(ctx context.Context, retention *domain.RetentionData) error {
	query := `
		INSERT INTO video_analytics_retention (
			id, video_id, timestamp_seconds, viewer_count, date, created_at, updated_at
		) VALUES ($1, $2, $3, $4, $5, $6, $7)
		ON CONFLICT (video_id, date, timestamp_seconds) DO UPDATE SET
			viewer_count = EXCLUDED.viewer_count,
			updated_at = CURRENT_TIMESTAMP`

	if retention.ID == uuid.Nil {
		retention.ID = uuid.New()
	}
	if retention.CreatedAt.IsZero() {
		retention.CreatedAt = time.Now()
	}
	retention.UpdatedAt = time.Now()

	_, err := r.db.ExecContext(ctx, query,
		retention.ID, retention.VideoID, retention.TimestampSeconds,
		retention.ViewerCount, retention.Date, retention.CreatedAt, retention.UpdatedAt,
	)

	return err
}

// CalculateRetentionCurve calculates and stores the retention curve for a video on a specific date
func (r *VideoAnalyticsRepository) CalculateRetentionCurve(ctx context.Context, videoID uuid.UUID, date time.Time) error {
	query := `
		WITH retention_points AS (
			SELECT
				video_id,
				timestamp_seconds,
				COUNT(DISTINCT session_id) AS viewer_count
			FROM video_analytics_events
			WHERE video_id = $1
			  AND created_at >= $2::date
			  AND created_at < ($2::date + INTERVAL '1 day')
			  AND timestamp_seconds IS NOT NULL
			GROUP BY video_id, timestamp_seconds
		)
		INSERT INTO video_analytics_retention (
			id, video_id, timestamp_seconds, viewer_count, date, created_at, updated_at
		)
		SELECT
			gen_random_uuid(),
			video_id,
			timestamp_seconds,
			viewer_count,
			$2::date,
			CURRENT_TIMESTAMP,
			CURRENT_TIMESTAMP
		FROM retention_points
		ON CONFLICT (video_id, date, timestamp_seconds) DO UPDATE SET
			viewer_count = EXCLUDED.viewer_count,
			updated_at = CURRENT_TIMESTAMP`

	_, err := r.db.ExecContext(ctx, query, videoID, date.Format("2006-01-02"))
	return err
}

// ======================================================================
// Channel Analytics
// ======================================================================

// GetChannelDailyAnalytics retrieves daily analytics for a channel on a specific date
func (r *VideoAnalyticsRepository) GetChannelDailyAnalytics(ctx context.Context, channelID uuid.UUID, date time.Time) (*domain.ChannelDailyAnalytics, error) {
	query := `
		SELECT id, channel_id, date, views, unique_viewers, watch_time_seconds,
			   subscribers_gained, subscribers_lost, total_subscribers, likes,
			   comments, shares, videos_published, created_at, updated_at
		FROM channel_analytics_daily
		WHERE channel_id = $1 AND date = $2`

	var analytics domain.ChannelDailyAnalytics
	err := r.db.GetContext(ctx, &analytics, query, channelID, date.Format("2006-01-02"))
	if err == sql.ErrNoRows {
		return nil, domain.ErrAnalyticsDailyNotFound
	}
	if err != nil {
		return nil, err
	}

	return &analytics, nil
}

// GetChannelDailyAnalyticsRange retrieves daily analytics for a channel within a date range
func (r *VideoAnalyticsRepository) GetChannelDailyAnalyticsRange(ctx context.Context, channelID uuid.UUID, startDate, endDate time.Time) ([]*domain.ChannelDailyAnalytics, error) {
	query := `
		SELECT id, channel_id, date, views, unique_viewers, watch_time_seconds,
			   subscribers_gained, subscribers_lost, total_subscribers, likes,
			   comments, shares, videos_published, created_at, updated_at
		FROM channel_analytics_daily
		WHERE channel_id = $1 AND date >= $2 AND date <= $3
		ORDER BY date ASC`

	var analytics []*domain.ChannelDailyAnalytics
	err := r.db.SelectContext(ctx, &analytics, query, channelID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}

	return analytics, nil
}

// UpsertChannelDailyAnalytics creates or updates channel daily analytics
func (r *VideoAnalyticsRepository) UpsertChannelDailyAnalytics(ctx context.Context, analytics *domain.ChannelDailyAnalytics) error {
	query := `
		INSERT INTO channel_analytics_daily (
			id, channel_id, date, views, unique_viewers, watch_time_seconds,
			subscribers_gained, subscribers_lost, total_subscribers, likes,
			comments, shares, videos_published, created_at, updated_at
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15
		)
		ON CONFLICT (channel_id, date) DO UPDATE SET
			views = EXCLUDED.views,
			unique_viewers = EXCLUDED.unique_viewers,
			watch_time_seconds = EXCLUDED.watch_time_seconds,
			subscribers_gained = EXCLUDED.subscribers_gained,
			subscribers_lost = EXCLUDED.subscribers_lost,
			total_subscribers = EXCLUDED.total_subscribers,
			likes = EXCLUDED.likes,
			comments = EXCLUDED.comments,
			shares = EXCLUDED.shares,
			videos_published = EXCLUDED.videos_published,
			updated_at = CURRENT_TIMESTAMP`

	if analytics.ID == uuid.Nil {
		analytics.ID = uuid.New()
	}
	if analytics.CreatedAt.IsZero() {
		analytics.CreatedAt = time.Now()
	}
	analytics.UpdatedAt = time.Now()

	_, err := r.db.ExecContext(ctx, query,
		analytics.ID, analytics.ChannelID, analytics.Date, analytics.Views,
		analytics.UniqueViewers, analytics.WatchTimeSeconds, analytics.SubscribersGained,
		analytics.SubscribersLost, analytics.TotalSubscribers, analytics.Likes,
		analytics.Comments, analytics.Shares, analytics.VideosPublished,
		analytics.CreatedAt, analytics.UpdatedAt,
	)

	return err
}

// ======================================================================
// Active Viewers (Real-time)
// ======================================================================

// UpsertActiveViewer creates or updates an active viewer heartbeat
func (r *VideoAnalyticsRepository) UpsertActiveViewer(ctx context.Context, viewer *domain.ActiveViewer) error {
	query := `
		INSERT INTO video_active_viewers (
			id, video_id, session_id, user_id, last_heartbeat, created_at
		) VALUES ($1, $2, $3, $4, $5, $6)
		ON CONFLICT (video_id, session_id) DO UPDATE SET
			last_heartbeat = EXCLUDED.last_heartbeat`

	if viewer.ID == uuid.Nil {
		viewer.ID = uuid.New()
	}
	if viewer.CreatedAt.IsZero() {
		viewer.CreatedAt = time.Now()
	}
	viewer.LastHeartbeat = time.Now()

	_, err := r.db.ExecContext(ctx, query,
		viewer.ID, viewer.VideoID, viewer.SessionID, viewer.UserID,
		viewer.LastHeartbeat, viewer.CreatedAt,
	)

	return err
}

// GetActiveViewerCount returns the current number of active viewers for a video
func (r *VideoAnalyticsRepository) GetActiveViewerCount(ctx context.Context, videoID uuid.UUID) (int, error) {
	query := `
		SELECT COUNT(*)
		FROM video_active_viewers
		WHERE video_id = $1
		  AND last_heartbeat > CURRENT_TIMESTAMP - INTERVAL '30 seconds'`

	var count int
	err := r.db.GetContext(ctx, &count, query, videoID)
	if err != nil {
		return 0, err
	}

	return count, nil
}

// GetActiveViewersForVideo returns the list of active viewers for a video
func (r *VideoAnalyticsRepository) GetActiveViewersForVideo(ctx context.Context, videoID uuid.UUID) ([]*domain.ActiveViewer, error) {
	query := `
		SELECT id, video_id, session_id, user_id, last_heartbeat, created_at
		FROM video_active_viewers
		WHERE video_id = $1
		  AND last_heartbeat > CURRENT_TIMESTAMP - INTERVAL '30 seconds'
		ORDER BY last_heartbeat DESC`

	var viewers []*domain.ActiveViewer
	err := r.db.SelectContext(ctx, &viewers, query, videoID)
	if err != nil {
		return nil, err
	}

	return viewers, nil
}

// CleanupInactiveViewers removes viewer records with no recent heartbeat
func (r *VideoAnalyticsRepository) CleanupInactiveViewers(ctx context.Context) (int64, error) {
	query := `
		DELETE FROM video_active_viewers
		WHERE last_heartbeat < CURRENT_TIMESTAMP - INTERVAL '30 seconds'`

	result, err := r.db.ExecContext(ctx, query)
	if err != nil {
		return 0, err
	}

	return result.RowsAffected()
}

// ======================================================================
// Summary and Aggregations
// ======================================================================

// GetVideoAnalyticsSummary retrieves a comprehensive analytics summary for a video
func (r *VideoAnalyticsRepository) GetVideoAnalyticsSummary(ctx context.Context, videoID uuid.UUID, startDate, endDate time.Time) (*domain.AnalyticsSummary, error) {
	query := `
		SELECT
			$1 AS video_id,
			COALESCE(SUM(views), 0) AS total_views,
			COALESCE(SUM(unique_viewers), 0) AS total_unique_viewers,
			COALESCE(SUM(watch_time_seconds), 0) AS total_watch_time_seconds,
			COALESCE(AVG(avg_watch_percentage), 0) AS avg_watch_percentage,
			COALESCE(AVG(completion_rate), 0) AS avg_completion_rate,
			COALESCE(SUM(likes), 0) AS total_likes,
			COALESCE(SUM(dislikes), 0) AS total_dislikes,
			COALESCE(SUM(comments), 0) AS total_comments,
			COALESCE(SUM(shares), 0) AS total_shares,
			COALESCE(MAX(peak_concurrent_viewers), 0) AS peak_viewers
		FROM video_analytics_daily
		WHERE video_id = $1 AND date >= $2 AND date <= $3`

	var summary domain.AnalyticsSummary
	err := r.db.GetContext(ctx, &summary, query, videoID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02"))
	if err != nil {
		return nil, err
	}

	// Get current active viewers
	activeCount, err := r.GetActiveViewerCount(ctx, videoID)
	if err == nil {
		summary.CurrentViewers = activeCount
	}

	// Initialize empty slices
	summary.TopCountries = []domain.CountryStat{}
	summary.DeviceBreakdown = []domain.DeviceStat{}
	summary.QualityBreakdown = []domain.QualityStat{}
	summary.TrafficSources = []domain.TrafficSource{}
	summary.RetentionCurve = []domain.RetentionPoint{}

	return &summary, nil
}

// GetTotalViewsForVideo returns the total view count for a video across all time
func (r *VideoAnalyticsRepository) GetTotalViewsForVideo(ctx context.Context, videoID uuid.UUID) (int, error) {
	query := `
		SELECT COALESCE(SUM(views), 0)
		FROM video_analytics_daily
		WHERE video_id = $1`

	var totalViews int
	err := r.db.GetContext(ctx, &totalViews, query, videoID)
	return totalViews, err
}

// GetTotalViewsForChannel returns the total view count for a channel across all time
func (r *VideoAnalyticsRepository) GetTotalViewsForChannel(ctx context.Context, channelID uuid.UUID) (int, error) {
	query := `
		SELECT COALESCE(SUM(views), 0)
		FROM channel_analytics_daily
		WHERE channel_id = $1`

	var totalViews int
	err := r.db.GetContext(ctx, &totalViews, query, channelID)
	return totalViews, err
}
