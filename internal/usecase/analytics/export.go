package analytics

import (
	"bytes"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"vidra-core/internal/domain"
	"vidra-core/internal/port"

	"github.com/google/uuid"
)

// ExportParams holds the parameters for analytics export operations.
type ExportParams struct {
	VideoID   *uuid.UUID
	ChannelID *uuid.UUID
	UserID    uuid.UUID
	StartDate time.Time
	EndDate   time.Time
}

// ExportService handles analytics data export in various formats.
type ExportService struct {
	analyticsRepo port.VideoAnalyticsRepository
	videoRepo     port.VideoRepository
	channelRepo   port.ChannelRepository
}

// NewExportService creates a new ExportService.
func NewExportService(
	analyticsRepo port.VideoAnalyticsRepository,
	videoRepo port.VideoRepository,
	channelRepo port.ChannelRepository,
) *ExportService {
	return &ExportService{
		analyticsRepo: analyticsRepo,
		videoRepo:     videoRepo,
		channelRepo:   channelRepo,
	}
}

// ValidateVideoOwnership checks that the authenticated user owns the video.
func (s *ExportService) ValidateVideoOwnership(ctx context.Context, videoID, userID uuid.UUID) error {
	video, err := s.videoRepo.GetByID(ctx, videoID.String())
	if err != nil {
		if errors.Is(err, domain.ErrNotFound) {
			return domain.ErrVideoNotFound
		}
		return fmt.Errorf("getting video for ownership check: %w", err)
	}

	if video.UserID != userID.String() {
		return domain.ErrForbidden
	}

	return nil
}

// ValidateChannelOwnership checks that the authenticated user owns the channel.
func (s *ExportService) ValidateChannelOwnership(ctx context.Context, channelID, userID uuid.UUID) error {
	isOwner, err := s.channelRepo.CheckOwnership(ctx, channelID, userID)
	if err != nil {
		return fmt.Errorf("checking channel ownership: %w", err)
	}

	if !isOwner {
		return domain.ErrForbidden
	}

	return nil
}

// GenerateCSV generates a CSV export of analytics data.
func (s *ExportService) GenerateCSV(ctx context.Context, params ExportParams) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if params.VideoID != nil {
		if err := s.writeVideoCSV(ctx, w, params); err != nil {
			return nil, fmt.Errorf("generating video CSV: %w", err)
		}
	} else if params.ChannelID != nil {
		if err := s.writeChannelCSV(ctx, w, params); err != nil {
			return nil, fmt.Errorf("generating channel CSV: %w", err)
		}
	} else {
		if err := s.writeAllChannelsCSV(ctx, w, params); err != nil {
			return nil, fmt.Errorf("generating all-channels CSV: %w", err)
		}
	}

	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("flushing CSV writer: %w", err)
	}

	return buf.Bytes(), nil
}

func (s *ExportService) writeVideoCSV(ctx context.Context, w *csv.Writer, params ExportParams) error {
	daily, err := s.analyticsRepo.GetDailyAnalyticsRange(ctx, *params.VideoID, params.StartDate, params.EndDate)
	if err != nil {
		return fmt.Errorf("fetching daily analytics: %w", err)
	}

	header := []string{"date", "views", "unique_viewers", "watch_time_seconds", "likes", "comments", "shares"}
	if err := w.Write(header); err != nil {
		return err
	}

	for _, d := range daily {
		row := []string{
			d.Date.Format("2006-01-02"),
			strconv.Itoa(d.Views),
			strconv.Itoa(d.UniqueViewers),
			strconv.FormatInt(d.WatchTimeSeconds, 10),
			strconv.Itoa(d.Likes),
			strconv.Itoa(d.Comments),
			strconv.Itoa(d.Shares),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	return nil
}

func (s *ExportService) writeChannelCSV(ctx context.Context, w *csv.Writer, params ExportParams) error {
	daily, err := s.analyticsRepo.GetChannelDailyAnalyticsRange(ctx, *params.ChannelID, params.StartDate, params.EndDate)
	if err != nil {
		return fmt.Errorf("fetching channel daily analytics: %w", err)
	}

	return writeChannelDailyCSV(w, daily)
}

func (s *ExportService) writeAllChannelsCSV(ctx context.Context, w *csv.Writer, params ExportParams) error {
	channels, err := s.channelRepo.GetChannelsByAccountID(ctx, params.UserID)
	if err != nil {
		return fmt.Errorf("fetching user channels: %w", err)
	}

	// Aggregate daily analytics across all channels
	aggregated := make(map[string]*domain.ChannelDailyAnalytics)
	for _, ch := range channels {
		daily, err := s.analyticsRepo.GetChannelDailyAnalyticsRange(ctx, ch.ID, params.StartDate, params.EndDate)
		if err != nil {
			continue // best-effort per channel
		}
		for _, d := range daily {
			key := d.Date.Format("2006-01-02")
			if existing, ok := aggregated[key]; ok {
				existing.Views += d.Views
				existing.UniqueViewers += d.UniqueViewers
				existing.WatchTimeSeconds += d.WatchTimeSeconds
				existing.Likes += d.Likes
				existing.Comments += d.Comments
				existing.Shares += d.Shares
				existing.SubscribersGained += d.SubscribersGained
				existing.SubscribersLost += d.SubscribersLost
			} else {
				aggregated[key] = &domain.ChannelDailyAnalytics{
					Date:              d.Date,
					Views:             d.Views,
					UniqueViewers:     d.UniqueViewers,
					WatchTimeSeconds:  d.WatchTimeSeconds,
					Likes:             d.Likes,
					Comments:          d.Comments,
					Shares:            d.Shares,
					SubscribersGained: d.SubscribersGained,
					SubscribersLost:   d.SubscribersLost,
				}
			}
		}
	}

	// Convert to sorted slice
	var sorted []*domain.ChannelDailyAnalytics
	for _, v := range aggregated {
		sorted = append(sorted, v)
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].Date.Before(sorted[j].Date)
	})

	return writeChannelDailyCSV(w, sorted)
}

