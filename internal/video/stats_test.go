package video

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// --- fakeRepo stats methods (mirror the video_stats queries) ---

// dayEntry keys the in-memory per-day rollup.
type dayEntry struct {
	video uuid.UUID
	day   string
}

func (f *fakeRepo) IncrementVideoViewDay(_ context.Context, videoID uuid.UUID) error {
	if f.viewDays == nil {
		f.viewDays = map[dayEntry]int64{}
	}
	f.viewDays[dayEntry{videoID, dayKey(time.Now())}]++
	return nil
}

func (f *fakeRepo) ListVideoViewDays(_ context.Context, a sqlcgen.ListVideoViewDaysParams) ([]sqlcgen.ListVideoViewDaysRow, error) {
	var rows []sqlcgen.ListVideoViewDaysRow
	for k, n := range f.viewDays {
		day, _ := time.Parse("2006-01-02", k.day)
		if k.video == a.VideoID && !day.Before(a.Since.Time) {
			rows = append(rows, sqlcgen.ListVideoViewDaysRow{Day: pgtype.Date{Time: day, Valid: true}, Views: n})
		}
	}
	return rows, nil
}

func (f *fakeRepo) ListChannelViewDays(_ context.Context, a sqlcgen.ListChannelViewDaysParams) ([]sqlcgen.ListChannelViewDaysRow, error) {
	perDay := map[string]int64{}
	for k, n := range f.viewDays {
		v, ok := f.videos[k.video]
		if !ok || v.ChannelID != a.ChannelID {
			continue
		}
		day, _ := time.Parse("2006-01-02", k.day)
		if !day.Before(a.Since.Time) {
			perDay[k.day] += n
		}
	}
	var rows []sqlcgen.ListChannelViewDaysRow
	for d, n := range perDay {
		day, _ := time.Parse("2006-01-02", d)
		rows = append(rows, sqlcgen.ListChannelViewDaysRow{Day: pgtype.Date{Time: day, Valid: true}, Views: n})
	}
	return rows, nil
}

func (f *fakeRepo) GetVideoEngagementTotals(_ context.Context, videoID uuid.UUID) (sqlcgen.GetVideoEngagementTotalsRow, error) {
	return sqlcgen.GetVideoEngagementTotalsRow{
		Views: f.views[videoID], Likes: f.likes[videoID], Dislikes: f.dislikes[videoID], Comments: f.comments[videoID],
	}, nil
}

func (f *fakeRepo) GetChannelEngagementTotals(_ context.Context, channelID uuid.UUID) (sqlcgen.GetChannelEngagementTotalsRow, error) {
	var row sqlcgen.GetChannelEngagementTotalsRow
	for id, v := range f.videos {
		if v.ChannelID != channelID {
			continue
		}
		row.Views += f.views[id]
		row.Likes += f.likes[id]
		row.Dislikes += f.dislikes[id]
		row.Comments += f.comments[id]
		row.Videos++
	}
	return row, nil
}

// --- tests ---

// TestRecordViewRollsUpTheDay proves the deduped view write also increments
// the per-day rollup that feeds the stats series.
func TestRecordViewRollsUpTheDay(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)

	v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	if _, err := repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: v.ID, State: "published"}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	for i := 0; i < 2; i++ {
		if err := svc.RecordView(ctx, v.ID, uuid.Nil, false, "viewer"); err != nil {
			t.Fatalf("RecordView %d: %v", i, err)
		}
	}
	if got := repo.viewDays[dayEntry{v.ID, dayKey(time.Now())}]; got != 2 {
		t.Fatalf("today's rollup = %d, want 2", got)
	}
}

// TestVideoStatsOwnerGateAndSeries proves the owner gate (non-owner →
// ErrForbidden, unknown → ErrNotFound) and the dense zero-filled 30-day series.
func TestVideoStatsOwnerGateAndSeries(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)

	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	_, _ = repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: v.ID, State: "published"})
	_ = svc.RecordView(ctx, v.ID, uuid.Nil, false, "a")
	repo.likes = map[uuid.UUID]int64{v.ID: 3}
	repo.dislikes = map[uuid.UUID]int64{v.ID: 1}
	repo.comments = map[uuid.UUID]int64{v.ID: 2}

	if _, err := svc.VideoStats(ctx, uuid.New(), v.ID); !errors.Is(err, ErrForbidden) {
		t.Fatalf("non-owner stats err = %v, want ErrForbidden", err)
	}
	if _, err := svc.VideoStats(ctx, owner, uuid.New()); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown video stats err = %v, want ErrNotFound", err)
	}

	stats, err := svc.VideoStats(ctx, owner, v.ID)
	if err != nil {
		t.Fatalf("VideoStats: %v", err)
	}
	if stats.Views != 1 || stats.Likes != 3 || stats.Dislikes != 1 || stats.Comments != 2 {
		t.Fatalf("totals = %+v", stats)
	}
	if len(stats.Daily) != StatsWindowDays {
		t.Fatalf("series length = %d, want %d", len(stats.Daily), StatsWindowDays)
	}
	last := stats.Daily[len(stats.Daily)-1]
	if got := dayKey(time.Now()); dayKey(last.Day) != got || last.Views != 1 {
		t.Fatalf("last day = %s/%d, want %s/1", dayKey(last.Day), last.Views, got)
	}
	for _, d := range stats.Daily[:len(stats.Daily)-1] {
		if d.Views != 0 {
			t.Fatalf("day %s = %d, want zero-filled", dayKey(d.Day), d.Views)
		}
	}
}

// TestChannelStatsAggregates proves channel totals sum across the channel's
// videos and the series aggregates per day.
func TestChannelStatsAggregates(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)

	channelID := uuid.New()
	v1, _ := svc.CreateDraft(ctx, channelID, CreateInput{Title: "a", Privacy: "public"})
	v2, _ := svc.CreateDraft(ctx, channelID, CreateInput{Title: "b", Privacy: "private"})
	_, _ = repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: v1.ID, State: "published"})
	_, _ = repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: v2.ID, State: "published"})
	_ = svc.RecordView(ctx, v1.ID, uuid.Nil, false, "x")
	_ = svc.RecordView(ctx, v2.ID, owner, true, "y")
	repo.likes = map[uuid.UUID]int64{v1.ID: 2, v2.ID: 1}
	repo.comments = map[uuid.UUID]int64{v1.ID: 4}

	stats, err := svc.ChannelStats(ctx, channelID)
	if err != nil {
		t.Fatalf("ChannelStats: %v", err)
	}
	if stats.Views != 2 || stats.Likes != 3 || stats.Comments != 4 || stats.Videos != 2 {
		t.Fatalf("channel totals = %+v", stats)
	}
	if len(stats.Daily) != StatsWindowDays {
		t.Fatalf("series length = %d", len(stats.Daily))
	}
	if last := stats.Daily[len(stats.Daily)-1]; last.Views != 2 {
		t.Fatalf("today aggregated = %d, want 2", last.Views)
	}
}
