package qoe

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// seedTwentyFourHours writes one hour's worth of measurements per hour for a
// full day, split across two delivery sources with very different quality — the
// exact shape the exit criterion is about.
func seedTwentyFourHours(t *testing.T, repo *fakeRepo, start time.Time) {
	t.Helper()
	id := byte(0)
	for h := 0; h < 24; h++ {
		at := start.Add(time.Duration(h) * time.Hour)
		for i := 0; i < 20; i++ {
			id++
			// CDN: consistently fast.
			fast := rawEvent(at.Add(time.Duration(i)*time.Second), id, EventStart, "cdn", EngineHLSJS, FormatCMAF)
			v := int32(300)
			fast.TtffMs = &v
			fast.ID = orderedUUIDWide(int(id) * 2)
			// Origin: consistently slow.
			slow := rawEvent(at.Add(time.Duration(i)*time.Second), id, EventStart, "api-proxy", EngineHLSJS, FormatCMAF)
			w := int32(2500)
			slow.TtffMs = &w
			slow.ID = orderedUUIDWide(int(id)*2 + 1)
			repo.events = append(repo.events, fast, slow)
		}
	}
}

func orderedUUIDWide(n int) (u [16]byte) {
	u[0] = byte(n >> 8)
	u[1] = byte(n)
	u[15] = 0x42
	return
}

