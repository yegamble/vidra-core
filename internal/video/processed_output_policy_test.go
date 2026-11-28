package video

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/processedupload"
	"github.com/vidra/vidra-core/internal/storage"
)

func TestOnlyReconstructedMediaGetsTheDurableScanPolicy(t *testing.T) {
	owner := uuid.New()
	repo := newFakeRepo(owner)
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	svc := NewService(repo, blobs)
	ctx := t.Context()
	for _, rebuilt := range []bool{true, false} {
		v, err := svc.CreateDraft(ctx, uuid.New(), CreateInput{Title: "fixture", Privacy: "private"})
		if err != nil {
			t.Fatal(err)
		}
		_, file, err := svc.AttachOriginal(ctx, owner, v.ID, UploadInput{
			Filename: "sample.vidra-processed.mp4", ContentType: "video/mp4",
			Reader: strings.NewReader("verified candidate"), Reconstructed: rebuilt,
		})
		if err != nil {
			t.Fatal(err)
		}
		if (file.StorageKey == processedupload.SourceKey(v.ID)) != rebuilt {
			t.Fatalf("reconstructed=%t: unexpected key %q", rebuilt, file.StorageKey)
		}
		art, err := svc.generatedArtwork(ctx, v.ID, file.StorageKey, []byte("poster"))
		if rebuilt {
			if art != nil || !errors.Is(err, processedupload.ErrOutputScannerRequired) {
				t.Fatalf("unscanned reconstructed artwork returned %v, %v", art, err)
			}
		} else if art == nil || err != nil {
			t.Fatalf("ordinary artwork returned %v, %v", art, err)
		} else {
			art.Close()
		}
	}
}

func TestProtectedArtworkScannerFollowsTheStoredSource(t *testing.T) {
	id := uuid.New()
	scanner := artworkTestScanner{seen: map[string]bool{}}
	svc := &Service{}
	WithProtectedArtworkScanner(scanner)(svc)
	for _, key := range []string{"web-videos/" + id.String() + ".mp4", processedupload.SourceKey(id)} {
		body, err := svc.generatedArtwork(context.Background(), id, key, []byte(key))
		if err != nil {
			t.Fatal(err)
		}
		data, err := io.ReadAll(body)
		body.Close()
		if err != nil || string(data) != key || scanner.seen[key] != (key == processedupload.SourceKey(id)) {
			t.Fatalf("wrong scan scope or stored artwork for %q", key)
		}
	}
}
