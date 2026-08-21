package jobrecovery

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// fakeQueries records which queues were swept and lets one of them fail.
type fakeQueries struct {
	called map[string]int
	counts map[string]int64
	errs   map[string]error
}

func newFakeQueries() *fakeQueries {
	return &fakeQueries{called: map[string]int{}, counts: map[string]int64{}, errs: map[string]error{}}
}

func (f *fakeQueries) run(queue string) (int64, error) {
	f.called[queue]++
	return f.counts[queue], f.errs[queue]
}

func (f *fakeQueries) SweepExpiredTranscodeJobs(context.Context) (int64, error) {
	return f.run("transcode_jobs")
}
func (f *fakeQueries) SweepExpiredImportJobs(context.Context) (int64, error) {
	return f.run("import_jobs")
}
func (f *fakeQueries) SweepExpiredCaptionJobs(context.Context) (int64, error) {
	return f.run("caption_jobs")
}
func (f *fakeQueries) SweepExpiredAccountExports(context.Context) (int64, error) {
	return f.run("account_exports")
}
func (f *fakeQueries) SweepExpiredImportRuns(context.Context) (int64, error) {
	return f.run("peertube_import_runs")
}
func (f *fakeQueries) SweepExpiredChannelSyncs(context.Context) (int64, error) {
	return f.run("channel_syncs")
}
func (f *fakeQueries) SweepExpiredStorageMigrationObjects(context.Context) (int64, error) {
	return f.run("storage_migration_objects")
}

// TestSweepVisitsEveryQueue guards the list itself: every queue that claims work
// by flipping a row to running/syncing must be swept, or that queue keeps the
// stranded-forever bug this package exists to fix. A new claiming queue added
// without a sweep statement fails here.
func TestSweepVisitsEveryQueue(t *testing.T) {
	f := newFakeQueries()
	f.counts["transcode_jobs"] = 2
	f.counts["channel_syncs"] = 1

	results := Sweep(context.Background(), f)

	want := []string{
		"transcode_jobs", "import_jobs", "caption_jobs",
		"account_exports", "peertube_import_runs", "channel_syncs",
		"storage_migration_objects",
	}
	if len(results) != len(want) {
		t.Fatalf("got %d results, want %d", len(results), len(want))
	}
	for i, queue := range want {
		if results[i].Queue != queue {
			t.Errorf("result[%d].Queue = %q, want %q (the order is the boot-log order)", i, results[i].Queue, queue)
		}
		if f.called[queue] != 1 {
			t.Errorf("%s was recovered %d times, want exactly 1", queue, f.called[queue])
		}
		if results[i].Err != nil {
			t.Errorf("%s: unexpected error %v", queue, results[i].Err)
		}
	}
	if results[0].Requeued != 2 || results[5].Requeued != 1 {
		t.Errorf("requeued counts = %d/%d, want 2/1 — the counts are what the boot log reports",
			results[0].Requeued, results[5].Requeued)
	}
}

// TestRecoverContinuesPastAFailingQueue proves one broken queue cannot wedge the
// rest. Aborting on the first error would leave (for example) every transcode
// stranded because an unrelated import statement failed — strictly worse than a
// partial recovery, and invisible until someone notices videos never publish.
func TestSweepContinuesPastAFailingQueue(t *testing.T) {
	boom := errors.New("relation does not exist")
	f := newFakeQueries()
	f.errs["import_jobs"] = boom
	f.counts["caption_jobs"] = 3

	results := Sweep(context.Background(), f)

	var failed *Result
	for i := range results {
		if results[i].Err != nil {
			failed = &results[i]
		}
	}
	if failed == nil || failed.Queue != "import_jobs" {
		t.Fatalf("expected exactly the import_jobs result to carry the error, got %+v", results)
	}
	if !errors.Is(failed.Err, boom) {
		t.Errorf("error %v does not wrap the cause", failed.Err)
	}
	if !strings.Contains(failed.Err.Error(), "import_jobs") {
		t.Errorf("error %q does not name the queue that failed", failed.Err)
	}
	// The queues after the failure still ran.
	if f.called["caption_jobs"] != 1 || f.called["channel_syncs"] != 1 {
		t.Error("recovery stopped at the first failing queue")
	}
	for _, r := range results {
		if r.Queue == "caption_jobs" && r.Requeued != 3 {
			t.Errorf("caption_jobs requeued = %d, want 3", r.Requeued)
		}
	}
}
