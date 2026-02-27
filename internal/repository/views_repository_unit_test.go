package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/lib/pq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupViewsMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newViewsRepo(t *testing.T) (*ViewsRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupViewsMockDB(t)
	repo := NewViewsRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func sampleUserView() *domain.UserView {
	now := time.Now()
	userID := uuid.New().String()
	return &domain.UserView{
		ID:                   uuid.New().String(),
		VideoID:              uuid.New().String(),
		UserID:               &userID,
		SessionID:            uuid.New().String(),
		FingerprintHash:      "hash123",
		WatchDuration:        120,
		VideoDuration:        300,
		CompletionPercentage: 40.0,
		IsCompleted:          false,
		SeekCount:            2,
		PauseCount:           1,
		ReplayCount:          0,
		QualityChanges:       1,
		BufferEvents:         0,
		DeviceType:           "mobile",
		OSName:               "iOS",
		BrowserName:          "Safari",
		ScreenResolution:     "375x667",
		IsMobile:             true,
		CountryCode:          "US",
		RegionCode:           "CA",
		CityName:             "San Francisco",
		Timezone:             "America/Los_Angeles",
		IsAnonymous:          false,
		TrackingConsent:      true,
		ViewDate:             now.Truncate(24 * time.Hour),
		ViewHour:             now.Hour(),
		Weekday:              int(now.Weekday()),
		CreatedAt:            now,
		UpdatedAt:            now,
	}
}

func sampleTrendingVideo() *domain.TrendingVideo {
	hr := 1
	dr := 3
	wr := 5
	return &domain.TrendingVideo{
		VideoID:         uuid.New().String(),
		ViewsLastHour:   50,
		ViewsLast24h:    1200,
		ViewsLast7d:     8500,
		EngagementScore: 245.67,
		VelocityScore:   89.34,
		HourlyRank:      &hr,
		DailyRank:       &dr,
		WeeklyRank:      &wr,
		IsTrending:      true,
		LastUpdated:     time.Now(),
	}
}

func TestViewsRepository_Unit_CreateUserView(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		view := sampleUserView()
		view.ID = ""

		mock.ExpectExec(`(?s)INSERT INTO user_views`).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.CreateUserView(ctx, view)
		require.NoError(t, err)
		assert.NotEmpty(t, view.ID)
		assert.False(t, view.CreatedAt.IsZero())
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		view := sampleUserView()
		mock.ExpectExec(`(?s)INSERT INTO user_views`).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateUserView(ctx, view)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_UpdateUserView(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		view := sampleUserView()
		view.WatchDuration = 200
		view.VideoDuration = 300

		mock.ExpectExec(`(?s)UPDATE user_views SET`).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateUserView(ctx, view)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		view := sampleUserView()
		mock.ExpectExec(`(?s)UPDATE user_views SET`).
			WillReturnError(errors.New("update failed"))

		err := repo.UpdateUserView(ctx, view)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "update failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetUserViewBySessionAndVideo(t *testing.T) {
	ctx := context.Background()

	viewColumns := []string{
		"id", "video_id", "user_id", "session_id", "fingerprint_hash",
		"watch_duration", "video_duration", "completion_percentage", "is_completed",
		"seek_count", "pause_count", "replay_count", "quality_changes",
		"initial_load_time", "buffer_events",
		"connection_type", "video_quality",
		"referrer_url", "referrer_type", "utm_source", "utm_medium", "utm_campaign",
		"device_type", "os_name", "browser_name", "screen_resolution",
		"is_mobile",
		"country_code", "region_code", "city_name", "timezone",
		"is_anonymous", "tracking_consent", "gdpr_consent",
		"view_date", "view_hour", "weekday",
		"created_at", "updated_at",
	}

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		view := sampleUserView()
		now := time.Now()
		rows := sqlmock.NewRows(viewColumns).AddRow(
			view.ID, view.VideoID, view.UserID, view.SessionID, view.FingerprintHash,
			view.WatchDuration, view.VideoDuration, view.CompletionPercentage, view.IsCompleted,
			view.SeekCount, view.PauseCount, view.ReplayCount, view.QualityChanges,
			nil, view.BufferEvents,
			"", "",
			"", "", "", "", "",
			view.DeviceType, view.OSName, view.BrowserName, view.ScreenResolution,
			view.IsMobile,
			view.CountryCode, view.RegionCode, view.CityName, view.Timezone,
			view.IsAnonymous, view.TrackingConsent, nil,
			now.Truncate(24*time.Hour), now.Hour(), int(now.Weekday()),
			now, now,
		)

		mock.ExpectQuery(`(?s)SELECT.*FROM user_views.*WHERE session_id = \$1 AND video_id = \$2`).
			WithArgs(view.SessionID, view.VideoID).
			WillReturnRows(rows)

		got, err := repo.GetUserViewBySessionAndVideo(ctx, view.SessionID, view.VideoID)
		require.NoError(t, err)
		require.NotNil(t, got)
		assert.Equal(t, view.ID, got.ID)
		assert.Equal(t, view.SessionID, got.SessionID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns nil", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT.*FROM user_views.*WHERE session_id = \$1 AND video_id = \$2`).
			WithArgs("no-session", "no-video").
			WillReturnError(sql.ErrNoRows)

		got, err := repo.GetUserViewBySessionAndVideo(ctx, "no-session", "no-video")
		require.NoError(t, err)
		assert.Nil(t, got)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT.*FROM user_views.*WHERE session_id = \$1 AND video_id = \$2`).
			WithArgs("s", "v").
			WillReturnError(errors.New("db error"))

		got, err := repo.GetUserViewBySessionAndVideo(ctx, "s", "v")
		require.Error(t, err)
		assert.Nil(t, got)
		assert.Contains(t, err.Error(), "failed to get user view")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetDailyVideoStats(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New().String()
	start := time.Now().AddDate(0, 0, -7)
	end := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "stat_date",
			"total_views", "unique_views", "authenticated_views", "anonymous_views",
			"total_watch_time", "avg_watch_duration", "avg_completion_percentage", "completed_views",
			"avg_initial_load_time", "total_buffer_events", "avg_seek_count",
			"desktop_views", "mobile_views", "tablet_views", "tv_views",
			"top_countries", "top_regions", "referrer_breakdown",
			"created_at", "updated_at",
		}).AddRow(
			"stat-1", videoID, start,
			100, 80, 60, 20,
			int64(5000), 50.0, 65.0, int64(30),
			nil, int64(10), 2.5,
			int64(50), int64(30), int64(15), int64(5),
			nil, nil, nil,
			time.Now(), time.Now(),
		)

		mock.ExpectQuery(`(?s)SELECT \* FROM daily_video_stats.*WHERE video_id = \$1.*AND stat_date BETWEEN \$2 AND \$3`).
			WithArgs(videoID, start, end).
			WillReturnRows(rows)

		stats, err := repo.GetDailyVideoStats(ctx, videoID, start, end)
		require.NoError(t, err)
		require.Len(t, stats, 1)
		assert.Equal(t, videoID, stats[0].VideoID)
		assert.Equal(t, int64(100), stats[0].TotalViews)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT \* FROM daily_video_stats`).
			WithArgs(videoID, start, end).
			WillReturnError(errors.New("query failed"))

		stats, err := repo.GetDailyVideoStats(ctx, videoID, start, end)
		require.Error(t, err)
		assert.Nil(t, stats)
		assert.Contains(t, err.Error(), "failed to get daily video stats")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetUserEngagementStats(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New().String()
	start := time.Now().AddDate(0, 0, -7)
	end := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"id", "user_id", "stat_date",
			"videos_watched", "total_watch_time", "avg_session_duration", "unique_videos_watched",
			"avg_completion_rate", "completed_videos", "sessions_count",
			"total_seeks", "total_pauses", "total_replays",
			"preferred_device", "device_diversity",
			"top_categories", "avg_video_duration_preference",
			"created_at", "updated_at",
		}).AddRow(
			"eng-1", userID, start,
			int64(10), int64(3600), 360.0, int64(8),
			75.0, int64(5), int64(3),
			int64(20), int64(15), int64(2),
			"mobile", 2,
			nil, nil,
			time.Now(), time.Now(),
		)

		mock.ExpectQuery(`(?s)SELECT \* FROM user_engagement_stats.*WHERE user_id = \$1.*AND stat_date BETWEEN \$2 AND \$3`).
			WithArgs(userID, start, end).
			WillReturnRows(rows)

		stats, err := repo.GetUserEngagementStats(ctx, userID, start, end)
		require.NoError(t, err)
		require.Len(t, stats, 1)
		assert.Equal(t, userID, stats[0].UserID)
		assert.Equal(t, int64(10), stats[0].VideosWatched)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT \* FROM user_engagement_stats`).
			WithArgs(userID, start, end).
			WillReturnError(errors.New("query failed"))

		stats, err := repo.GetUserEngagementStats(ctx, userID, start, end)
		require.Error(t, err)
		assert.Nil(t, stats)
		assert.Contains(t, err.Error(), "failed to get user engagement stats")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetTrendingVideos(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		tv := sampleTrendingVideo()
		rows := sqlmock.NewRows([]string{
			"video_id", "views_last_hour", "views_last_24h", "views_last_7d",
			"engagement_score", "velocity_score",
			"hourly_rank", "daily_rank", "weekly_rank",
			"last_updated", "is_trending",
		}).AddRow(
			tv.VideoID, tv.ViewsLastHour, tv.ViewsLast24h, tv.ViewsLast7d,
			tv.EngagementScore, tv.VelocityScore,
			tv.HourlyRank, tv.DailyRank, tv.WeeklyRank,
			tv.LastUpdated, tv.IsTrending,
		)

		mock.ExpectQuery(`(?s)SELECT \* FROM trending_videos.*WHERE is_trending = true.*ORDER BY engagement_score DESC.*LIMIT \$1`).
			WithArgs(10).
			WillReturnRows(rows)

		trending, err := repo.GetTrendingVideos(ctx, 10)
		require.NoError(t, err)
		require.Len(t, trending, 1)
		assert.Equal(t, tv.VideoID, trending[0].VideoID)
		assert.Equal(t, tv.EngagementScore, trending[0].EngagementScore)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{
			"video_id", "views_last_hour", "views_last_24h", "views_last_7d",
			"engagement_score", "velocity_score",
			"hourly_rank", "daily_rank", "weekly_rank",
			"last_updated", "is_trending",
		})

		mock.ExpectQuery(`(?s)SELECT \* FROM trending_videos`).
			WithArgs(5).
			WillReturnRows(rows)

		trending, err := repo.GetTrendingVideos(ctx, 5)
		require.NoError(t, err)
		assert.Empty(t, trending)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT \* FROM trending_videos`).
			WithArgs(10).
			WillReturnError(errors.New("query failed"))

		trending, err := repo.GetTrendingVideos(ctx, 10)
		require.Error(t, err)
		assert.Nil(t, trending)
		assert.Contains(t, err.Error(), "failed to get trending videos")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_UpdateTrendingVideo(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		tv := sampleTrendingVideo()
		mock.ExpectExec(`(?s)INSERT INTO trending_videos`).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.UpdateTrendingVideo(ctx, tv)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		tv := sampleTrendingVideo()
		mock.ExpectExec(`(?s)INSERT INTO trending_videos`).
			WillReturnError(errors.New("upsert failed"))

		err := repo.UpdateTrendingVideo(ctx, tv)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upsert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetBatchTrendingStats(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns empty slice", func(t *testing.T) {
		repo, _, cleanup := newViewsRepo(t)
		defer cleanup()

		stats, err := repo.GetBatchTrendingStats(ctx, []string{})
		require.NoError(t, err)
		assert.Empty(t, stats)
	})

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		ids := []string{uuid.New().String(), uuid.New().String()}
		rows := sqlmock.NewRows([]string{
			"video_id", "views_last_hour", "views_last_24h", "views_last_7d",
			"score_1h", "score_24h", "score_7d",
		}).
			AddRow(ids[0], int64(10), int64(200), int64(1500), 1.5, 3.0, 5.0).
			AddRow(ids[1], int64(20), int64(400), int64(3000), 2.5, 4.0, 6.0)

		mock.ExpectQuery(`(?s)SELECT.*FROM unnest`).
			WithArgs(pq.Array(ids)).
			WillReturnRows(rows)

		stats, err := repo.GetBatchTrendingStats(ctx, ids)
		require.NoError(t, err)
		require.Len(t, stats, 2)
		assert.Equal(t, ids[0], stats[0].VideoID)
		assert.Equal(t, int64(10), stats[0].ViewsLastHour)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		ids := []string{"vid-1"}
		mock.ExpectQuery(`(?s)SELECT.*FROM unnest`).
			WithArgs(pq.Array(ids)).
			WillReturnError(errors.New("query failed"))

		stats, err := repo.GetBatchTrendingStats(ctx, ids)
		require.Error(t, err)
		assert.Nil(t, stats)
		assert.Contains(t, err.Error(), "failed to get batch trending stats")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_BatchUpdateTrendingVideos(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns nil", func(t *testing.T) {
		repo, _, cleanup := newViewsRepo(t)
		defer cleanup()

		err := repo.BatchUpdateTrendingVideos(ctx, []*domain.TrendingVideo{})
		require.NoError(t, err)
	})

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		videos := []*domain.TrendingVideo{sampleTrendingVideo(), sampleTrendingVideo()}

		mock.ExpectExec(`(?s)INSERT INTO trending_videos`).
			WithArgs(
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			).
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := repo.BatchUpdateTrendingVideos(ctx, videos)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		videos := []*domain.TrendingVideo{sampleTrendingVideo()}
		mock.ExpectExec(`(?s)INSERT INTO trending_videos`).
			WithArgs(
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
				sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			).
			WillReturnError(errors.New("batch insert failed"))

		err := repo.BatchUpdateTrendingVideos(ctx, videos)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "batch insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_IncrementVideoViews(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New().String()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`SELECT increment_video_views($1)`)).
			WithArgs(videoID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.IncrementVideoViews(ctx, videoID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`SELECT increment_video_views($1)`)).
			WithArgs(videoID).
			WillReturnError(errors.New("exec failed"))

		err := repo.IncrementVideoViews(ctx, videoID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "exec failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetUniqueViews(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New().String()
	start := time.Now().Add(-24 * time.Hour)
	end := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT get_unique_views($1, $2, $3)`)).
			WithArgs(videoID, start, end).
			WillReturnRows(sqlmock.NewRows([]string{"get_unique_views"}).AddRow(int64(42)))

		count, err := repo.GetUniqueViews(ctx, videoID, start, end)
		require.NoError(t, err)
		assert.Equal(t, int64(42), count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT get_unique_views($1, $2, $3)`)).
			WithArgs(videoID, start, end).
			WillReturnError(errors.New("query failed"))

		count, err := repo.GetUniqueViews(ctx, videoID, start, end)
		require.Error(t, err)
		assert.Equal(t, int64(0), count)
		assert.Contains(t, err.Error(), "failed to get unique views")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_CalculateEngagementScore(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New().String()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT calculate_engagement_score($1, $2)`)).
			WithArgs(videoID, 24).
			WillReturnRows(sqlmock.NewRows([]string{"calculate_engagement_score"}).AddRow(87.5))

		score, err := repo.CalculateEngagementScore(ctx, videoID, 24)
		require.NoError(t, err)
		assert.Equal(t, 87.5, score)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT calculate_engagement_score($1, $2)`)).
			WithArgs(videoID, 24).
			WillReturnError(errors.New("query failed"))

		score, err := repo.CalculateEngagementScore(ctx, videoID, 24)
		require.Error(t, err)
		assert.Equal(t, 0.0, score)
		assert.Contains(t, err.Error(), "failed to calculate engagement score")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_AggregateDailyStats(t *testing.T) {
	ctx := context.Background()
	date := time.Now().Truncate(24 * time.Hour)

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`SELECT aggregate_daily_stats($1)`)).
			WithArgs(date).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.AggregateDailyStats(ctx, date)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`SELECT aggregate_daily_stats($1)`)).
			WithArgs(date).
			WillReturnError(errors.New("aggregate failed"))

		err := repo.AggregateDailyStats(ctx, date)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "aggregate failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_CleanupOldViews(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`SELECT cleanup_old_views($1)`)).
			WithArgs(90).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.CleanupOldViews(ctx, 90)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(`SELECT cleanup_old_views($1)`)).
			WithArgs(90).
			WillReturnError(errors.New("cleanup failed"))

		err := repo.CleanupOldViews(ctx, 90)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "cleanup failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetViewsByDateRange(t *testing.T) {
	ctx := context.Background()

	userViewColumns := []string{
		"id", "video_id", "user_id", "session_id", "fingerprint_hash",
		"watch_duration", "video_duration", "completion_percentage", "is_completed",
		"seek_count", "pause_count", "replay_count", "quality_changes",
		"initial_load_time", "buffer_events",
		"connection_type", "video_quality",
		"referrer_url", "referrer_type", "utm_source", "utm_medium", "utm_campaign",
		"device_type", "os_name", "browser_name", "screen_resolution",
		"is_mobile",
		"country_code", "region_code", "city_name", "timezone",
		"is_anonymous", "tracking_consent", "gdpr_consent",
		"view_date", "view_hour", "weekday",
		"created_at", "updated_at",
	}

	addViewRow := func(rows *sqlmock.Rows, view *domain.UserView) *sqlmock.Rows {
		return rows.AddRow(
			view.ID, view.VideoID, view.UserID, view.SessionID, view.FingerprintHash,
			view.WatchDuration, view.VideoDuration, view.CompletionPercentage, view.IsCompleted,
			view.SeekCount, view.PauseCount, view.ReplayCount, view.QualityChanges,
			nil, view.BufferEvents,
			nil, nil,
			"", "", "", "", "",
			view.DeviceType, view.OSName, view.BrowserName, view.ScreenResolution,
			view.IsMobile,
			view.CountryCode, view.RegionCode, view.CityName, view.Timezone,
			view.IsAnonymous, view.TrackingConsent, nil,
			view.ViewDate, view.ViewHour, view.Weekday,
			view.CreatedAt, view.UpdatedAt,
		)
	}

	t.Run("success with video filter only", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		v := sampleUserView()
		filter := &domain.ViewAnalyticsFilter{VideoID: v.VideoID}

		rows := addViewRow(sqlmock.NewRows(userViewColumns), v)

		mock.ExpectQuery(`(?s)SELECT \* FROM user_views WHERE 1=1 AND video_id = \$1`).
			WithArgs(v.VideoID).
			WillReturnRows(rows)

		views, err := repo.GetViewsByDateRange(ctx, filter)
		require.NoError(t, err)
		require.Len(t, views, 1)
		assert.Equal(t, v.VideoID, views[0].VideoID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with all filters", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		start := time.Now().Add(-24 * time.Hour)
		end := time.Now()
		filter := &domain.ViewAnalyticsFilter{
			VideoID:   "vid-1",
			StartDate: &start,
			EndDate:   &end,
			Limit:     10,
			Offset:    5,
		}

		rows := sqlmock.NewRows(userViewColumns)

		mock.ExpectQuery(`(?s)SELECT \* FROM user_views WHERE 1=1`).
			WithArgs("vid-1", start, end, 10, 5).
			WillReturnRows(rows)

		views, err := repo.GetViewsByDateRange(ctx, filter)
		require.NoError(t, err)
		assert.Empty(t, views)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		filter := &domain.ViewAnalyticsFilter{}
		mock.ExpectQuery(`(?s)SELECT \* FROM user_views WHERE 1=1`).
			WillReturnError(errors.New("query failed"))

		views, err := repo.GetViewsByDateRange(ctx, filter)
		require.Error(t, err)
		assert.Nil(t, views)
		assert.Contains(t, err.Error(), "failed to get views by date range")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetTopVideos(t *testing.T) {
	ctx := context.Background()
	start := time.Now().AddDate(0, 0, -7)
	end := time.Now()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		rows := sqlmock.NewRows([]string{"video_id", "total_views", "unique_views", "avg_duration"}).
			AddRow("vid-1", int64(100), int64(80), 120.5).
			AddRow("vid-2", int64(50), int64(40), 90.0)

		mock.ExpectQuery(`(?s)SELECT.*FROM user_views.*WHERE created_at BETWEEN \$1 AND \$2.*GROUP BY video_id.*LIMIT \$3`).
			WithArgs(start, end, 10).
			WillReturnRows(rows)

		top, err := repo.GetTopVideos(ctx, start, end, 10)
		require.NoError(t, err)
		require.Len(t, top, 2)
		assert.Equal(t, "vid-1", top[0].VideoID)
		assert.Equal(t, int64(100), top[0].TotalViews)
		assert.Equal(t, int64(80), top[0].UniqueViews)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT.*FROM user_views.*WHERE created_at BETWEEN`).
			WithArgs(start, end, 5).
			WillReturnError(errors.New("query failed"))

		top, err := repo.GetTopVideos(ctx, start, end, 5)
		require.Error(t, err)
		assert.Nil(t, top)
		assert.Contains(t, err.Error(), "failed to get top videos")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetViewCountsByVideo(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns empty map", func(t *testing.T) {
		repo, _, cleanup := newViewsRepo(t)
		defer cleanup()

		counts, err := repo.GetViewCountsByVideo(ctx, []string{})
		require.NoError(t, err)
		assert.Empty(t, counts)
	})

	t.Run("success - multiple videos with counts", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		ids := []string{"vid-1", "vid-2"}
		rows := sqlmock.NewRows([]string{"video_id", "view_count"}).
			AddRow("vid-1", int64(10)).
			AddRow("vid-2", int64(5))

		mock.ExpectQuery(`SELECT video_id, COUNT\(\*\) as view_count FROM user_views WHERE video_id = ANY\(\$1\) GROUP BY video_id`).
			WithArgs(pq.Array(ids)).
			WillReturnRows(rows)

		counts, err := repo.GetViewCountsByVideo(ctx, ids)
		require.NoError(t, err)
		assert.Len(t, counts, 2)
		assert.Equal(t, int64(10), counts["vid-1"])
		assert.Equal(t, int64(5), counts["vid-2"])
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		ids := []string{"vid-1"}
		mock.ExpectQuery(`SELECT video_id, COUNT\(\*\) as view_count FROM user_views WHERE video_id = ANY\(\$1\) GROUP BY video_id`).
			WithArgs(pq.Array(ids)).
			WillReturnError(assert.AnError)

		counts, err := repo.GetViewCountsByVideo(ctx, ids)
		require.Error(t, err)
		assert.Nil(t, counts)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetRecentViews(t *testing.T) {
	ctx := context.Background()
	userID := uuid.New().String()

	userViewColumns := []string{
		"id", "video_id", "user_id", "session_id", "fingerprint_hash",
		"watch_duration", "video_duration", "completion_percentage", "is_completed",
		"seek_count", "pause_count", "replay_count", "quality_changes",
		"initial_load_time", "buffer_events",
		"connection_type", "video_quality",
		"referrer_url", "referrer_type", "utm_source", "utm_medium", "utm_campaign",
		"device_type", "os_name", "browser_name", "screen_resolution",
		"is_mobile",
		"country_code", "region_code", "city_name", "timezone",
		"is_anonymous", "tracking_consent", "gdpr_consent",
		"view_date", "view_hour", "weekday",
		"created_at", "updated_at",
	}

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		v := sampleUserView()
		v.UserID = &userID
		now := time.Now()
		rows := sqlmock.NewRows(userViewColumns).AddRow(
			v.ID, v.VideoID, v.UserID, v.SessionID, v.FingerprintHash,
			v.WatchDuration, v.VideoDuration, v.CompletionPercentage, v.IsCompleted,
			v.SeekCount, v.PauseCount, v.ReplayCount, v.QualityChanges,
			nil, v.BufferEvents,
			nil, nil,
			"", "", "", "", "",
			v.DeviceType, v.OSName, v.BrowserName, v.ScreenResolution,
			v.IsMobile,
			v.CountryCode, v.RegionCode, v.CityName, v.Timezone,
			v.IsAnonymous, v.TrackingConsent, nil,
			now.Truncate(24*time.Hour), now.Hour(), int(now.Weekday()),
			now, now,
		)

		mock.ExpectQuery(`(?s)SELECT \* FROM user_views.*WHERE user_id = \$1.*ORDER BY created_at DESC.*LIMIT \$2`).
			WithArgs(userID, 10).
			WillReturnRows(rows)

		views, err := repo.GetRecentViews(ctx, userID, 10)
		require.NoError(t, err)
		require.Len(t, views, 1)
		assert.Equal(t, v.ID, views[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectQuery(`(?s)SELECT \* FROM user_views.*WHERE user_id = \$1`).
			WithArgs(userID, 5).
			WillReturnError(errors.New("query failed"))

		views, err := repo.GetRecentViews(ctx, userID, 5)
		require.Error(t, err)
		assert.Nil(t, views)
		assert.Contains(t, err.Error(), "failed to get recent views")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_BatchCreateUserViews(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns nil", func(t *testing.T) {
		repo, _, cleanup := newViewsRepo(t)
		defer cleanup()

		err := repo.BatchCreateUserViews(ctx, []*domain.UserView{})
		require.NoError(t, err)
	})

	t.Run("success single view", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		view := sampleUserView()
		view.ID = ""

		args := make([]driver.Value, 39)
		for i := 0; i < 39; i++ {
			args[i] = sqlmock.AnyArg()
		}

		mock.ExpectExec(`(?s)INSERT INTO user_views`).
			WithArgs(args...).
			WillReturnResult(sqlmock.NewResult(1, 1))

		err := repo.BatchCreateUserViews(ctx, []*domain.UserView{view})
		require.NoError(t, err)
		assert.NotEmpty(t, view.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success multiple views", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		v1 := sampleUserView()
		v2 := sampleUserView()
		v1.ID = ""
		v2.ID = ""

		args := make([]driver.Value, 39)
		for i := 0; i < 39; i++ {
			args[i] = sqlmock.AnyArg()
		}

		mock.ExpectExec(`(?s)INSERT INTO user_views`).
			WithArgs(args...).
			WillReturnResult(sqlmock.NewResult(1, 2))

		err := repo.BatchCreateUserViews(ctx, []*domain.UserView{v1, v2})
		require.NoError(t, err)
		assert.NotEmpty(t, v1.ID)
		assert.NotEmpty(t, v2.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		mock.ExpectExec(`(?s)INSERT INTO user_views`).
			WillReturnError(errors.New("insert failed"))

		err := repo.BatchCreateUserViews(ctx, []*domain.UserView{sampleUserView()})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestViewsRepository_Unit_GetVideoAnalytics(t *testing.T) {
	ctx := context.Background()

	t.Run("success with minimal filter", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		filter := &domain.ViewAnalyticsFilter{VideoID: "vid-1"}

		mock.ExpectQuery(`(?s)SELECT.*COUNT\(\*\) as total_views.*FROM \(SELECT \* FROM user_views WHERE 1=1 AND video_id`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{
				"total_views", "unique_views", "avg_duration", "avg_watch_duration",
				"completion", "avg_completion_rate", "completed_views", "total_watch_time",
			}).AddRow(int64(100), int64(80), 120.0, 120.0, 65.0, 65.0, int64(30), int64(12000)))

		mock.ExpectQuery(`(?s)SELECT.*COALESCE\(device_type.*GROUP BY device_type`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{"field", "count"}).
				AddRow("mobile", int64(60)).
				AddRow("desktop", int64(40)))

		mock.ExpectQuery(`(?s)SELECT.*COALESCE\(country_code.*GROUP BY country_code`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{"field", "count"}).
				AddRow("US", int64(70)).
				AddRow("CA", int64(30)))

		mock.ExpectQuery(`(?s)SELECT.*view_hour.*GROUP BY view_hour`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{"view_hour", "count"}).
				AddRow(10, int64(20)).
				AddRow(14, int64(30)))

		analytics, err := repo.GetVideoAnalytics(ctx, filter)
		require.NoError(t, err)
		require.NotNil(t, analytics)

		assert.Equal(t, int64(100), analytics.TotalViews)
		assert.Equal(t, int64(80), analytics.UniqueViews)
		assert.Equal(t, int64(30), analytics.CompletedViews)
		assert.Equal(t, int64(12000), analytics.TotalWatchTime)

		assert.Equal(t, int64(60), analytics.DeviceBreakdown["mobile"])
		assert.Equal(t, int64(40), analytics.DeviceBreakdown["desktop"])

		assert.Equal(t, int64(70), analytics.CountryBreakdown["US"])
		assert.Equal(t, int64(30), analytics.CountryBreakdown["CA"])

		assert.Equal(t, int64(20), analytics.HourlyStats[10])
		assert.Equal(t, int64(30), analytics.HourlyStats[14])

		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("aggregate stats failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		filter := &domain.ViewAnalyticsFilter{VideoID: "vid-1"}

		mock.ExpectQuery(`(?s)SELECT.*COUNT\(\*\) as total_views`).
			WithArgs("vid-1").
			WillReturnError(errors.New("aggregate failed"))

		analytics, err := repo.GetVideoAnalytics(ctx, filter)
		require.Error(t, err)
		assert.Nil(t, analytics)
		assert.Contains(t, err.Error(), "failed to get video analytics")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("device stats failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		filter := &domain.ViewAnalyticsFilter{VideoID: "vid-1"}

		mock.ExpectQuery(`(?s)SELECT.*COUNT\(\*\) as total_views`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{
				"total_views", "unique_views", "avg_duration", "avg_watch_duration",
				"completion", "avg_completion_rate", "completed_views", "total_watch_time",
			}).AddRow(int64(10), int64(8), 60.0, 60.0, 50.0, 50.0, int64(3), int64(600)))

		mock.ExpectQuery(`(?s)SELECT.*COALESCE\(device_type`).
			WithArgs("vid-1").
			WillReturnError(errors.New("device query failed"))

		analytics, err := repo.GetVideoAnalytics(ctx, filter)
		require.Error(t, err)
		assert.Nil(t, analytics)
		assert.Contains(t, err.Error(), "failed to get device_type stats")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("geo stats failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		filter := &domain.ViewAnalyticsFilter{VideoID: "vid-1"}

		mock.ExpectQuery(`(?s)SELECT.*COUNT\(\*\) as total_views`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{
				"total_views", "unique_views", "avg_duration", "avg_watch_duration",
				"completion", "avg_completion_rate", "completed_views", "total_watch_time",
			}).AddRow(int64(10), int64(8), 60.0, 60.0, 50.0, 50.0, int64(3), int64(600)))

		mock.ExpectQuery(`(?s)SELECT.*COALESCE\(device_type`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{"field", "count"}).AddRow("mobile", int64(10)))

		mock.ExpectQuery(`(?s)SELECT.*COALESCE\(country_code`).
			WithArgs("vid-1").
			WillReturnError(errors.New("geo query failed"))

		analytics, err := repo.GetVideoAnalytics(ctx, filter)
		require.Error(t, err)
		assert.Nil(t, analytics)
		assert.Contains(t, err.Error(), "failed to get country_code stats")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("hourly stats failure", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		filter := &domain.ViewAnalyticsFilter{VideoID: "vid-1"}

		mock.ExpectQuery(`(?s)SELECT.*COUNT\(\*\) as total_views`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{
				"total_views", "unique_views", "avg_duration", "avg_watch_duration",
				"completion", "avg_completion_rate", "completed_views", "total_watch_time",
			}).AddRow(int64(10), int64(8), 60.0, 60.0, 50.0, 50.0, int64(3), int64(600)))

		mock.ExpectQuery(`(?s)SELECT.*COALESCE\(device_type`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{"field", "count"}).AddRow("mobile", int64(10)))

		mock.ExpectQuery(`(?s)SELECT.*COALESCE\(country_code`).
			WithArgs("vid-1").
			WillReturnRows(sqlmock.NewRows([]string{"field", "count"}).AddRow("US", int64(10)))

		mock.ExpectQuery(`(?s)SELECT.*view_hour.*GROUP BY view_hour`).
			WithArgs("vid-1").
			WillReturnError(errors.New("hourly query failed"))

		analytics, err := repo.GetVideoAnalytics(ctx, filter)
		require.Error(t, err)
		assert.Nil(t, analytics)
		assert.Contains(t, err.Error(), "failed to get hourly stats")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestBatchIncrementVideoViews(t *testing.T) {
	ctx := context.Background()

	t.Run("empty map does nothing", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		err := repo.BatchIncrementVideoViews(ctx, map[string]int64{})
		assert.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("single entry uses single query", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		videoID := uuid.New().String()
		mock.ExpectExec(`UPDATE videos.*unnest`).
			WithArgs(pq.Array([]string{videoID}), pq.Array([]int64{5})).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.BatchIncrementVideoViews(ctx, map[string]int64{videoID: 5})
		assert.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("multiple entries use single query", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		id1 := uuid.New().String()
		id2 := uuid.New().String()

		// Single query expected (not two)
		mock.ExpectExec(`UPDATE videos.*unnest`).
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := repo.BatchIncrementVideoViews(ctx, map[string]int64{id1: 3, id2: 7})
		assert.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("db error propagated", func(t *testing.T) {
		repo, mock, cleanup := newViewsRepo(t)
		defer cleanup()

		videoID := uuid.New().String()
		mock.ExpectExec(`UPDATE videos.*unnest`).
			WillReturnError(errors.New("db error"))

		err := repo.BatchIncrementVideoViews(ctx, map[string]int64{videoID: 1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "batch increment views")
	})
}
