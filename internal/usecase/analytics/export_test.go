package analytics

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// --- Mock implementations ---

type mockVideoAnalyticsRepo struct {
	dailyAnalytics    []*domain.DailyAnalytics
	summary           *domain.AnalyticsSummary
	retentionData     []*domain.RetentionData
	channelDaily      []*domain.ChannelDailyAnalytics
	channelTotalViews int
	totalViews        int
	err               error
}

func (m *mockVideoAnalyticsRepo) CreateEvent(_ context.Context, _ *domain.AnalyticsEvent) error {
	return nil
}
func (m *mockVideoAnalyticsRepo) CreateEventsBatch(_ context.Context, _ []*domain.AnalyticsEvent) error {
	return nil
}
func (m *mockVideoAnalyticsRepo) GetEventsByVideoID(_ context.Context, _ port.EventQueryFilter) ([]*domain.AnalyticsEvent, error) {
	return nil, nil
}
func (m *mockVideoAnalyticsRepo) GetEventsBySessionID(_ context.Context, _ string) ([]*domain.AnalyticsEvent, error) {
	return nil, nil
}
func (m *mockVideoAnalyticsRepo) DeleteOldEvents(_ context.Context, _ int) (int64, error) {
	return 0, nil
}
func (m *mockVideoAnalyticsRepo) UpsertActiveViewer(_ context.Context, _ *domain.ActiveViewer) error {
	return nil
}
func (m *mockVideoAnalyticsRepo) UpsertActiveViewersBatch(_ context.Context, _ []*domain.ActiveViewer) error {
	return nil
}
func (m *mockVideoAnalyticsRepo) GetActiveViewerCount(_ context.Context, _ uuid.UUID) (int, error) {
	return 0, nil
}
func (m *mockVideoAnalyticsRepo) GetActiveViewersForVideo(_ context.Context, _ uuid.UUID) ([]*domain.ActiveViewer, error) {
	return nil, nil
}
func (m *mockVideoAnalyticsRepo) CleanupInactiveViewers(_ context.Context) (int64, error) {
	return 0, nil
}
func (m *mockVideoAnalyticsRepo) AggregateDailyAnalytics(_ context.Context, _ uuid.UUID, _ time.Time) error {
	return nil
}
func (m *mockVideoAnalyticsRepo) GetDailyAnalytics(_ context.Context, _ uuid.UUID, _ time.Time) (*domain.DailyAnalytics, error) {
	return nil, nil
}
func (m *mockVideoAnalyticsRepo) GetDailyAnalyticsRange(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]*domain.DailyAnalytics, error) {
	return m.dailyAnalytics, m.err
}
func (m *mockVideoAnalyticsRepo) CalculateRetentionCurve(_ context.Context, _ uuid.UUID, _ time.Time) error {
	return nil
}
func (m *mockVideoAnalyticsRepo) GetRetentionData(_ context.Context, _ uuid.UUID, _ time.Time) ([]*domain.RetentionData, error) {
	return m.retentionData, m.err
}
func (m *mockVideoAnalyticsRepo) GetVideoAnalyticsSummary(_ context.Context, _ uuid.UUID, _, _ time.Time) (*domain.AnalyticsSummary, error) {
	return m.summary, m.err
}
func (m *mockVideoAnalyticsRepo) GetTotalViewsForVideo(_ context.Context, _ uuid.UUID) (int, error) {
	return m.totalViews, m.err
}
func (m *mockVideoAnalyticsRepo) GetChannelDailyAnalytics(_ context.Context, _ uuid.UUID, _ time.Time) (*domain.ChannelDailyAnalytics, error) {
	return nil, nil
}
func (m *mockVideoAnalyticsRepo) GetChannelDailyAnalyticsRange(_ context.Context, _ uuid.UUID, _, _ time.Time) ([]*domain.ChannelDailyAnalytics, error) {
	return m.channelDaily, m.err
}
func (m *mockVideoAnalyticsRepo) GetTotalViewsForChannel(_ context.Context, _ uuid.UUID) (int, error) {
	return m.channelTotalViews, m.err
}

type mockVideoRepo struct {
	video *domain.Video
	err   error
}

func (m *mockVideoRepo) Create(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockVideoRepo) GetByID(_ context.Context, _ string) (*domain.Video, error) {
	return m.video, m.err
}
func (m *mockVideoRepo) GetByIDs(_ context.Context, _ []string) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) GetByUserID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) GetByChannelID(_ context.Context, _ string, _, _ int) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) Update(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockVideoRepo) Delete(_ context.Context, _, _ string) error     { return nil }
func (m *mockVideoRepo) List(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) Search(_ context.Context, _ *domain.VideoSearchRequest) ([]*domain.Video, int64, error) {
	return nil, 0, nil
}
func (m *mockVideoRepo) UpdateProcessingInfo(_ context.Context, _ port.VideoProcessingParams) error {
	return nil
}
func (m *mockVideoRepo) UpdateProcessingInfoWithCIDs(_ context.Context, _ port.VideoProcessingWithCIDsParams) error {
	return nil
}
func (m *mockVideoRepo) Count(_ context.Context) (int64, error) { return 0, nil }
func (m *mockVideoRepo) GetVideosForMigration(_ context.Context, _ int) ([]*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) GetByRemoteURI(_ context.Context, _ string) (*domain.Video, error) {
	return nil, nil
}
func (m *mockVideoRepo) CreateRemoteVideo(_ context.Context, _ *domain.Video) error { return nil }
func (m *mockVideoRepo) GetVideoQuotaUsed(_ context.Context, _ string) (int64, error) {
	return 0, nil
}

