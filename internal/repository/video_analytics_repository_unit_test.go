package repository_test

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"athena/internal/domain"
	"athena/internal/repository"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/google/uuid"
	"github.com/jmoiron/sqlx"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupMockDB(t *testing.T) (*sqlx.DB, sqlmock.Sqlmock, *repository.VideoAnalyticsRepository) {
	mockDB, mock, err := sqlmock.New()
	if err != nil {
		t.Fatalf("an error '%s' was not expected when opening a stub database connection", err)
	}
	t.Cleanup(func() {
		mockDB.Close()
	})

	sqlxDB := sqlx.NewDb(mockDB, "sqlmock")
	repo := repository.NewVideoAnalyticsRepository(sqlxDB)
	return sqlxDB, mock, repo
}

func TestUpsertActiveViewersBatch_QueryCount(t *testing.T) {
	_, mock, repo := setupMockDB(t)

	viewers := make([]*domain.ActiveViewer, 5)
	for i := 0; i < 5; i++ {
		viewers[i] = &domain.ActiveViewer{
			ID:            uuid.New(),
			VideoID:       uuid.New(),
			SessionID:     uuid.New().String(),
			UserID:        nil,
			LastHeartbeat: time.Now(),
			CreatedAt:     time.Now(),
		}
	}

	mock.ExpectExec("INSERT INTO video_active_viewers").
		WithArgs(sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(),
			sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg(), sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 5))

	err := repo.UpsertActiveViewersBatch(context.Background(), viewers)
	assert.NoError(t, err)

	err = mock.ExpectationsWereMet()
	assert.NoError(t, err)
}

