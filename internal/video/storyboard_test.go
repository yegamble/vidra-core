package video

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

// The exported generator is the SAME code the publish paths run, with the one
// difference that matters to a retrying caller: it says why it failed. These
// cover that seam. The best-effort behaviour of the publish paths themselves —
// a storyboard failure never blocking a publish, the storyboards_enabled gate —
// is covered in scanmode_test.go and must stay exactly as it was.

func TestGenerateStoryboardReportsGeneratorFailures(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	boom := errors.New("ffmpeg boom")
	svc := NewService(repo, blobs, WithStoryboarder(fakeStoryboarder{err: boom}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})

	err = svc.GenerateStoryboard(ctx, v.ID, "web-videos/x.mp4", 0)
	if !errors.Is(err, boom) {
		t.Fatalf("GenerateStoryboard error = %v, want the generator's own error", err)
	}
	if svc.HasStoryboard(ctx, v.ID) {
		t.Error("a failed generation stored a storyboard anyway")
	}
	// The unexported wrapper is the same call with the error dropped on purpose —
	// that is what keeps Process publishing through a storyboard failure.
	svc.generateStoryboard(ctx, v.ID, "web-videos/x.mp4", 0)
	if svc.HasStoryboard(ctx, v.ID) {
		t.Error("the best-effort wrapper stored a storyboard from a failing generator")
	}
}

// The permanent failure has to survive the trip intact, because the backfill's
// give-up-immediately decision is an errors.Is on it. A wrapper that stringified
// it would silently turn a one-decode verdict back into five.
func TestGenerateStoryboardPreservesTheUnmeasurableDurationSentinel(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs, WithStoryboarder(fakeStoryboarder{
		err: media.ErrNoMeasurableDuration,
	}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})

	if err := svc.GenerateStoryboard(ctx, v.ID, "web-videos/x.mp4", 0); !errors.Is(err, media.ErrNoMeasurableDuration) {
		t.Fatalf("GenerateStoryboard error = %v, want it to wrap media.ErrNoMeasurableDuration", err)
	}
}

// An ffmpeg that exits 0 having written nothing is not a success. The old
// best-effort code returned early on it; the exported one has to say so, or the
// backfill would clear the ledger row and call a video done that has no sheet.
func TestGenerateStoryboardRejectsAnEmptySheet(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs, WithStoryboarder(fakeStoryboarder{sprite: nil, vtt: []byte("WEBVTT\n")}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})

	if err := svc.GenerateStoryboard(ctx, v.ID, "web-videos/x.mp4", 0); err == nil {
		t.Fatal("GenerateStoryboard returned nil for an empty sprite sheet")
	}
	if svc.HasStoryboard(ctx, v.ID) {
		t.Error("an empty sheet was stored")
	}
}

// No generator wired (no ffmpeg at boot) is its own answer, so a caller can tell
// it apart from a generator that ran and failed.
func TestGenerateStoryboardWithoutAGenerator(t *testing.T) {
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(newFakeRepo(uuid.New()), blobs)
	if err := svc.GenerateStoryboard(context.Background(), uuid.New(), "k", 0); !errors.Is(err, ErrStoryboarderUnavailable) {
		t.Fatalf("GenerateStoryboard error = %v, want ErrStoryboarderUnavailable", err)
	}
}

// The happy path through the exported entry point: both files stored, both rows
// written, and no error.
func TestGenerateStoryboardStoresBothFiles(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, _ := storage.NewLocal(t.TempDir())
	sprite := []byte("\xff\xd8\xff\xe0sprite")
	vtt := []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nstoryboard.jpg#xywh=0,0,160,90\n")
	svc := NewService(repo, blobs, WithStoryboarder(fakeStoryboarder{sprite: sprite, vtt: vtt}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})

	if err := svc.GenerateStoryboard(ctx, v.ID, "web-videos/x.mp4", 42); err != nil {
		t.Fatalf("GenerateStoryboard: %v", err)
	}
	jf, err := svc.FileForView(ctx, v.ID, uuid.Nil, false, "storyboard")
	if err != nil {
		t.Fatalf("FileForView storyboard: %v", err)
	}
	if jf.ContentType != "image/jpeg" || jf.StorageKey != media.StoryboardKeyJPG(v.ID) {
		t.Errorf("unexpected storyboard file: %+v", jf)
	}
	vf, err := svc.FileForView(ctx, v.ID, uuid.Nil, false, "storyboard_vtt")
	if err != nil {
		t.Fatalf("FileForView storyboard_vtt: %v", err)
	}
	if vf.ContentType != "text/vtt" || vf.StorageKey != media.StoryboardKeyVTT(v.ID) {
		t.Errorf("unexpected storyboard vtt file: %+v", vf)
	}
}
