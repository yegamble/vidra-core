package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"

	"athena/internal/domain"
)

// ViewsRepository implements the views repository interface
type ViewsRepository struct {
	db *sqlx.DB
}

// NewViewsRepository creates a new views repository
func NewViewsRepository(db *sqlx.DB) *ViewsRepository {
	return &ViewsRepository{db: db}
}

// CreateUserView creates a new user view record
func (r *ViewsRepository) CreateUserView(ctx context.Context, view *domain.UserView) error {
	query := `
		INSERT INTO user_views (
			id, video_id, user_id, session_id, fingerprint_hash,
			watch_duration, video_duration, completion_percentage, is_completed,
			seek_count, pause_count, replay_count, quality_changes,
			initial_load_time, buffer_events, connection_type, video_quality,
			referrer_url, referrer_type, utm_source, utm_medium, utm_campaign,
			device_type, os_name, browser_name, screen_resolution, is_mobile,
			country_code, region_code, city_name, timezone,
			is_anonymous, tracking_consent, gdpr_consent,
			view_date, view_hour, weekday,
			created_at, updated_at
		) VALUES (
			:id, :video_id, :user_id, :session_id, :fingerprint_hash,
			:watch_duration, :video_duration, :completion_percentage, :is_completed,
			:seek_count, :pause_count, :replay_count, :quality_changes,
			:initial_load_time, :buffer_events, :connection_type, :video_quality,
			:referrer_url, :referrer_type, :utm_source, :utm_medium, :utm_campaign,
			:device_type, :os_name, :browser_name, :screen_resolution, :is_mobile,
			:country_code, :region_code, :city_name, :timezone,
			:is_anonymous, :tracking_consent, :gdpr_consent,
			:view_date, :view_hour, :weekday,
			:created_at, :updated_at
		)`

	if view.ID == "" {
		view.ID = generateUUID()
	}

	view.CreatedAt = time.Now()
	view.UpdatedAt = view.CreatedAt
	view.SetViewDate(view.CreatedAt)

	_, err := r.db.NamedExecContext(ctx, query, view)
	return err
}

// UpdateUserView updates an existing user view record
func (r *ViewsRepository) UpdateUserView(ctx context.Context, view *domain.UserView) error {
	query := `
		UPDATE user_views SET
			watch_duration = :watch_duration,
			completion_percentage = :completion_percentage,
			is_completed = :is_completed,
			seek_count = :seek_count,
			pause_count = :pause_count,
			replay_count = :replay_count,
			quality_changes = :quality_changes,
			buffer_events = :buffer_events,
			updated_at = NOW()
		WHERE id = :id`

	view.UpdatedAt = time.Now()
	view.CalculateCompletion()

	_, err := r.db.NamedExecContext(ctx, query, view)
	return err
}

// GetUserViewBySessionAndVideo finds a view by session ID and video ID
func (r *ViewsRepository) GetUserViewBySessionAndVideo(ctx context.Context, sessionID, videoID string) (*domain.UserView, error) {
	query := `
		SELECT 
			id, video_id, user_id, session_id, fingerprint_hash,
			watch_duration, video_duration, completion_percentage, is_completed,
			seek_count, pause_count, replay_count, quality_changes,
			initial_load_time, buffer_events, 
			connection_type, video_quality,
			COALESCE(referrer_url, '') as referrer_url,
			COALESCE(referrer_type, '') as referrer_type,
			COALESCE(utm_source, '') as utm_source,
			COALESCE(utm_medium, '') as utm_medium,
			COALESCE(utm_campaign, '') as utm_campaign,
			COALESCE(device_type, '') as device_type,
			COALESCE(os_name, '') as os_name,
			COALESCE(browser_name, '') as browser_name,
			COALESCE(screen_resolution, '') as screen_resolution,
			is_mobile,
			COALESCE(country_code, '') as country_code,
			COALESCE(region_code, '') as region_code,
			COALESCE(city_name, '') as city_name,
			COALESCE(timezone, '') as timezone,
			is_anonymous, tracking_consent, gdpr_consent,
			view_date, view_hour, weekday,
			created_at, updated_at
		FROM user_views 
		WHERE session_id = $1 AND video_id = $2 
		ORDER BY created_at DESC 
		LIMIT 1`

	var view domain.UserView
	err := r.db.GetContext(ctx, &view, query, sessionID, videoID)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get user view: %w", err)
	}

	return &view, nil
}

