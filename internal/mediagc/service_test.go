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
	fileKeys  []string
	capKeys   []string
	videoIDs  []uuid.UUID
	plThumbs  []sqlcgen.ListPlaylistThumbnailRefsRow
	playlists []sqlcgen.ListStreamingPlaylistRefsRow
}

func (f *fakeRepo) ListAllVideoFileKeys(context.Context) ([]string, error) { return f.fileKeys, nil }
func (f *fakeRepo) ListAllCaptionKeys(context.Context) ([]string, error)   { return f.capKeys, nil }
func (f *fakeRepo) ListAllVideoIDs(context.Context) ([]uuid.UUID, error)   { return f.videoIDs, nil }
func (f *fakeRepo) ListPlaylistThumbnailRefs(context.Context) ([]sqlcgen.ListPlaylistThumbnailRefsRow, error) {
	return f.plThumbs, nil
}
func (f *fakeRepo) ListStreamingPlaylistRefs(context.Context) ([]sqlcgen.ListStreamingPlaylistRefsRow, error) {
	return f.playlists, nil
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

// TestSweepKeepsRenditionsOutsideCurrentLadder proves ladder shrinkage
// (transcoding_resolutions, config-parity W10) can never orphan an existing
// video's renditions: the HLS tree is collected at the VIDEO-ID level — the
// sweep never consults the configured ladder — so rung directories that are no
// longer in the admin-selected ladder (here 1440p/240p alongside 720p) survive
// as long as their video exists, and the whole tree goes only when the video
// row does.
func TestSweepKeepsRenditionsOutsideCurrentLadder(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	liveVid := uuid.New()
	keys := []string{
		"streaming-playlists/" + liveVid.String() + "/master.m3u8",
		"streaming-playlists/" + liveVid.String() + "/1440p/playlist.m3u8", // not in the default ladder
		"streaming-playlists/" + liveVid.String() + "/1440p/seg_00000.ts",
		"streaming-playlists/" + liveVid.String() + "/720p/seg_00000.ts",
		"streaming-playlists/" + liveVid.String() + "/240p/seg_00000.ts", // not in the default ladder
	}
	for _, k := range keys {
		put(t, blobs, k)
	}
	svc := NewService(&fakeRepo{videoIDs: []uuid.UUID{liveVid}}, blobs)
	res, err := svc.Sweep(ctx, false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if res.Deleted != 0 || len(res.Orphans) != 0 {
		t.Fatalf("sweep = deleted %d orphans %v, want none (video exists)", res.Deleted, res.Orphans)
	}
	for _, k := range keys {
		if !exists(t, blobs, k) {
			t.Errorf("rendition object %q was deleted despite its video existing", k)
		}
	}
}

// TestSweepCollectsSupersededHLSGenerations (W14): after a replacement
// promotes generation r1, the legacy tree and the old source blob become
// orphans; the promoted generation and the current source stay. A NOT-yet-
// promoted target generation (re-transcode in flight) must also stay.
func TestSweepCollectsSupersededHLSGenerations(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	promoted := uuid.New() // replacement promoted: master points at r1
	inflight := uuid.New() // replacement mid-transcode: master still legacy, source already r1

	// promoted video: r1 is live, legacy tree + v0 source superseded.
	promotedSrc := "web-videos/" + promoted.String() + ".r1.webm"
	promotedOldSrc := "web-videos/" + promoted.String() + ".mp4"
	promotedKeep := []string{
		promotedSrc,
		"streaming-playlists/" + promoted.String() + "/r1/master.m3u8",
		"streaming-playlists/" + promoted.String() + "/r1/720p/seg_00000.ts",
		"streaming-playlists/" + promoted.String() + "/r1/vp9.webm",
	}
	promotedOrphans := []string{
		promotedOldSrc,
		"streaming-playlists/" + promoted.String() + "/master.m3u8",
		"streaming-playlists/" + promoted.String() + "/720p/seg_00000.ts",
		"streaming-playlists/" + promoted.String() + "/audio.m4a",
	}

	// in-flight video: legacy generation still promoted AND the r1 target tree
	// being written must BOTH survive the sweep.
	inflightSrc := "web-videos/" + inflight.String() + ".r1.mp4"
	inflightKeep := []string{
		inflightSrc,
		"streaming-playlists/" + inflight.String() + "/master.m3u8",
		"streaming-playlists/" + inflight.String() + "/480p/seg_00000.ts",
		"streaming-playlists/" + inflight.String() + "/r1/720p/seg_00000.ts", // half-written target
	}

	for _, k := range append(append(append([]string{}, promotedKeep...), promotedOrphans...), inflightKeep...) {
		put(t, blobs, k)
	}

	repo := &fakeRepo{
		fileKeys: []string{promotedSrc, inflightSrc},
		videoIDs: []uuid.UUID{promoted, inflight},
		playlists: []sqlcgen.ListStreamingPlaylistRefsRow{
			{VideoID: promoted, MasterKey: "streaming-playlists/" + promoted.String() + "/r1/master.m3u8"},
			{VideoID: inflight, MasterKey: "streaming-playlists/" + inflight.String() + "/master.m3u8"},
		},
	}
	res, err := NewService(repo, blobs).Sweep(ctx, false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	wantOrphans := append([]string{}, promotedOrphans...)
	sort.Strings(wantOrphans)
	if strings.Join(res.Orphans, "|") != strings.Join(wantOrphans, "|") {
		t.Fatalf("orphans:\n got %v\nwant %v", res.Orphans, wantOrphans)
	}
	for _, k := range append(append([]string{}, promotedKeep...), inflightKeep...) {
		if !exists(t, blobs, k) {
			t.Errorf("live key %q was deleted", k)
		}
	}
	for _, k := range promotedOrphans {
		if exists(t, blobs, k) {
			t.Errorf("superseded key %q survived the sweep", k)
		}
	}
}

// TestSweepKeepsWholeTreeWithoutAttributableMaster (W14): a live video with no
// playlist row (first transcode possibly mid-write), an empty master (dead-
// lettered job), or a foreign-layout master (PeerTube import) keeps its whole
// HLS tree — the sweep never risks files it cannot attribute to a generation.
func TestSweepKeepsWholeTreeWithoutAttributableMaster(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	noRow := uuid.New()
	emptyMaster := uuid.New()
	keep := []string{
		"streaming-playlists/" + noRow.String() + "/720p/seg_00000.ts",
		"streaming-playlists/" + noRow.String() + "/r1/720p/seg_00000.ts",
		"streaming-playlists/" + emptyMaster.String() + "/master.m3u8",
		"streaming-playlists/" + emptyMaster.String() + "/r2/master.m3u8",
	}
	for _, k := range keep {
		put(t, blobs, k)
	}
	repo := &fakeRepo{
		videoIDs: []uuid.UUID{noRow, emptyMaster},
		playlists: []sqlcgen.ListStreamingPlaylistRefsRow{
			{VideoID: emptyMaster, MasterKey: ""}, // dead-lettered replacement transcode
		},
	}
	res, err := NewService(repo, blobs).Sweep(ctx, false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if len(res.Orphans) != 0 {
		t.Fatalf("orphans = %v, want none (unattributable trees are kept)", res.Orphans)
	}
	for _, k := range keep {
		if !exists(t, blobs, k) {
			t.Errorf("key %q was deleted", k)
		}
	}
}
