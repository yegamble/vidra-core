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
	// generations is videos.transcode_generation per id (0136); absent means 0.
	// It is what an in-flight transcode is WRITING INTO, so it is what keeps a
	// half-written tree out of the orphan set.
	generations map[uuid.UUID]int32
	plThumbs    []sqlcgen.ListPlaylistThumbnailRefsRow
	playlists   []sqlcgen.ListStreamingPlaylistRefsRow
}

func (f *fakeRepo) ListAllVideoFileKeys(context.Context) ([]string, error) { return f.fileKeys, nil }
func (f *fakeRepo) ListAllCaptionKeys(context.Context) ([]string, error)   { return f.capKeys, nil }
func (f *fakeRepo) ListVideoTranscodeGenerations(context.Context) ([]sqlcgen.ListVideoTranscodeGenerationsRow, error) {
	out := make([]sqlcgen.ListVideoTranscodeGenerationsRow, 0, len(f.videoIDs))
	for _, id := range f.videoIDs {
		out = append(out, sqlcgen.ListVideoTranscodeGenerationsRow{ID: id, TranscodeGeneration: f.generations[id]})
	}
	return out, nil
}
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

// mintedOrphan is a key of a shape THIS INSTALL writes, for an entity that does
// not exist — the only kind of object a sweep may ever collect. Fixtures that
// just want "an orphan" call it rather than naming a file freehand: a key whose
// id position is not one of our ids is now KEPT as unattributable (isMintedKey),
// so "web-videos/orphan.mp4" would make a rail's test pass for the wrong reason.
func mintedOrphan() string { return media.OriginalVideoKey(uuid.New(), 0, ".mp4") }

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
	// Orphans are keys THIS INSTALL minted for videos that are gone — the only
	// objects the sweep may collect. A freehand name (the old fixture said
	// "web-videos/orphan.mp4") is unattributable and is kept instead.
	orphanOrig := media.OriginalVideoKey(deadVid, 0, ".mp4")
	orphanThumb := media.VideoThumbnailKey(deadVid)
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
		// Both are at generation 1: the promoted video's r1 tree IS its current
		// generation, and the in-flight one is writing r1 while r0 still serves.
		// Since 0136 this comes from videos.transcode_generation, not from the
		// source key — a re-transcode of an unchanged source advances one and
		// not the other.
		generations: map[uuid.UUID]int32{promoted: 1, inflight: 1},
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

