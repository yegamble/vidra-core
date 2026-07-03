package video

import (
	"context"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// StatsWindowDays is the length of the daily-views series exposed on the
// owner stats endpoints (product-decisions §8: a 30-day series, zero-filled).
const StatsWindowDays = 30

// DayViews is one day of a stats series. Day is a UTC calendar day.
type DayViews struct {
	Day   time.Time
	Views int64
}

// VideoStats is the owner-only stats view of a single video: engagement
// totals plus a zero-filled 30-day daily-views series (oldest first).
type VideoStats struct {
	Views    int64
	Likes    int64
	Dislikes int64
	Comments int64
	Daily    []DayViews
}

// ChannelStats is the owner-only stats view of a whole channel: totals across
// all its videos (any privacy/state — the owner sees their full numbers),
// the video count, and the aggregated zero-filled 30-day series. The follower
// count lives in the channel domain and is attached by the HTTP layer.
type ChannelStats struct {
	Views    int64
	Likes    int64
	Dislikes int64
	Comments int64
	Videos   int64
	Daily    []DayViews
}

// VideoStats returns the stats for a video the caller owns. Non-owner →
// ErrForbidden (the HTTP layer reports 404 so existence is not leaked);
// unknown id → ErrNotFound.
func (s *Service) VideoStats(ctx context.Context, ownerID, videoID uuid.UUID) (VideoStats, error) {
	v, err := s.GetByID(ctx, videoID)
	if err != nil {
		return VideoStats{}, err
	}
	if v.OwnerID != ownerID {
		return VideoStats{}, ErrForbidden
	}
	totals, err := s.repo.GetVideoEngagementTotals(ctx, videoID)
	if err != nil {
		return VideoStats{}, err
	}
	since := statsWindowStart(time.Now())
	rows, err := s.repo.ListVideoViewDays(ctx, sqlcgen.ListVideoViewDaysParams{
		VideoID: videoID,
		Since:   pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		return VideoStats{}, err
	}
	byDay := make(map[string]int64, len(rows))
	for _, r := range rows {
		byDay[dayKey(r.Day.Time)] = r.Views
	}
	return VideoStats{
		Views:    totals.Views,
		Likes:    totals.Likes,
		Dislikes: totals.Dislikes,
		Comments: totals.Comments,
		Daily:    fillDays(since, byDay),
	}, nil
}

// ChannelStats returns the aggregated stats for a channel. Ownership is
// enforced by the caller (the HTTP layer resolves the handle and 404s
// non-owners before asking).
func (s *Service) ChannelStats(ctx context.Context, channelID uuid.UUID) (ChannelStats, error) {
	totals, err := s.repo.GetChannelEngagementTotals(ctx, channelID)
	if err != nil {
		return ChannelStats{}, err
	}
	since := statsWindowStart(time.Now())
	rows, err := s.repo.ListChannelViewDays(ctx, sqlcgen.ListChannelViewDaysParams{
		ChannelID: channelID,
		Since:     pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		return ChannelStats{}, err
	}
	byDay := make(map[string]int64, len(rows))
	for _, r := range rows {
		byDay[dayKey(r.Day.Time)] = r.Views
	}
	return ChannelStats{
		Views:    totals.Views,
		Likes:    totals.Likes,
		Dislikes: totals.Dislikes,
		Comments: totals.Comments,
		Videos:   totals.Videos,
		Daily:    fillDays(since, byDay),
	}, nil
}

// statsWindowStart returns the UTC calendar day starting the 30-day series
// that ends today (inclusive): today - 29 days.
func statsWindowStart(now time.Time) time.Time {
	day := now.UTC().Truncate(24 * time.Hour)
	return day.AddDate(0, 0, -(StatsWindowDays - 1))
}

// dayKey normalizes a day to its map key (UTC calendar date).
func dayKey(t time.Time) string {
	return t.UTC().Format("2006-01-02")
}

// fillDays renders a dense StatsWindowDays-long series from since (oldest
// first), zero-filling days with no recorded views.
func fillDays(since time.Time, byDay map[string]int64) []DayViews {
	out := make([]DayViews, 0, StatsWindowDays)
	for i := 0; i < StatsWindowDays; i++ {
		day := since.AddDate(0, 0, i)
		out = append(out, DayViews{Day: day, Views: byDay[dayKey(day)]})
	}
	return out
}
