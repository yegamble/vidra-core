package ipfsmirror

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/ipfs"
	"github.com/vidra/vidra-core/internal/media"
)

// promotedTreeCID is what the pinned car_root MUST equal: the root of a UnixFS
// directory built from the promoted generation's files addressed RELATIVE to that
// generation's own directory. Building it with a throwaway fake makes the
// assertion content-addressed — it pins the exact tree shape (every relative path
// and every byte) without the test having to decode the fake's buffer.
func promotedTreeCID(t *testing.T, files map[string]string) string {
	t.Helper()
	entries := make([]ipfs.DirEntry, 0, len(files))
	for p, data := range files {
		entries = append(entries, ipfs.DirEntry{Path: p, Data: strings.NewReader(data)})
	}
	res, err := ipfs.NewFakeIPFSClient().AddDirectory(context.Background(), entries)
	if err != nil {
		t.Fatalf("build expected tree: %v", err)
	}
	return res.CID
}

// TestPinsPromotedGenerationTree is the core#199 regression.
//
// Since A33 slice 1 made transcode output generation-addressed, a video's HLS
// bytes live one level BELOW the mirror's stable ledger key
// (streaming-playlists/<id>/rN/…), and more than one generation is in the object
// store at a time — the superseded one until mediagc collects it, the in-flight
// one from the moment a re-transcode starts writing. Listing the stable prefix
// wrapped every generation into one root whose top level held "r1/", "r2/" and NO
// master.m3u8. Nothing failed: the pin succeeded, the CID was valid, the ledger
// said pinned — and {gateway}/ipfs/{car_root}/master.m3u8 was a 404, which is how
// this reached vidra-user's ipfs-backed lane rather than a test.
//
// The tree that gets pinned is the PROMOTED generation's alone, addressed
// relative to it.
func TestPinsPromotedGenerationTree(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	vid := uuid.New()

	stable := media.HLSKeyPrefix(vid) + "/"
	superseded := media.HLSPrefixForGeneration(vid, 1) + "/"
	promoted := media.HLSPrefixForGeneration(vid, 2) + "/"

	// A superseded generation still sitting in the store (mediagc has not swept it).
	putBlob(t, blobs, superseded+"master.m3u8", "#EXTM3U\nSUPERSEDED-r1\n")
	putBlob(t, blobs, superseded+"720p/seg_00000.ts", "SUPERSEDED-r1-seg")
	// The promoted one, plus the VP9/WebM alternate that shares its directory and
	// is pinned separately as its own media class.
	promotedFiles := map[string]string{
		"master.m3u8":        "#EXTM3U\n720p/playlist.m3u8\n",
		"720p/playlist.m3u8": "#EXTM3U\nseg_00000.ts\n",
		"720p/seg_00000.ts":  "r2-ts-bytes",
	}
	for rel, data := range promotedFiles {
		putBlob(t, blobs, promoted+rel, data)
	}
	putBlob(t, blobs, promoted+media.VP9WebMFilename, "webm-alt-bytes")

	seedPendingVideo(repo, stable, string(ClassHLS), vid)
	lk := &fakeLookups{hlsTree: strings.TrimSuffix(promoted, "/")}
	svc := New(repo, lk, blobs, client, testConfig())

	n, err := svc.DrainDue(context.Background(), 10)
	if err != nil {
		t.Fatalf("DrainDue: %v", err)
	}
	if n != 1 {
		t.Fatalf("drained %d, want 1", n)
	}
	row := repo.rows[stable]
	if row.State != "pinned" {
		t.Fatalf("state = %q (%s), want pinned", row.State, row.LastError)
	}
	if want := promotedTreeCID(t, promotedFiles); row.CarRoot != want {
		t.Errorf("car_root = %q, want %q — the wrapped tree is not the promoted generation's, addressed relative to it", row.CarRoot, want)
	}

	// The same failure said in plain bytes, because the CID comparison above is
	// only as clear as its expectation: no generation directory may appear as a
	// path segment inside the root, and the superseded generation's bytes may not
	// be in there at all.
	content, ok := client.Content(row.CarRoot)
	if !ok {
		t.Fatal("car_root content not stored on the fake node")
	}
	if got := string(content); strings.Contains(got, "r1/") || strings.Contains(got, "r2/") {
		t.Error("a generation directory is a path segment inside the car_root: the root holds rN/, not master.m3u8")
	}
	if strings.Contains(string(content), "SUPERSEDED") {
		t.Error("the superseded generation's bytes were wrapped into the promoted tree's car_root")
	}
	if strings.Contains(string(content), "webm-alt-bytes") {
		t.Error("vp9.webm was wrapped into the HLS car_root, want excluded (separate media class)")
	}
}