// TestSweepIsBlindToPackagingFormat is the claim that let CMAF ship without
// touching this package: the sweep's unit is the video id and, for a
// replacement, the GENERATION directory — it never reads a rendition name, a
// segment name, or anything else below that. So a CMAF tree, whose media all
// lives in a "cmaf" directory rather than per-rung ones, is kept and collected
// on exactly the same rule as an MPEG-TS one, and a mixed library (old MPEG-TS
// videos, new CMAF videos, and a video re-transcoded from one into the other)
// sweeps correctly with no format knowledge at all.
//
// The dangerous case is the last one: "cmaf" sits in the same path position a
// generation name does, so if IsHLSGenerationName ever grew loose enough to
// match it, a legacy-generation CMAF tree would be attributed to a generation
// that does not exist and swept while it was playing.
func TestSweepIsBlindToPackagingFormat(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	legacy := uuid.New()   // never replaced: CMAF tree at the v0 prefix
	replaced := uuid.New() // was MPEG-TS, re-transcoded as CMAF into r1

	legacySrc := "web-videos/" + legacy.String() + ".mp4"
	legacyKeep := []string{
		legacySrc,
		"streaming-playlists/" + legacy.String() + "/master.m3u8",
		"streaming-playlists/" + legacy.String() + "/audio.m4a",
		"streaming-playlists/" + legacy.String() + "/cmaf/stream.mpd",
		"streaming-playlists/" + legacy.String() + "/cmaf/media_0.m3u8",
		"streaming-playlists/" + legacy.String() + "/cmaf/init-0.mp4",
		"streaming-playlists/" + legacy.String() + "/cmaf/chunk-0-00001.m4s",
		"streaming-playlists/" + legacy.String() + "/cmaf/iframe-0.mp4",
		"streaming-playlists/" + legacy.String() + "/720p/video.mp4",
	}

	replacedSrc := "web-videos/" + replaced.String() + ".r1.mp4"
	replacedKeep := []string{
		replacedSrc,
		"streaming-playlists/" + replaced.String() + "/r1/master.m3u8",
		"streaming-playlists/" + replaced.String() + "/r1/cmaf/stream.mpd",
		"streaming-playlists/" + replaced.String() + "/r1/cmaf/chunk-2-00007.m4s",
		"streaming-playlists/" + replaced.String() + "/r1/1080p/video-only.mp4",
	}
	// Its superseded MPEG-TS generation, which must still be collected.
	replacedOrphans := []string{
		"web-videos/" + replaced.String() + ".mp4",
		"streaming-playlists/" + replaced.String() + "/720p/seg_00000.ts",
		"streaming-playlists/" + replaced.String() + "/master.m3u8",
	}

	for _, k := range append(append(append([]string{}, legacyKeep...), replacedKeep...), replacedOrphans...) {
		put(t, blobs, k)
	}

	repo := &fakeRepo{
		fileKeys: []string{legacySrc, replacedSrc},
		videoIDs: []uuid.UUID{legacy, replaced},
		// The replaced video is at generation 1 (0136's backfill puts an
		// existing row at its source version); the legacy one has never been
		// re-transcoded and is still at 0.
		generations: map[uuid.UUID]int32{replaced: 1},
		playlists: []sqlcgen.ListStreamingPlaylistRefsRow{
			{VideoID: legacy, MasterKey: "streaming-playlists/" + legacy.String() + "/master.m3u8"},
			{VideoID: replaced, MasterKey: "streaming-playlists/" + replaced.String() + "/r1/master.m3u8"},
		},
	}
	res, err := NewService(repo, blobs).Sweep(ctx, false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	wantOrphans := append([]string{}, replacedOrphans...)
	sort.Strings(wantOrphans)
	if strings.Join(res.Orphans, "|") != strings.Join(wantOrphans, "|") {
		t.Fatalf("orphans:\n got %v\nwant %v", res.Orphans, wantOrphans)
	}
	for _, k := range append(append([]string{}, legacyKeep...), replacedKeep...) {
		if !exists(t, blobs, k) {
			t.Errorf("live CMAF key %q was deleted", k)
		}
	}
}

// A11's close-out: a sweep writes no job_runs row (that projection is
// trigger-maintained off the durable QUEUE tables, and a scheduled sweep has no
// queue row), so the audit row is the only operator-facing record of one. The
// two facts that decide whether it deleted anything must be queryable there,
// not buried in a formatted sentence.
func TestAuditFieldsCarryDryRunAndTheBreaker(t *testing.T) {
	for _, tc := range []struct {
		name                    string
		res                     Result
		wantDryRun, wantBreaker string
	}{
		{"a real delete", Result{DryRun: false}, "false", "false"},
		{"an asked-for dry run", Result{DryRun: true}, "true", "false"},
		{
			"a delete the breaker refused",
			// Sweep reports the refusal as a dry run, so BOTH fields move.
			Result{DryRun: true, BreakerTripped: true},
			"true", "true",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := map[string]string{}
			for _, f := range tc.res.AuditFields() {
				got[f.Key] = f.Value
			}
			if got["dry_run"] != tc.wantDryRun || got["breaker_tripped"] != tc.wantBreaker {
				t.Fatalf("AuditFields = %v, want dry_run=%s breaker_tripped=%s",
					got, tc.wantDryRun, tc.wantBreaker)
			}
		})
	}

	// The audit envelope validates its metadata VOCABULARY and refuses the whole
	// event on an unknown key, so the sweep's fields have to be in it —
	// internal/audit's own test pins that end.
}

