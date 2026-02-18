package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"

	"athena/internal/domain"
)

type ViewsRepository struct {
	db *sqlx.DB
}

func NewViewsRepository(db *sqlx.DB) *ViewsRepository {
	return &ViewsRepository{db: db}
}

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

func (r *ViewsRepository) buildAnalyticsQuery(filter *domain.ViewAnalyticsFilter) (string, []interface{}) {
	baseQuery := `SELECT * FROM user_views WHERE 1=1`
	args := []interface{}{}
	argIndex := 1

	filterMap := map[string]interface{}{
		"video_id":     filter.VideoID,
		"user_id":      filter.UserID,
		"country_code": filter.CountryCode,
		"device_type":  filter.DeviceType,
	}

	for field, value := range filterMap {
		if str, ok := value.(string); ok && str != "" {
			baseQuery += fmt.Sprintf(" AND %s = $%d", field, argIndex)
			args = append(args, str)
			argIndex++
		}
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

	if filter.IsAnonymous != nil {
		baseQuery += fmt.Sprintf(" AND is_anonymous = $%d", argIndex)
		args = append(args, *filter.IsAnonymous)
	}

	return baseQuery, args
}

func (r *ViewsRepository) GetVideoAnalytics(ctx context.Context, filter *domain.ViewAnalyticsFilter) (*domain.ViewAnalyticsResponse, error) {
	baseQuery, args := r.buildAnalyticsQuery(filter)

	response, err := r.getAggregateStats(ctx, baseQuery, args)
	if err != nil {
		return nil, err
	}

	deviceStats, err := r.getGroupedStats(ctx, baseQuery, args, "device_type")
	if err != nil {
		return nil, err
	}
	response.DeviceStats = deviceStats
	response.DeviceBreakdown = deviceStats

	geoStats, err := r.getGroupedStats(ctx, baseQuery, args, "country_code")
	if err != nil {
		return nil, err
	}
	response.GeoStats = geoStats
	response.CountryBreakdown = geoStats

	hourlyStats, err := r.getHourlyStats(ctx, baseQuery, args)
	if err != nil {
		return nil, err
	}
	response.HourlyStats = hourlyStats

	return response, nil
}

func (r *ViewsRepository) getAggregateStats(ctx context.Context, baseQuery string, args []interface{}) (*domain.ViewAnalyticsResponse, error) {
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

	response.DailyStats = make([]domain.DailyViewStats, 0)
	return &response, nil
}

func (r *ViewsRepository) getGroupedStats(ctx context.Context, baseQuery string, args []interface{}, groupField string) (map[string]int64, error) {
	query := fmt.Sprintf(`
		SELECT
			COALESCE(%s, 'unknown') as field,
			COUNT(*) as count
		FROM (%s) views
		GROUP BY %s`, groupField, baseQuery, groupField)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get %s stats: %w", groupField, err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	stats := make(map[string]int64)
	for rows.Next() {
		var field string
		var count int64
		if err := rows.Scan(&field, &count); err != nil {
			return nil, fmt.Errorf("failed to scan %s stats: %w", groupField, err)
		}
		stats[field] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating %s stats: %w", groupField, err)
	}

	return stats, nil
}

func (r *ViewsRepository) getHourlyStats(ctx context.Context, baseQuery string, args []interface{}) (map[int]int64, error) {
	hourlyQuery := fmt.Sprintf(`
		SELECT
			view_hour,
			COUNT(*) as count
		FROM (%s) views
		GROUP BY view_hour
		ORDER BY view_hour`, baseQuery)

	rows, err := r.db.QueryContext(ctx, hourlyQuery, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to get hourly stats: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			_ = err
		}
	}()

	stats := make(map[int]int64)
	for rows.Next() {
		var hour int
		var count int64
		if err := rows.Scan(&hour, &count); err != nil {
			return nil, fmt.Errorf("failed to scan hourly stats: %w", err)
		}
		stats[hour] = count
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("failed to iterate hourly stats rows: %w", err)
	}

	return stats, nil
}

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