// GetVideoAnalytics retrieves analytics for a video with filters
func (r *ViewsRepository) GetVideoAnalytics(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error) {
	baseQuery := `SELECT * FROM user_views WHERE 1=1`
	args := []interface{}{}
	argIndex := 1

	if filter.VideoID != "" {
		baseQuery += fmt.Sprintf(" AND video_id = $%d", argIndex)
		args = append(args, filter.VideoID)
		argIndex++
	}

	if filter.UserID != "" {
		baseQuery += fmt.Sprintf(" AND user_id = $%d", argIndex)
		args = append(args, filter.UserID)
		argIndex++
	}

	if filter.StartDate != nil {
		baseQuery += fmt.Sprintf(" AND created_at >= $%d", argIndex)
		args = append(args, *filter.StartDate)
		argIndex++
	}

	if filter.EndDate != nil {
		baseQuery += fmt.Sprintf(" AND created_at <= $%d", argIndex)
		args = append(args, *filter.EndDate)
		argIndex++
	}

	if filter.CountryCode != "" {
		baseQuery += fmt.Sprintf(" AND country_code = $%d", argIndex)
		args = append(args, filter.CountryCode)
		argIndex++
	}

	if filter.DeviceType != "" {
		baseQuery += fmt.Sprintf(" AND device_type = $%d", argIndex)
		args = append(args, filter.DeviceType)
		argIndex++
	}

	if filter.IsAnonymous != nil {
		baseQuery += fmt.Sprintf(" AND is_anonymous = $%d", argIndex)
		args = append(args, *filter.IsAnonymous)
	}

	// Get aggregate stats
	statsQuery := fmt.Sprintf(`
		SELECT 
			COUNT(*) as total_views,
			COUNT(DISTINCT session_id) as unique_views,
			AVG(watch_duration) as avg_duration,
			AVG(watch_duration) as avg_watch_duration,
			AVG(completion_percentage) as completion,
			AVG(completion_percentage) as avg_completion_rate,
			SUM(CASE WHEN is_completed THEN 1 ELSE 0 END) as completed_views,
			SUM(watch_duration) as total_watch_time
		FROM (%s) views`, baseQuery)

	row := r.db.QueryRowContext(ctx, statsQuery, args...)
	var response domain.ViewAnalyticsResponse
	err := row.Scan(
		&response.TotalViews,
		&response.UniqueViews,
		&response.AvgDuration,
		&response.AvgWatchDuration,
		&response.Completion,
		&response.AvgCompletionRate,
		&response.CompletedViews,
		&response.TotalWatchTime,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get video analytics: %w", err)
	}

	// Initialize daily stats slice
	response.DailyStats = make([]domain.DailyViewStats, 0)

	// Get device stats
	deviceQuery := fmt.Sprintf(`
		SELECT 
			COALESCE(device_type, 'unknown') as device,
			COUNT(*) as count
		FROM (%s) views
		GROUP BY device_type`, baseQuery)

	rows, err := r.db.QueryContext(ctx, deviceQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get device stats: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			// Log error but don't override return value
			_ = err
		}
	}()

	response.DeviceStats = make(map[string]int64)
	response.DeviceBreakdown = make(map[string]int64)
	for rows.Next() {
		var device string
		var count int64
		if err := rows.Scan(&device, &count); err != nil {
			return nil, fmt.Errorf("failed to scan device stats: %w", err)
		}
		response.DeviceStats[device] = count
		response.DeviceBreakdown[device] = count // Same data for now
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating device stats: %w", err)
	}

	// Get geo stats
	geoQuery := fmt.Sprintf(`
		SELECT 
			COALESCE(country_code, 'unknown') as country,
			COUNT(*) as count
		FROM (%s) views
		GROUP BY country_code`, baseQuery)

	rows, err = r.db.QueryContext(ctx, geoQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get geo stats: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			// Log error but don't override return value
			_ = err
		}
	}()

	response.GeoStats = make(map[string]int64)
	response.CountryBreakdown = make(map[string]int64)
	for rows.Next() {
		var country string
		var count int64
		if err := rows.Scan(&country, &count); err != nil {
			return nil, fmt.Errorf("failed to scan geo stats: %w", err)
		}
		response.GeoStats[country] = count
		response.CountryBreakdown[country] = count // Same data for now
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating geo stats: %w", err)
	}

	// Get hourly stats
	hourlyQuery := fmt.Sprintf(`
		SELECT 
			view_hour,
			COUNT(*) as count
		FROM (%s) views
		GROUP BY view_hour
		ORDER BY view_hour`, baseQuery)

	rows, err = r.db.QueryContext(ctx, hourlyQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get hourly stats: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			// Log error but don't override return value
			_ = err
		}
	}()

	response.HourlyStats = make(map[int]int64)
	for rows.Next() {
		var hour int
		var count int64
		if err := rows.Scan(&hour, &count); err != nil {
			return nil, fmt.Errorf("failed to scan hourly stats: %w", err)
		}
		response.HourlyStats[hour] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate hourly stats rows: %w", err)
	}

	return &response, nil
}

