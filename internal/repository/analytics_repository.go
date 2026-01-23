package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"athena/internal/domain"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
)

// AnalyticsRepository handles analytics data operations
type AnalyticsRepository interface {
	// Analytics data
	CreateAnalytics(ctx context.Context, analytics *domain.StreamAnalytics) error
	GetAnalyticsByStream(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.StreamAnalytics, error)
	GetAnalyticsTimeSeries(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.AnalyticsDataPoint, error)
	GetLatestAnalytics(ctx context.Context, streamID uuid.UUID) (*domain.StreamAnalytics, error)

	// Summary statistics
	GetStreamSummary(ctx context.Context, streamID uuid.UUID) (*domain.StreamStatsSummary, error)
	UpdateStreamSummary(ctx context.Context, streamID uuid.UUID) error
	CreateOrUpdateSummary(ctx context.Context, summary *domain.StreamStatsSummary) error

	// Viewer sessions
	CreateViewerSession(ctx context.Context, session *domain.AnalyticsViewerSession) error
	EndViewerSession(ctx context.Context, sessionID string) error
	GetActiveViewers(ctx context.Context, streamID uuid.UUID) ([]*domain.AnalyticsViewerSession, error)
	GetViewerSession(ctx context.Context, sessionID string) (*domain.AnalyticsViewerSession, error)
	UpdateSessionEngagement(ctx context.Context, sessionID string, messagesSent int, liked, shared bool) error

	// Utility
	CleanupOldAnalytics(ctx context.Context, retentionDays int) error
	GetCurrentViewerCount(ctx context.Context, streamID uuid.UUID) (int, error)

	// Batch operations
	GetActiveViewersForStreams(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID][]*domain.AnalyticsViewerSession, error)
	GetCurrentViewerCounts(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID]int, error)
	BatchCreateAnalytics(ctx context.Context, analytics []*domain.StreamAnalytics) error
	BatchUpdateStreamSummaries(ctx context.Context, streamIDs []uuid.UUID) error
}

// analyticsRepository implements AnalyticsRepository
type analyticsRepository struct {
	db *sqlx.DB
}

// NewAnalyticsRepository creates a new analytics repository
func NewAnalyticsRepository(db *sqlx.DB) AnalyticsRepository {
	return &analyticsRepository{db: db}
}

// CreateAnalytics creates a new analytics record
func (r *analyticsRepository) CreateAnalytics(ctx context.Context, analytics *domain.StreamAnalytics) error {
	query := `
		INSERT INTO stream_analytics (
			id, stream_id, collected_at,
			viewer_count, peak_viewer_count, unique_viewers, average_watch_time,
			chat_messages_count, chat_participants, likes_count, shares_count,
			bitrate, framerate, resolution, buffering_ratio, avg_latency,
			viewer_countries, viewer_devices, viewer_browsers,
			created_at, updated_at
		) VALUES (
			$1, $2, $3,
			$4, $5, $6, $7,
			$8, $9, $10, $11,
			$12, $13, $14, $15, $16,
			$17, $18, $19,
			$20, $21
		)`

	_, err := r.db.ExecContext(ctx, query,
		analytics.ID, analytics.StreamID, analytics.CollectedAt,
		analytics.ViewerCount, analytics.PeakViewerCount, analytics.UniqueViewers, analytics.AverageWatchTime,
		analytics.ChatMessagesCount, analytics.ChatParticipants, analytics.LikesCount, analytics.SharesCount,
		analytics.Bitrate, analytics.Framerate, analytics.Resolution, analytics.BufferingRatio, analytics.AvgLatency,
		analytics.ViewerCountries, analytics.ViewerDevices, analytics.ViewerBrowsers,
		analytics.CreatedAt, analytics.UpdatedAt,
	)

	return err
}

// GetAnalyticsByStream retrieves analytics data for a stream within a time range
func (r *analyticsRepository) GetAnalyticsByStream(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.StreamAnalytics, error) {
	query := `
		SELECT * FROM stream_analytics
		WHERE stream_id = $1
		AND collected_at BETWEEN $2 AND $3
		ORDER BY collected_at ASC`

	var analytics []*domain.StreamAnalytics
	err := r.db.SelectContext(ctx, &analytics, query, streamID, timeRange.StartTime, timeRange.EndTime)
	if err != nil {
		return nil, fmt.Errorf("failed to get analytics: %w", err)
	}

	return analytics, nil
}

