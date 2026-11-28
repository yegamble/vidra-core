package video

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/processedupload"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

type artworkTestScanner struct{ seen map[string]bool }

func (s artworkTestScanner) ScanReader(_ context.Context, body io.Reader) (bool, error) {
	data, err := io.ReadAll(body)
	if err != nil {
		return false, err
	}
	value := string(data)
	s.seen[value] = true
	if strings.HasPrefix(value, "unavailable") {
		return false, errors.New("scanner unavailable")
	}
	return !strings.HasPrefix(value, "rejected"), nil
}

func TestGeneratedArtworkScansBeforeReplacingPublicFiles(t *testing.T) {
	ctx := context.Background()
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	poster := &capturingThumbnailer{jpg: []byte("old poster")}
	scanner := artworkTestScanner{seen: make(map[string]bool)}
	svc := NewService(repo, blobs, WithGeneratedArtworkScanner(scanner), WithThumbnailer(poster),
		WithProber(fakeProber{md: media.Metadata{DurationSeconds: 10, Width: 320, Height: 240}}),
		WithStoryboarder(fakeStoryboarder{sprite: []byte("old sheet"), vtt: []byte("old cues")}))
	id, source := processedVideo(t, svc, owner, 10)
	svc.generateThumbnail(ctx, id, source, 10)
	if err := svc.GenerateStoryboard(ctx, id, source, 10); err != nil {
		t.Fatal(err)
	}
	assertFile := func(kind, want string) {
		t.Helper()
		f, err := repo.GetVideoFileByKind(ctx, sqlcgen.GetVideoFileByKindParams{VideoID: id, Kind: kind})
		if err != nil {
			t.Fatal(err)
		}
		rc, err := blobs.Open(ctx, f.StorageKey)
		if err != nil {
			t.Fatal(err)
		}
		defer rc.Close()
		data, err := io.ReadAll(rc)
		if err != nil || string(data) != want || f.Sha256 != fmt.Sprintf("%x", sha256.Sum256(data)) || !scanner.seen[want] {
			t.Fatalf("%s: bytes %q, recorded hash %q, scanned %t, error %v", kind, data, f.Sha256, scanner.seen[want], err)
		}
	}
	assertFile("thumbnail", "old poster")
	assertFile("storyboard", "old sheet")
	assertFile("storyboard_vtt", "old cues")

	// Automatic poster generation is best effort. The creator's frame picker
	// reports its error. Neither path can remove or replace the prior poster.
	poster.jpg = []byte("unavailable poster")
	svc.generateThumbnail(ctx, id, source, 10)
	poster.jpg = []byte("rejected frame")
	if _, err := svc.SetThumbnailFromFrame(ctx, owner, id, 5); !errors.Is(err, processedupload.ErrInfected) {
		t.Fatalf("frame picker returned %v", err)
	}
	assertFile("thumbnail", "old poster")

	// The map is scanned second. Its failure must preserve the old pair
	// even though the newly generated JPG has already passed its own scan.
	svc.storyboarder = fakeStoryboarder{sprite: []byte("new sheet"), vtt: []byte("rejected cues")}
	if err := svc.GenerateStoryboard(ctx, id, source, 10); !errors.Is(err, processedupload.ErrInfected) {
		t.Fatalf("storyboard returned %v", err)
	}
	assertFile("storyboard", "old sheet")
	assertFile("storyboard_vtt", "old cues")

	poster.jpg = []byte("new frame")
	if _, err := svc.SetThumbnailFromFrame(ctx, owner, id, 5); err != nil {
		t.Fatal(err)
	}
	svc.storyboarder = fakeStoryboarder{sprite: []byte("new sheet"), vtt: []byte("new cues")}
	if err := svc.GenerateStoryboard(ctx, id, source, 10); err != nil {
		t.Fatal(err)
	}
	assertFile("thumbnail", "new frame")
	assertFile("storyboard", "new sheet")
	assertFile("storyboard_vtt", "new cues")
}
