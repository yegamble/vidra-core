package video

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
)

func TestAudioContainersAccepted(t *testing.T) {
	for _, ext := range []string{".mp3", ".m4a", ".m4b", ".aac", ".flac", ".wav", ".opus", ".ogg"} {
		// Base set: accepted even with the additional-extensions gate OFF.
		if got, ok := AcceptedVideoExtGated("track"+ext, false); !ok || got != ext {
			t.Errorf("AcceptedVideoExtGated(track%s, false) = (%q, %v), want (%q, true)", ext, got, ok, ext)
		}
	}
	if _, ok := AcceptedVideoExt("TRACK.MP3"); !ok {
		t.Error("uppercase .MP3 must be accepted")
	}
}

func TestAudioContentTypesAccepted(t *testing.T) {
	for ct, want := range map[string]string{
		"audio/mpeg": ".mp3", "audio/mp4": ".m4a", "audio/x-m4a": ".m4a", "audio/aac": ".aac",
		"audio/flac": ".flac", "audio/x-flac": ".flac", "audio/wav": ".wav", "audio/x-wav": ".wav",
		"audio/ogg": ".ogg", "audio/opus": ".opus", "audio/mpeg; foo=bar": ".mp3",
	} {
		ext, ok := extForContentType(ct)
		if !ok || ext != want {
			t.Errorf("extForContentType(%q) = (%q, %v), want (%q, true)", ct, ext, ok, want)
		}
		if _, ok := AcceptedVideoExtGated("x"+ext, false); !ok {
			t.Errorf("%q maps to %q which the base gate refuses", ct, ext)
		}
	}
}

type countingThumb struct{ n *atomic.Int32 }

func (c countingThumb) Thumbnail(context.Context, string, int) ([]byte, error) {
	c.n.Add(1)
	return []byte("jpg"), nil
}
func (c countingThumb) ThumbnailAt(context.Context, string, float64) ([]byte, error) {
	return nil, nil
}

type countingBoard struct{ n *atomic.Int32 }

func (c countingBoard) Storyboard(context.Context, string, int) ([]byte, []byte, error) {
	c.n.Add(1)
	return []byte("s"), []byte("WEBVTT"), nil
}

func processWithMeta(t *testing.T, md media.Metadata) (state string, thumbs, boards int32) {
	t.Helper()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	var tn, bn atomic.Int32
	svc := NewService(newFakeRepo(uuid.New()), blobs,
		WithProber(fakeProber{md: md}),
		WithThumbnailer(countingThumb{&tn}),
		WithStoryboarder(countingBoard{&bn}))
	ctx := context.Background()
	v, _ := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "t", Privacy: "public"})
	got, err := svc.Process(ctx, v.ID, "web-videos/x.mp3")
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	return got.State, tn.Load(), bn.Load()
}

func TestProcessAudioOnlyPublishesWithoutFrameExtraction(t *testing.T) {
	state, thumbs, boards := processWithMeta(t, media.Metadata{DurationSeconds: 60, HasAudio: true})
	if state != "published" {
		t.Fatalf("audio-only state = %q, want published", state)
	}
	if thumbs != 0 || boards != 0 {
		t.Errorf("audio-only ran thumbnail=%d storyboard=%d, want 0/0", thumbs, boards)
	}
}

func TestProcessVideoStillExtractsFrames(t *testing.T) {
	state, thumbs, boards := processWithMeta(t, media.Metadata{DurationSeconds: 60, Width: 320, Height: 240, HasAudio: true})
	if state != "published" || thumbs != 1 || boards != 1 {
		t.Errorf("video: state=%q thumbs=%d boards=%d, want published/1/1", state, thumbs, boards)
	}
}
