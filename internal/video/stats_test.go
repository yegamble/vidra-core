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

// GetOwnerChannelStats groups this fake's videos by channel (the fake models a
// single owner's world), mirroring the account-stats query. Handle/display_name
// are left blank — the fake carries no channel metadata; the HTTP layer's own
// fake (which has the channel service) exercises those fields.
func (f *fakeRepo) GetOwnerChannelStats(_ context.Context, a sqlcgen.GetOwnerChannelStatsParams) ([]sqlcgen.GetOwnerChannelStatsRow, error) {
	byCh := map[uuid.UUID]*sqlcgen.GetOwnerChannelStatsRow{}
	order := []uuid.UUID{}
	for id, v := range f.videos {
		row := byCh[v.ChannelID]
		if row == nil {
			row = &sqlcgen.GetOwnerChannelStatsRow{ChannelID: v.ChannelID}
			byCh[v.ChannelID] = row
			order = append(order, v.ChannelID)
		}
		row.Views += f.views[id]
		row.Likes += f.likes[id]
		row.Dislikes += f.dislikes[id]
		row.Comments += f.comments[id]
		row.Videos++
	}
	for k, n := range f.viewDays {
		v, ok := f.videos[k.video]
		if !ok {
			continue
		}
		day, _ := time.Parse("2006-01-02", k.day)
		if row := byCh[v.ChannelID]; row != nil && !day.Before(a.Since28d.Time) {
			row.Views28d += n
		}
	}
	out := make([]sqlcgen.GetOwnerChannelStatsRow, 0, len(order))
	for _, id := range order {
		out = append(out, *byCh[id])
	}
	return out, nil
}

func (f *fakeRepo) ListOwnerViewDays(_ context.Context, a sqlcgen.ListOwnerViewDaysParams) ([]sqlcgen.ListOwnerViewDaysRow, error) {
	perDay := map[string]int64{}
	for k, n := range f.viewDays {
		day, _ := time.Parse("2006-01-02", k.day)
		if !day.Before(a.Since.Time) {
			perDay[k.day] += n
		}
	}
	var rows []sqlcgen.ListOwnerViewDaysRow
	for d, n := range perDay {
		day, _ := time.Parse("2006-01-02", d)
		rows = append(rows, sqlcgen.ListOwnerViewDaysRow{Day: pgtype.Date{Time: day, Valid: true}, Views: n})
	}
	return rows, nil
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
func TestAccountStatsAggregatesAcrossChannels(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	svc := NewService(repo, nil)

	chA, chB := uuid.New(), uuid.New()
	a1, _ := svc.CreateDraft(ctx, chA, CreateInput{Title: "a1", Privacy: "public"})
	a2, _ := svc.CreateDraft(ctx, chA, CreateInput{Title: "a2", Privacy: "public"})
	b1, _ := svc.CreateDraft(ctx, chB, CreateInput{Title: "b1", Privacy: "public"})
	for _, id := range []uuid.UUID{a1.ID, a2.ID, b1.ID} {
		_, _ = repo.SetVideoState(ctx, sqlcgen.SetVideoStateParams{ID: id, State: "published"})
	}
	// Views today (also roll up the per-day series).
	_ = svc.RecordView(ctx, a1.ID, uuid.Nil, false, "x")
	_ = svc.RecordView(ctx, a2.ID, uuid.Nil, false, "y")
	_ = svc.RecordView(ctx, b1.ID, uuid.Nil, false, "z")
	repo.likes = map[uuid.UUID]int64{a1.ID: 2, b1.ID: 1}
	repo.comments = map[uuid.UUID]int64{a1.ID: 4}

	stats, err := svc.AccountStats(ctx, owner)
	if err != nil {
		t.Fatalf("AccountStats: %v", err)
	}
	// Totals summed across BOTH channels (followers filled by the HTTP layer).
	if stats.Totals.Views != 3 || stats.Totals.Likes != 3 || stats.Totals.Comments != 4 || stats.Totals.Videos != 3 {
		t.Fatalf("account totals = %+v", stats.Totals)
	}
	if len(stats.Channels) != 2 {
		t.Fatalf("breakdown channels = %d, want 2", len(stats.Channels))
	}
	// Per-channel rollup: channel A has 2 videos / 2 views; channel B has 1/1.
	byCh := map[uuid.UUID]ChannelBreakdown{}
	for _, c := range stats.Channels {
		byCh[c.ID] = c
	}
	if byCh[chA].Views != 2 || byCh[chA].Videos != 2 || byCh[chA].Views28d != 2 {
		t.Errorf("channel A breakdown = %+v", byCh[chA])
	}
	if byCh[chB].Views != 1 || byCh[chB].Videos != 1 || byCh[chB].Views28d != 1 {
		t.Errorf("channel B breakdown = %+v", byCh[chB])
	}
	// Merged 30-day series: today = 3 (one view per video across both channels).
	if len(stats.Daily) != StatsWindowDays {
		t.Fatalf("series length = %d, want %d", len(stats.Daily), StatsWindowDays)
	}
	if last := stats.Daily[len(stats.Daily)-1]; last.Views != 3 {
		t.Errorf("today merged views = %d, want 3", last.Views)
	}
}

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