// TestPinRefusesWithoutPromotedTree: no playlist row, or a dead-lettered
// transcode that never recorded a master key, means there is no promoted
// generation — and therefore nothing this row can honestly pin. It must FAIL
// (and be retried/dead-lettered like any other failure) rather than fall back to
// wrapping whatever the stable prefix happens to hold, which is exactly the
// silent-success shape the generation regression had.
func TestPinRefusesWithoutPromotedTree(t *testing.T) {
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	vid := uuid.New()
	stable := media.HLSKeyPrefix(vid) + "/"
	putBlob(t, blobs, media.HLSPrefixForGeneration(vid, 1)+"/master.m3u8", "#EXTM3U\n")

	seedPendingVideo(repo, stable, string(ClassHLS), vid)
	svc := New(repo, &fakeLookups{}, blobs, client, testConfig()) // hlsTree unset ⇒ no promoted tree

	if _, err := svc.DrainDue(context.Background(), 10); err != nil {
		t.Fatalf("DrainDue: %v", err)
	}
	row := repo.rows[stable]
	if row.State != "failed" && row.State != "pending" {
		t.Fatalf("state = %q, want a retryable failure, not %q", row.State, row.State)
	}
	if row.Cid != "" || row.CarRoot != "" {
		t.Errorf("cid=%q car_root=%q, want nothing recorded for a video with no promoted tree", row.Cid, row.CarRoot)
	}
	if client.AddCount != 0 {
		t.Errorf("AddCount = %d, want 0 (nothing may be published for a video with no promoted tree)", client.AddCount)
	}
}