func (m *mockVideoRepo) AppendOutputPath(_ context.Context, _ string, _ string, _ string) error {
	return nil
}

type mockChannelRepo struct {
	isOwner  bool
	channels []domain.Channel
	err      error
}

func (m *mockChannelRepo) Create(_ context.Context, _ *domain.Channel) error { return nil }
func (m *mockChannelRepo) GetByID(_ context.Context, _ uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}
func (m *mockChannelRepo) GetByHandle(_ context.Context, _ string) (*domain.Channel, error) {
	return nil, nil
}
func (m *mockChannelRepo) List(_ context.Context, _ domain.ChannelListParams) (*domain.ChannelListResponse, error) {
	return nil, nil
}
func (m *mockChannelRepo) Update(_ context.Context, _ uuid.UUID, _ domain.ChannelUpdateRequest) (*domain.Channel, error) {
	return nil, nil
}
func (m *mockChannelRepo) Delete(_ context.Context, _ uuid.UUID) error { return nil }
func (m *mockChannelRepo) GetChannelsByAccountID(_ context.Context, _ uuid.UUID) ([]domain.Channel, error) {
	return m.channels, m.err
}
func (m *mockChannelRepo) GetDefaultChannelForAccount(_ context.Context, _ uuid.UUID) (*domain.Channel, error) {
	return nil, nil
}
func (m *mockChannelRepo) CheckOwnership(_ context.Context, _, _ uuid.UUID) (bool, error) {
	return m.isOwner, m.err
}

// --- Tests ---

