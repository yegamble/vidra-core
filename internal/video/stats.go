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

// StatsRollupDays is the trailing window for the per-channel "recent views"
// rollup surfaced on the studio dashboard/analytics (views_28d). PeerTube-style
// analytics anchor on 28 days.
const StatsRollupDays = 28

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

// AccountTotals are the engagement totals summed across every channel a user
// owns. Followers is filled by the HTTP layer (it lives in the channel domain).
type AccountTotals struct {
	Views     int64
	Likes     int64
	Dislikes  int64
	Comments  int64
	Followers int64
	Videos    int64
}

// ChannelBreakdown is one channel's row in the account stats table. Followers
// is filled by the HTTP layer.
type ChannelBreakdown struct {
	ID          uuid.UUID
	Handle      string
	DisplayName string
	Views       int64
	Followers   int64
	Videos      int64
	Views28d    int64
}

// AccountStats is the owner-only rollup across all channels a user owns:
// account-wide totals, the aggregated zero-filled 30-day daily series, and a
// per-channel breakdown. Follower counts are attached by the HTTP layer.
type AccountStats struct {
	Totals   AccountTotals
	Daily    []DayViews
	Channels []ChannelBreakdown
}

// AccountStats aggregates stats across every channel ownerID owns, in two
// grouped queries (per-channel rollup + the account-wide daily series) rather
// than N per-channel round-trips. Totals are summed from the per-channel rows.
// Follower counts are the channel domain's concern and left zero here — the
// HTTP layer merges them in.
func (s *Service) AccountStats(ctx context.Context, ownerID uuid.UUID) (AccountStats, error) {
	now := time.Now()
	since := statsWindowStart(now)
	since28 := now.UTC().Truncate(24*time.Hour).AddDate(0, 0, -(StatsRollupDays - 1))

	rows, err := s.repo.GetOwnerChannelStats(ctx, sqlcgen.GetOwnerChannelStatsParams{
		OwnerID:  ownerID,
		Since28d: pgtype.Date{Time: since28, Valid: true},
	})
	if err != nil {
		return AccountStats{}, err
	}
	out := AccountStats{Channels: make([]ChannelBreakdown, 0, len(rows))}
	for _, r := range rows {
		out.Channels = append(out.Channels, ChannelBreakdown{
			ID:          r.ChannelID,
			Handle:      r.Handle,
			DisplayName: r.DisplayName,
			Views:       r.Views,
			Videos:      r.Videos,
			Views28d:    r.Views28d,
		})
		out.Totals.Views += r.Views
		out.Totals.Likes += r.Likes
		out.Totals.Dislikes += r.Dislikes
		out.Totals.Comments += r.Comments
		out.Totals.Videos += r.Videos
	}

	days, err := s.repo.ListOwnerViewDays(ctx, sqlcgen.ListOwnerViewDaysParams{
		OwnerID: ownerID,
		Since:   pgtype.Date{Time: since, Valid: true},
	})
	if err != nil {
		return AccountStats{}, err
	}
	byDay := make(map[string]int64, len(days))
	for _, d := range days {
		byDay[dayKey(d.Day.Time)] = d.Views
	}
	out.Daily = fillDays(since, byDay)
	return out, nil
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
