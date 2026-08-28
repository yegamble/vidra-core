package video

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

// publishWithOriginal drives a draft through attach + process so it is a
// published video with a stored v0 original — the replacement precondition.
func publishWithOriginal(t *testing.T, svc *Service, owner uuid.UUID, content string) uuid.UUID {
	t.Helper()
	ctx := context.Background()
	v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if err != nil {
		t.Fatalf("CreateDraft: %v", err)
	}
	_, file, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
		Filename: "clip.mp4", ContentType: "video/mp4", Reader: strings.NewReader(content),
	})
	if err != nil {
		t.Fatalf("AttachOriginal: %v", err)
	}
	if _, err := svc.Process(ctx, v.ID, file.StorageKey); err != nil {
		t.Fatalf("Process: %v", err)
	}
	return v.ID
}

func blobString(t *testing.T, blobs storage.Backend, key string) string {
	t.Helper()
	rc, err := blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("open %q: %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	b, _ := io.ReadAll(rc)
	return string(b)
}

// TestReplaceSourceSwapsVersionedKeyAndAccounts: the happy path — the new
// source lands at the versioned key, the file row swaps, probed metadata is
// recorded, the daily ledger and update hooks fire, the old blob is retained
// (mediagc's to collect), and the video stays published. A second replacement
// bumps the version again.
func TestReplaceSourceSwapsVersionedKeyAndAccounts(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())

	var recordedOwner uuid.UUID
	var recordedBytes int64
	updates := 0
	svc := NewService(repo, blobs,
		WithProber(fakeProber{md: media.Metadata{DurationSeconds: 42, Width: 1920, Height: 1080}}),
		WithUploadUsageRecorder(func(_ context.Context, ownerID uuid.UUID, bytes int64) error {
			recordedOwner, recordedBytes = ownerID, bytes
			return nil
		}),
		WithUpdateHook(func(context.Context, uuid.UUID) { updates++ }),
	)
	ctx := context.Background()
	id := publishWithOriginal(t, svc, owner, "old source bytes")
	oldKey := "web-videos/" + id.String() + ".mp4"

	const newContent = "brand new source!"
	updated, file, err := svc.ReplaceSource(ctx, owner, id, UploadInput{
		Filename: "v2.webm", ContentType: "video/webm", Reader: strings.NewReader(newContent),
	}, false)
	if err != nil {
		t.Fatalf("ReplaceSource: %v", err)
	}
	if updated.State != "published" {
		t.Errorf("state = %q, want published (a replacement never unpublishes)", updated.State)
	}
	wantKey := "web-videos/" + id.String() + ".r1.webm"
	if file.StorageKey != wantKey {
		t.Errorf("new key = %q, want %q", file.StorageKey, wantKey)
	}
	if got := blobString(t, blobs, wantKey); got != newContent {
		t.Errorf("stored bytes = %q, want %q", got, newContent)
	}
	// The old blob is retained until mediagc collects it (the row no longer
	// references it, but nothing here deletes it).
	if got := blobString(t, blobs, oldKey); got != "old source bytes" {
		t.Errorf("old blob = %q, want retained", got)
	}
	// The file row swapped: exactly one original, pointing at the new key.
	cur, err := svc.OriginalFileKey(ctx, id)
	if err != nil || cur != wantKey {
		t.Errorf("OriginalFileKey = %q, %v; want %q", cur, err, wantKey)
	}
	// Probed metadata recorded.
	if md, ok, _ := svc.GetMetadata(ctx, id); !ok || md.DurationSeconds == nil || *md.DurationSeconds != 42 {
		t.Errorf("metadata after replace = %+v ok=%v, want duration 42", md, ok)
	}
	// Daily-quota ledger recorded against the OWNER with the stored size.
	if recordedOwner != owner || recordedBytes != int64(len(newContent)) {
		t.Errorf("usage recorded = (%s, %d), want (%s, %d)", recordedOwner, recordedBytes, owner, len(newContent))
	}
	// Update hooks fired (federation Update / IPFS re-eval seam).
	if updates != 1 {
		t.Errorf("update hooks fired %d times, want 1", updates)
	}

	// A second replacement bumps to r2.
	_, f2, err := svc.ReplaceSource(ctx, owner, id, UploadInput{
		Filename: "v3.mp4", Reader: strings.NewReader("third"),
	}, false)
	if err != nil {
		t.Fatalf("second ReplaceSource: %v", err)
	}
	if want := "web-videos/" + id.String() + ".r2.mp4"; f2.StorageKey != want {
		t.Errorf("second key = %q, want %q", f2.StorageKey, want)
	}
}

