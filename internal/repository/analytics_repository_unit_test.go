package repository

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func setupAnalyticsMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock) {
	t.Helper()
	mockDB, mock, err := sqlmock.New()
	require.NoError(t, err)
	return sqlx.NewDb(mockDB, "sqlmock"), mock
}

func newAnalyticsRepo(t *testing.T) (AnalyticsRepository, sqlmock.Sqlmock, func()) {
	t.Helper()
	db, mock := setupAnalyticsMockDB(t)
	repo := NewAnalyticsRepository(db)
	cleanup := func() { _ = db.Close() }
	return repo, mock, cleanup
}

func sampleStreamAnalytics() *domain.StreamAnalytics {
	now := time.Now().Truncate(time.Second)
	bitrate := 4500
	framerate := 30.0
	bufferingRatio := 0.02
	avgLatency := 150
	return &domain.StreamAnalytics{
		ID:                uuid.New(),
		StreamID:          uuid.New(),
		CollectedAt:       now,
		ViewerCount:       100,
		PeakViewerCount:   150,
		UniqueViewers:     80,
		AverageWatchTime:  300,
		ChatMessagesCount: 50,
		ChatParticipants:  20,
		LikesCount:        30,
		SharesCount:       10,
		Bitrate:           &bitrate,
		Framerate:         &framerate,
		Resolution:        "1920x1080",
		BufferingRatio:    &bufferingRatio,
		AvgLatency:        &avgLatency,
		ViewerCountries:   json.RawMessage(`{"US":45,"UK":20}`),
		ViewerDevices:     json.RawMessage(`{"desktop":60,"mobile":30}`),
		ViewerBrowsers:    json.RawMessage(`{"chrome":50,"firefox":25}`),
		CreatedAt:         now,
		UpdatedAt:         now,
	}
}

func sampleViewerSession() *domain.AnalyticsViewerSession {
	now := time.Now().Truncate(time.Second)
	userID := uuid.New()
	return &domain.AnalyticsViewerSession{
		ID:              uuid.New(),
		StreamID:        uuid.New(),
		UserID:          &userID,
		SessionID:       "sess-abc-123",
		JoinedAt:        now,
		IPAddress:       "192.168.1.1",
		CountryCode:     "US",
		City:            "New York",
		DeviceType:      "desktop",
		Browser:         "chrome",
		OperatingSystem: "linux",
		CreatedAt:       now,
		UpdatedAt:       now,
	}
}

func sampleStreamStatsSummary() *domain.StreamStatsSummary {
	now := time.Now().Truncate(time.Second)
	avgBitrate := 4500
	avgFramerate := 30.0
	firstViewer := now.Add(-1 * time.Hour)
	peakTime := now.Add(-30 * time.Minute)
	return &domain.StreamStatsSummary{
		ID:                    uuid.New(),
		StreamID:              uuid.New(),
		TotalViewers:          500,
		PeakConcurrentViewers: 150,
		AverageViewers:        80,
		TotalWatchTime:        36000,
		AverageWatchDuration:  300,
		TotalChatMessages:     200,
		TotalUniqueChatters:   50,
		TotalLikes:            100,
		TotalShares:           30,
		EngagementRate:        36.0,
		AverageBitrate:        &avgBitrate,
		AverageFramerate:      &avgFramerate,
		QualityScore:          95.0,
		StreamDuration:        3600,
		FirstViewerJoinedAt:   &firstViewer,
		PeakTime:              &peakTime,
		TopCountries:          json.RawMessage(`[{"country":"US","viewers":100}]`),
		CountriesCount:        5,
		TopDevices:            json.RawMessage(`{"desktop":60}`),
		TopBrowsers:           json.RawMessage(`{"chrome":50}`),
		CreatedAt:             now,
		UpdatedAt:             now,
	}
}

func analyticsColumns() []string {
	return []string{
		"id", "stream_id", "collected_at",
		"viewer_count", "peak_viewer_count", "unique_viewers", "average_watch_time",
		"chat_messages_count", "chat_participants", "likes_count", "shares_count",
		"bitrate", "framerate", "resolution", "buffering_ratio", "avg_latency",
		"viewer_countries", "viewer_devices", "viewer_browsers",
		"created_at", "updated_at",
	}
}