// GetAnalyticsTimeSeries retrieves aggregated time-series data
func (r *analyticsRepository) GetAnalyticsTimeSeries(ctx context.Context, streamID uuid.UUID, timeRange *domain.AnalyticsTimeRange) ([]*domain.AnalyticsDataPoint, error) {
	query := `
		SELECT * FROM get_stream_analytics_range($1, $2, $3, $4)`

	var dataPoints []*domain.AnalyticsDataPoint
	err := r.db.SelectContext(ctx, &dataPoints, query,
		streamID, timeRange.StartTime, timeRange.EndTime, timeRange.Interval)
	if err != nil {
		return nil, fmt.Errorf("failed to get analytics time series: %w", err)
	}

	return dataPoints, nil
}

// GetLatestAnalytics retrieves the most recent analytics data for a stream
func (r *analyticsRepository) GetLatestAnalytics(ctx context.Context, streamID uuid.UUID) (*domain.StreamAnalytics, error) {
	query := `
		SELECT * FROM stream_analytics
		WHERE stream_id = $1
		ORDER BY collected_at DESC
		LIMIT 1`

	var analytics domain.StreamAnalytics
	err := r.db.GetContext(ctx, &analytics, query, streamID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get latest analytics: %w", err)
	}

	return &analytics, nil
}

// GetStreamSummary retrieves the summary statistics for a stream
func (r *analyticsRepository) GetStreamSummary(ctx context.Context, streamID uuid.UUID) (*domain.StreamStatsSummary, error) {
	query := `SELECT * FROM stream_stats_summary WHERE stream_id = $1`

	var summary domain.StreamStatsSummary
	err := r.db.GetContext(ctx, &summary, query, streamID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get stream summary: %w", err)
	}

	return &summary, nil
}

// UpdateStreamSummary recalculates and updates the summary statistics
func (r *analyticsRepository) UpdateStreamSummary(ctx context.Context, streamID uuid.UUID) error {
	query := `SELECT update_stream_stats_summary($1)`

	_, err := r.db.ExecContext(ctx, query, streamID)
	if err != nil {
		return fmt.Errorf("failed to update stream summary: %w", err)
	}

	return nil
}

// CreateOrUpdateSummary creates or updates a stream summary
func (r *analyticsRepository) CreateOrUpdateSummary(ctx context.Context, summary *domain.StreamStatsSummary) error {
	// Calculate derived metrics
	summary.CalculateEngagementRate()
	summary.CalculateQualityScore()

	query := `
		INSERT INTO stream_stats_summary (
			id, stream_id,
			total_viewers, peak_concurrent_viewers, average_viewers,
			total_watch_time, average_watch_duration,
			total_chat_messages, total_unique_chatters,
			total_likes, total_shares, engagement_rate,
			average_bitrate, average_framerate, quality_score,
			stream_duration, first_viewer_joined_at, peak_time,
			top_countries, countries_count,
			top_devices, top_browsers
		) VALUES (
			$1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17, $18, $19, $20, $21, $22
		)
		ON CONFLICT (stream_id) DO UPDATE SET
			total_viewers = EXCLUDED.total_viewers,
			peak_concurrent_viewers = EXCLUDED.peak_concurrent_viewers,
			average_viewers = EXCLUDED.average_viewers,
			total_watch_time = EXCLUDED.total_watch_time,
			average_watch_duration = EXCLUDED.average_watch_duration,
			total_chat_messages = EXCLUDED.total_chat_messages,
			total_unique_chatters = EXCLUDED.total_unique_chatters,
			total_likes = EXCLUDED.total_likes,
			total_shares = EXCLUDED.total_shares,
			engagement_rate = EXCLUDED.engagement_rate,
			average_bitrate = EXCLUDED.average_bitrate,
			average_framerate = EXCLUDED.average_framerate,
			quality_score = EXCLUDED.quality_score,
			stream_duration = EXCLUDED.stream_duration,
			first_viewer_joined_at = EXCLUDED.first_viewer_joined_at,
			peak_time = EXCLUDED.peak_time,
			top_countries = EXCLUDED.top_countries,
			countries_count = EXCLUDED.countries_count,
			top_devices = EXCLUDED.top_devices,
			top_browsers = EXCLUDED.top_browsers,
			updated_at = NOW()`

	// Ensure JSON fields are not nil
	if summary.TopCountries == nil {
		summary.TopCountries = json.RawMessage("[]")
	}
	if summary.TopDevices == nil {
		summary.TopDevices = json.RawMessage("{}")
	}
	if summary.TopBrowsers == nil {
		summary.TopBrowsers = json.RawMessage("{}")
	}

	_, err := r.db.ExecContext(ctx, query,
		summary.ID, summary.StreamID,
		summary.TotalViewers, summary.PeakConcurrentViewers, summary.AverageViewers,
		summary.TotalWatchTime, summary.AverageWatchDuration,
		summary.TotalChatMessages, summary.TotalUniqueChatters,
		summary.TotalLikes, summary.TotalShares, summary.EngagementRate,
		summary.AverageBitrate, summary.AverageFramerate, summary.QualityScore,
		summary.StreamDuration, summary.FirstViewerJoinedAt, summary.PeakTime,
		summary.TopCountries, summary.CountriesCount,
		summary.TopDevices, summary.TopBrowsers,
	)

	return err
}