func TestVideoAnalyticsRepo_Unit_CreateEvent(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_analytics_events`)).
					WithArgs(
						sqlmock.AnyArg(), videoID, domain.EventTypeView, (*uuid.UUID)(nil),
						"session-1", (*int)(nil), (*int)(nil), (*string)(nil),
						"Mozilla/5.0", "US", "CA", "LA",
						domain.VideoDeviceTypeDesktop, "Chrome", "Linux", "", "720p", "1.0",
						sqlmock.AnyArg(),
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_analytics_events`)).
					WithArgs(
						sqlmock.AnyArg(), videoID, domain.EventTypeView, (*uuid.UUID)(nil),
						"session-1", (*int)(nil), (*int)(nil), (*string)(nil),
						"Mozilla/5.0", "US", "CA", "LA",
						domain.VideoDeviceTypeDesktop, "Chrome", "Linux", "", "720p", "1.0",
						sqlmock.AnyArg(),
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			event := &domain.AnalyticsEvent{
				VideoID:       videoID,
				EventType:     domain.EventTypeView,
				SessionID:     "session-1",
				UserAgent:     "Mozilla/5.0",
				CountryCode:   "US",
				Region:        "CA",
				City:          "LA",
				DeviceType:    domain.VideoDeviceTypeDesktop,
				Browser:       "Chrome",
				OS:            "Linux",
				Quality:       "720p",
				PlayerVersion: "1.0",
			}
			err := repo.CreateEvent(ctx, event)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
				assert.NotEqual(t, uuid.Nil, event.ID)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_DeleteOldEvents(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name        string
		setup       func(sqlmock.Sqlmock)
		wantDeleted int64
		wantErr     bool
	}{
		{
			name: "success - deleted some events",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_analytics_events WHERE created_at < $1`)).
					WithArgs(sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(0, 42))
			},
			wantDeleted: 42,
		},
		{
			name: "success - nothing to delete",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_analytics_events WHERE created_at < $1`)).
					WithArgs(sqlmock.AnyArg()).
					WillReturnResult(sqlmock.NewResult(0, 0))
			},
			wantDeleted: 0,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_analytics_events WHERE created_at < $1`)).
					WithArgs(sqlmock.AnyArg()).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			deleted, err := repo.DeleteOldEvents(ctx, 30)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantDeleted, deleted)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_UpsertDailyAnalytics(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	date := time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_analytics_daily`)).
					WithArgs(
						sqlmock.AnyArg(), videoID, date, 100, 80, int64(3600),
						(*float64)(nil), (*float64)(nil), 10, 2, 5, 3, 1,
						json.RawMessage("{}"), json.RawMessage("{}"), json.RawMessage("{}"),
						json.RawMessage("{}"), json.RawMessage("{}"),
						15, 0, 2, (*float64)(nil), sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_analytics_daily`)).
					WithArgs(
						sqlmock.AnyArg(), videoID, date, 100, 80, int64(3600),
						(*float64)(nil), (*float64)(nil), 10, 2, 5, 3, 1,
						json.RawMessage("{}"), json.RawMessage("{}"), json.RawMessage("{}"),
						json.RawMessage("{}"), json.RawMessage("{}"),
						15, 0, 2, (*float64)(nil), sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			analytics := &domain.DailyAnalytics{
				VideoID:               videoID,
				Date:                  date,
				Views:                 100,
				UniqueViewers:         80,
				WatchTimeSeconds:      3600,
				Likes:                 10,
				Dislikes:              2,
				Comments:              5,
				Shares:                3,
				Downloads:             1,
				PeakConcurrentViewers: 15,
				BufferingEvents:       2,
			}
			err := repo.UpsertDailyAnalytics(ctx, analytics)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_AggregateDailyAnalytics(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	date := time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`WITH event_stats AS`)).
					WithArgs(videoID, "2026-02-14").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`WITH event_stats AS`)).
					WithArgs(videoID, "2026-02-14").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			err := repo.AggregateDailyAnalytics(ctx, videoID, date)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_UpsertRetentionData(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	date := time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_analytics_retention`)).
					WithArgs(
						sqlmock.AnyArg(), videoID, 30, 150, date,
						sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_analytics_retention`)).
					WithArgs(
						sqlmock.AnyArg(), videoID, 30, 150, date,
						sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			retention := &domain.RetentionData{
				VideoID:          videoID,
				TimestampSeconds: 30,
				ViewerCount:      150,
				Date:             date,
			}
			err := repo.UpsertRetentionData(ctx, retention)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_CalculateRetentionCurve(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	date := time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`WITH retention_points AS`)).
					WithArgs(videoID, "2026-02-14").
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`WITH retention_points AS`)).
					WithArgs(videoID, "2026-02-14").
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			err := repo.CalculateRetentionCurve(ctx, videoID, date)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_UpsertChannelDailyAnalytics(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()
	date := time.Date(2026, 2, 14, 0, 0, 0, 0, time.UTC)

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO channel_analytics_daily`)).
					WithArgs(
						sqlmock.AnyArg(), channelID, date, 500, 400, int64(18000),
						50, 5, 1000, 30, 10, 8, 2,
						sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO channel_analytics_daily`)).
					WithArgs(
						sqlmock.AnyArg(), channelID, date, 500, 400, int64(18000),
						50, 5, 1000, 30, 10, 8, 2,
						sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			analytics := &domain.ChannelDailyAnalytics{
				ChannelID:         channelID,
				Date:              date,
				Views:             500,
				UniqueViewers:     400,
				WatchTimeSeconds:  18000,
				SubscribersGained: 50,
				SubscribersLost:   5,
				TotalSubscribers:  1000,
				Likes:             30,
				Comments:          10,
				Shares:            8,
				VideosPublished:   2,
			}
			err := repo.UpsertChannelDailyAnalytics(ctx, analytics)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_UpsertActiveViewer(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()

	tests := []struct {
		name    string
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_active_viewers`)).
					WithArgs(
						sqlmock.AnyArg(), videoID, "session-abc", (*uuid.UUID)(nil),
						sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_active_viewers`)).
					WithArgs(
						sqlmock.AnyArg(), videoID, "session-abc", (*uuid.UUID)(nil),
						sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			viewer := &domain.ActiveViewer{
				VideoID:   videoID,
				SessionID: "session-abc",
			}
			err := repo.UpsertActiveViewer(ctx, viewer)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_UpsertActiveViewersBatch(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()

	tests := []struct {
		name    string
		viewers []*domain.ActiveViewer
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name:    "empty list - noop",
			viewers: nil,
			setup:   func(mock sqlmock.Sqlmock) {},
		},
		{
			name: "single viewer",
			viewers: []*domain.ActiveViewer{
				{VideoID: videoID, SessionID: "s1"},
			},
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO video_active_viewers").
					WithArgs(
						sqlmock.AnyArg(), videoID, "s1", (*uuid.UUID)(nil),
						sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
			},
		},
		{
			name: "database error",
			viewers: []*domain.ActiveViewer{
				{VideoID: videoID, SessionID: "s1"},
			},
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec("INSERT INTO video_active_viewers").
					WithArgs(
						sqlmock.AnyArg(), videoID, "s1", (*uuid.UUID)(nil),
						sqlmock.AnyArg(), sqlmock.AnyArg(),
					).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			err := repo.UpsertActiveViewersBatch(ctx, tt.viewers)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_GetActiveViewerCount(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()

	tests := []struct {
		name      string
		setup     func(sqlmock.Sqlmock)
		wantCount int
		wantErr   bool
	}{
		{
			name: "success - active viewers",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*)`)).
					WithArgs(videoID).
					WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(25))
			},
			wantCount: 25,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*)`)).
					WithArgs(videoID).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			count, err := repo.GetActiveViewerCount(ctx, videoID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantCount, count)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_CleanupInactiveViewers(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		setup       func(sqlmock.Sqlmock)
		wantDeleted int64
		wantErr     bool
	}{
		{
			name: "success - cleaned up some",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_active_viewers`)).
					WillReturnResult(sqlmock.NewResult(0, 15))
			},
			wantDeleted: 15,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectExec(regexp.QuoteMeta(`DELETE FROM video_active_viewers`)).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			deleted, err := repo.CleanupInactiveViewers(ctx)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantDeleted, deleted)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_GetTotalViewsForVideo(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()

	tests := []struct {
		name      string
		setup     func(sqlmock.Sqlmock)
		wantViews int
		wantErr   bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(SUM(views), 0)`)).
					WithArgs(videoID).
					WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(1500))
			},
			wantViews: 1500,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(SUM(views), 0)`)).
					WithArgs(videoID).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			views, err := repo.GetTotalViewsForVideo(ctx, videoID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantViews, views)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_GetTotalViewsForChannel(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()

	tests := []struct {
		name      string
		setup     func(sqlmock.Sqlmock)
		wantViews int
		wantErr   bool
	}{
		{
			name: "success",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(SUM(views), 0)`)).
					WithArgs(channelID).
					WillReturnRows(sqlmock.NewRows([]string{"coalesce"}).AddRow(5000))
			},
			wantViews: 5000,
		},
		{
			name: "database error",
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectQuery(regexp.QuoteMeta(`SELECT COALESCE(SUM(views), 0)`)).
					WithArgs(channelID).
					WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			views, err := repo.GetTotalViewsForChannel(ctx, channelID)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, tt.wantViews, views)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestVideoAnalyticsRepo_Unit_CreateEventsBatch(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()

	tests := []struct {
		name    string
		events  []*domain.AnalyticsEvent
		setup   func(sqlmock.Sqlmock)
		wantErr bool
	}{
		{
			name:   "empty list - noop",
			events: nil,
			setup:  func(mock sqlmock.Sqlmock) {},
		},
		{
			name: "single event in batch",
			events: []*domain.AnalyticsEvent{
				{
					VideoID: videoID, EventType: domain.EventTypeView,
					SessionID: "s1", DeviceType: domain.VideoDeviceTypeDesktop,
				},
			},
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin()
				mock.ExpectPrepare(regexp.QuoteMeta(`INSERT INTO video_analytics_events`))
				mock.ExpectExec(regexp.QuoteMeta(`INSERT INTO video_analytics_events`)).
					WithArgs(
						sqlmock.AnyArg(), videoID, domain.EventTypeView, (*uuid.UUID)(nil),
						"s1", (*int)(nil), (*int)(nil), (*string)(nil),
						"", "", "", "",
						domain.VideoDeviceTypeDesktop, "", "", "", "", "",
						sqlmock.AnyArg(),
					).
					WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			},
		},
		{
			name: "begin tx error",
			events: []*domain.AnalyticsEvent{
				{VideoID: videoID, EventType: domain.EventTypeView, SessionID: "s1"},
			},
			setup: func(mock sqlmock.Sqlmock) {
				mock.ExpectBegin().WillReturnError(assert.AnError)
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, mock, repo := setupMockDB(t)
			tt.setup(mock)

			err := repo.CreateEventsBatch(ctx, tt.events)
			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
			assert.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
