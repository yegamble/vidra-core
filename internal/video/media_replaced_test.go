package video

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// The poster and the storyboard sprite are the two media objects this service
// replaces IN PLACE at a key whose URL never changes. Everything else it writes
// is either generation-addressed (the ladder, since 0136) or removed outright,
// so these two are the only ones a shared cache can be left holding after the
// origin has moved on — measured, with the origin at one digest and the edge
// still at the previous one (docs/release-readiness.md, "A32/A33 delivery").
//
// WithMediaReplacedHook is the seam that lets a CDN invalidation see them. It is
// on the SERVICE and not on the HTTP handler for a reason these tests are the
// record of: the storyboard's write sites include the publish path, a source
// replacement and internal/storyboardbackfill's worker, and that last one runs
// in a process where no HTTP server exists at all.

type replacementLog struct {
	mu    sync.Mutex
	kinds []string
	ids   []uuid.UUID
}

func (r *replacementLog) hook(_ context.Context, videoID uuid.UUID, kind string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ids = append(r.ids, videoID)
	r.kinds = append(r.kinds, kind)
}

func (r *replacementLog) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.kinds...)
}

func TestThumbnailUploadFiresTheReplacementHook(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	log := &replacementLog{}
	svc := NewService(repo, blobs, WithMediaReplacedHook(log.hook))
	ctx := context.Background()

	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if _, err := svc.SetThumbnail(ctx, owner, v.ID, UploadInput{
		Filename: "poster.jpg", Reader: strings.NewReader("bytes"),
	}); err != nil {
		t.Fatalf("SetThumbnail: %v", err)
	}

	got := log.snapshot()
	if len(got) != 1 || got[0] != "thumbnail" {
		t.Fatalf("hook fired %v, want exactly [thumbnail]", got)
	}
	if log.ids[0] != v.ID {
		t.Fatalf("hook got video %s, want %s", log.ids[0], v.ID)
	}
}

// A PRIVATE video's poster was never handed to a shared cache — delivery's own
// Eligible fence is public AND published — so replacing it must spend nothing.
func TestPrivateVideoThumbnailDoesNotFireTheHook(t *testing.T) {
	owner := uuid.New()
	blobs, _ := storage.NewLocal(t.TempDir())
	log := &replacementLog{}
	svc := NewService(newFakeRepo(owner), blobs, WithMediaReplacedHook(log.hook))
	ctx := context.Background()

	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "private"})
	if _, err := svc.Process(ctx, v.ID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if _, err := svc.SetThumbnail(ctx, owner, v.ID, UploadInput{
		Filename: "poster.jpg", Reader: strings.NewReader("bytes"),
	}); err != nil {
		t.Fatalf("SetThumbnail: %v", err)
	}
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("hook fired %v for a private video, want nothing", got)
	}
}

// An UNPUBLISHED (draft) video is the other half of the same fence.
func TestDraftThumbnailDoesNotFireTheHook(t *testing.T) {
	owner := uuid.New()
	blobs, _ := storage.NewLocal(t.TempDir())
	log := &replacementLog{}
	svc := NewService(newFakeRepo(owner), blobs, WithMediaReplacedHook(log.hook))
	ctx := context.Background()

	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.SetThumbnail(ctx, owner, v.ID, UploadInput{
		Filename: "poster.jpg", Reader: strings.NewReader("bytes"),
	}); err != nil {
		t.Fatalf("SetThumbnail: %v", err)
	}
	if got := log.snapshot(); len(got) != 0 {
		t.Fatalf("hook fired %v for an unpublished draft, want nothing", got)
	}
}

// The storyboard is the write site a per-handler hook would have missed: this
// is GenerateStoryboard, which the backfill worker calls directly.
func TestStoryboardRegenerationFiresTheReplacementHook(t *testing.T) {
	owner := uuid.New()
	blobs, _ := storage.NewLocal(t.TempDir())
	log := &replacementLog{}
	svc := NewService(newFakeRepo(owner), blobs,
		WithStoryboarder(fakeStoryboarder{sprite: []byte("sheet"), vtt: []byte("WEBVTT")}),
		WithMediaReplacedHook(log.hook))
	ctx := context.Background()

	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	before := len(log.snapshot())

	if err := svc.GenerateStoryboard(ctx, v.ID, "web-videos/x.mp4", 10); err != nil {
		t.Fatalf("GenerateStoryboard: %v", err)
	}
	got := log.snapshot()
	if len(got) != before+1 || got[len(got)-1] != "storyboard" {
		t.Fatalf("hook fired %v, want a trailing [storyboard]", got)
	}
}

// A FAILED generation replaces nothing, so it must invalidate nothing: purging a
// live sprite because a regeneration failed would cold-start every viewer's seek
// preview to fix a problem that did not happen.
func TestFailedStoryboardGenerationDoesNotFireTheHook(t *testing.T) {
	owner := uuid.New()
	blobs, _ := storage.NewLocal(t.TempDir())
	log := &replacementLog{}
	svc := NewService(newFakeRepo(owner), blobs,
		WithStoryboarder(fakeStoryboarder{err: errStoryboardTest}),
		WithMediaReplacedHook(log.hook))
	ctx := context.Background()

	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	_ = svc.GenerateStoryboard(ctx, v.ID, "web-videos/x.mp4", 10)
	for _, k := range log.snapshot() {
		if k == "storyboard" {
			t.Fatal("a failed storyboard generation fired the replacement hook")
		}
	}
}

// errStoryboardTest is the generator failure used above; a package-level value
// so the assertion is about the hook, not about error identity.
var errStoryboardTest = errors.New("ffmpeg boom")