// GetDailyVideoStats retrieves daily stats for a video
func (r *ViewsRepository) GetDailyVideoStats(ctx context.Context, videoID string, startDate, endDate time.Time) ([]domain.DailyVideoStats, error) {
	query := `
		SELECT * FROM daily_video_stats 
		WHERE video_id = $1 
		AND stat_date BETWEEN $2 AND $3
		ORDER BY stat_date`

	var stats []domain.DailyVideoStats
	err := r.db.SelectContext(ctx, &stats, query, videoID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get daily video stats: %w", err)
	}

	return stats, nil
}

// GetUserEngagementStats retrieves engagement stats for a user
func (r *ViewsRepository) GetUserEngagementStats(ctx context.Context, userID string, startDate, endDate time.Time) ([]domain.UserEngagementStats, error) {
	query := `
		SELECT * FROM user_engagement_stats 
		WHERE user_id = $1 
		AND stat_date BETWEEN $2 AND $3
		ORDER BY stat_date`

	var stats []domain.UserEngagementStats
	err := r.db.SelectContext(ctx, &stats, query, userID, startDate, endDate)
	if err != nil {
		return nil, fmt.Errorf("failed to get user engagement stats: %w", err)
	}

	return stats, nil
}

// GetTrendingVideos retrieves trending videos
func (r *ViewsRepository) GetTrendingVideos(ctx context.Context, limit int) ([]domain.TrendingVideo, error) {
	query := `
		SELECT * FROM trending_videos 
		WHERE is_trending = true
		ORDER BY engagement_score DESC, velocity_score DESC
		LIMIT $1`

	var trending []domain.TrendingVideo
	err := r.db.SelectContext(ctx, &trending, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get trending videos: %w", err)
	}

	return trending, nil
}

// UpdateTrendingVideo updates trending data for a video
func (r *ViewsRepository) UpdateTrendingVideo(ctx context.Context, trending *domain.TrendingVideo) error {
	query := `
		INSERT INTO trending_videos (
			video_id, views_last_hour, views_last_24h, views_last_7d,
			engagement_score, velocity_score, hourly_rank, daily_rank, weekly_rank,
			last_updated, is_trending
		) VALUES (
			:video_id, :views_last_hour, :views_last_24h, :views_last_7d,
			:engagement_score, :velocity_score, :hourly_rank, :daily_rank, :weekly_rank,
			NOW(), :is_trending
		)
		ON CONFLICT (video_id) DO UPDATE SET
			views_last_hour = EXCLUDED.views_last_hour,
			views_last_24h = EXCLUDED.views_last_24h,
			views_last_7d = EXCLUDED.views_last_7d,
			engagement_score = EXCLUDED.engagement_score,
			velocity_score = EXCLUDED.velocity_score,
			hourly_rank = EXCLUDED.hourly_rank,
			daily_rank = EXCLUDED.daily_rank,
			weekly_rank = EXCLUDED.weekly_rank,
			last_updated = NOW(),
			is_trending = EXCLUDED.is_trending`

	trending.LastUpdated = time.Now()
	_, err := r.db.NamedExecContext(ctx, query, trending)
	return err
}