func viewerSessionColumns() []string {
	return []string{
		"id", "stream_id", "user_id", "session_id",
		"joined_at", "left_at", "watch_duration",
		"ip_address", "country_code", "city",
		"device_type", "browser", "operating_system",
		"messages_sent", "liked", "shared",
		"created_at", "updated_at",
	}
}

func summaryColumns() []string {
	return []string{
		"id", "stream_id",
		"total_viewers", "peak_concurrent_viewers", "average_viewers",
		"total_watch_time", "average_watch_duration",
		"total_chat_messages", "total_unique_chatters",
		"total_likes", "total_shares", "engagement_rate",
		"average_bitrate", "average_framerate", "quality_score",
		"stream_duration", "first_viewer_joined_at", "peak_time",
		"top_countries", "countries_count",
		"top_devices", "top_browsers",
		"created_at", "updated_at",
	}
}

func analyticsRow(a *domain.StreamAnalytics) []driver.Value {
	return []driver.Value{
		a.ID, a.StreamID, a.CollectedAt,
		a.ViewerCount, a.PeakViewerCount, a.UniqueViewers, a.AverageWatchTime,
		a.ChatMessagesCount, a.ChatParticipants, a.LikesCount, a.SharesCount,
		a.Bitrate, a.Framerate, a.Resolution, a.BufferingRatio, a.AvgLatency,
		a.ViewerCountries, a.ViewerDevices, a.ViewerBrowsers,
		a.CreatedAt, a.UpdatedAt,
	}
}

func viewerSessionRow(s *domain.AnalyticsViewerSession) []driver.Value {
	return []driver.Value{
		s.ID, s.StreamID, s.UserID, s.SessionID,
		s.JoinedAt, s.LeftAt, s.WatchDuration,
		s.IPAddress, s.CountryCode, s.City,
		s.DeviceType, s.Browser, s.OperatingSystem,
		s.MessagesSent, s.Liked, s.Shared,
		s.CreatedAt, s.UpdatedAt,
	}
}

func summaryRow(s *domain.StreamStatsSummary) []driver.Value {
	return []driver.Value{
		s.ID, s.StreamID,
		s.TotalViewers, s.PeakConcurrentViewers, s.AverageViewers,
		s.TotalWatchTime, s.AverageWatchDuration,
		s.TotalChatMessages, s.TotalUniqueChatters,
		s.TotalLikes, s.TotalShares, s.EngagementRate,
		s.AverageBitrate, s.AverageFramerate, s.QualityScore,
		s.StreamDuration, s.FirstViewerJoinedAt, s.PeakTime,
		s.TopCountries, s.CountriesCount,
		s.TopDevices, s.TopBrowsers,
		s.CreatedAt, s.UpdatedAt,
	}
}

func dataPointColumns() []string {
	return []string{"time_bucket", "avg_viewers", "max_viewers", "messages", "avg_bitrate"}
}

