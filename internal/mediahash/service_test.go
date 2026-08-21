package mediahash

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// helloWorldSHA256 is the published SHA-256 of "hello world", pinned as a
// literal so the test checks the digest rather than restating how it is taken.
const (
	helloWorld       = "hello world"
	helloWorldSHA256 = "b94d27b9934d3e08a52e52d7da7dabfac484efe37a5380ee9088f7ace2efcde9"
)

type fakeRepo struct {
	rows    []sqlcgen.ListUnhashedVideoFilesRow
	written map[uuid.UUID]string
	listErr error
}

func newFakeRepo() *fakeRepo { return &fakeRepo{written: map[uuid.UUID]string{}} }

func (f *fakeRepo) add(key string) uuid.UUID {
	id := uuid.New()
	f.rows = append(f.rows, sqlcgen.ListUnhashedVideoFilesRow{ID: id, StorageKey: key})
	return id
}

func (f *fakeRepo) ListUnhashedVideoFiles(_ context.Context, limit int32) ([]sqlcgen.ListUnhashedVideoFilesRow, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	var out []sqlcgen.ListUnhashedVideoFilesRow
	for _, r := range f.rows {
		if _, done := f.written[r.ID]; done {
			continue
		}
		if int32(len(out)) == limit {
			break
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *fakeRepo) SetVideoFileSHA256(_ context.Context, arg sqlcgen.SetVideoFileSHA256Params) error {
	f.written[arg.ID] = arg.Sha256
	return nil
}

// unreadable fails Open with something that is NOT ErrNotFound, standing in for
// a timeout or a transient object-store error.
type unreadable struct{ storage.Backend }

func (unreadable) Open(context.Context, string) (io.ReadCloser, error) {
	return nil, errors.New("object store unavailable")
}

func TestBackfillHashesStoredObjects(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := newFakeRepo()
	const key = "web-videos/present.mp4"
	if _, err := blobs.Put(ctx, key, strings.NewReader(helloWorld)); err != nil {
		t.Fatal(err)
	}
	id := repo.add(key)

	res, err := NewService(repo, blobs, nil).BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}
	if res.Scanned != 1 || res.Hashed != 1 || res.Missing != 0 || res.Failed != 0 {
		t.Fatalf("result = %+v, want one row hashed", res)
	}
	if got := repo.written[id]; got != helloWorldSHA256 {
		t.Errorf("recorded sha256 = %q, want %q", got, helloWorldSHA256)
	}
}

// A row whose object the store does not have gets the sentinel, not a retry
// forever: that is what lets the backfill ever report completion, and what a
// later consistency check reads to find dangling references.
func TestBackfillMarksMissingObjects(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := newFakeRepo()
	id := repo.add("web-videos/gone.mp4")

	res, err := NewService(repo, blobs, nil).BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}
	if res.Missing != 1 || res.Hashed != 0 {
		t.Fatalf("result = %+v, want one missing", res)
	}
	if got := repo.written[id]; got != SentinelMissing {
		t.Errorf("recorded sha256 = %q, want the %q sentinel", got, SentinelMissing)
	}
}

// A read failure that is not "not found" must leave the row alone so the next
// tick retries it — and must not stop the pass.
func TestBackfillLeavesUnreadableRowsForTheNextPass(t *testing.T) {
	ctx := context.Background()
	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := newFakeRepo()
	id := repo.add("web-videos/flaky.mp4")

	res, err := NewService(repo, unreadable{Backend: local}, nil).BackfillOnce(ctx, 25)
	if err != nil {
		t.Fatalf("BackfillOnce returned an error for a per-row failure: %v", err)
	}
	if res.Failed != 1 || res.Hashed != 0 || res.Missing != 0 {
		t.Fatalf("result = %+v, want one failure", res)
	}
	if _, written := repo.written[id]; written {
		t.Error("an unreadable object was recorded; it must stay unhashed and be retried")
	}
}

// The drained state — what the worker's completion log is keyed on.
func TestBackfillReportsNothingLeftToDo(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	res, err := NewService(newFakeRepo(), blobs, nil).BackfillOnce(context.Background(), 25)
	if err != nil {
		t.Fatalf("BackfillOnce: %v", err)
	}
	if res.Scanned != 0 {
		t.Errorf("scanned = %d on an empty backlog, want 0", res.Scanned)
	}
}

// A database error is the one thing that aborts the pass: a pass whose scan
// failed has not established anything about the library.
func TestBackfillReturnsScanErrors(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	repo := newFakeRepo()
	repo.listErr = errors.New("connection reset")
	if _, err := NewService(repo, blobs, nil).BackfillOnce(context.Background(), 25); err == nil {
		t.Fatal("BackfillOnce swallowed a scan error")
	}
}
