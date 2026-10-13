package playbackstats

import (
	"context"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type fakeRepo struct {
	plays, secs int64
	dims        map[string]int64
}

func (f *fakeRepo) UpsertVideoPlaybackSample(_ context.Context, a sqlcgen.UpsertVideoPlaybackSampleParams) error {
	f.plays += a.Plays
	f.secs += a.WatchSeconds
	if a.WatchSeconds > 0 {
		if f.dims == nil {
			f.dims = map[string]int64{}
		}
		f.dims["retention/"+a.RetentionKey] += a.WatchSeconds
		f.dims["device/"+a.Device] += a.WatchSeconds
		f.dims["os/"+a.Os] += a.WatchSeconds
	}
	return nil
}
func (f *fakeRepo) ListVideoPlaybackDays(context.Context, sqlcgen.ListVideoPlaybackDaysParams) ([]sqlcgen.ListVideoPlaybackDaysRow, error) {
	return []sqlcgen.ListVideoPlaybackDaysRow{{Day: pgtype.Date{Time: time.Now(), Valid: true}, Plays: f.plays, WatchSeconds: f.secs}}, nil
}
func (f *fakeRepo) SumVideoPlaybackDims(context.Context, sqlcgen.SumVideoPlaybackDimsParams) ([]sqlcgen.SumVideoPlaybackDimsRow, error) {
	var rows []sqlcgen.SumVideoPlaybackDimsRow
	for k, v := range f.dims {
		rows = append(rows, sqlcgen.SumVideoPlaybackDimsRow{Kind: k[:indexSlash(k)], Key: k[indexSlash(k)+1:], Value: v})
	}
	return rows, nil
}
func (f *fakeRepo) PruneVideoPlaybackDays(context.Context, pgtype.Date) (int64, error) { return 2, nil }
func (f *fakeRepo) PruneVideoPlaybackDims(context.Context, pgtype.Date) (int64, error) { return 3, nil }

func indexSlash(s string) int {
	for i := range s {
		if s[i] == '/' {
			return i
		}
	}
	return -1
}

func TestBucket(t *testing.T) {
	for _, c := range []struct{ pos, dur, want int }{
		{0, 100, 0}, {4, 100, 0}, {5, 100, 1}, {99, 100, 19}, {100, 100, 19}, {500, 100, 19}, {-3, 100, 0}, {10, 0, 0},
	} {
		if got := Bucket(c.pos, c.dur); got != c.want {
			t.Errorf("Bucket(%d,%d)=%d want %d", c.pos, c.dur, got, c.want)
		}
	}
}

func TestRecordAndReportMath(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	vid := uuid.New()
	ctx := context.Background()
	// Two plays of a 200 s video (10 s per twentieth). Play A watches the first
	// two slices fully; play B watches only the first slice, in 5 s pieces.
	for _, s := range []Sample{
		{VideoID: vid, PositionSeconds: 5, WatchedSeconds: 10, DurationSeconds: 200, Started: true, Device: "mobile", OS: "ios"},
		{VideoID: vid, PositionSeconds: 15, WatchedSeconds: 10, DurationSeconds: 200, Device: "mobile", OS: "ios"},
		{VideoID: vid, PositionSeconds: 2, WatchedSeconds: 5, DurationSeconds: 200, Started: true, Device: "desktop", OS: "linux"},
		{VideoID: vid, PositionSeconds: 7, WatchedSeconds: 5, DurationSeconds: 200, Device: "desktop", OS: "linux"},
	} {
		if err := svc.Record(ctx, s); err != nil {
			t.Fatal(err)
		}
	}
	rep, err := svc.Report(ctx, vid, 200, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Plays != 2 || rep.WatchSeconds != 30 || rep.AvgViewDurationSeconds != 15 {
		t.Fatalf("totals: %+v", rep)
	}
	// Slice 0: 10+5+5=20 s over 2 plays x 10 s = 1.0; slice 1: 10 / 20 = 0.5.
	if len(rep.Retention) != Buckets || rep.Retention[0] != 1 || math.Abs(rep.Retention[1]-0.5) > 1e-9 || rep.Retention[2] != 0 {
		t.Fatalf("retention: %v", rep.Retention)
	}
	if len(rep.Devices) != 2 || rep.Devices[0].Key != "mobile" || math.Abs(rep.Devices[0].Share-2.0/3) > 1e-9 {
		t.Fatalf("devices: %+v", rep.Devices)
	}
}

func TestRecordClampsAndSanitises(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	if err := svc.Record(context.Background(), Sample{VideoID: uuid.New(), PositionSeconds: 1, WatchedSeconds: 9999, DurationSeconds: 60, Device: "<script>", OS: "x"}); err != nil {
		t.Fatal(err)
	}
	if repo.secs != MaxWatchedPerSample || repo.dims["device/other"] != MaxWatchedPerSample || repo.dims["os/other"] != MaxWatchedPerSample {
		t.Fatalf("not clamped/sanitised: %d %v", repo.secs, repo.dims)
	}
	// Nothing to record -> no writes.
	repo2 := &fakeRepo{}
	_ = NewService(repo2).Record(context.Background(), Sample{VideoID: uuid.New()})
	if repo2.plays != 0 || len(repo2.dims) != 0 {
		t.Fatal("empty sample wrote")
	}
}

func TestDailyCeilingDropsImplausibleWatchTime(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	vid := uuid.New()
	ctx := context.Background()
	// One counted play of a 60 s video: ceiling = 1 x 60 x 3 = 180 s.
	_ = svc.Record(ctx, Sample{VideoID: vid, WatchedSeconds: 12, DurationSeconds: 60, Started: true})
	for i := 0; i < 100; i++ {
		_ = svc.Record(ctx, Sample{VideoID: vid, WatchedSeconds: 12, DurationSeconds: 60})
	}
	if repo.secs > 180 {
		t.Fatalf("watch seconds %d exceeded the ceiling 180", repo.secs)
	}
	if repo.secs < 168 {
		t.Fatalf("ceiling cut too early: %d", repo.secs)
	}
}

func TestReportWithoutDurationOmitsRetention(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo)
	_ = svc.Record(context.Background(), Sample{VideoID: uuid.New(), WatchedSeconds: 10, Started: true})
	rep, _ := svc.Report(context.Background(), uuid.New(), 0, time.Now())
	if rep.Retention != nil || rep.Plays != 1 {
		t.Fatalf("%+v", rep)
	}
}

func TestPrune(t *testing.T) {
	n, err := NewService(&fakeRepo{}).Prune(context.Background(), time.Now())
	if err != nil || n != 5 {
		t.Fatalf("%d %v", n, err)
	}
}

func TestClassifyUA(t *testing.T) {
	for _, c := range []struct{ ua, dev, os string }{
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) AppleWebKit Mobile/15E148", "mobile", "ios"},
		{"Mozilla/5.0 (Linux; Android 14; Pixel 8) AppleWebKit Chrome/120 Mobile Safari", "mobile", "android"},
		{"Mozilla/5.0 (Windows NT 10.0; Win64; x64) Chrome/120", "desktop", "windows"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Safari", "desktop", "macos"},
		{"Mozilla/5.0 (X11; Linux x86_64) Firefox/120", "desktop", "linux"},
		{"Mozilla/5.0 (X11; CrOS x86_64 14541) Chrome", "desktop", "chromeos"},
		{"Mozilla/5.0 (SMART-TV; Linux; Tizen 7.0) AppleWebKit", "tv", "linux"},
		{"Googlebot/2.1", "other", "other"},
		{"", "other", "other"},
	} {
		d, o := ClassifyUA(c.ua)
		if d != c.dev || o != c.os {
			t.Errorf("%q => %s/%s want %s/%s", c.ua, d, o, c.dev, c.os)
		}
	}
}