// CreateViewerSession creates a new viewer session
func (r *analyticsRepository) CreateViewerSession(ctx context.Context, session *domain.AnalyticsViewerSession) error {
	query := `
		INSERT INTO viewer_sessions (
			id, stream_id, user_id, session_id,
			joined_at, ip_address, country_code, city,
			device_type, browser, operating_system
		) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11)`

	_, err := r.db.ExecContext(ctx, query,
		session.ID, session.StreamID, session.UserID, session.SessionID,
		session.JoinedAt, session.IPAddress, session.CountryCode, session.City,
		session.DeviceType, session.Browser, session.OperatingSystem,
	)

	return err
}

// EndViewerSession marks a viewer session as ended
func (r *analyticsRepository) EndViewerSession(ctx context.Context, sessionID string) error {
	query := `
		UPDATE viewer_sessions
		SET left_at = NOW()
		WHERE session_id = $1 AND left_at IS NULL`

	result, err := r.db.ExecContext(ctx, query, sessionID)
	if err != nil {
		return fmt.Errorf("failed to end viewer session: %w", err)
	}

	rows, err := result.RowsAffected()
	if err != nil {
		return err
	}

	if rows == 0 {
		return fmt.Errorf("session not found or already ended: %s", sessionID)
	}

	return nil
}

// GetActiveViewers retrieves all active viewer sessions for a stream
func (r *analyticsRepository) GetActiveViewers(ctx context.Context, streamID uuid.UUID) ([]*domain.AnalyticsViewerSession, error) {
	query := `
		SELECT * FROM viewer_sessions
		WHERE stream_id = $1 AND left_at IS NULL
		ORDER BY joined_at DESC`

	var sessions []*domain.AnalyticsViewerSession
	err := r.db.SelectContext(ctx, &sessions, query, streamID)
	if err != nil {
		return nil, fmt.Errorf("failed to get active viewers: %w", err)
	}

	return sessions, nil
}

// GetViewerSession retrieves a specific viewer session
func (r *analyticsRepository) GetViewerSession(ctx context.Context, sessionID string) (*domain.AnalyticsViewerSession, error) {
	query := `SELECT * FROM viewer_sessions WHERE session_id = $1`

	var session domain.AnalyticsViewerSession
	err := r.db.GetContext(ctx, &session, query, sessionID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get viewer session: %w", err)
	}

	return &session, nil
}

// UpdateSessionEngagement updates engagement metrics for a session
func (r *analyticsRepository) UpdateSessionEngagement(ctx context.Context, sessionID string, messagesSent int, liked, shared bool) error {
	query := `
		UPDATE viewer_sessions
		SET messages_sent = messages_sent + $2,
		    liked = $3,
		    shared = $4,
		    updated_at = NOW()
		WHERE session_id = $1`

	_, err := r.db.ExecContext(ctx, query, sessionID, messagesSent, liked, shared)
	if err != nil {
		return fmt.Errorf("failed to update session engagement: %w", err)
	}

	return nil
}

// CleanupOldAnalytics removes analytics data older than the retention period
func (r *analyticsRepository) CleanupOldAnalytics(ctx context.Context, retentionDays int) error {
	cutoffDate := time.Now().AddDate(0, 0, -retentionDays)

	// Clean up analytics data
	analyticsQuery := `DELETE FROM stream_analytics WHERE collected_at < $1`
	_, err := r.db.ExecContext(ctx, analyticsQuery, cutoffDate)
	if err != nil {
		return fmt.Errorf("failed to cleanup analytics data: %w", err)
	}

	// Clean up old viewer sessions
	sessionsQuery := `DELETE FROM viewer_sessions WHERE created_at < $1`
	_, err = r.db.ExecContext(ctx, sessionsQuery, cutoffDate)
	if err != nil {
		return fmt.Errorf("failed to cleanup viewer sessions: %w", err)
	}

	return nil
}