// TestPlaybackHealthAnswersTheExitCriterion is the phase-4 acceptance test in
// code: TTFF percentiles PER SOURCE for the last 24h, merged across 24 hourly
// rollup rows. The merge is the part that could silently be wrong — averaging
// 24 hourly p95s would produce a plausible number with no meaning — so the two
// sources are seeded far enough apart that a merge which mixed them, or which
// took only one hour, would be visible.
func TestPlaybackHealthAnswersTheExitCriterion(t *testing.T) {
	repo := newFakeRepo()
	start := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	seedTwentyFourHours(t, repo, start)
	svc := quietService(repo)

	if _, err := svc.RollUp(context.Background(), start.Add(25*time.Hour)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	if len(repo.rollups) != 48 {
		t.Fatalf("wrote %d rollup rows, want 48 (24 hours x 2 sources)", len(repo.rollups))
	}

	health, err := svc.PlaybackHealth(context.Background(), start, start.Add(24*time.Hour), 200, 0)
	if err != nil {
		t.Fatalf("PlaybackHealth: %v", err)
	}
	if len(health.Sources) != 2 {
		t.Fatalf("got %d source summaries, want 2", len(health.Sources))
	}
	bySource := map[DeliverySource]SourceSummary{}
	for _, s := range health.Sources {
		bySource[s.DeliverySource] = s
	}
	cdn, origin := bySource[SourceCDN], bySource[SourceAPIProxy]

	if cdn.StartCount != 480 || origin.StartCount != 480 {
		t.Errorf("start counts = cdn %d origin %d, want 480 each (24h x 20)", cdn.StartCount, origin.StartCount)
	}
	assertNear(t, "cdn 24h ttff p50", cdn.TTFF.P50Ms, 300)
	assertNear(t, "cdn 24h ttff p95", cdn.TTFF.P95Ms, 300)
	assertNear(t, "origin 24h ttff p50", origin.TTFF.P50Ms, 2500)
	assertNear(t, "origin 24h ttff p95", origin.TTFF.P95Ms, 2500)

	// Nobody rebuffered, so the rebuffer percentiles must be absent rather than
	// zero.
	if cdn.Rebuffer.P95Ms != nil {
		t.Errorf("cdn rebuffer p95 = %d, want nil for a window with no rebuffers", *cdn.Rebuffer.P95Ms)
	}
	if cdn.PartialPercentiles || origin.PartialPercentiles {
		t.Error("partial_percentiles set for rows this package just wrote")
	}
	// Ordering is the vocabulary's, not the map's, so a reload does not
	// reshuffle the admin table.
	if health.Sources[0].DeliverySource != SourceAPIProxy || health.Sources[1].DeliverySource != SourceCDN {
		t.Errorf("source order = %q, %q; want vocabulary order", health.Sources[0].DeliverySource, health.Sources[1].DeliverySource)
	}
}

// TestPlaybackHealthMergeBeatsAveragingPercentiles is the reason histograms are
// stored at all. One hour is pathologically slow and the other 23 are fast; the
// true 24h p95 sits in the fast mass, and an implementation that averaged the
// hourly p95s would report something far higher.
func TestPlaybackHealthMergeBeatsAveragingPercentiles(t *testing.T) {
	repo := newFakeRepo()
	start := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	id := 0
	for h := 0; h < 24; h++ {
		at := start.Add(time.Duration(h) * time.Hour)
		ttff := int32(200)
		if h == 5 {
			ttff = 30000 // one catastrophic hour
		}
		for i := 0; i < 100; i++ {
			id++
			e := rawEvent(at.Add(time.Duration(i)*time.Second), byte(id%251+1), EventStart, "cdn", EngineHLSJS, FormatCMAF)
			v := ttff
			e.TtffMs = &v
			e.ID = orderedUUIDWide(id)
			repo.events = append(repo.events, e)
		}
	}
	svc := quietService(repo)
	if _, err := svc.RollUp(context.Background(), start.Add(25*time.Hour)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	health, err := svc.PlaybackHealth(context.Background(), start, start.Add(24*time.Hour), 200, 0)
	if err != nil {
		t.Fatalf("PlaybackHealth: %v", err)
	}
	got := health.Sources[0].TTFF.P95Ms
	if got == nil {
		t.Fatal("p95 = nil")
	}
	// 100 of 2400 samples are slow, so the 95th percentile is still in the fast
	// mass. Averaging the 24 hourly p95s would give roughly (23*200+30000)/24 ≈
	// 1442 — a number that looks like a mild regression rather than one very bad
	// hour.
	if *got > 400 {
		t.Errorf("24h p95 = %d, want ~200: the merge is averaging percentiles rather than summing distributions", *got)
	}
	// The bad hour must still be visible in the hourly detail.
	var worst int32
	for _, b := range health.Buckets {
		if b.TTFF.P95Ms != nil && *b.TTFF.P95Ms > worst {
			worst = *b.TTFF.P95Ms
		}
	}
	if worst < 25000 {
		t.Errorf("worst hourly p95 = %d, want the ~30000 ms hour to be visible in the buckets", worst)
	}
}

// TestPlaybackHealthPagesBucketsButNotSources: a page of a summary is not a
// summary, so `sources` must cover the whole window regardless of limit/offset.
func TestPlaybackHealthPagesBucketsButNotSources(t *testing.T) {
	repo := newFakeRepo()
	start := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	seedTwentyFourHours(t, repo, start)
	svc := quietService(repo)
	if _, err := svc.RollUp(context.Background(), start.Add(25*time.Hour)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}

	page, err := svc.PlaybackHealth(context.Background(), start, start.Add(24*time.Hour), 5, 10)
	if err != nil {
		t.Fatalf("PlaybackHealth: %v", err)
	}
	if len(page.Buckets) != 5 {
		t.Errorf("buckets on the page = %d, want 5", len(page.Buckets))
	}
	if page.BucketsTotal != 48 {
		t.Errorf("buckets_total = %d, want 48", page.BucketsTotal)
	}
	if len(page.Sources) != 2 {
		t.Errorf("sources = %d on a 5-row page; the summary must not be paged", len(page.Sources))
	}
	for _, s := range page.Sources {
		if s.StartCount != 480 {
			t.Errorf("%q start_count = %d on a paged request, want the whole window's 480", s.DeliverySource, s.StartCount)
		}
	}
	// Offset past the end is an empty page, never a nil that renders as `null`.
	past, err := svc.PlaybackHealth(context.Background(), start, start.Add(24*time.Hour), 5, 1000)
	if err != nil {
		t.Fatalf("PlaybackHealth: %v", err)
	}
	if past.Buckets == nil || len(past.Buckets) != 0 {
		t.Errorf("buckets past the end = %v, want an empty slice", past.Buckets)
	}
}

// TestPlaybackHealthMarksNativeHLSRenditionsUnsupported: zero bitrate switches
// on Safari is a capability gap, and the response has to say so or an admin
// reads it as flawless ABR.
func TestPlaybackHealthMarksNativeHLSRenditionsUnsupported(t *testing.T) {
	repo := newFakeRepo()
	start := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	for i := byte(1); i <= 5; i++ {
		e := rawEvent(start.Add(time.Duration(i)*time.Minute), i, EventStart, "cdn", EngineNativeHLS, FormatHLSTS)
		v := int32(700)
		e.TtffMs = &v
		repo.events = append(repo.events, e)
	}
	svc := quietService(repo)
	if _, err := svc.RollUp(context.Background(), start.Add(2*time.Hour)); err != nil {
		t.Fatalf("RollUp: %v", err)
	}
	health, err := svc.PlaybackHealth(context.Background(), start, start.Add(2*time.Hour), 10, 0)
	if err != nil {
		t.Fatalf("PlaybackHealth: %v", err)
	}
	if len(health.Buckets) != 1 {
		t.Fatalf("buckets = %d, want 1", len(health.Buckets))
	}
	b := health.Buckets[0]
	if b.RenditionReportingSupported {
		t.Error("native-hls bucket reports rendition support; it can never have any")
	}
	if b.BitrateSwitchCount != 0 {
		t.Errorf("bitrate_switch_count = %d, want 0", b.BitrateSwitchCount)
	}
}

// TestPlaybackHealthWindowGuards: an unbounded window is refused with a fixable
// instruction rather than a timeout.
func TestPlaybackHealthWindowGuards(t *testing.T) {
	svc := quietService(newFakeRepo())
	now := time.Date(2026, 8, 23, 0, 0, 0, 0, time.UTC)
	if _, err := svc.PlaybackHealth(context.Background(), now.Add(-30*24*time.Hour), now, 10, 0); !errors.Is(err, ErrWindowTooWide) {
		t.Errorf("30-day window err = %v, want ErrWindowTooWide", err)
	}
	if _, err := svc.PlaybackHealth(context.Background(), now, now, 10, 0); err == nil {
		t.Error("an empty window was accepted")
	}
	if _, err := svc.PlaybackHealth(context.Background(), now, now.Add(-time.Hour), 10, 0); err == nil {
		t.Error("a reversed window was accepted")
	}
}

// TestSummaryReportsAHistogramVersionMismatch: counts stay trustworthy across a
// boundary-table change, percentiles do not, and the response says which.
func TestSummaryReportsAHistogramVersionMismatch(t *testing.T) {
	hour := time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC)
	future := NewHistogram()
	future.Observe(500)
	rows := []sqlcgen.QoeRollup{
		{
			HourBucket: hour, DeliverySource: "cdn", Engine: "hls-js", PackagingFormat: "cmaf",
			EventCount: 10, StartCount: 10,
			HistogramVersion: HistogramVersion + 1,
			TtffHistogram:    future.Counts(), RebufferHistogram: NewHistogram().Counts(),
		},
	}
	got := summarize(rows)
	if len(got) != 1 {
		t.Fatalf("summaries = %d, want 1", len(got))
	}
	if !got[0].PartialPercentiles {
		t.Error("a row from another histogram version was merged silently")
	}
	if got[0].StartCount != 10 {
		t.Errorf("start_count = %d, want 10 — counts are plain integers and survive a version change", got[0].StartCount)
	}
	if got[0].TTFF.P50Ms != nil {
		t.Error("percentiles were computed from a foreign histogram version")
	}
}