// TestReplaceSourceGates: draft/processing state → ErrReplaceConflict;
// non-owner → ErrForbidden; canManage lets a moderator through; a bad
// extension is ErrUnsupportedMedia (and honors the additional-extensions
// gate).
func TestReplaceSourceGates(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs, WithAdditionalExtGate(func() bool { return false }))
	ctx := context.Background()

	draft, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "d", Privacy: "public"})
	if _, _, err := svc.ReplaceSource(ctx, owner, draft.ID, UploadInput{
		Filename: "n.mp4", Reader: strings.NewReader("x"),
	}, false); !errors.Is(err, ErrReplaceConflict) {
		t.Errorf("draft replace err = %v, want ErrReplaceConflict", err)
	}

	id := publishWithOriginal(t, svc, owner, "src")
	stranger := uuid.New()
	if _, _, err := svc.ReplaceSource(ctx, stranger, id, UploadInput{
		Filename: "n.mp4", Reader: strings.NewReader("x"),
	}, false); !errors.Is(err, ErrForbidden) {
		t.Errorf("non-owner err = %v, want ErrForbidden", err)
	}
	// canManage (moderator/admin) may replace a video they do not own.
	if _, _, err := svc.ReplaceSource(ctx, stranger, id, UploadInput{
		Filename: "n.mp4", Reader: strings.NewReader("x"),
	}, true); err != nil {
		t.Errorf("canManage replace err = %v, want nil", err)
	}
	// Extension gate, with the additional set disabled: .mkv refused.
	if _, _, err := svc.ReplaceSource(ctx, owner, id, UploadInput{
		Filename: "n.mkv", Reader: strings.NewReader("x"),
	}, false); !errors.Is(err, ErrUnsupportedMedia) {
		t.Errorf("gated ext err = %v, want ErrUnsupportedMedia", err)
	}
}

// TestReplaceSourceRejectionsKeepCurrentSource: an infected scan, an
// unscannable file under fail-closed AND quarantine modes, and a failed probe
// all reject the replacement — the current source row/blob stay live and the
// new blob is dropped. Fail-open accepts an unscannable file (loud log).
func TestReplaceSourceRejectionsKeepCurrentSource(t *testing.T) {
	cases := []struct {
		name   string
		opts   []Option
		reject bool
	}{
		{"infected", []Option{WithScanner(fakeScanner{clean: false})}, true},
		{"scan error fail-closed", []Option{WithScanner(fakeScanner{err: errors.New("clamd down")})}, true},
		{"scan error quarantine mode", []Option{WithScanner(fakeScanner{err: errors.New("clamd down")}), WithScanMode("quarantine")}, true},
		{"scan error fail-open proceeds", []Option{WithScanner(fakeScanner{err: errors.New("clamd down")}), WithScanMode("fail-open")}, false},
		{"probe failure", []Option{WithProber(fakeProber{err: errors.New("not media")})}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			owner := uuid.New()
			repo := newFakeRepo(owner)
			blobs, _ := storage.NewLocal(t.TempDir())
			svc := NewService(repo, blobs, tc.opts...)
			ctx := context.Background()

			// Publish WITHOUT the rejecting seams interfering: attach+process
			// happen with clean fakes on a parallel service sharing the repo.
			plain := NewService(repo, blobs)
			id := publishWithOriginal(t, plain, owner, "current")
			oldKey := "web-videos/" + id.String() + ".mp4"

			_, _, err := svc.ReplaceSource(ctx, owner, id, UploadInput{
				Filename: "next.mp4", Reader: strings.NewReader("candidate"),
			}, false)

			newKey := "web-videos/" + id.String() + ".r1.mp4"
			if tc.reject {
				var rre *ReplaceRejectedError
				if !errors.As(err, &rre) {
					t.Fatalf("err = %v, want ReplaceRejectedError", err)
				}
				// Current source untouched, candidate blob dropped.
				if cur, _ := svc.OriginalFileKey(ctx, id); cur != oldKey {
					t.Errorf("original key = %q, want untouched %q", cur, oldKey)
				}
				if ok, _ := blobs.Exists(ctx, newKey); ok {
					t.Errorf("rejected candidate blob %q still stored", newKey)
				}
				return
			}
			if err != nil {
				t.Fatalf("ReplaceSource err = %v, want accepted", err)
			}
			if cur, _ := svc.OriginalFileKey(ctx, id); cur != newKey {
				t.Errorf("original key = %q, want swapped to %q", cur, newKey)
			}
		})
	}
}

// TestReplaceSourceBillsOncePerStoredSource: the upload-finalize worker retries a
// replace-purpose session, so a failure after the swap re-runs ReplaceSource over
// the same bytes. The ledger must see one event, not one per attempt.
func TestReplaceSourceBillsOncePerStoredSource(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, _ := storage.NewLocal(t.TempDir())

	var billed []int64
	svc := NewService(repo, blobs,
		WithUploadUsageRecorder(func(_ context.Context, _ uuid.UUID, bytes int64) error {
			billed = append(billed, bytes)
			return nil
		}),
	)
	ctx := context.Background()
	id := publishWithOriginal(t, svc, owner, "old source bytes")
	billed = nil // the initial upload's event is not what this is about

	const newContent = "brand new source!"
	replace := func(body string) {
		t.Helper()
		if _, _, err := svc.ReplaceSource(ctx, owner, id, UploadInput{
			Filename: "v2.mp4", ContentType: "video/mp4", Reader: strings.NewReader(body),
		}, false); err != nil {
			t.Fatalf("ReplaceSource(%q): %v", body, err)
		}
	}

	replace(newContent)
	if len(billed) != 1 || billed[0] != int64(len(newContent)) {
		t.Fatalf("first replace billed %v, want one event of %d bytes", billed, len(newContent))
	}
	// The retry: same session, same bytes.
	replace(newContent)
	if len(billed) != 1 {
		t.Errorf("a retried replace billed again (%v) — one replacement must not bill twice", billed)
	}
	// A genuinely different source is a new original and bills.
	const third = "a third, different source"
	replace(third)
	if len(billed) != 2 || billed[1] != int64(len(third)) {
		t.Errorf("a different source billed %v, want a second event of %d bytes", billed, len(third))
	}
}
