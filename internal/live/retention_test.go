package live

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
)

// writeRecording plants a recording file with a given age.
func writeRecording(t *testing.T, root, name string, age time.Duration) string {
	t.Helper()
	dir := filepath.Join(root, recordingSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte("flv"), 0o600); err != nil {
		t.Fatal(err)
	}
	when := time.Now().Add(-age)
	if err := os.Chtimes(path, when, when); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestRetentionZeroSweepsNothing is the default's contract: at 0 the recording
// is deleted when its replay publishes, and the SWEEP invents no window of its
// own. A sweep that deleted files at a threshold nobody configured is how a
// failed replay's only copy disappears.
func TestRetentionZeroSweepsNothing(t *testing.T) {
	root := t.TempDir()
	id := uuid.New()
	path := writeRecording(t, root, id.String()+"-1000.flv", 400*24*time.Hour)

	svc := NewService(newFakeRepo(uuid.New()),
		WithRecordingStore(NewDirRecordingStore(root)),
		WithRecordingRetention(func() time.Duration { return 0 }))
	n, err := svc.PruneRecordings(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 0 {
		t.Errorf("pruned %d at retention 0; the sweep must do nothing without a configured window", n)
	}
	if _, err := os.Stat(path); err != nil {
		t.Error("a year-old recording was deleted at retention 0, where deletion only ever happens on a successful replay publish")
	}
}

// TestRetentionDeletesPastTheWindowAndKeepsInside it.
func TestRetentionDeletesPastTheWindow(t *testing.T) {
	root := t.TempDir()
	id := uuid.New()
	old := writeRecording(t, root, id.String()+"-1000.flv", 10*24*time.Hour)
	fresh := writeRecording(t, root, id.String()+"-2000.flv", 1*time.Hour)

	svc := NewService(newFakeRepo(uuid.New()),
		WithRecordingStore(NewDirRecordingStore(root)),
		WithRecordingRetention(func() time.Duration { return 7 * 24 * time.Hour }))
	n, err := svc.PruneRecordings(context.Background(), time.Now())
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 1 {
		t.Errorf("pruned %d, want 1", n)
	}
	if _, err := os.Stat(old); !os.IsNotExist(err) {
		t.Error("the 10-day-old recording survived a 7-day retention")
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Error("the one-hour-old recording was deleted inside a 7-day retention")
	}
}

// TestRetentionSweepSpares_keep is the A26 defect this must never reintroduce:
// nginx-rtmp's HLS cleanup rmdir()s an EMPTY recording directory, so the compose
// service plants a `.keep`. A sweep that deleted it would let the directory
// vanish and replay-to-VOD would silently stop running — as it had never run at
// all before A26 found this.
func TestRetentionSweepSparesTheKeepFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, recordingSubdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	keep := filepath.Join(dir, ".keep")
	if err := os.WriteFile(keep, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(keep, old, old); err != nil {
		t.Fatal(err)
	}

	svc := NewService(newFakeRepo(uuid.New()),
		WithRecordingStore(NewDirRecordingStore(root)),
		WithRecordingRetention(func() time.Duration { return time.Hour }))
	if _, err := svc.PruneRecordings(context.Background(), time.Now()); err != nil {
		t.Fatalf("prune: %v", err)
	}
	if _, err := os.Stat(keep); err != nil {
		t.Error(".keep was deleted by the retention sweep; the HLS cleanup will now rmdir() the recording directory and replay-to-VOD stops running")
	}
}

// TestRetentionSweepIsBatchedOldestFirst: a volume that has accumulated
// recordings since install must drain at a bounded rate, oldest first.
func TestRetentionSweepIsBatchedOldestFirst(t *testing.T) {
	root := t.TempDir()
	id := uuid.New()
	store := NewDirRecordingStore(root)
	// Three stale files, ages 3d / 2d / 1d.
	a := writeRecording(t, root, id.String()+"-1.flv", 72*time.Hour)
	b := writeRecording(t, root, id.String()+"-2.flv", 48*time.Hour)
	c := writeRecording(t, root, id.String()+"-3.flv", 24*time.Hour)

	n, err := store.PruneRecordings(time.Now().Add(-time.Hour), 2)
	if err != nil {
		t.Fatalf("prune: %v", err)
	}
	if n != 2 {
		t.Fatalf("pruned %d with batch 2, want 2", n)
	}
	if _, err := os.Stat(a); !os.IsNotExist(err) {
		t.Error("the oldest recording survived a batch that should have started with it")
	}
	if _, err := os.Stat(b); !os.IsNotExist(err) {
		t.Error("the second-oldest recording survived")
	}
	if _, err := os.Stat(c); err != nil {
		t.Error("the newest recording was deleted; the batch was not honoured")
	}
}

// TestRemoveRecordingRefusesAForeignName: the store deletes what it produced, by
// a name it re-validates. A store that unlinks whatever it is handed is one bug
// from deleting an arbitrary path.
func TestRemoveRecordingRefusesAForeignName(t *testing.T) {
	root := t.TempDir()
	mine := uuid.New()
	theirs := uuid.New()
	other := writeRecording(t, root, theirs.String()+"-1.flv", time.Minute)
	store := NewDirRecordingStore(root)

	if err := store.RemoveRecording(mine, theirs.String()+"-1.flv"); err == nil {
		t.Error("removing another stream's recording was accepted")
	}
	if _, err := os.Stat(other); err != nil {
		t.Error("another stream's recording was deleted")
	}
	if err := store.RemoveRecording(mine, "../../etc/passwd"); err == nil {
		t.Error("a path-escaping name was accepted")
	}
	// A file that is already gone is the outcome the caller wanted.
	if err := store.RemoveRecording(mine, mine.String()+"-9.flv"); err != nil {
		t.Errorf("removing an absent recording returned %v, want nil", err)
	}
}
