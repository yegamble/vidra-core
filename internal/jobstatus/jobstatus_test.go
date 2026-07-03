package jobstatus

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeQuerier returns canned rows so the aggregation/merge logic is testable
// without a database.
type fakeQuerier struct {
	transcode sqlcgen.TranscodeJobStatsRow
	fed       sqlcgen.FederationDeliveryStatsRow
	imp       sqlcgen.ImportJobStatsRow
	caption   sqlcgen.CaptionJobStatsRow
	export    sqlcgen.AccountExportStatsRow
	upload    sqlcgen.UploadSessionStatsRow

	transcodeFails []sqlcgen.TranscodeRecentFailuresRow
	fedFails       []sqlcgen.FederationRecentFailuresRow
	impFails       []sqlcgen.ImportRecentFailuresRow
	captionFails   []sqlcgen.CaptionRecentFailuresRow
	exportFails    []sqlcgen.AccountExportRecentFailuresRow
}

func (f *fakeQuerier) TranscodeJobStats(context.Context) (sqlcgen.TranscodeJobStatsRow, error) {
	return f.transcode, nil
}
func (f *fakeQuerier) FederationDeliveryStats(context.Context) (sqlcgen.FederationDeliveryStatsRow, error) {
	return f.fed, nil
}
func (f *fakeQuerier) ImportJobStats(context.Context) (sqlcgen.ImportJobStatsRow, error) {
	return f.imp, nil
}
func (f *fakeQuerier) CaptionJobStats(context.Context) (sqlcgen.CaptionJobStatsRow, error) {
	return f.caption, nil
}
func (f *fakeQuerier) AccountExportStats(context.Context) (sqlcgen.AccountExportStatsRow, error) {
	return f.export, nil
}
func (f *fakeQuerier) UploadSessionStats(context.Context) (sqlcgen.UploadSessionStatsRow, error) {
	return f.upload, nil
}
func (f *fakeQuerier) TranscodeRecentFailures(context.Context, int32) ([]sqlcgen.TranscodeRecentFailuresRow, error) {
	return f.transcodeFails, nil
}
func (f *fakeQuerier) FederationRecentFailures(context.Context, int32) ([]sqlcgen.FederationRecentFailuresRow, error) {
	return f.fedFails, nil
}
func (f *fakeQuerier) ImportRecentFailures(context.Context, int32) ([]sqlcgen.ImportRecentFailuresRow, error) {
	return f.impFails, nil
}
func (f *fakeQuerier) CaptionRecentFailures(context.Context, int32) ([]sqlcgen.CaptionRecentFailuresRow, error) {
	return f.captionFails, nil
}
func (f *fakeQuerier) AccountExportRecentFailures(context.Context, int32) ([]sqlcgen.AccountExportRecentFailuresRow, error) {
	return f.exportFails, nil
}

func TestOverviewNormalisesAndMergesFailures(t *testing.T) {
	now := time.Now()
	q := &fakeQuerier{
		transcode: sqlcgen.TranscodeJobStatsRow{Pending: 2, Running: 1, Done: 10, Failed: 3, OldestPendingAgeSeconds: 42},
		fed:       sqlcgen.FederationDeliveryStatsRow{Pending: 1, Done: 5, Failed: 1},
		imp:       sqlcgen.ImportJobStatsRow{Done: 7},
		caption:   sqlcgen.CaptionJobStatsRow{Pending: 4},
		export:    sqlcgen.AccountExportStatsRow{Done: 1},
		// upload_sessions maps active→pending, completed→done, cancelled→failed.
		upload: sqlcgen.UploadSessionStatsRow{Pending: 6, Done: 2, Failed: 1},
		transcodeFails: []sqlcgen.TranscodeRecentFailuresRow{
			{ID: uuid.New(), Error: "ffmpeg boom", Attempts: 5, UpdatedAt: now.Add(-1 * time.Minute)},
		},
		fedFails: []sqlcgen.FederationRecentFailuresRow{
			{ID: uuid.New(), Error: "inbox 500", Attempts: 5, UpdatedAt: now}, // newest
		},
		impFails: []sqlcgen.ImportRecentFailuresRow{
			{ID: uuid.New(), Error: "fetch failed", Attempts: 5, UpdatedAt: now.Add(-2 * time.Minute)},
		},
	}
	ov, err := NewService(q).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(ov.Queues) != 6 {
		t.Fatalf("want 6 queues, got %d", len(ov.Queues))
	}
	if ov.Queues[0].Queue != QueueTranscode || ov.Queues[0].Pending != 2 || ov.Queues[0].OldestPendingAgeSeconds != 42 {
		t.Errorf("transcode queue = %+v", ov.Queues[0])
	}
	// upload_sessions is last and uses the remapped counts.
	us := ov.Queues[5]
	if us.Queue != QueueUploadSessions || us.Pending != 6 || us.Done != 2 || us.Failed != 1 || us.Running != 0 {
		t.Errorf("upload_sessions queue = %+v", us)
	}
	// Failures merged and sorted newest-first across queues.
	if len(ov.RecentFailures) != 3 {
		t.Fatalf("want 3 failures, got %d", len(ov.RecentFailures))
	}
	if ov.RecentFailures[0].Queue != QueueFederation {
		t.Errorf("newest failure queue = %q, want federation", ov.RecentFailures[0].Queue)
	}
	if ov.RecentFailures[2].Queue != QueueImport {
		t.Errorf("oldest failure queue = %q, want import", ov.RecentFailures[2].Queue)
	}
}

func TestOverviewCapsRecentFailures(t *testing.T) {
	now := time.Now()
	q := &fakeQuerier{}
	// 10 from each of two queues = 20 candidates; the cap is maxRecentFailures.
	for i := 0; i < perQueueFailureFetch; i++ {
		q.transcodeFails = append(q.transcodeFails, sqlcgen.TranscodeRecentFailuresRow{ID: uuid.New(), UpdatedAt: now.Add(-time.Duration(i) * time.Second)})
		q.impFails = append(q.impFails, sqlcgen.ImportRecentFailuresRow{ID: uuid.New(), UpdatedAt: now.Add(-time.Duration(i) * time.Second)})
	}
	ov, err := NewService(q).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(ov.RecentFailures) != maxRecentFailures {
		t.Errorf("failures = %d, want cap %d", len(ov.RecentFailures), maxRecentFailures)
	}
}

func TestDepthsFlattensAllStates(t *testing.T) {
	q := &fakeQuerier{
		transcode: sqlcgen.TranscodeJobStatsRow{Pending: 2, Running: 1, Done: 10, Failed: 3},
	}
	depths, err := NewService(q).Depths(context.Background())
	if err != nil {
		t.Fatalf("Depths: %v", err)
	}
	// 6 queues x 4 states.
	if len(depths) != 24 {
		t.Fatalf("want 24 depth samples, got %d", len(depths))
	}
	seen := map[string]int64{}
	for _, d := range depths {
		if d.Queue == QueueTranscode {
			seen[d.State] = d.Count
		}
	}
	if seen["pending"] != 2 || seen["running"] != 1 || seen["done"] != 10 || seen["failed"] != 3 {
		t.Errorf("transcode depths = %+v", seen)
	}
}