// TestReTranscodeMovesGenerationAndReleasesTheOld is the lifecycle half: a
// re-transcode promotes a NEW generation, and both of the transcode's outputs
// have to follow it — by different mechanisms, which is the point.
//
//   - The HLS tree keeps ONE ledger row across generations (its key is the stable
//     directory intent), so the forced re-claim re-lists the now-promoted
//     directory, records a new car_root against the same row, and swap-unpins the
//     superseded root. Nothing about the ledger has to know a generation moved.
//   - The VP9/WebM alternate does NOT: it is recorded in video_files at the
//     generation's own key, so the new generation is a NEW row and the previous
//     one would sit 'pinned' for ever — mirroring bytes mediagc has already
//     collected — unless it is released explicitly.
func TestReTranscodeMovesGenerationAndReleasesTheOld(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	blobs := newBlobs(t)
	client := ipfs.NewFakeIPFSClient()
	vid := uuid.New()

	stable := media.HLSKeyPrefix(vid) + "/"
	gen1 := media.HLSPrefixForGeneration(vid, 1)
	gen2 := media.HLSPrefixForGeneration(vid, 2)
	webm1, webm2 := media.VP9WebMKey(gen1), media.VP9WebMKey(gen2)

	lk := &fakeLookups{
		videoPrivacy: "public", videoState: "published", videoOK: true, userOK: true,
		hlsTree:    gen1,
		videoFiles: []VideoFileRef{{Kind: "webm", StorageKey: webm1}},
	}
	svc := New(repo, lk, blobs, client, testConfig())

	// --- generation 1 ---
	putBlob(t, blobs, gen1+"/master.m3u8", "r1-master")
	putBlob(t, blobs, gen1+"/720p/seg_00000.ts", "r1-seg")
	putBlob(t, blobs, webm1, "r1-webm")
	if err := svc.OnTranscodeComplete(ctx, vid); err != nil {
		t.Fatalf("first OnTranscodeComplete: %v", err)
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("first drain: %v", err)
	}
	if repo.state(stable) != "pinned" {
		t.Fatalf("generation 1 tree state = %q (%s), want pinned", repo.state(stable), repo.rows[stable].LastError)
	}
	if repo.state(webm1) != "pinned" {
		t.Fatalf("generation 1 webm state = %q, want pinned", repo.state(webm1))
	}
	oldRoot, oldWebMCID := repo.rows[stable].CarRoot, repo.rows[webm1].Cid
	if pinned, _ := client.IsPinned(ctx, oldRoot); !pinned {
		t.Fatal("generation 1 car_root not pinned on the node")
	}

	// --- generation 2 is written and promoted (the DB master key moves) ---
	putBlob(t, blobs, gen2+"/master.m3u8", "r2-master")
	putBlob(t, blobs, gen2+"/720p/seg_00000.ts", "r2-seg")
	putBlob(t, blobs, webm2, "r2-webm")
	lk.hlsTree = gen2
	lk.videoFiles = []VideoFileRef{{Kind: "webm", StorageKey: webm2}}

	if err := svc.OnTranscodeComplete(ctx, vid); err != nil {
		t.Fatalf("re-transcode OnTranscodeComplete: %v", err)
	}
	if repo.state(stable) != "pending" {
		t.Fatalf("HLS row state = %q, want pending (forced re-claim onto the new generation)", repo.state(stable))
	}
	if repo.state(webm1) != "unpinning" {
		t.Fatalf("superseded webm row state = %q, want unpinning — the old generation's alternate is stranded pinned", repo.state(webm1))
	}
	if repo.state(webm2) != "pending" {
		t.Fatalf("promoted webm row state = %q, want pending", repo.state(webm2))
	}
	if _, err := svc.DrainDue(ctx, 10); err != nil {
		t.Fatalf("re-transcode drain: %v", err)
	}

	newRoot := repo.rows[stable].CarRoot
	if newRoot == oldRoot {
		t.Fatal("car_root unchanged after the generation moved: the worker is still listing the old tree")
	}
	if want := promotedTreeCID(t, map[string]string{"master.m3u8": "r2-master", "720p/seg_00000.ts": "r2-seg"}); newRoot != want {
		t.Errorf("car_root = %q, want %q (generation 2's tree, relative to its own directory)", newRoot, want)
	}
	if pinned, _ := client.IsPinned(ctx, oldRoot); pinned {
		t.Error("superseded car_root still pinned after the generation moved, want swap-unpinned")
	}
	if repo.state(webm1) != "unpinned" {
		t.Errorf("superseded webm row state = %q, want unpinned (terminal)", repo.state(webm1))
	}
	if pinned, _ := client.IsPinned(ctx, oldWebMCID); pinned {
		t.Error("superseded generation's vp9.webm still pinned on the node")
	}
	if repo.state(webm2) != "pinned" {
		t.Errorf("promoted webm row state = %q, want pinned", repo.state(webm2))
	}
}

// TestReTranscodeLeavesOtherClassesAlone fences the release above: only the
// transcode's OWN output is generation-addressed. A thumbnail, caption or the
// original keeps its key across re-transcodes and belongs to SyncVideo — a
// completion hook that unpinned them would silently pull a live video's images
// off the network on every re-encode.
func TestReTranscodeLeavesOtherClassesAlone(t *testing.T) {
	ctx := context.Background()
	repo := newFakeRepo()
	vid := uuid.New()
	thumb := "thumbnails/" + vid.String() + ".jpg"
	original := "web-videos/" + vid.String() + ".mp4"
	seedPinned(repo, thumb, string(ClassThumbnail), ipfs.DirCIDv1([]byte("thumb")), vid)
	seedPinned(repo, original, string(ClassVideoOriginal), ipfs.DirCIDv1([]byte("orig")), vid)

	lk := &fakeLookups{
		videoPrivacy: "public", videoState: "published", videoOK: true, userOK: true,
		hlsTree: media.HLSPrefixForGeneration(vid, 2),
	}
	svc := New(repo, lk, newBlobs(t), ipfs.NewFakeIPFSClient(), testConfig())
	if err := svc.OnTranscodeComplete(ctx, vid); err != nil {
		t.Fatalf("OnTranscodeComplete: %v", err)
	}
	if got := repo.state(thumb); got != "pinned" {
		t.Errorf("thumbnail state = %q, want pinned (untouched by a re-transcode)", got)
	}
	if got := repo.state(original); got != "pinned" {
		t.Errorf("original state = %q, want pinned (untouched by a re-transcode)", got)
	}
}