// TestSweepCollectsASupersededSameSourceGeneration closes the other half of
// migration 0136 — the half that says where the old bytes go.
//
// A same-source re-transcode used to overwrite its output in place, so there
// was never an old generation to collect and never a moment when both existed.
// Now there is: the run writes rN+1, promotion swaps the rows, and rN becomes
// unreferenced. Two things have to be true of that, and only one of them is
// "the old one is garbage":
//
//   - IN-FLIGHT VIEWERS ARE NOT CUT OFF. Nothing deletes the old generation at
//     promotion; it survives until the collector's next sweep, which is what
//     lets a player mid-ladder finish the segments it already has URLs for.
//     The dry run below is that guarantee, and it is also what an operator sees
//     before arming a destructive sweep.
//   - THE OLD GENERATION IS ACTUALLY COLLECTIBLE. A scheme that minted fresh
//     prefixes without being sweepable would trade a stale-cache bug for an
//     unbounded storage leak, one whole ladder per re-transcode.
func TestSweepCollectsASupersededSameSourceGeneration(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vid := uuid.New()
	// The source never changed — this is a RERUN, not a replacement, which is
	// exactly the case the old addressing could not express.
	src := "web-videos/" + vid.String() + ".mp4"
	live := []string{
		src,
		"streaming-playlists/" + vid.String() + "/r2/master.m3u8",
		"streaming-playlists/" + vid.String() + "/r2/cmaf/chunk-0-00001.m4s",
		"web-videos/" + vid.String() + "/r2/720p.mp4",
	}
	superseded := []string{
		"streaming-playlists/" + vid.String() + "/r1/master.m3u8",
		"streaming-playlists/" + vid.String() + "/r1/cmaf/chunk-0-00001.m4s",
		"web-videos/" + vid.String() + "/r1/720p.mp4",
	}
	for _, k := range append(append([]string{}, live...), superseded...) {
		put(t, blobs, k)
	}
	repo := &fakeRepo{
		fileKeys:    []string{src, "web-videos/" + vid.String() + "/r2/720p.mp4"},
		videoIDs:    []uuid.UUID{vid},
		generations: map[uuid.UUID]int32{vid: 2},
		playlists: []sqlcgen.ListStreamingPlaylistRefsRow{
			{VideoID: vid, MasterKey: "streaming-playlists/" + vid.String() + "/r2/master.m3u8"},
		},
	}
	svc := NewService(repo, blobs)

	// A dry run names the superseded generation and touches nothing: this is
	// where an in-flight viewer's bytes still are.
	dry, err := svc.Sweep(ctx, true)
	if err != nil {
		t.Fatalf("dry sweep: %v", err)
	}
	wantOrphans := append([]string{}, superseded...)
	sort.Strings(wantOrphans)
	if strings.Join(dry.Orphans, "|") != strings.Join(wantOrphans, "|") {
		t.Fatalf("dry-run orphans:\n got %v\nwant %v", dry.Orphans, wantOrphans)
	}
	for _, k := range superseded {
		if !exists(t, blobs, k) {
			t.Errorf("the dry run deleted %q; a superseded generation must survive until a real sweep", k)
		}
	}

	// And the real sweep collects it, both trees, leaving the promoted one whole.
	if _, err := svc.Sweep(ctx, false); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	for _, k := range superseded {
		if exists(t, blobs, k) {
			t.Errorf("superseded generation key %q survived the sweep; every re-transcode would leak a ladder", k)
		}
	}
	for _, k := range live {
		if !exists(t, blobs, k) {
			t.Errorf("live key %q was deleted", k)
		}
	}
}

// TestSweepKeepsAnInFlightWebVideoGeneration. The progressive MP4s a run derives
// have no rows until the run promotes them, and since 0136 they go into a NEW
// directory every time — so between the first PUT and the promotion the whole
// generation is unreferenced. Collecting it there would delete a transcode's
// output from under the job that is writing it.
func TestSweepKeepsAnInFlightWebVideoGeneration(t *testing.T) {
	ctx := context.Background()
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	vid := uuid.New()
	src := "web-videos/" + vid.String() + ".mp4"
	inflight := "web-videos/" + vid.String() + "/r3/720p.mp4" // being written now
	promoted := "web-videos/" + vid.String() + "/r2/720p.mp4" // superseded
	for _, k := range []string{src, inflight, promoted} {
		put(t, blobs, k)
	}
	repo := &fakeRepo{
		fileKeys:    []string{src},
		videoIDs:    []uuid.UUID{vid},
		generations: map[uuid.UUID]int32{vid: 3},
	}
	res, err := NewService(repo, blobs).Sweep(ctx, false)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if !exists(t, blobs, inflight) {
		t.Errorf("the sweep deleted %q, which the running transcode is still writing", inflight)
	}
	if exists(t, blobs, promoted) {
		t.Errorf("the superseded generation key %q survived", promoted)
	}
	if len(res.Orphans) != 1 || res.Orphans[0] != promoted {
		t.Errorf("orphans = %v, want just %q", res.Orphans, promoted)
	}
}
