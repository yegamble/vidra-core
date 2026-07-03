package mediagc

import (
	"context"
	"sort"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/media"
	"github.com/vidra/vidra-core/internal/storage"
	"github.com/vidra/vidra-core/internal/store/sqlcgen"
)

// fakeRepo returns fixed reference sets.
type fakeRepo struct {
	fileKeys []string
	capKeys  []string
	videoIDs []uuid.UUID
	plThumbs []sqlcgen.ListPlaylistThumbnailRefsRow
}

func (f *fakeRepo) ListAllVideoFileKeys(context.Context) ([]string, error) { return f.fileKeys, nil }
func (f *fakeRepo) ListAllCaptionKeys(context.Context) ([]string, error)   { return f.capKeys, nil }
func (f *fakeRepo) ListAllVideoIDs(context.Context) ([]uuid.UUID, error)   { return f.videoIDs, nil }
func (f *fakeRepo) ListPlaylistThumbnailRefs(context.Context) ([]sqlcgen.ListPlaylistThumbnailRefsRow, error) {
	return f.plThumbs, nil
}

func put(t *testing.T, b storage.Backend, key string) {
	t.Helper()
	if _, err := b.Put(context.Background(), key, strings.NewReader("x")); err != nil {
		t.Fatalf("put %q: %v", key, err)
	}
}

func exists(t *testing.T, b storage.Backend, key string) bool {
	t.Helper()
	ok, err := b.Exists(context.Background(), key)
	if err != nil {
		t.Fatalf("exists %q: %v", key, err)
	}
	return ok
}

func TestSweepFindsAndDeletesOrphans(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	liveVid := uuid.New()
	deadVid := uuid.New()
	livePl := uuid.New()
	jpg := "jpg"

	// Referenced objects.
	origKey := "web-videos/" + liveVid.String() + ".mp4"
	thumbKey := "thumbnails/" + liveVid.String() + ".jpg"
	capKey := "captions/" + liveVid.String() + "/en.vtt"
	plThumbKey := media.PlaylistThumbnailKey(livePl, jpg)
	hlsMaster := "streaming-playlists/" + liveVid.String() + "/master.m3u8"
	hlsSeg := "streaming-playlists/" + liveVid.String() + "/720p/seg_00000.ts"
	for _, k := range []string{origKey, thumbKey, capKey, plThumbKey, hlsMaster, hlsSeg} {
		put(t, blobs, k)
	}

	// Orphans (no DB reference).
	orphanOrig := "web-videos/orphan.mp4"
	orphanThumb := "thumbnails/gone.jpg"
	orphanCap := "captions/" + uuid.New().String() + "/fr.vtt"
	orphanPl := media.PlaylistThumbnailKey(uuid.New(), "png")
	deadHLS := "streaming-playlists/" + deadVid.String() + "/master.m3u8"
	for _, k := range []string{orphanOrig, orphanThumb, orphanCap, orphanPl, deadHLS} {
		put(t, blobs, k)
	}

	// Unknown prefixes the sweep must NEVER touch.
	untouched := []string{"avatars/" + uuid.New().String() + ".jpg", "uploads/sess/0"}
	for _, k := range untouched {
		put(t, blobs, k)
	}

	repo := &fakeRepo{
		fileKeys: []string{origKey, thumbKey},
		capKeys:  []string{capKey},
		videoIDs: []uuid.UUID{liveVid},
		plThumbs: []sqlcgen.ListPlaylistThumbnailRefsRow{{ID: livePl, ThumbnailExt: &jpg}},
	}
	svc := NewService(repo, blobs)

	// Dry run: reports orphans, deletes nothing.
	res, err := svc.Sweep(ctx, true)
	if err != nil {
		t.Fatalf("dry-run sweep: %v", err)
	}
	if !res.DryRun || res.Deleted != 0 {
		t.Fatalf("dry-run: DryRun=%v Deleted=%d, want true/0", res.DryRun, res.Deleted)
	}
	wantOrphans := []string{orphanOrig, orphanThumb, orphanCap, orphanPl, deadHLS}
	sort.Strings(wantOrphans)
	if strings.Join(res.Orphans, "|") != strings.Join(wantOrphans, "|") {
		t.Fatalf("dry-run orphans:\n got %v\nwant %v", res.Orphans, wantOrphans)
	}
	// Nothing deleted yet.
	for _, k := range wantOrphans {
		if !exists(t, blobs, k) {
			t.Errorf("dry run deleted %q", k)
		}
	}

	// Real run: deletes the orphans, keeps referenced + unknown-prefix objects.
	res, err = svc.Sweep(ctx, false)
	if err != nil {
		t.Fatalf("delete sweep: %v", err)
	}
	if res.Deleted != len(wantOrphans) {
		t.Fatalf("deleted=%d, want %d", res.Deleted, len(wantOrphans))
	}
	for _, k := range wantOrphans {
		if exists(t, blobs, k) {
			t.Errorf("orphan %q survived deletion", k)
		}
	}
	for _, k := range []string{origKey, thumbKey, capKey, plThumbKey, hlsMaster, hlsSeg} {
		if !exists(t, blobs, k) {
			t.Errorf("referenced %q was deleted", k)
		}
	}
	for _, k := range untouched {
		if !exists(t, blobs, k) {
			t.Errorf("unknown-prefix object %q was touched", k)
		}
	}
}

// nonListerBackend is a storage.Backend WITHOUT ObjectLister, to prove the sweep
// degrades with a clear error rather than deleting anything.
type nonListerBackend struct{ storage.Backend }

func TestSweepRequiresLister(t *testing.T) {
	repo := &fakeRepo{}
	svc := NewService(repo, nonListerBackend{})
	if _, err := svc.Sweep(context.Background(), true); err != ErrListingUnsupported {
		t.Fatalf("want ErrListingUnsupported, got %v", err)
	}
}