// IncrementVideoViews calls the database function to increment view count
func (r *ViewsRepository) IncrementVideoViews(ctx context.Context, videoID string) error {
	query := `SELECT increment_video_views($1)`
	_, err := r.db.ExecContext(ctx, query, videoID)
	return err
}

// GetUniqueViews calls the database function to get unique view count
func (r *ViewsRepository) GetUniqueViews(ctx context.Context, videoID string, startDate, endDate time.Time) (int64, error) {
	query := `SELECT get_unique_views($1, $2, $3)`
	var count int64
	err := r.db.GetContext(ctx, &count, query, videoID, startDate, endDate)
	if err != nil {
		return 0, fmt.Errorf("failed to get unique views: %w", err)
	}
	return count, nil
}

// CalculateEngagementScore calls the database function to calculate engagement score
func (r *ViewsRepository) CalculateEngagementScore(ctx context.Context, videoID string, hoursBack int) (float64, error) {
	query := `SELECT calculate_engagement_score($1, $2)`
	var score float64
	err := r.db.GetContext(ctx, &score, query, videoID, hoursBack)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate engagement score: %w", err)
	}
	return score, nil
}

// AggregateDailyStats calls the database function to aggregate daily stats
func (r *ViewsRepository) AggregateDailyStats(ctx context.Context, date time.Time) error {
	query := `SELECT aggregate_daily_stats($1)`
	_, err := r.db.ExecContext(ctx, query, date)
	return err
}

// CleanupOldViews calls the database function to cleanup old views
func (r *ViewsRepository) CleanupOldViews(ctx context.Context, daysToKeep int) error {
	query := `SELECT cleanup_old_views($1)`
	_, err := r.db.ExecContext(ctx, query, daysToKeep)
	return err
}

// GetViewsByDateRange retrieves views filtered by date range
func (r *ViewsRepository) GetViewsByDateRange(ctx context.Context, filter *domain.ViewAnalyticsFilter) ([]domain.UserView, error) {
	baseQuery := `SELECT * FROM user_views WHERE 1=1`
	args := []interface{}{}
	argIndex := 1

	if filter.VideoID != "" {
		baseQuery += fmt.Sprintf(" AND video_id = $%d", argIndex)
		args = append(args, filter.VideoID)
		argIndex++
	}

	if filter.StartDate != nil {
		baseQuery += fmt.Sprintf(" AND created_at >= $%d", argIndex)
		args = append(args, *filter.StartDate)
		argIndex++
	}

	if filter.EndDate != nil {
		baseQuery += fmt.Sprintf(" AND created_at <= $%d", argIndex)
		args = append(args, *filter.EndDate)
		argIndex++
	}

	baseQuery += " ORDER BY created_at DESC"

	if filter.Limit > 0 {
		baseQuery += fmt.Sprintf(" LIMIT $%d", argIndex)
		args = append(args, filter.Limit)
		argIndex++
	}

	if filter.Offset > 0 {
		baseQuery += fmt.Sprintf(" OFFSET $%d", argIndex)
		args = append(args, filter.Offset)
	}

	var views []domain.UserView
	err := r.db.SelectContext(ctx, &views, baseQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get views by date range: %w", err)
	}

	return views, nil
}

// GetTopVideos retrieves top videos by views
func (r *ViewsRepository) GetTopVideos(ctx context.Context, startDate, endDate time.Time, limit int) ([]struct {
	VideoID     string  `db:"video_id"`
	TotalViews  int64   `db:"total_views"`
	UniqueViews int64   `db:"unique_views"`
	AvgDuration float64 `db:"avg_duration"`
}, error) {
	query := `
		SELECT 
			video_id,
			COUNT(*) as total_views,
			COUNT(DISTINCT session_id) as unique_views,
			AVG(watch_duration) as avg_duration
		FROM user_views 
		WHERE created_at BETWEEN $1 AND $2
		GROUP BY video_id 
		ORDER BY total_views DESC
		LIMIT $3`

	var results []struct {
		VideoID     string  `db:"video_id"`
		TotalViews  int64   `db:"total_views"`
		UniqueViews int64   `db:"unique_views"`
		AvgDuration float64 `db:"avg_duration"`
	}

	err := r.db.SelectContext(ctx, &results, query, startDate, endDate, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get top videos: %w", err)
	}

	return results, nil
}