// ---------------------------------------------------------------------------
// CreateAnalytics
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_CreateAnalytics(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		a := sampleStreamAnalytics()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO stream_analytics`)).
			WithArgs(
				a.ID, a.StreamID, a.CollectedAt,
				a.ViewerCount, a.PeakViewerCount, a.UniqueViewers, a.AverageWatchTime,
				a.ChatMessagesCount, a.ChatParticipants, a.LikesCount, a.SharesCount,
				a.Bitrate, a.Framerate, a.Resolution, a.BufferingRatio, a.AvgLatency,
				a.ViewerCountries, a.ViewerDevices, a.ViewerBrowsers,
				a.CreatedAt, a.UpdatedAt,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateAnalytics(ctx, a)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		a := sampleStreamAnalytics()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO stream_analytics`)).
			WithArgs(
				a.ID, a.StreamID, a.CollectedAt,
				a.ViewerCount, a.PeakViewerCount, a.UniqueViewers, a.AverageWatchTime,
				a.ChatMessagesCount, a.ChatParticipants, a.LikesCount, a.SharesCount,
				a.Bitrate, a.Framerate, a.Resolution, a.BufferingRatio, a.AvgLatency,
				a.ViewerCountries, a.ViewerDevices, a.ViewerBrowsers,
				a.CreatedAt, a.UpdatedAt,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateAnalytics(ctx, a)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetAnalyticsByStream
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_GetAnalyticsByStream(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		a := sampleStreamAnalytics()
		tr := &domain.AnalyticsTimeRange{
			StartTime: time.Now().Add(-1 * time.Hour),
			EndTime:   time.Now(),
		}

		rows := sqlmock.NewRows(analyticsColumns()).AddRow(analyticsRow(a)...)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM stream_analytics
		WHERE stream_id = $1
		AND collected_at BETWEEN $2 AND $3
		ORDER BY collected_at ASC`)).
			WithArgs(a.StreamID, tr.StartTime, tr.EndTime).
			WillReturnRows(rows)

		result, err := repo.GetAnalyticsByStream(ctx, a.StreamID, tr)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, a.ID, result[0].ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()
		tr := &domain.AnalyticsTimeRange{
			StartTime: time.Now().Add(-1 * time.Hour),
			EndTime:   time.Now(),
		}

		rows := sqlmock.NewRows(analyticsColumns())

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM stream_analytics
		WHERE stream_id = $1
		AND collected_at BETWEEN $2 AND $3
		ORDER BY collected_at ASC`)).
			WithArgs(streamID, tr.StartTime, tr.EndTime).
			WillReturnRows(rows)

		result, err := repo.GetAnalyticsByStream(ctx, streamID, tr)
		require.NoError(t, err)
		assert.Empty(t, result)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()
		tr := &domain.AnalyticsTimeRange{
			StartTime: time.Now().Add(-1 * time.Hour),
			EndTime:   time.Now(),
		}

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM stream_analytics
		WHERE stream_id = $1
		AND collected_at BETWEEN $2 AND $3
		ORDER BY collected_at ASC`)).
			WithArgs(streamID, tr.StartTime, tr.EndTime).
			WillReturnError(errors.New("query failed"))

		result, err := repo.GetAnalyticsByStream(ctx, streamID, tr)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get analytics")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetAnalyticsTimeSeries
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_GetAnalyticsTimeSeries(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()
		now := time.Now().Truncate(time.Second)
		avgBitrate := 4500
		tr := &domain.AnalyticsTimeRange{
			StartTime: now.Add(-1 * time.Hour),
			EndTime:   now,
			Interval:  5,
		}

		rows := sqlmock.NewRows(dataPointColumns()).
			AddRow(now.Add(-30*time.Minute), 80, 120, 15, &avgBitrate).
			AddRow(now.Add(-15*time.Minute), 90, 130, 20, &avgBitrate)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM get_stream_analytics_range($1, $2, $3, $4)`)).
			WithArgs(streamID, tr.StartTime, tr.EndTime, tr.Interval).
			WillReturnRows(rows)

		result, err := repo.GetAnalyticsTimeSeries(ctx, streamID, tr)
		require.NoError(t, err)
		require.Len(t, result, 2)
		assert.Equal(t, 80, result[0].AvgViewers)
		assert.Equal(t, 90, result[1].AvgViewers)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()
		tr := &domain.AnalyticsTimeRange{
			StartTime: time.Now().Add(-1 * time.Hour),
			EndTime:   time.Now(),
			Interval:  5,
		}

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM get_stream_analytics_range($1, $2, $3, $4)`)).
			WithArgs(streamID, tr.StartTime, tr.EndTime, tr.Interval).
			WillReturnError(errors.New("function error"))

		result, err := repo.GetAnalyticsTimeSeries(ctx, streamID, tr)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get analytics time series")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetLatestAnalytics
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_GetLatestAnalytics(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		a := sampleStreamAnalytics()
		rows := sqlmock.NewRows(analyticsColumns()).AddRow(analyticsRow(a)...)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM stream_analytics
		WHERE stream_id = $1
		ORDER BY collected_at DESC
		LIMIT 1`)).
			WithArgs(a.StreamID).
			WillReturnRows(rows)

		result, err := repo.GetLatestAnalytics(ctx, a.StreamID)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, a.ID, result.ID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns nil", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM stream_analytics
		WHERE stream_id = $1
		ORDER BY collected_at DESC
		LIMIT 1`)).
			WithArgs(streamID).
			WillReturnError(sql.ErrNoRows)

		result, err := repo.GetLatestAnalytics(ctx, streamID)
		require.NoError(t, err)
		require.Nil(t, result)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM stream_analytics
		WHERE stream_id = $1
		ORDER BY collected_at DESC
		LIMIT 1`)).
			WithArgs(streamID).
			WillReturnError(errors.New("db error"))

		result, err := repo.GetLatestAnalytics(ctx, streamID)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get latest analytics")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetStreamSummary
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_GetStreamSummary(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		s := sampleStreamStatsSummary()
		rows := sqlmock.NewRows(summaryColumns()).AddRow(summaryRow(s)...)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM stream_stats_summary WHERE stream_id = $1`)).
			WithArgs(s.StreamID).
			WillReturnRows(rows)

		result, err := repo.GetStreamSummary(ctx, s.StreamID)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, s.ID, result.ID)
		assert.Equal(t, s.TotalViewers, result.TotalViewers)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns nil", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM stream_stats_summary WHERE stream_id = $1`)).
			WithArgs(streamID).
			WillReturnError(sql.ErrNoRows)

		result, err := repo.GetStreamSummary(ctx, streamID)
		require.NoError(t, err)
		require.Nil(t, result)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM stream_stats_summary WHERE stream_id = $1`)).
			WithArgs(streamID).
			WillReturnError(errors.New("db error"))

		result, err := repo.GetStreamSummary(ctx, streamID)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get stream summary")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// UpdateStreamSummary
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_UpdateStreamSummary(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()

		mock.ExpectExec(regexp.QuoteMeta(
			`SELECT update_stream_stats_summary($1)`)).
			WithArgs(streamID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateStreamSummary(ctx, streamID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()

		mock.ExpectExec(regexp.QuoteMeta(
			`SELECT update_stream_stats_summary($1)`)).
			WithArgs(streamID).
			WillReturnError(errors.New("function error"))

		err := repo.UpdateStreamSummary(ctx, streamID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update stream summary")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateOrUpdateSummary
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_CreateOrUpdateSummary(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		s := sampleStreamStatsSummary()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO stream_stats_summary`)).
			WithArgs(
				s.ID, s.StreamID,
				s.TotalViewers, s.PeakConcurrentViewers, s.AverageViewers,
				s.TotalWatchTime, s.AverageWatchDuration,
				s.TotalChatMessages, s.TotalUniqueChatters,
				s.TotalLikes, s.TotalShares, sqlmock.AnyArg(), // EngagementRate recalculated
				s.AverageBitrate, s.AverageFramerate, sqlmock.AnyArg(), // QualityScore recalculated
				s.StreamDuration, s.FirstViewerJoinedAt, s.PeakTime,
				s.TopCountries, s.CountriesCount,
				s.TopDevices, s.TopBrowsers,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateOrUpdateSummary(ctx, s)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("nil JSON fields get defaults", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		s := sampleStreamStatsSummary()
		s.TopCountries = nil
		s.TopDevices = nil
		s.TopBrowsers = nil

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO stream_stats_summary`)).
			WithArgs(
				s.ID, s.StreamID,
				s.TotalViewers, s.PeakConcurrentViewers, s.AverageViewers,
				s.TotalWatchTime, s.AverageWatchDuration,
				s.TotalChatMessages, s.TotalUniqueChatters,
				s.TotalLikes, s.TotalShares, sqlmock.AnyArg(),
				s.AverageBitrate, s.AverageFramerate, sqlmock.AnyArg(),
				s.StreamDuration, s.FirstViewerJoinedAt, s.PeakTime,
				json.RawMessage("[]"), s.CountriesCount,
				json.RawMessage("{}"), json.RawMessage("{}"),
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateOrUpdateSummary(ctx, s)
		require.NoError(t, err)
		assert.Equal(t, json.RawMessage("[]"), s.TopCountries)
		assert.Equal(t, json.RawMessage("{}"), s.TopDevices)
		assert.Equal(t, json.RawMessage("{}"), s.TopBrowsers)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		s := sampleStreamStatsSummary()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO stream_stats_summary`)).
			WithArgs(
				s.ID, s.StreamID,
				s.TotalViewers, s.PeakConcurrentViewers, s.AverageViewers,
				s.TotalWatchTime, s.AverageWatchDuration,
				s.TotalChatMessages, s.TotalUniqueChatters,
				s.TotalLikes, s.TotalShares, sqlmock.AnyArg(),
				s.AverageBitrate, s.AverageFramerate, sqlmock.AnyArg(),
				s.StreamDuration, s.FirstViewerJoinedAt, s.PeakTime,
				s.TopCountries, s.CountriesCount,
				s.TopDevices, s.TopBrowsers,
			).
			WillReturnError(errors.New("upsert failed"))

		err := repo.CreateOrUpdateSummary(ctx, s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "upsert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CreateViewerSession
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_CreateViewerSession(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		s := sampleViewerSession()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO viewer_sessions`)).
			WithArgs(
				s.ID, s.StreamID, s.UserID, s.SessionID,
				s.JoinedAt, s.IPAddress, s.CountryCode, s.City,
				s.DeviceType, s.Browser, s.OperatingSystem,
			).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.CreateViewerSession(ctx, s)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		s := sampleViewerSession()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO viewer_sessions`)).
			WithArgs(
				s.ID, s.StreamID, s.UserID, s.SessionID,
				s.JoinedAt, s.IPAddress, s.CountryCode, s.City,
				s.DeviceType, s.Browser, s.OperatingSystem,
			).
			WillReturnError(errors.New("insert failed"))

		err := repo.CreateViewerSession(ctx, s)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "insert failed")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// EndViewerSession
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_EndViewerSession(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		sessionID := "sess-end-123"

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE viewer_sessions
		SET left_at = NOW()
		WHERE session_id = $1 AND left_at IS NULL`)).
			WithArgs(sessionID).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.EndViewerSession(ctx, sessionID)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("session not found or already ended", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		sessionID := "sess-missing"

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE viewer_sessions
		SET left_at = NOW()
		WHERE session_id = $1 AND left_at IS NULL`)).
			WithArgs(sessionID).
			WillReturnResult(sqlmock.NewResult(0, 0))

		err := repo.EndViewerSession(ctx, sessionID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "session not found or already ended")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		sessionID := "sess-err"

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE viewer_sessions
		SET left_at = NOW()
		WHERE session_id = $1 AND left_at IS NULL`)).
			WithArgs(sessionID).
			WillReturnError(errors.New("update failed"))

		err := repo.EndViewerSession(ctx, sessionID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to end viewer session")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("rows affected error", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		sessionID := "sess-rows-err"

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE viewer_sessions
		SET left_at = NOW()
		WHERE session_id = $1 AND left_at IS NULL`)).
			WithArgs(sessionID).
			WillReturnResult(sqlmock.NewErrorResult(errors.New("rows affected error")))

		err := repo.EndViewerSession(ctx, sessionID)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "rows affected error")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetActiveViewers
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_GetActiveViewers(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		s := sampleViewerSession()
		rows := sqlmock.NewRows(viewerSessionColumns()).AddRow(viewerSessionRow(s)...)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM viewer_sessions
		WHERE stream_id = $1 AND left_at IS NULL
		ORDER BY joined_at DESC`)).
			WithArgs(s.StreamID).
			WillReturnRows(rows)

		result, err := repo.GetActiveViewers(ctx, s.StreamID)
		require.NoError(t, err)
		require.Len(t, result, 1)
		assert.Equal(t, s.SessionID, result[0].SessionID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()
		rows := sqlmock.NewRows(viewerSessionColumns())

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM viewer_sessions
		WHERE stream_id = $1 AND left_at IS NULL
		ORDER BY joined_at DESC`)).
			WithArgs(streamID).
			WillReturnRows(rows)

		result, err := repo.GetActiveViewers(ctx, streamID)
		require.NoError(t, err)
		assert.Empty(t, result)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM viewer_sessions
		WHERE stream_id = $1 AND left_at IS NULL
		ORDER BY joined_at DESC`)).
			WithArgs(streamID).
			WillReturnError(errors.New("query failed"))

		result, err := repo.GetActiveViewers(ctx, streamID)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get active viewers")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetViewerSession
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_GetViewerSession(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		s := sampleViewerSession()
		rows := sqlmock.NewRows(viewerSessionColumns()).AddRow(viewerSessionRow(s)...)

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM viewer_sessions WHERE session_id = $1`)).
			WithArgs(s.SessionID).
			WillReturnRows(rows)

		result, err := repo.GetViewerSession(ctx, s.SessionID)
		require.NoError(t, err)
		require.NotNil(t, result)
		assert.Equal(t, s.SessionID, result.SessionID)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found returns nil", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM viewer_sessions WHERE session_id = $1`)).
			WithArgs("missing-sess").
			WillReturnError(sql.ErrNoRows)

		result, err := repo.GetViewerSession(ctx, "missing-sess")
		require.NoError(t, err)
		require.Nil(t, result)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT * FROM viewer_sessions WHERE session_id = $1`)).
			WithArgs("err-sess").
			WillReturnError(errors.New("db error"))

		result, err := repo.GetViewerSession(ctx, "err-sess")
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get viewer session")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// UpdateSessionEngagement
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_UpdateSessionEngagement(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		sessionID := "sess-engage"

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE viewer_sessions
		SET messages_sent = messages_sent + $2,
		    liked = $3,
		    shared = $4,
		    updated_at = NOW()
		WHERE session_id = $1`)).
			WithArgs(sessionID, 5, true, false).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.UpdateSessionEngagement(ctx, sessionID, 5, true, false)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		sessionID := "sess-engage-err"

		mock.ExpectExec(regexp.QuoteMeta(
			`UPDATE viewer_sessions
		SET messages_sent = messages_sent + $2,
		    liked = $3,
		    shared = $4,
		    updated_at = NOW()
		WHERE session_id = $1`)).
			WithArgs(sessionID, 3, false, true).
			WillReturnError(errors.New("update failed"))

		err := repo.UpdateSessionEngagement(ctx, sessionID, 3, false, true)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to update session engagement")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// CleanupOldAnalytics
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_CleanupOldAnalytics(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM stream_analytics WHERE collected_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 10))

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM viewer_sessions WHERE created_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 5))

		err := repo.CleanupOldAnalytics(ctx, 30)
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("analytics delete failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM stream_analytics WHERE collected_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnError(errors.New("delete analytics failed"))

		err := repo.CleanupOldAnalytics(ctx, 30)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to cleanup analytics data")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("sessions delete failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM stream_analytics WHERE collected_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnResult(sqlmock.NewResult(0, 10))

		mock.ExpectExec(regexp.QuoteMeta(
			`DELETE FROM viewer_sessions WHERE created_at < $1`)).
			WithArgs(sqlmock.AnyArg()).
			WillReturnError(errors.New("delete sessions failed"))

		err := repo.CleanupOldAnalytics(ctx, 30)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to cleanup viewer sessions")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetCurrentViewerCount
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_GetCurrentViewerCount(t *testing.T) {
	ctx := context.Background()

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT get_current_viewer_count($1)`)).
			WithArgs(streamID).
			WillReturnRows(sqlmock.NewRows([]string{"get_current_viewer_count"}).AddRow(42))

		count, err := repo.GetCurrentViewerCount(ctx, streamID)
		require.NoError(t, err)
		assert.Equal(t, 42, count)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamID := uuid.New()

		mock.ExpectQuery(regexp.QuoteMeta(
			`SELECT get_current_viewer_count($1)`)).
			WithArgs(streamID).
			WillReturnError(errors.New("function error"))

		count, err := repo.GetCurrentViewerCount(ctx, streamID)
		require.Error(t, err)
		assert.Equal(t, 0, count)
		assert.Contains(t, err.Error(), "failed to get current viewer count")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetActiveViewersForStreams
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_GetActiveViewersForStreams(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns empty map", func(t *testing.T) {
		repo, _, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		result, err := repo.GetActiveViewersForStreams(ctx, []uuid.UUID{})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("success with multiple streams", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		s1 := sampleViewerSession()
		s2 := sampleViewerSession()
		s2.StreamID = uuid.New()
		s2.SessionID = "sess-other-456"

		streamIDs := []uuid.UUID{s1.StreamID, s2.StreamID}

		rows := sqlmock.NewRows(viewerSessionColumns()).
			AddRow(viewerSessionRow(s1)...).
			AddRow(viewerSessionRow(s2)...)

		// sqlx.In rebinds ? to $1, $2 for postgres driver; sqlmock "sqlmock"
		// driver keeps ? placeholders. Match the rebound query loosely.
		mock.ExpectQuery(`SELECT \* FROM viewer_sessions`).
			WithArgs(s1.StreamID, s2.StreamID).
			WillReturnRows(rows)

		result, err := repo.GetActiveViewersForStreams(ctx, streamIDs)
		require.NoError(t, err)
		require.Len(t, result, 2)
		assert.Len(t, result[s1.StreamID], 1)
		assert.Len(t, result[s2.StreamID], 1)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamIDs := []uuid.UUID{uuid.New()}

		mock.ExpectQuery(`SELECT \* FROM viewer_sessions`).
			WithArgs(streamIDs[0]).
			WillReturnError(errors.New("query failed"))

		result, err := repo.GetActiveViewersForStreams(ctx, streamIDs)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get active viewers for streams")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// GetCurrentViewerCounts
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_GetCurrentViewerCounts(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns empty map", func(t *testing.T) {
		repo, _, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		result, err := repo.GetCurrentViewerCounts(ctx, []uuid.UUID{})
		require.NoError(t, err)
		assert.Empty(t, result)
	})

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		id1 := uuid.New()
		id2 := uuid.New()
		streamIDs := []uuid.UUID{id1, id2}

		rows := sqlmock.NewRows([]string{"stream_id", "count"}).
			AddRow(id1, 10).
			AddRow(id2, 25)

		mock.ExpectQuery(`SELECT stream_id, COUNT`).
			WithArgs(id1, id2).
			WillReturnRows(rows)

		result, err := repo.GetCurrentViewerCounts(ctx, streamIDs)
		require.NoError(t, err)
		assert.Equal(t, 10, result[id1])
		assert.Equal(t, 25, result[id2])
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("query failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamIDs := []uuid.UUID{uuid.New()}

		mock.ExpectQuery(`SELECT stream_id, COUNT`).
			WithArgs(streamIDs[0]).
			WillReturnError(errors.New("query failed"))

		result, err := repo.GetCurrentViewerCounts(ctx, streamIDs)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to get viewer counts")
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("scan failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		streamIDs := []uuid.UUID{uuid.New()}

		// Return a column that will cause scan to fail
		rows := sqlmock.NewRows([]string{"stream_id", "count"}).
			AddRow("not-a-uuid", 10)

		mock.ExpectQuery(`SELECT stream_id, COUNT`).
			WithArgs(streamIDs[0]).
			WillReturnRows(rows)

		result, err := repo.GetCurrentViewerCounts(ctx, streamIDs)
		require.Nil(t, result)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to scan row")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// BatchCreateAnalytics
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_BatchCreateAnalytics(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns nil", func(t *testing.T) {
		repo, _, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		err := repo.BatchCreateAnalytics(ctx, []*domain.StreamAnalytics{})
		require.NoError(t, err)
	})

	t.Run("success", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		a := sampleStreamAnalytics()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO stream_analytics`)).
			WillReturnResult(sqlmock.NewResult(0, 1))

		err := repo.BatchCreateAnalytics(ctx, []*domain.StreamAnalytics{a})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		a := sampleStreamAnalytics()

		mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO stream_analytics`)).
			WillReturnError(errors.New("batch insert failed"))

		err := repo.BatchCreateAnalytics(ctx, []*domain.StreamAnalytics{a})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to batch create analytics")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}

// ---------------------------------------------------------------------------
// BatchUpdateStreamSummaries
// ---------------------------------------------------------------------------

func TestAnalyticsRepository_Unit_BatchUpdateStreamSummaries(t *testing.T) {
	ctx := context.Background()

	t.Run("empty input returns nil", func(t *testing.T) {
		repo, _, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		err := repo.BatchUpdateStreamSummaries(ctx, []uuid.UUID{})
		require.NoError(t, err)
	})

	t.Run("success with multiple IDs", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		id1 := uuid.New()
		id2 := uuid.New()

		mock.ExpectExec(regexp.QuoteMeta(`SELECT update_stream_stats_summary(id) FROM unnest($1::uuid[]) AS id`)).
			WithArgs(pq.Array([]uuid.UUID{id1, id2})).
			WillReturnResult(sqlmock.NewResult(0, 2))

		err := repo.BatchUpdateStreamSummaries(ctx, []uuid.UUID{id1, id2})
		require.NoError(t, err)
		require.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("exec failure", func(t *testing.T) {
		repo, mock, cleanup := newAnalyticsRepo(t)
		defer cleanup()

		id1 := uuid.New()

		mock.ExpectExec(regexp.QuoteMeta(`SELECT update_stream_stats_summary(id) FROM unnest($1::uuid[]) AS id`)).
			WithArgs(pq.Array([]uuid.UUID{id1})).
			WillReturnError(errors.New("exec failed"))

		err := repo.BatchUpdateStreamSummaries(ctx, []uuid.UUID{id1})
		require.Error(t, err)
		assert.Contains(t, err.Error(), "failed to batch update stream summaries")
		require.NoError(t, mock.ExpectationsWereMet())
	})
}
