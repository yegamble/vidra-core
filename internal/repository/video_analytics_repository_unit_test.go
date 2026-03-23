package repository_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"regexp"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/repository"

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

func TestVideoAnalyticsRepository_GetEventsByVideoID(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	startDate := time.Now().Add(-24 * time.Hour)
	endDate := time.Now()

	t.Run("success with events", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "event_type", "user_id", "session_id", "timestamp_seconds",
			"watch_duration_seconds", "ip_address", "user_agent", "country_code", "region",
			"city", "device_type", "browser", "os", "referrer", "quality", "player_version", "created_at",
		}).AddRow(
			uuid.New(), videoID, domain.EventTypeView, (*uuid.UUID)(nil), "session1", 0,
			60, "10.0.0.1", "browser", "US", "CA", "SF", "desktop", "chrome", "mac", "ref", "1080p", "1.0", time.Now(),
		)

		mock.ExpectQuery(`SELECT .* FROM video_analytics_events WHERE video_id`).
			WithArgs(videoID, sqlmock.AnyArg(), sqlmock.AnyArg(), 10, 0).
			WillReturnRows(rows)

		events, err := repo.GetEventsByVideoID(ctx, videoID, startDate, endDate, 10, 0)
		assert.NoError(t, err)
		assert.Len(t, events, 1)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(`SELECT .* FROM video_analytics_events WHERE video_id`).
			WillReturnError(assert.AnError)

		events, err := repo.GetEventsByVideoID(ctx, videoID, startDate, endDate, 10, 0)
		assert.Error(t, err)
		assert.Nil(t, events)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoAnalyticsRepository_GetEventsBySessionID(t *testing.T) {
	ctx := context.Background()
	sessionID := "test-session-123"

	t.Run("success with events", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "event_type", "user_id", "session_id", "timestamp_seconds",
			"watch_duration_seconds", "ip_address", "user_agent", "country_code", "region",
			"city", "device_type", "browser", "os", "referrer", "quality", "player_version", "created_at",
		}).AddRow(
			uuid.New(), uuid.New(), domain.EventTypeView, (*uuid.UUID)(nil), sessionID, 0,
			30, "10.0.0.1", "ua", "US", "", "", "mobile", "safari", "ios", "", "720p", "1.0", time.Now(),
		)

		mock.ExpectQuery(`SELECT .* FROM video_analytics_events WHERE session_id`).
			WithArgs(sessionID).
			WillReturnRows(rows)

		events, err := repo.GetEventsBySessionID(ctx, sessionID)
		assert.NoError(t, err)
		assert.Len(t, events, 1)
		assert.Equal(t, sessionID, events[0].SessionID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(`SELECT .* FROM video_analytics_events WHERE session_id`).
			WillReturnError(assert.AnError)

		events, err := repo.GetEventsBySessionID(ctx, sessionID)
		assert.Error(t, err)
		assert.Nil(t, events)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoAnalyticsRepository_GetDailyAnalytics(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	testDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		expectedAnalytics := &domain.DailyAnalytics{
			ID:                       uuid.New(),
			VideoID:                  videoID,
			Date:                     testDate,
			Views:                    1000,
			UniqueViewers:            750,
			WatchTimeSeconds:         15000,
			AvgWatchPercentage:       func() *float64 { v := 75.5; return &v }(),
			CompletionRate:           func() *float64 { v := 65.2; return &v }(),
			Likes:                    50,
			Dislikes:                 5,
			Comments:                 25,
			Shares:                   10,
			Downloads:                3,
			Countries:                json.RawMessage(`{}`),
			Devices:                  json.RawMessage(`{}`),
			Browsers:                 json.RawMessage(`{}`),
			TrafficSources:           json.RawMessage(`{}`),
			Qualities:                json.RawMessage(`{}`),
			PeakConcurrentViewers:    120,
			Errors:                   2,
			BufferingEvents:          8,
			AvgBufferingDurationSecs: func() *float64 { v := 1.5; return &v }(),
			CreatedAt:                time.Now(),
			UpdatedAt:                time.Now(),
		}

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "date", "views", "unique_viewers", "watch_time_seconds",
			"avg_watch_percentage", "completion_rate", "likes", "dislikes", "comments",
			"shares", "downloads", "countries", "devices", "browsers", "traffic_sources",
			"qualities", "peak_concurrent_viewers", "errors", "buffering_events",
			"avg_buffering_duration_seconds", "created_at", "updated_at",
		}).AddRow(
			expectedAnalytics.ID, expectedAnalytics.VideoID, expectedAnalytics.Date,
			expectedAnalytics.Views, expectedAnalytics.UniqueViewers, expectedAnalytics.WatchTimeSeconds,
			expectedAnalytics.AvgWatchPercentage, expectedAnalytics.CompletionRate,
			expectedAnalytics.Likes, expectedAnalytics.Dislikes, expectedAnalytics.Comments,
			expectedAnalytics.Shares, expectedAnalytics.Downloads,
			expectedAnalytics.Countries, expectedAnalytics.Devices, expectedAnalytics.Browsers,
			expectedAnalytics.TrafficSources, expectedAnalytics.Qualities,
			expectedAnalytics.PeakConcurrentViewers, expectedAnalytics.Errors,
			expectedAnalytics.BufferingEvents, expectedAnalytics.AvgBufferingDurationSecs,
			expectedAnalytics.CreatedAt, expectedAnalytics.UpdatedAt,
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(videoID, testDate.Format("2006-01-02")).
			WillReturnRows(rows)

		result, err := repo.GetDailyAnalytics(ctx, videoID, testDate)
		require.NoError(t, err)
		assert.Equal(t, expectedAnalytics.ID, result.ID)
		assert.Equal(t, expectedAnalytics.VideoID, result.VideoID)
		assert.Equal(t, expectedAnalytics.Views, result.Views)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(videoID, testDate.Format("2006-01-02")).
			WillReturnError(sql.ErrNoRows)

		result, err := repo.GetDailyAnalytics(ctx, videoID, testDate)
		assert.Error(t, err)
		assert.Equal(t, domain.ErrAnalyticsDailyNotFound, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(videoID, testDate.Format("2006-01-02")).
			WillReturnError(assert.AnError)

		result, err := repo.GetDailyAnalytics(ctx, videoID, testDate)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoAnalyticsRepository_GetDailyAnalyticsRange(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)

	t.Run("success with multiple records", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "date", "views", "unique_viewers", "watch_time_seconds",
			"avg_watch_percentage", "completion_rate", "likes", "dislikes", "comments",
			"shares", "downloads", "countries", "devices", "browsers", "traffic_sources",
			"qualities", "peak_concurrent_viewers", "errors", "buffering_events",
			"avg_buffering_duration_seconds", "created_at", "updated_at",
		}).
			AddRow(uuid.New(), videoID, startDate, 100, 75, 1500, 70.0, 60.0, 5, 1, 2, 1, 0,
				json.RawMessage(`{}`), json.RawMessage(`{}`), json.RawMessage(`{}`),
				json.RawMessage(`{}`), json.RawMessage(`{}`), 10, 0, 1, 0.5, time.Now(), time.Now()).
			AddRow(uuid.New(), videoID, startDate.AddDate(0, 0, 1), 150, 100, 2000, 75.0, 65.0, 8, 0, 3, 2, 1,
				json.RawMessage(`{}`), json.RawMessage(`{}`), json.RawMessage(`{}`),
				json.RawMessage(`{}`), json.RawMessage(`{}`), 15, 0, 2, 0.7, time.Now(), time.Now())

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(videoID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
			WillReturnRows(rows)

		result, err := repo.GetDailyAnalyticsRange(ctx, videoID, startDate, endDate)
		require.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, videoID, result[0].VideoID)
		assert.Equal(t, 100, result[0].Views)
		assert.Equal(t, 150, result[1].Views)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "date", "views", "unique_viewers", "watch_time_seconds",
			"avg_watch_percentage", "completion_rate", "likes", "dislikes", "comments",
			"shares", "downloads", "countries", "devices", "browsers", "traffic_sources",
			"qualities", "peak_concurrent_viewers", "errors", "buffering_events",
			"avg_buffering_duration_seconds", "created_at", "updated_at",
		})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(videoID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
			WillReturnRows(rows)

		result, err := repo.GetDailyAnalyticsRange(ctx, videoID, startDate, endDate)
		require.NoError(t, err)
		assert.Empty(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(videoID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
			WillReturnError(assert.AnError)

		result, err := repo.GetDailyAnalyticsRange(ctx, videoID, startDate, endDate)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoAnalyticsRepository_GetRetentionData(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	testDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	t.Run("success with retention data", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "timestamp_seconds", "viewer_count", "date", "created_at", "updated_at",
		}).
			AddRow(uuid.New(), videoID, 0, 100, testDate, time.Now(), time.Now()).
			AddRow(uuid.New(), videoID, 30, 85, testDate, time.Now(), time.Now()).
			AddRow(uuid.New(), videoID, 60, 70, testDate, time.Now(), time.Now())

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, timestamp_seconds, viewer_count, date, created_at, updated_at`)).
			WithArgs(videoID, testDate.Format("2006-01-02")).
			WillReturnRows(rows)

		result, err := repo.GetRetentionData(ctx, videoID, testDate)
		require.NoError(t, err)
		assert.Len(t, result, 3)
		assert.Equal(t, 0, result[0].TimestampSeconds)
		assert.Equal(t, 100, result[0].ViewerCount)
		assert.Equal(t, 30, result[1].TimestampSeconds)
		assert.Equal(t, 85, result[1].ViewerCount)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "timestamp_seconds", "viewer_count", "date", "created_at", "updated_at",
		})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, timestamp_seconds, viewer_count, date, created_at, updated_at`)).
			WithArgs(videoID, testDate.Format("2006-01-02")).
			WillReturnRows(rows)

		result, err := repo.GetRetentionData(ctx, videoID, testDate)
		require.NoError(t, err)
		assert.Empty(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, timestamp_seconds, viewer_count, date, created_at, updated_at`)).
			WithArgs(videoID, testDate.Format("2006-01-02")).
			WillReturnError(assert.AnError)

		result, err := repo.GetRetentionData(ctx, videoID, testDate)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoAnalyticsRepository_GetChannelDailyAnalytics(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()
	testDate := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		expectedAnalytics := &domain.ChannelDailyAnalytics{
			ID:                channelID,
			ChannelID:         channelID,
			Date:              testDate,
			Views:             5000,
			UniqueViewers:     3500,
			WatchTimeSeconds:  75000,
			SubscribersGained: 50,
			SubscribersLost:   10,
			TotalSubscribers:  10000,
			Likes:             250,
			Comments:          120,
			Shares:            45,
			VideosPublished:   3,
			CreatedAt:         time.Now(),
			UpdatedAt:         time.Now(),
		}

		rows := sqlmock.NewRows([]string{
			"id", "channel_id", "date", "views", "unique_viewers", "watch_time_seconds",
			"subscribers_gained", "subscribers_lost", "total_subscribers", "likes",
			"comments", "shares", "videos_published", "created_at", "updated_at",
		}).AddRow(
			expectedAnalytics.ID, expectedAnalytics.ChannelID, expectedAnalytics.Date,
			expectedAnalytics.Views, expectedAnalytics.UniqueViewers, expectedAnalytics.WatchTimeSeconds,
			expectedAnalytics.SubscribersGained, expectedAnalytics.SubscribersLost,
			expectedAnalytics.TotalSubscribers, expectedAnalytics.Likes,
			expectedAnalytics.Comments, expectedAnalytics.Shares, expectedAnalytics.VideosPublished,
			expectedAnalytics.CreatedAt, expectedAnalytics.UpdatedAt,
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, channel_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(channelID, testDate.Format("2006-01-02")).
			WillReturnRows(rows)

		result, err := repo.GetChannelDailyAnalytics(ctx, channelID, testDate)
		require.NoError(t, err)
		assert.Equal(t, expectedAnalytics.ChannelID, result.ChannelID)
		assert.Equal(t, expectedAnalytics.Views, result.Views)
		assert.Equal(t, expectedAnalytics.SubscribersGained, result.SubscribersGained)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("not found", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, channel_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(channelID, testDate.Format("2006-01-02")).
			WillReturnError(sql.ErrNoRows)

		result, err := repo.GetChannelDailyAnalytics(ctx, channelID, testDate)
		assert.Error(t, err)
		assert.Equal(t, domain.ErrAnalyticsDailyNotFound, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, channel_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(channelID, testDate.Format("2006-01-02")).
			WillReturnError(assert.AnError)

		result, err := repo.GetChannelDailyAnalytics(ctx, channelID, testDate)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoAnalyticsRepository_GetChannelDailyAnalyticsRange(t *testing.T) {
	ctx := context.Background()
	channelID := uuid.New()
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)

	t.Run("success with multiple records", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		rows := sqlmock.NewRows([]string{
			"id", "channel_id", "date", "views", "unique_viewers", "watch_time_seconds",
			"subscribers_gained", "subscribers_lost", "total_subscribers", "likes",
			"comments", "shares", "videos_published", "created_at", "updated_at",
		}).
			AddRow(uuid.New(), channelID, startDate, 1000, 700, 15000, 10, 2, 5000, 50, 20, 10, 1, time.Now(), time.Now()).
			AddRow(uuid.New(), channelID, startDate.AddDate(0, 0, 1), 1200, 850, 18000, 15, 3, 5012, 60, 25, 12, 2, time.Now(), time.Now())

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, channel_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(channelID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
			WillReturnRows(rows)

		result, err := repo.GetChannelDailyAnalyticsRange(ctx, channelID, startDate, endDate)
		require.NoError(t, err)
		assert.Len(t, result, 2)
		assert.Equal(t, channelID, result[0].ChannelID)
		assert.Equal(t, 1000, result[0].Views)
		assert.Equal(t, 1200, result[1].Views)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		rows := sqlmock.NewRows([]string{
			"id", "channel_id", "date", "views", "unique_viewers", "watch_time_seconds",
			"subscribers_gained", "subscribers_lost", "total_subscribers", "likes",
			"comments", "shares", "videos_published", "created_at", "updated_at",
		})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, channel_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(channelID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
			WillReturnRows(rows)

		result, err := repo.GetChannelDailyAnalyticsRange(ctx, channelID, startDate, endDate)
		require.NoError(t, err)
		assert.Empty(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, channel_id, date, views, unique_viewers, watch_time_seconds`)).
			WithArgs(channelID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
			WillReturnError(assert.AnError)

		result, err := repo.GetChannelDailyAnalyticsRange(ctx, channelID, startDate, endDate)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoAnalyticsRepository_GetActiveViewersForVideo(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()

	t.Run("success with active viewers", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		userID1 := uuid.New()
		userID2 := uuid.New()

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "session_id", "user_id", "last_heartbeat", "created_at",
		}).
			AddRow(uuid.New(), videoID, "session-1", &userID1, time.Now(), time.Now()).
			AddRow(uuid.New(), videoID, "session-2", &userID2, time.Now(), time.Now()).
			AddRow(uuid.New(), videoID, "session-3", (*uuid.UUID)(nil), time.Now(), time.Now())

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, session_id, user_id, last_heartbeat, created_at`)).
			WithArgs(videoID).
			WillReturnRows(rows)

		result, err := repo.GetActiveViewersForVideo(ctx, videoID)
		require.NoError(t, err)
		assert.Len(t, result, 3)
		assert.Equal(t, videoID, result[0].VideoID)
		assert.Equal(t, "session-1", result[0].SessionID)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("empty result", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		rows := sqlmock.NewRows([]string{
			"id", "video_id", "session_id", "user_id", "last_heartbeat", "created_at",
		})

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, session_id, user_id, last_heartbeat, created_at`)).
			WithArgs(videoID).
			WillReturnRows(rows)

		result, err := repo.GetActiveViewersForVideo(ctx, videoID)
		require.NoError(t, err)
		assert.Empty(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT id, video_id, session_id, user_id, last_heartbeat, created_at`)).
			WithArgs(videoID).
			WillReturnError(assert.AnError)

		result, err := repo.GetActiveViewersForVideo(ctx, videoID)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}

func TestVideoAnalyticsRepository_GetVideoAnalyticsSummary(t *testing.T) {
	ctx := context.Background()
	videoID := uuid.New()
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	endDate := time.Date(2024, 1, 7, 0, 0, 0, 0, time.UTC)

	t.Run("success", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		summaryRows := sqlmock.NewRows([]string{
			"video_id", "total_views", "total_unique_viewers", "total_watch_time_seconds",
			"avg_watch_percentage", "avg_completion_rate", "total_likes", "total_dislikes",
			"total_comments", "total_shares", "peak_viewers",
		}).AddRow(
			videoID, 10000, 7500, 150000, 72.5, 63.2, 500, 50, 250, 100, 150,
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(videoID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
			WillReturnRows(summaryRows)

		countRows := sqlmock.NewRows([]string{"count"}).AddRow(25)
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM video_active_viewers`)).
			WithArgs(videoID).
			WillReturnRows(countRows)

		result, err := repo.GetVideoAnalyticsSummary(ctx, videoID, startDate, endDate)
		require.NoError(t, err)
		assert.Equal(t, videoID, result.VideoID)
		assert.Equal(t, 10000, result.TotalViews)
		assert.Equal(t, 7500, result.TotalUniqueViewers)
		assert.Equal(t, int64(150000), result.TotalWatchTimeSeconds)
		assert.Equal(t, 25, result.CurrentViewers)
		assert.NotNil(t, result.TopCountries)
		assert.NotNil(t, result.DeviceBreakdown)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("database error on summary", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(videoID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
			WillReturnError(assert.AnError)

		result, err := repo.GetVideoAnalyticsSummary(ctx, videoID, startDate, endDate)
		assert.Error(t, err)
		assert.Nil(t, result)
		assert.NoError(t, mock.ExpectationsWereMet())
	})

	t.Run("success with zero values", func(t *testing.T) {
		_, mock, repo := setupMockDB(t)

		summaryRows := sqlmock.NewRows([]string{
			"video_id", "total_views", "total_unique_viewers", "total_watch_time_seconds",
			"avg_watch_percentage", "avg_completion_rate", "total_likes", "total_dislikes",
			"total_comments", "total_shares", "peak_viewers",
		}).AddRow(
			videoID, 0, 0, 0, 0.0, 0.0, 0, 0, 0, 0, 0,
		)

		mock.ExpectQuery(regexp.QuoteMeta(`SELECT`)).
			WithArgs(videoID, startDate.Format("2006-01-02"), endDate.Format("2006-01-02")).
			WillReturnRows(summaryRows)

		countRows := sqlmock.NewRows([]string{"count"}).AddRow(0)
		mock.ExpectQuery(regexp.QuoteMeta(`SELECT COUNT(*) FROM video_active_viewers`)).
			WithArgs(videoID).
			WillReturnRows(countRows)

		result, err := repo.GetVideoAnalyticsSummary(ctx, videoID, startDate, endDate)
		require.NoError(t, err)
		assert.Equal(t, videoID, result.VideoID)
		assert.Equal(t, 0, result.TotalViews)
		assert.Equal(t, 0, result.CurrentViewers)
		assert.NoError(t, mock.ExpectationsWereMet())
	})
}