// GetCurrentViewerCount returns the current number of active viewers for a stream
func (r *analyticsRepository) GetCurrentViewerCount(ctx context.Context, streamID uuid.UUID) (int, error) {
	query := `SELECT get_current_viewer_count($1)`

	var count int
	err := r.db.GetContext(ctx, &count, query, streamID)
	if err != nil {
		return 0, fmt.Errorf("failed to get current viewer count: %w", err)
	}

	return count, nil
}

// GetActiveViewersForStreams retrieves active viewer sessions for multiple streams
func (r *analyticsRepository) GetActiveViewersForStreams(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID][]*domain.AnalyticsViewerSession, error) {
	if len(streamIDs) == 0 {
		return make(map[uuid.UUID][]*domain.AnalyticsViewerSession), nil
	}

	query, args, err := sqlx.In(`
		SELECT * FROM viewer_sessions
		WHERE stream_id IN (?) AND left_at IS NULL
		ORDER BY joined_at DESC`, streamIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to build query: %w", err)
	}

	query = r.db.Rebind(query)
	var sessions []*domain.AnalyticsViewerSession
	err = r.db.SelectContext(ctx, &sessions, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get active viewers for streams: %w", err)
	}

	result := make(map[uuid.UUID][]*domain.AnalyticsViewerSession)
	for _, session := range sessions {
		result[session.StreamID] = append(result[session.StreamID], session)
	}

	return result, nil
}

// GetCurrentViewerCounts returns the current number of active viewers for multiple streams
func (r *analyticsRepository) GetCurrentViewerCounts(ctx context.Context, streamIDs []uuid.UUID) (map[uuid.UUID]int, error) {
	if len(streamIDs) == 0 {
		return make(map[uuid.UUID]int), nil
	}

	query, args, err := sqlx.In(`
		SELECT stream_id, COUNT(*) as count
		FROM viewer_sessions
		WHERE stream_id IN (?) AND left_at IS NULL
		GROUP BY stream_id`, streamIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to build query: %w", err)
	}

	query = r.db.Rebind(query)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get viewer counts: %w", err)
	}
	defer rows.Close()

	result := make(map[uuid.UUID]int)
	for rows.Next() {
		var streamID uuid.UUID
		var count int
		if err := rows.Scan(&streamID, &count); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		result[streamID] = count
	}

	return result, nil
}

// BatchCreateAnalytics creates multiple analytics records
func (r *analyticsRepository) BatchCreateAnalytics(ctx context.Context, analytics []*domain.StreamAnalytics) error {
	if len(analytics) == 0 {
		return nil
	}

	query := `
		INSERT INTO stream_analytics (
			id, stream_id, collected_at,
			viewer_count, peak_viewer_count, unique_viewers, average_watch_time,
			chat_messages_count, chat_participants, likes_count, shares_count,
			bitrate, framerate, resolution, buffering_ratio, avg_latency,
			viewer_countries, viewer_devices, viewer_browsers,
			created_at, updated_at
		) VALUES (
			:id, :stream_id, :collected_at,
			:viewer_count, :peak_viewer_count, :unique_viewers, :average_watch_time,
			:chat_messages_count, :chat_participants, :likes_count, :shares_count,
			:bitrate, :framerate, :resolution, :buffering_ratio, :avg_latency,
			:viewer_countries, :viewer_devices, :viewer_browsers,
			:created_at, :updated_at
		)`

	_, err := r.db.NamedExecContext(ctx, query, analytics)
	if err != nil {
		return fmt.Errorf("failed to batch create analytics: %w", err)
	}

	return nil
}

// BatchUpdateStreamSummaries updates summaries for multiple streams
func (r *analyticsRepository) BatchUpdateStreamSummaries(ctx context.Context, streamIDs []uuid.UUID) error {
	if len(streamIDs) == 0 {
		return nil
	}

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	stmt, err := tx.PrepareContext(ctx, `SELECT update_stream_stats_summary($1)`)
	if err != nil {
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	for _, id := range streamIDs {
		if _, err := stmt.ExecContext(ctx, id); err != nil {
			return fmt.Errorf("failed to update summary for stream %s: %w", id, err)
		}
	}

	return tx.Commit()
}