func (r *ViewsRepository) GetBatchTrendingStats(ctx context.Context, videoIDs []string) ([]domain.VideoTrendingStats, error) {
	if len(videoIDs) == 0 {
		return []domain.VideoTrendingStats{}, nil
	}

	query := `
		SELECT
			v.video_id,
			get_unique_views(v.video_id::uuid, NOW() - INTERVAL '1 hour', NOW()) as views_last_hour,
			get_unique_views(v.video_id::uuid, NOW() - INTERVAL '24 hours', NOW()) as views_last_24h,
			get_unique_views(v.video_id::uuid, NOW() - INTERVAL '7 days', NOW()) as views_last_7d,
			calculate_engagement_score(v.video_id::uuid, 1) as score_1h,
			calculate_engagement_score(v.video_id::uuid, 24) as score_24h,
			calculate_engagement_score(v.video_id::uuid, 168) as score_7d
		FROM unnest($1::text[]) as v(video_id)`

	var stats []domain.VideoTrendingStats
	err := r.db.SelectContext(ctx, &stats, query, pq.Array(videoIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to get batch trending stats: %w", err)
	}

	return stats, nil
}

func (r *ViewsRepository) BatchUpdateTrendingVideos(ctx context.Context, videos []*domain.TrendingVideo) error {
	if len(videos) == 0 {
		return nil
	}

	count := len(videos)
	videoIDs := make([]string, count)
	viewsLastHour := make([]int64, count)
	viewsLast24h := make([]int64, count)
	viewsLast7d := make([]int64, count)
	engagementScores := make([]float64, count)
	velocityScores := make([]float64, count)
	hourlyRanks := make([]*int, count)
	dailyRanks := make([]*int, count)
	weeklyRanks := make([]*int, count)
	lastUpdated := make([]time.Time, count)
	isTrendings := make([]bool, count)

	now := time.Now()

	for i, v := range videos {
		videoIDs[i] = v.VideoID
		viewsLastHour[i] = v.ViewsLastHour
		viewsLast24h[i] = v.ViewsLast24h
		viewsLast7d[i] = v.ViewsLast7d
		engagementScores[i] = v.EngagementScore
		velocityScores[i] = v.VelocityScore
		hourlyRanks[i] = v.HourlyRank
		dailyRanks[i] = v.DailyRank
		weeklyRanks[i] = v.WeeklyRank
		lastUpdated[i] = now
		isTrendings[i] = v.IsTrending
	}

	query := `
		INSERT INTO trending_videos (
			video_id, views_last_hour, views_last_24h, views_last_7d,
			engagement_score, velocity_score, hourly_rank, daily_rank, weekly_rank,
			last_updated, is_trending
		)
		SELECT
			v.video_id::uuid, v.views_last_hour, v.views_last_24h, v.views_last_7d,
			v.engagement_score, v.velocity_score, v.hourly_rank, v.daily_rank, v.weekly_rank,
			v.last_updated, v.is_trending
		FROM UNNEST(
			$1::text[], $2::bigint[], $3::bigint[], $4::bigint[],
			$5::decimal[], $6::decimal[], $7::int[], $8::int[], $9::int[],
			$10::timestamp[], $11::boolean[]
		) AS v(
			video_id, views_last_hour, views_last_24h, views_last_7d,
			engagement_score, velocity_score, hourly_rank, daily_rank, weekly_rank,
			last_updated, is_trending
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
			last_updated = EXCLUDED.last_updated,
			is_trending = EXCLUDED.is_trending`

	_, err := r.db.ExecContext(ctx, query,
		pq.Array(videoIDs), pq.Array(viewsLastHour), pq.Array(viewsLast24h), pq.Array(viewsLast7d),
		pq.Array(engagementScores), pq.Array(velocityScores), pq.Array(hourlyRanks), pq.Array(dailyRanks), pq.Array(weeklyRanks),
		pq.Array(lastUpdated), pq.Array(isTrendings),
	)
	return err
}

func (r *ViewsRepository) IncrementVideoViews(ctx context.Context, videoID string) error {
	query := `SELECT increment_video_views($1)`
	_, err := r.db.ExecContext(ctx, query, videoID)
	return err
}

func (r *ViewsRepository) BatchIncrementVideoViews(ctx context.Context, counts map[string]int64) error {
	for videoID, count := range counts {
		_, err := r.db.ExecContext(ctx,
			`UPDATE videos SET views = views + $2, updated_at = NOW() WHERE id = $1::uuid`,
			videoID, count)
		if err != nil {
			return fmt.Errorf("batch increment views for %s: %w", videoID, err)
		}
	}
	return nil
}

func (r *ViewsRepository) GetUniqueViews(ctx context.Context, videoID string, startDate, endDate time.Time) (int64, error) {
	query := `SELECT get_unique_views($1, $2, $3)`
	var count int64
	err := r.db.GetContext(ctx, &count, query, videoID, startDate, endDate)
	if err != nil {
		return 0, fmt.Errorf("failed to get unique views: %w", err)
	}
	return count, nil
}

func (r *ViewsRepository) CalculateEngagementScore(ctx context.Context, videoID string, hoursBack int) (float64, error) {
	query := `SELECT calculate_engagement_score($1, $2)`
	var score float64
	err := r.db.GetContext(ctx, &score, query, videoID, hoursBack)
	if err != nil {
		return 0, fmt.Errorf("failed to calculate engagement score: %w", err)
	}
	return score, nil
}

func (r *ViewsRepository) AggregateDailyStats(ctx context.Context, date time.Time) error {
	query := `SELECT aggregate_daily_stats($1)`
	_, err := r.db.ExecContext(ctx, query, date)
	return err
}

func (r *ViewsRepository) CleanupOldViews(ctx context.Context, daysToKeep int) error {
	query := `SELECT cleanup_old_views($1)`
	_, err := r.db.ExecContext(ctx, query, daysToKeep)
	return err
}

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

func generateUUID() string {
	return uuid.New().String()
}

func (r *ViewsRepository) GetViewCountsByVideo(ctx context.Context, videoIDs []string) (map[string]int64, error) {
	if len(videoIDs) == 0 {
		return make(map[string]int64), nil
	}

	query := `
		SELECT video_id, COUNT(*) as view_count
		FROM user_views
		WHERE video_id = ANY($1)
		GROUP BY video_id`

	rows, err := r.db.QueryContext(ctx, query, pq.Array(videoIDs))
	if err != nil {
		return nil, fmt.Errorf("failed to get view counts: %w", err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
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

func (r *ViewsRepository) BatchCreateUserViews(ctx context.Context, views []*domain.UserView) error {
	if len(views) == 0 {
		return nil
	}

	count := len(views)
	ids := make([]string, count)
	videoIDs := make([]string, count)
	userIDs := make([]*string, count)
	sessionIDs := make([]string, count)
	fingerprintHashes := make([]string, count)
	watchDurations := make([]int, count)
	videoDurations := make([]int, count)
	completionPercentages := make([]float64, count)
	isCompleteds := make([]bool, count)
	seekCounts := make([]int, count)
	pauseCounts := make([]int, count)
	replayCounts := make([]int, count)
	qualityChanges := make([]int, count)
	initialLoadTimes := make([]*int, count)
	bufferEvents := make([]int, count)
	connectionTypes := make([]*string, count)
	videoQualities := make([]*string, count)
	referrerURLs := make([]string, count)
	referrerTypes := make([]string, count)
	utmSources := make([]string, count)
	utmMediums := make([]string, count)
	utmCampaigns := make([]string, count)
	deviceTypes := make([]string, count)
	osNames := make([]string, count)
	browserNames := make([]string, count)
	screenResolutions := make([]string, count)
	isMobiles := make([]bool, count)
	countryCodes := make([]string, count)
	regionCodes := make([]string, count)
	cityNames := make([]string, count)
	timezones := make([]string, count)
	isAnonymouss := make([]bool, count)
	trackingConsents := make([]bool, count)
	gdprConsents := make([]*bool, count)
	viewDates := make([]time.Time, count)
	viewHours := make([]int, count)
	weekdays := make([]int, count)
	createdAts := make([]time.Time, count)
	updatedAts := make([]time.Time, count)

	now := time.Now()

	for i, view := range views {
		if view.ID == "" {
			view.ID = generateUUID()
		}
		view.CreatedAt = now
		view.UpdatedAt = now
		view.SetViewDate(now)

		ids[i] = view.ID
		videoIDs[i] = view.VideoID
		userIDs[i] = view.UserID
		sessionIDs[i] = view.SessionID
		fingerprintHashes[i] = view.FingerprintHash
		watchDurations[i] = view.WatchDuration
		videoDurations[i] = view.VideoDuration
		completionPercentages[i] = view.CompletionPercentage
		isCompleteds[i] = view.IsCompleted
		seekCounts[i] = view.SeekCount
		pauseCounts[i] = view.PauseCount
		replayCounts[i] = view.ReplayCount
		qualityChanges[i] = view.QualityChanges
		initialLoadTimes[i] = view.InitialLoadTime
		bufferEvents[i] = view.BufferEvents
		connectionTypes[i] = view.ConnectionType
		videoQualities[i] = view.VideoQuality
		referrerURLs[i] = view.ReferrerURL
		referrerTypes[i] = view.ReferrerType
		utmSources[i] = view.UTMSource
		utmMediums[i] = view.UTMMedium
		utmCampaigns[i] = view.UTMCampaign
		deviceTypes[i] = view.DeviceType
		osNames[i] = view.OSName
		browserNames[i] = view.BrowserName
		screenResolutions[i] = view.ScreenResolution
		isMobiles[i] = view.IsMobile
		countryCodes[i] = view.CountryCode
		regionCodes[i] = view.RegionCode
		cityNames[i] = view.CityName
		timezones[i] = view.Timezone
		isAnonymouss[i] = view.IsAnonymous
		trackingConsents[i] = view.TrackingConsent
		gdprConsents[i] = view.GDPRConsent
		viewDates[i] = view.ViewDate
		viewHours[i] = view.ViewHour
		weekdays[i] = view.Weekday
		createdAts[i] = view.CreatedAt
		updatedAts[i] = view.UpdatedAt
	}

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
		)
		SELECT
			v.id::uuid, v.video_id::uuid, v.user_id::uuid, v.session_id::uuid, v.fingerprint_hash,
			v.watch_duration, v.video_duration, v.completion_percentage, v.is_completed,
			v.seek_count, v.pause_count, v.replay_count, v.quality_changes,
			v.initial_load_time, v.buffer_events, v.connection_type, v.video_quality,
			v.referrer_url, v.referrer_type, v.utm_source, v.utm_medium, v.utm_campaign,
			v.device_type, v.os_name, v.browser_name, v.screen_resolution, v.is_mobile,
			v.country_code, v.region_code, v.city_name, v.timezone,
			v.is_anonymous, v.tracking_consent, v.gdpr_consent,
			v.view_date, v.view_hour, v.weekday,
			v.created_at, v.updated_at
		FROM UNNEST(
			$1::text[], $2::text[], $3::text[], $4::text[], $5::text[],
			$6::int[], $7::int[], $8::decimal[], $9::boolean[],
			$10::int[], $11::int[], $12::int[], $13::int[],
			$14::int[], $15::int[], $16::text[], $17::text[],
			$18::text[], $19::text[], $20::text[], $21::text[], $22::text[],
			$23::text[], $24::text[], $25::text[], $26::text[], $27::boolean[],
			$28::char(2)[], $29::text[], $30::text[], $31::text[],
			$32::boolean[], $33::boolean[], $34::boolean[],
			$35::date[], $36::int[], $37::int[],
			$38::timestamp[], $39::timestamp[]
		) AS v(
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
		)`

	_, err := r.db.ExecContext(ctx, query,
		pq.Array(ids), pq.Array(videoIDs), pq.Array(userIDs), pq.Array(sessionIDs), pq.Array(fingerprintHashes),
		pq.Array(watchDurations), pq.Array(videoDurations), pq.Array(completionPercentages), pq.Array(isCompleteds),
		pq.Array(seekCounts), pq.Array(pauseCounts), pq.Array(replayCounts), pq.Array(qualityChanges),
		pq.Array(initialLoadTimes), pq.Array(bufferEvents), pq.Array(connectionTypes), pq.Array(videoQualities),
		pq.Array(referrerURLs), pq.Array(referrerTypes), pq.Array(utmSources), pq.Array(utmMediums), pq.Array(utmCampaigns),
		pq.Array(deviceTypes), pq.Array(osNames), pq.Array(browserNames), pq.Array(screenResolutions), pq.Array(isMobiles),
		pq.Array(countryCodes), pq.Array(regionCodes), pq.Array(cityNames), pq.Array(timezones),
		pq.Array(isAnonymouss), pq.Array(trackingConsents), pq.Array(gdprConsents),
		pq.Array(viewDates), pq.Array(viewHours), pq.Array(weekdays),
		pq.Array(createdAts), pq.Array(updatedAts),
	)
	return err
}
