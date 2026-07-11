package video

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// scan-error behaviour across the three MALWARE_SCAN_MODE policies. An infected
// result always fails, independent of mode (covered separately). These cover the
// scan-ERROR path.

func TestProcessScanErrorFailClosed(t *testing.T) {
	// Default (no WithScanMode) is fail-closed: a scan error → failed.
	svc := NewService(newFakeRepo(uuid.New()), nil, WithScanner(fakeScanner{err: errors.New("clamd down")}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "failed" {
		t.Errorf("state = %q, want failed (fail-closed scan error)", got.State)
	}
}

func TestProcessScanErrorFailOpenPublishes(t *testing.T) {
	// fail-open: a scan error publishes anyway.
	svc := NewService(newFakeRepo(uuid.New()), nil,
		WithScanner(fakeScanner{err: errors.New("clamd down")}),
		WithScanMode("fail-open"),
	)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "published" {
		t.Errorf("state = %q, want published (fail-open scan error)", got.State)
	}
}

func TestProcessScanErrorFailOpenStillFailsInfected(t *testing.T) {
	// fail-open never rescues an INFECTED result — infection always fails.
	svc := NewService(newFakeRepo(uuid.New()), nil,
		WithScanner(fakeScanner{clean: false}),
		WithScanMode("fail-open"),
	)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "failed" {
		t.Errorf("state = %q, want failed (infected under fail-open)", got.State)
	}
}

func TestProcessScanErrorQuarantines(t *testing.T) {
	// quarantine: a scan error parks the upload in 'quarantined' for review, and
	// the transcode hook must NOT fire (no publish).
	transcoded := false
	svc := NewService(newFakeRepo(uuid.New()), nil,
		WithScanner(fakeScanner{err: errors.New("clamd down")}),
		WithScanMode("quarantine"),
		WithTranscodeHook(func(context.Context, uuid.UUID, string) { transcoded = true }),
	)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "quarantined" {
		t.Errorf("state = %q, want quarantined (scan error under quarantine mode)", got.State)
	}
	if transcoded {
		t.Error("transcode hook fired for a quarantined upload")
	}
}

func TestProcessScanErrorQuarantineStillFailsInfected(t *testing.T) {
	// Even in quarantine mode, an INFECTED result fails outright (not quarantined).
	svc := NewService(newFakeRepo(uuid.New()), nil,
		WithScanner(fakeScanner{clean: false}),
		WithScanMode("quarantine"),
	)
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "failed" {
		t.Errorf("state = %q, want failed (infected under quarantine mode)", got.State)
	}
}

func TestValidScanMode(t *testing.T) {
	for _, m := range []string{"fail-closed", "fail-open", "quarantine"} {
		if !ValidScanMode(m) {
			t.Errorf("ValidScanMode(%q) = false, want true", m)
		}
	}
	for _, m := range []string{"", "open", "closed", "FAIL-OPEN"} {
		if ValidScanMode(m) {
			t.Errorf("ValidScanMode(%q) = true, want false", m)
		}
	}
}

// fakeStoryboarder returns fixed sprite/vtt bytes.
type fakeStoryboarder struct {
	sprite []byte
	vtt    []byte
	err    error
}

func (f fakeStoryboarder) Storyboard(context.Context, string, int) ([]byte, []byte, error) {
	return f.sprite, f.vtt, f.err
}

func TestProcessStoresStoryboard(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	sprite := []byte("\xff\xd8\xff\xe0sprite")
	vtt := []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nstoryboard.jpg#xywh=0,0,160,90\n")
	svc := NewService(repo, blobs, WithStoryboarder(fakeStoryboarder{sprite: sprite, vtt: vtt}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	if _, err := svc.Process(ctx, v.ID, "web-videos/x.mp4"); err != nil {
		t.Fatalf("Process: %v", err)
	}
	if !svc.HasStoryboard(ctx, v.ID) {
		t.Fatal("HasStoryboard = false, want true")
	}
	jf, err := svc.FileForView(ctx, v.ID, uuid.Nil, false, "storyboard")
	if err != nil {
		t.Fatalf("FileForView storyboard: %v", err)
	}
	if jf.ContentType != "image/jpeg" || jf.StorageKey != "storyboards/"+v.ID.String()+".jpg" {
		t.Errorf("unexpected storyboard file: %+v", jf)
	}
	vf, err := svc.FileForView(ctx, v.ID, uuid.Nil, false, "storyboard_vtt")
	if err != nil {
		t.Fatalf("FileForView storyboard_vtt: %v", err)
	}
	if vf.ContentType != "text/vtt" || vf.StorageKey != "storyboards/"+v.ID.String()+".vtt" {
		t.Errorf("unexpected storyboard vtt file: %+v", vf)
	}
}

func TestProcessStoryboardFailureStillPublishes(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, _ := storage.NewLocal(t.TempDir())
	svc := NewService(repo, blobs, WithStoryboarder(fakeStoryboarder{err: errors.New("ffmpeg boom")}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "k")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got.State != "published" {
		t.Errorf("state = %q, want published (storyboard failure is non-fatal)", got.State)
	}
	if svc.HasStoryboard(ctx, v.ID) {
		t.Error("storyboard stored despite generator error")
	}
}

// TestProcessStoryboardGate covers the storyboards_enabled runtime gate
// (config-parity W8): gate off → publish proceeds with NO storyboard; the gate
// is re-read per Process so an admin flip applies without reconstruction.
func TestProcessStoryboardGate(t *testing.T) {
	repo := newFakeRepo(uuid.New())
	blobs, _ := storage.NewLocal(t.TempDir())
	sprite := []byte("\xff\xd8\xff\xe0sprite")
	vtt := []byte("WEBVTT\n\n00:00:00.000 --> 00:00:01.000\nstoryboard.jpg#xywh=0,0,160,90\n")
	enabled := false
	svc := NewService(repo, blobs,
		WithStoryboarder(fakeStoryboarder{sprite: sprite, vtt: vtt}),
		WithStoryboardGate(func() bool { return enabled }))
	ctx := context.Background()

	// Gate OFF: publishes fine, generates nothing.
	v1, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "off", Privacy: "public"})
	got, err := svc.Process(ctx, v1.ID, "web-videos/a.mp4")
	if err != nil {
		t.Fatalf("Process (gate off): %v", err)
	}
	if got.State != "published" {
		t.Fatalf("state = %q, want published", got.State)
	}
	if svc.HasStoryboard(ctx, v1.ID) {
		t.Error("storyboard generated while the gate is off")
	}

	// Gate ON (runtime flip): the next publish generates one.
	enabled = true
	v2, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "on", Privacy: "public"})
	if _, err := svc.Process(ctx, v2.ID, "web-videos/b.mp4"); err != nil {
		t.Fatalf("Process (gate on): %v", err)
	}
	if !svc.HasStoryboard(ctx, v2.ID) {
		t.Error("storyboard missing with the gate on")
	}
}
