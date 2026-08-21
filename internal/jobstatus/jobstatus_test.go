package jobstatus

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"

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
	storagemi sqlcgen.StorageMigrationStatsRow

	transcodeFails []sqlcgen.TranscodeRecentFailuresRow
	fedFails       []sqlcgen.FederationRecentFailuresRow
	impFails       []sqlcgen.ImportRecentFailuresRow
	captionFails   []sqlcgen.CaptionRecentFailuresRow
	exportFails    []sqlcgen.AccountExportRecentFailuresRow
	storagemiFails []sqlcgen.StorageMigrationRecentFailuresRow
}

type fakeOperationalQuerier struct {
	fakeQuerier

	maxCursor int64
	minCursor int64
	runs      []sqlcgen.JobRun
	run       sqlcgen.JobRun
	events    []sqlcgen.JobEvent
	totalRuns int64
	totalEvts int64
	health    []sqlcgen.OperationalJobQueueMetricsRow

	listParams       sqlcgen.ListOperationalJobRunsParams
	countParams      sqlcgen.CountOperationalJobRunsParams
	eventListParams  sqlcgen.ListOperationalJobEventsParams
	eventCountParams sqlcgen.CountOperationalJobEventsParams
	staleSeconds     int32
	pruneEvents      sqlcgen.PruneOperationalJobEventsParams
	pruneRuns        sqlcgen.PruneTerminalOperationalJobRunsParams
	prunePipelines   sqlcgen.PruneTerminalPipelineRunsParams
	calls            []string
}

func (f *fakeOperationalQuerier) MaxOperationalJobEventCursor(context.Context) (int64, error) {
	f.calls = append(f.calls, "max-cursor")
	return f.maxCursor, nil
}

func (f *fakeOperationalQuerier) MinOperationalJobEventCursor(context.Context) (int64, error) {
	return f.minCursor, nil
}

func (f *fakeOperationalQuerier) ListOperationalJobRuns(_ context.Context, p sqlcgen.ListOperationalJobRunsParams) ([]sqlcgen.JobRun, error) {
	f.calls = append(f.calls, "list-runs")
	f.listParams = p
	return f.runs, nil
}

func (f *fakeOperationalQuerier) CountOperationalJobRuns(_ context.Context, p sqlcgen.CountOperationalJobRunsParams) (int64, error) {
	f.calls = append(f.calls, "count-runs")
	f.countParams = p
	return f.totalRuns, nil
}

func (f *fakeOperationalQuerier) GetOperationalJobRun(context.Context, uuid.UUID) (sqlcgen.JobRun, error) {
	f.calls = append(f.calls, "get-run")
	return f.run, nil
}

func (f *fakeOperationalQuerier) ListOperationalJobEvents(_ context.Context, p sqlcgen.ListOperationalJobEventsParams) ([]sqlcgen.JobEvent, error) {
	f.calls = append(f.calls, "list-events")
	f.eventListParams = p
	return f.events, nil
}

func (f *fakeOperationalQuerier) CountOperationalJobEvents(_ context.Context, p sqlcgen.CountOperationalJobEventsParams) (int64, error) {
	f.calls = append(f.calls, "count-events")
	f.eventCountParams = p
	return f.totalEvts, nil
}

func (f *fakeOperationalQuerier) ListOperationalJobEventsAfter(context.Context, sqlcgen.ListOperationalJobEventsAfterParams) ([]sqlcgen.JobEvent, error) {
	return f.events, nil
}

func (f *fakeOperationalQuerier) OperationalJobQueueMetrics(_ context.Context, staleSeconds int32) ([]sqlcgen.OperationalJobQueueMetricsRow, error) {
	f.staleSeconds = staleSeconds
	return f.health, nil
}

func (f *fakeOperationalQuerier) PruneOperationalJobEvents(_ context.Context, p sqlcgen.PruneOperationalJobEventsParams) (int64, error) {
	f.pruneEvents = p
	return 3, nil
}

func (f *fakeOperationalQuerier) PruneTerminalOperationalJobRuns(_ context.Context, p sqlcgen.PruneTerminalOperationalJobRunsParams) (int64, error) {
	f.pruneRuns = p
	return 2, nil
}