// Helper function to generate UUID
func generateUUID() string {
	return uuid.New().String()
}

// Additional helper methods for repository operations

// GetViewCountsByVideo gets view counts grouped by video
func (r *ViewsRepository) GetViewCountsByVideo(ctx context.Context, videoIDs []string) (map[string]int64, error) {
	if len(videoIDs) == 0 {
		return make(map[string]int64), nil
	}

	query := `
		SELECT video_id, COUNT(*) as view_count
		FROM user_views 
		WHERE video_id = ANY($1)
		GROUP BY video_id`

	rows, err := r.db.QueryContext(ctx, query, videoIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to get view counts: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			// Log error but don't override return value
			_ = err
		}
	}()

	counts := make(map[string]int64)
	for rows.Next() {
		var videoID string
		var count int64
		if err := rows.Scan(&videoID, &count); err != nil {
			return nil, fmt.Errorf("failed to scan view counts: %w", err)
		}
		counts[videoID] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate view counts rows: %w", err)
	}

	return counts, nil
}

// GetRecentViews gets recent views for a user
func (r *ViewsRepository) GetRecentViews(ctx context.Context, userID string, limit int) ([]domain.UserView, error) {
	query := `
		SELECT * FROM user_views 
		WHERE user_id = $1 
		ORDER BY created_at DESC 
		LIMIT $2`

	var views []domain.UserView
	err := r.db.SelectContext(ctx, &views, query, userID, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to get recent views: %w", err)
	}

	return views, nil
}

// BatchCreateUserViews creates multiple user views in a single transaction
func (r *ViewsRepository) BatchCreateUserViews(ctx context.Context, views []*domain.UserView) error {
	if len(views) == 0 {
		return nil
	}

	tx, err := r.db.BeginTxx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if rollbackErr := tx.Rollback(); rollbackErr != nil {
			// Rollback errors are usually not critical since the transaction
			// might have already been committed or rolled back
			_ = rollbackErr
		}
	}()

	query := `
		INSERT INTO user_views (
			id, video_id, user_id, session_id, fingerprint_hash,
			watch_duration, video_duration, completion_percentage, is_completed,
			seek_count, pause_count, replay_count, quality_changes,
			initial_load_time, buffer_events, connection_type, video_quality,
			referrer_url, referrer_type, utm_source, utm_medium, utm_campaign,
			device_type, os_name, browser_name, screen_resolution, is_mobile,
			country_code, region_code, city_name, timezone,
			is_anonymous, tracking_consent, gdpr_consent,
			view_date, view_hour, weekday,
			created_at, updated_at
		) VALUES (
			:id, :video_id, :user_id, :session_id, :fingerprint_hash,
			:watch_duration, :video_duration, :completion_percentage, :is_completed,
			:seek_count, :pause_count, :replay_count, :quality_changes,
			:initial_load_time, :buffer_events, :connection_type, :video_quality,
			:referrer_url, :referrer_type, :utm_source, :utm_medium, :utm_campaign,
			:device_type, :os_name, :browser_name, :screen_resolution, :is_mobile,
			:country_code, :region_code, :city_name, :timezone,
			:is_anonymous, :tracking_consent, :gdpr_consent,
			:view_date, :view_hour, :weekday,
			:created_at, :updated_at
		)`

	for _, view := range views {
		if view.ID == "" {
			view.ID = generateUUID()
		}
		view.CreatedAt = time.Now()
		view.UpdatedAt = view.CreatedAt
		view.SetViewDate(view.CreatedAt)

		_, err := tx.NamedExecContext(ctx, query, view)
		if err != nil {
			return fmt.Errorf("failed to insert view: %w", err)
		}
	}

	return tx.Commit()
}