func writeChannelDailyCSV(w *csv.Writer, daily []*domain.ChannelDailyAnalytics) error {
	header := []string{"date", "views", "unique_viewers", "watch_time_seconds", "likes", "comments", "shares", "subscribers_gained", "subscribers_lost"}
	if err := w.Write(header); err != nil {
		return err
	}

	for _, d := range daily {
		row := []string{
			d.Date.Format("2006-01-02"),
			strconv.Itoa(d.Views),
			strconv.Itoa(d.UniqueViewers),
			strconv.FormatInt(d.WatchTimeSeconds, 10),
			strconv.Itoa(d.Likes),
			strconv.Itoa(d.Comments),
			strconv.Itoa(d.Shares),
			strconv.Itoa(d.SubscribersGained),
			strconv.Itoa(d.SubscribersLost),
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}

	return nil
}

// GenerateJSON generates a JSON export of analytics data.
func (s *ExportService) GenerateJSON(ctx context.Context, params ExportParams) ([]byte, error) {
	if params.VideoID != nil {
		return s.generateVideoJSON(ctx, params)
	}

	if params.ChannelID != nil {
		return s.generateChannelJSON(ctx, params)
	}

	return s.generateAllChannelsJSON(ctx, params)
}

func (s *ExportService) generateVideoJSON(ctx context.Context, params ExportParams) ([]byte, error) {
	summary, err := s.analyticsRepo.GetVideoAnalyticsSummary(ctx, *params.VideoID, params.StartDate, params.EndDate)
	if err != nil {
		return nil, fmt.Errorf("fetching video analytics summary: %w", err)
	}

	daily, err := s.analyticsRepo.GetDailyAnalyticsRange(ctx, *params.VideoID, params.StartDate, params.EndDate)
	if err != nil {
		return nil, fmt.Errorf("fetching daily analytics: %w", err)
	}

	result := map[string]interface{}{
		"summary":     summary,
		"daily":       daily,
		"start_date":  params.StartDate.Format("2006-01-02"),
		"end_date":    params.EndDate.Format("2006-01-02"),
		"exported_at": time.Now().UTC().Format(time.RFC3339),
	}

	return json.MarshalIndent(result, "", "  ")
}

func (s *ExportService) generateChannelJSON(ctx context.Context, params ExportParams) ([]byte, error) {
	daily, err := s.analyticsRepo.GetChannelDailyAnalyticsRange(ctx, *params.ChannelID, params.StartDate, params.EndDate)
	if err != nil {
		return nil, fmt.Errorf("fetching channel daily analytics: %w", err)
	}

	totalViews, _ := s.analyticsRepo.GetTotalViewsForChannel(ctx, *params.ChannelID)

	result := map[string]interface{}{
		"channel_id":  params.ChannelID.String(),
		"total_views": totalViews,
		"daily":       daily,
		"start_date":  params.StartDate.Format("2006-01-02"),
		"end_date":    params.EndDate.Format("2006-01-02"),
		"exported_at": time.Now().UTC().Format(time.RFC3339),
	}

	return json.MarshalIndent(result, "", "  ")
}

func (s *ExportService) generateAllChannelsJSON(ctx context.Context, params ExportParams) ([]byte, error) {
	channels, err := s.channelRepo.GetChannelsByAccountID(ctx, params.UserID)
	if err != nil {
		return nil, fmt.Errorf("fetching user channels: %w", err)
	}

	var channelsData []map[string]interface{}
	for _, ch := range channels {
		daily, err := s.analyticsRepo.GetChannelDailyAnalyticsRange(ctx, ch.ID, params.StartDate, params.EndDate)
		if err != nil {
			continue
		}
		totalViews, _ := s.analyticsRepo.GetTotalViewsForChannel(ctx, ch.ID)

		channelsData = append(channelsData, map[string]interface{}{
			"channel_id":   ch.ID.String(),
			"channel_name": ch.Name,
			"total_views":  totalViews,
			"daily":        daily,
		})
	}

	result := map[string]interface{}{
		"channels":    channelsData,
		"start_date":  params.StartDate.Format("2006-01-02"),
		"end_date":    params.EndDate.Format("2006-01-02"),
		"exported_at": time.Now().UTC().Format(time.RFC3339),
	}

	return json.MarshalIndent(result, "", "  ")
}