func TestExportService_ValidateVideoOwnership(t *testing.T) {
	userID := uuid.New()

	tests := []struct {
		name    string
		video   *domain.Video
		err     error
		wantErr bool
	}{
		{
			name:    "owner can access",
			video:   &domain.Video{ID: uuid.New().String(), UserID: userID.String()},
			wantErr: false,
		},
		{
			name:    "non-owner forbidden",
			video:   &domain.Video{ID: uuid.New().String(), UserID: uuid.New().String()},
			wantErr: true,
		},
		{
			name:    "video not found",
			err:     domain.ErrNotFound,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewExportService(
				&mockVideoAnalyticsRepo{},
				&mockVideoRepo{video: tt.video, err: tt.err},
				&mockChannelRepo{},
			)
			err := svc.ValidateVideoOwnership(context.Background(), uuid.New(), userID)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestExportService_ValidateChannelOwnership(t *testing.T) {
	tests := []struct {
		name    string
		isOwner bool
		err     error
		wantErr bool
	}{
		{
			name:    "owner can access",
			isOwner: true,
			wantErr: false,
		},
		{
			name:    "non-owner forbidden",
			isOwner: false,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			svc := NewExportService(
				&mockVideoAnalyticsRepo{},
				&mockVideoRepo{},
				&mockChannelRepo{isOwner: tt.isOwner, err: tt.err},
			)
			err := svc.ValidateChannelOwnership(context.Background(), uuid.New(), uuid.New())
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestExportService_GenerateVideoCSV(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	dailyData := []*domain.DailyAnalytics{
		{
			VideoID:          videoID,
			Date:             now.AddDate(0, 0, -1),
			Views:            100,
			UniqueViewers:    80,
			WatchTimeSeconds: 5000,
			Likes:            10,
			Comments:         5,
			Shares:           2,
		},
		{
			VideoID:          videoID,
			Date:             now,
			Views:            150,
			UniqueViewers:    120,
			WatchTimeSeconds: 7500,
			Likes:            15,
			Comments:         8,
			Shares:           3,
		},
	}

	svc := NewExportService(
		&mockVideoAnalyticsRepo{dailyAnalytics: dailyData},
		&mockVideoRepo{video: &domain.Video{ID: videoID.String(), UserID: userID.String()}},
		&mockChannelRepo{},
	)

	params := ExportParams{
		VideoID:   &videoID,
		UserID:    userID,
		StartDate: now.AddDate(0, 0, -7),
		EndDate:   now,
	}

	csvBytes, err := svc.GenerateCSV(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, csvBytes)

	reader := csv.NewReader(strings.NewReader(string(csvBytes)))
	records, err := reader.ReadAll()
	require.NoError(t, err)

	// Header + 2 data rows
	assert.Len(t, records, 3)
	assert.Equal(t, []string{"date", "views", "unique_viewers", "watch_time_seconds", "likes", "comments", "shares"}, records[0])
	assert.Equal(t, "100", records[1][1])
	assert.Equal(t, "150", records[2][1])
}

func TestExportService_GenerateChannelCSV(t *testing.T) {
	channelID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	channelDaily := []*domain.ChannelDailyAnalytics{
		{
			ChannelID:        channelID,
			Date:             now.AddDate(0, 0, -1),
			Views:            200,
			UniqueViewers:    160,
			WatchTimeSeconds: 10000,
			Likes:            20,
			Comments:         10,
			Shares:           5,
		},
	}

	svc := NewExportService(
		&mockVideoAnalyticsRepo{channelDaily: channelDaily},
		&mockVideoRepo{},
		&mockChannelRepo{isOwner: true},
	)

	params := ExportParams{
		ChannelID: &channelID,
		UserID:    userID,
		StartDate: now.AddDate(0, 0, -7),
		EndDate:   now,
	}

	csvBytes, err := svc.GenerateCSV(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, csvBytes)

	reader := csv.NewReader(strings.NewReader(string(csvBytes)))
	records, err := reader.ReadAll()
	require.NoError(t, err)

	assert.Len(t, records, 2) // header + 1 data row
	assert.Equal(t, "200", records[1][1])
}

func TestExportService_GenerateAllChannelsCSV(t *testing.T) {
	userID := uuid.New()
	channelID := uuid.New()
	now := time.Now()

	channelDaily := []*domain.ChannelDailyAnalytics{
		{
			ChannelID:        channelID,
			Date:             now,
			Views:            300,
			UniqueViewers:    250,
			WatchTimeSeconds: 15000,
			Likes:            30,
			Comments:         15,
			Shares:           8,
		},
	}

	svc := NewExportService(
		&mockVideoAnalyticsRepo{channelDaily: channelDaily},
		&mockVideoRepo{},
		&mockChannelRepo{
			isOwner:  true,
			channels: []domain.Channel{{ID: channelID, Name: "TestChannel"}},
		},
	)

	params := ExportParams{
		UserID:    userID,
		StartDate: now.AddDate(0, 0, -7),
		EndDate:   now,
	}

	csvBytes, err := svc.GenerateCSV(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, csvBytes)

	reader := csv.NewReader(strings.NewReader(string(csvBytes)))
	records, err := reader.ReadAll()
	require.NoError(t, err)
	assert.Len(t, records, 2) // header + 1 aggregated row
}

func TestExportService_GenerateVideoJSON(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	summary := &domain.AnalyticsSummary{
		VideoID:    videoID,
		TotalViews: 500,
		TotalLikes: 50,
	}

	dailyData := []*domain.DailyAnalytics{
		{VideoID: videoID, Date: now, Views: 100},
	}

	svc := NewExportService(
		&mockVideoAnalyticsRepo{summary: summary, dailyAnalytics: dailyData},
		&mockVideoRepo{video: &domain.Video{ID: videoID.String(), UserID: userID.String()}},
		&mockChannelRepo{},
	)

	params := ExportParams{
		VideoID:   &videoID,
		UserID:    userID,
		StartDate: now.AddDate(0, 0, -7),
		EndDate:   now,
	}

	jsonBytes, err := svc.GenerateJSON(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, jsonBytes)

	var result map[string]interface{}
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)
	assert.Contains(t, result, "summary")
	assert.Contains(t, result, "daily")
}

func TestExportService_GenerateAllChannelsJSON(t *testing.T) {
	userID := uuid.New()
	channelID := uuid.New()
	now := time.Now()

	channelDaily := []*domain.ChannelDailyAnalytics{
		{ChannelID: channelID, Date: now, Views: 300},
	}

	svc := NewExportService(
		&mockVideoAnalyticsRepo{channelDaily: channelDaily, channelTotalViews: 500},
		&mockVideoRepo{},
		&mockChannelRepo{
			isOwner:  true,
			channels: []domain.Channel{{ID: channelID, Name: "TestChannel"}},
		},
	)

	params := ExportParams{
		UserID:    userID,
		StartDate: now.AddDate(0, 0, -7),
		EndDate:   now,
	}

	jsonBytes, err := svc.GenerateJSON(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, jsonBytes)

	var result map[string]interface{}
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)
	assert.Contains(t, result, "channels")
	assert.Contains(t, result, "start_date")
	assert.Contains(t, result, "end_date")
}

func TestExportService_GenerateChannelJSON(t *testing.T) {
	channelID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	channelDaily := []*domain.ChannelDailyAnalytics{
		{ChannelID: channelID, Date: now, Views: 200},
	}

	svc := NewExportService(
		&mockVideoAnalyticsRepo{channelDaily: channelDaily, channelTotalViews: 1000},
		&mockVideoRepo{},
		&mockChannelRepo{isOwner: true},
	)

	params := ExportParams{
		ChannelID: &channelID,
		UserID:    userID,
		StartDate: now.AddDate(0, 0, -7),
		EndDate:   now,
	}

	jsonBytes, err := svc.GenerateJSON(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, jsonBytes)

	var result map[string]interface{}
	err = json.Unmarshal(jsonBytes, &result)
	require.NoError(t, err)
	assert.Contains(t, result, "total_views")
	assert.Contains(t, result, "daily")
}