func (f *fakeOperationalQuerier) PruneTerminalPipelineRuns(_ context.Context, p sqlcgen.PruneTerminalPipelineRunsParams) (int64, error) {
	f.prunePipelines = p
	return 1, nil
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
func (f *fakeQuerier) StorageMigrationStats(context.Context) (sqlcgen.StorageMigrationStatsRow, error) {
	return f.storagemi, nil
}
func (f *fakeQuerier) StorageMigrationRecentFailures(context.Context, int32) ([]sqlcgen.StorageMigrationRecentFailuresRow, error) {
	return f.storagemiFails, nil
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
		// storage_migration_objects counts OBJECTS: copying→running,
		// verified+source_deleted→done.
		storagemi: sqlcgen.StorageMigrationStatsRow{Pending: 9, Running: 2, Done: 41, Failed: 1, OldestPendingAgeSeconds: 12},
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
	if len(ov.Queues) != 7 {
		t.Fatalf("want 7 queues, got %d", len(ov.Queues))
	}
	if ov.Queues[0].Queue != QueueTranscode || ov.Queues[0].Pending != 2 || ov.Queues[0].OldestPendingAgeSeconds != 42 {
		t.Errorf("transcode queue = %+v", ov.Queues[0])
	}
	// upload_sessions uses the remapped counts.
	us := ov.Queues[5]
	if us.Queue != QueueUploadSessions || us.Pending != 6 || us.Done != 2 || us.Failed != 1 || us.Running != 0 {
		t.Errorf("upload_sessions queue = %+v", us)
	}
	// storage_migration_objects is last; its unit is the object, not the campaign.
	sm := ov.Queues[6]
	if sm.Queue != QueueStorageMigration || sm.Pending != 9 || sm.Running != 2 || sm.Done != 41 || sm.Failed != 1 {
		t.Errorf("storage_migration_objects queue = %+v", sm)
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
	// 7 queues x 4 states.
	if len(depths) != 28 {
		t.Fatalf("want 28 depth samples, got %d", len(depths))
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

func TestOverviewRedactsLegacyFailureDetail(t *testing.T) {
	q := &fakeQuerier{transcodeFails: []sqlcgen.TranscodeRecentFailuresRow{{
		ID: uuid.New(), Attempts: 5, UpdatedAt: time.Now(),
		Error: "ffmpeg failed for https://storage.test/a?sig=secret user=ada@example.test token:abc",
	}}}
	ov, err := NewService(q).Overview(context.Background())
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(ov.RecentFailures) != 1 {
		t.Fatalf("failures = %d, want 1", len(ov.RecentFailures))
	}
	detail := ov.RecentFailures[0].Error
	for _, secret := range []string{"https://", "sig=secret", "ada@example.test", "token:abc"} {
		if strings.Contains(detail, secret) {
			t.Errorf("legacy failure leaked %q: %q", secret, detail)
		}
	}
}

func TestSafeMetadataRejectsNestedAndSensitiveAllowedValues(t *testing.T) {
	raw := []byte(`{
		"resolver":"direct",
		"planned":12,
		"dry_run":true,
		"reason_code":"token:abc",
		"mode":"https://example.test/run?sig=secret",
		"language":{"payload":"nested"},
		"items":[1,2],
		"unknown":"value"
	}`)
	got := safeMetadata(raw)
	var decoded map[string]any
	if err := json.Unmarshal(got, &decoded); err != nil {
		t.Fatalf("safeMetadata JSON: %v", err)
	}
	if decoded["resolver"] != "direct" || decoded["planned"] != float64(12) || decoded["dry_run"] != true {
		t.Fatalf("safe scalar fields missing: %s", got)
	}
	for _, key := range []string{"reason_code", "mode", "language", "items", "unknown"} {
		if _, ok := decoded[key]; ok {
			t.Errorf("unsafe metadata key/value %q survived: %s", key, got)
		}
	}
}

func TestRedactDetailIsPIISafeBoundedAndUTF8Valid(t *testing.T) {
	detail := "contact ada@example.test at https://example.test/a?token=secret authorization=Bearer-secret " +
		strings.Repeat("é", 3000)
	got := redactDetail(detail)
	if strings.Contains(got, "ada@example.test") || strings.Contains(got, "https://") ||
		strings.Contains(strings.ToLower(got), "authorization=") {
		t.Fatalf("sensitive detail survived: %q", got)
	}
	if len(got) > 2048 {
		t.Fatalf("detail length = %d, want <= 2048", len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatal("bounded detail is not valid UTF-8")
	}
}

func TestEventCursorStaysDecimalString(t *testing.T) {
	e := eventFromRow(sqlcgen.JobEvent{Cursor: int64(^uint64(0) >> 1)})
	if e.Cursor != "9223372036854775807" {
		t.Fatalf("cursor = %q", e.Cursor)
	}
}

func TestOperationalListAndDetailUseHighWaterCursorAndMapRows(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	jobID := uuid.New()
	pipelineID := uuid.New()
	actorID := uuid.New()
	progress := int16(47)
	maxAttempts := int32(5)
	retryable := true
	row := sqlcgen.JobRun{
		ID: jobID, PipelineRunID: pgtype.UUID{Bytes: pipelineID, Valid: true},
		Type: "video_transcode", Queue: "transcode_jobs", SourceID: "hidden-source-id",
		State: StateRunning, Stage: "encoding", ProgressPercent: &progress,
		Priority: 7, Attempt: 2, MaxAttempts: &maxAttempts,
		ActorID: pgtype.UUID{Bytes: actorID, Valid: true}, ResourceType: "video", ResourceID: "video-42",
		RequestID: "req-1", CorrelationID: "corr-1", TraceID: "trace-1", WorkerID: "worker-a",
		InputMetadata:  []byte(`{"mode":"direct","unknown":"drop-me","reason_code":"token:secret"}`),
		OutputMetadata: []byte(`{"width":1920,"height":1080}`),
		ErrorClass:     "process", ErrorCode: "ffmpeg_exit", ErrorDetail: "failed https://signed.test/a?sig=secret for ada@example.test",
		ErrorRetryable: &retryable, CreatedAt: now.Add(-time.Minute), UpdatedAt: now,
	}
	eventID := uuid.New()
	event := sqlcgen.JobEvent{
		Cursor: 9007199254740993, ID: eventID, JobID: jobID,
		PipelineRunID: pgtype.UUID{Bytes: pipelineID, Valid: true}, Kind: "progress",
		State: StateRunning, Stage: "encoding", ProgressPercent: &progress, Attempt: 2,
		WorkerID: "worker-a", Message: "read https://signed.test/a?sig=secret",
		Metadata:  []byte(`{"planned":12,"items":["drop-me"]}`),
		RequestID: "req-1", CorrelationID: "corr-1", TraceID: "trace-1", OccurredAt: now,
	}
	q := &fakeOperationalQuerier{
		maxCursor: event.Cursor, runs: []sqlcgen.JobRun{row}, run: row,
		events: []sqlcgen.JobEvent{event}, totalRuns: 6, totalEvts: 3,
	}
	svc := NewService(q)
	failure := false
	after := now.Add(-24 * time.Hour)
	filter := Filter{State: StateRunning, Type: "video_transcode", Queue: "transcode_jobs", Failure: &failure, CreatedAfter: &after}

	runs, total, cursor, err := svc.ListRuns(context.Background(), filter, 25, 5)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if total != 6 || cursor != event.Cursor || len(runs) != 1 {
		t.Fatalf("ListRuns = runs:%d total:%d cursor:%d", len(runs), total, cursor)
	}
	if strings.Join(q.calls, ",") != "max-cursor,list-runs,count-runs" {
		t.Fatalf("ListRuns call order = %v; cursor must be captured before snapshot", q.calls)
	}
	got := runs[0]
	if got.ID != jobID || got.PipelineRunID != pipelineID.String() || got.ActorID != actorID.String() ||
		got.ProgressPercent == nil || *got.ProgressPercent != progress || got.WorkerID != "worker-a" {
		t.Fatalf("mapped run = %+v", got)
	}
	if string(got.InputMetadata) != `{"mode":"direct"}` || string(got.OutputMetadata) != `{"height":1080,"width":1920}` {
		t.Fatalf("sanitized metadata = input:%s output:%s", got.InputMetadata, got.OutputMetadata)
	}
	if strings.Contains(got.ErrorDetail, "https://") || strings.Contains(got.ErrorDetail, "ada@example.test") {
		t.Fatalf("mapped error detail leaked sensitive data: %q", got.ErrorDetail)
	}
	if q.listParams.State == nil || *q.listParams.State != StateRunning || q.listParams.ResultLimit != 25 ||
		q.listParams.ResultOffset != 5 || !q.listParams.CreatedAfter.Valid || q.countParams.Failure == nil || *q.countParams.Failure {
		t.Fatalf("list/count params = %+v / %+v", q.listParams, q.countParams)
	}

	q.calls = nil
	detail, err := svc.GetDetail(context.Background(), jobID, 10, 2)
	if err != nil {
		t.Fatalf("GetDetail: %v", err)
	}
	if strings.Join(q.calls, ",") != "max-cursor,get-run,list-events,count-events" {
		t.Fatalf("GetDetail call order = %v; cursor must be captured before snapshot", q.calls)
	}
	if detail.EventCursor != event.Cursor || detail.EventsTotal != 3 || detail.Run.ID != jobID || len(detail.Events) != 1 {
		t.Fatalf("detail = %+v", detail)
	}
	gotEvent := detail.Events[0]
	if gotEvent.ID != eventID || gotEvent.Cursor != "9007199254740993" || gotEvent.PipelineRunID != pipelineID.String() {
		t.Fatalf("mapped event = %+v", gotEvent)
	}
	if strings.Contains(gotEvent.Message, "https://") || string(gotEvent.Metadata) != `{"planned":12}` {
		t.Fatalf("sanitized event = message:%q metadata:%s", gotEvent.Message, gotEvent.Metadata)
	}
	if q.eventListParams.ThroughCursor != event.Cursor || q.eventListParams.ResultLimit != 10 ||
		q.eventListParams.ResultOffset != 2 || q.eventCountParams.ThroughCursor != event.Cursor {
		t.Fatalf("event page params = %+v / %+v", q.eventListParams, q.eventCountParams)
	}
}

func TestOperationalQueueHealthAndPruneAdapters(t *testing.T) {
	now := time.Date(2026, 7, 11, 12, 0, 0, 0, time.UTC)
	q := &fakeOperationalQuerier{health: []sqlcgen.OperationalJobQueueMetricsRow{{
		Queue: "transcode_jobs", OldestQueuedAgeSeconds: 123, StaleRunning: 2,
	}}}
	svc := NewService(q)

	health, err := svc.QueueHealth(context.Background(), 7*time.Minute)
	if err != nil {
		t.Fatalf("QueueHealth: %v", err)
	}
	if q.staleSeconds != 420 || len(health) != 1 || health[0].Queue != "transcode_jobs" ||
		health[0].OldestQueuedAgeSeconds != 123 || health[0].StaleRunning != 2 {
		t.Fatalf("QueueHealth = %+v, stale seconds = %d", health, q.staleSeconds)
	}

	events, runs, pipelines, err := svc.Prune(context.Background(), now)
	if err != nil {
		t.Fatalf("Prune: %v", err)
	}
	if events != 3 || runs != 2 || pipelines != 1 {
		t.Fatalf("Prune counts = %d/%d/%d", events, runs, pipelines)
	}
	if q.pruneEvents.BatchSize != 10000 || !q.pruneEvents.Cutoff.Equal(now.Add(-30*24*time.Hour)) {
		t.Fatalf("event prune args = %+v", q.pruneEvents)
	}
	wantRunCutoff := now.Add(-90 * 24 * time.Hour)
	if q.pruneRuns.BatchSize != 10000 || !q.pruneRuns.Cutoff.Valid || !q.pruneRuns.Cutoff.Time.Equal(wantRunCutoff) {
		t.Fatalf("run prune args = %+v", q.pruneRuns)
	}
	if q.prunePipelines.BatchSize != 10000 || !q.prunePipelines.Cutoff.Valid || !q.prunePipelines.Cutoff.Time.Equal(wantRunCutoff) {
		t.Fatalf("pipeline prune args = %+v", q.prunePipelines)
	}
}
