package analytics

import (
	"context"
	"testing"
	"time"

	"vidra-core/internal/domain"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestPDFExport_VideoAnalytics(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	summary := &domain.AnalyticsSummary{
		VideoID:               videoID,
		TotalViews:            500,
		TotalUniqueViewers:    400,
		TotalWatchTimeSeconds: 25000,
		TotalLikes:            50,
		TotalComments:         20,
		TotalShares:           10,
		TopCountries: []domain.CountryStat{
			{Country: "US", Views: 200},
			{Country: "GB", Views: 100},
		},
		DeviceBreakdown: []domain.DeviceStat{
			{Device: "desktop", Views: 300},
			{Device: "mobile", Views: 200},
		},
		TrafficSources: []domain.TrafficSource{
			{Source: "direct", Views: 250},
			{Source: "search", Views: 150},
		},
	}

	retentionData := []*domain.RetentionData{
		{TimestampSeconds: 0, ViewerCount: 500},
		{TimestampSeconds: 30, ViewerCount: 400},
		{TimestampSeconds: 60, ViewerCount: 300},
	}

	dailyData := []*domain.DailyAnalytics{
		{VideoID: videoID, Date: now.AddDate(0, 0, -1), Views: 100},
	}

	svc := NewExportService(
		&mockVideoAnalyticsRepo{
			summary:        summary,
			retentionData:  retentionData,
			dailyAnalytics: dailyData,
		},
		&mockVideoRepo{video: &domain.Video{ID: videoID.String(), UserID: userID.String(), Title: "Test Video"}},
		&mockChannelRepo{},
	)

	params := ExportParams{
		VideoID:   &videoID,
		UserID:    userID,
		StartDate: now.AddDate(0, 0, -30),
		EndDate:   now,
	}

	pdfBytes, err := svc.GeneratePDF(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, pdfBytes)

	// Verify it starts with PDF magic bytes
	assert.True(t, len(pdfBytes) > 4)
	assert.Equal(t, "%PDF", string(pdfBytes[:4]))
}

func TestPDFExport_ChannelAnalytics(t *testing.T) {
	channelID := uuid.New()
	userID := uuid.New()
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
		},
	}

	svc := NewExportService(
		&mockVideoAnalyticsRepo{channelDaily: channelDaily, channelTotalViews: 1000},
		&mockVideoRepo{},
		&mockChannelRepo{isOwner: true},
	)

	params := ExportParams{
		ChannelID: &channelID,
		UserID:    userID,
		StartDate: now.AddDate(0, 0, -30),
		EndDate:   now,
	}

	pdfBytes, err := svc.GeneratePDF(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, pdfBytes)
	assert.Equal(t, "%PDF", string(pdfBytes[:4]))
}

func TestPDFExport_AllChannels(t *testing.T) {
	userID := uuid.New()
	channelID := uuid.New()
	now := time.Now()

	channelDaily := []*domain.ChannelDailyAnalytics{
		{ChannelID: channelID, Date: now, Views: 200},
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
		StartDate: now.AddDate(0, 0, -30),
		EndDate:   now,
	}

	pdfBytes, err := svc.GeneratePDF(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, pdfBytes)
	assert.Equal(t, "%PDF", string(pdfBytes[:4]))
}

func TestPDFExport_EmptyData(t *testing.T) {
	videoID := uuid.New()
	userID := uuid.New()
	now := time.Now()

	svc := NewExportService(
		&mockVideoAnalyticsRepo{
			summary: &domain.AnalyticsSummary{VideoID: videoID},
		},
		&mockVideoRepo{video: &domain.Video{ID: videoID.String(), UserID: userID.String(), Title: "Empty Video"}},
		&mockChannelRepo{},
	)

	params := ExportParams{
		VideoID:   &videoID,
		UserID:    userID,
		StartDate: now.AddDate(0, 0, -30),
		EndDate:   now,
	}

	pdfBytes, err := svc.GeneratePDF(context.Background(), params)
	require.NoError(t, err)
	require.NotEmpty(t, pdfBytes)
	assert.Equal(t, "%PDF", string(pdfBytes[:4]))
}
