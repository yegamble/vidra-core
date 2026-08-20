//go:build integration

// Excluded from `make ci`; run with: go test -tags integration ./internal/media/
package media

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

func assertStoredStreamTypes(t *testing.T, blobs *storage.Local, key string, want ...string) {
	t.Helper()
	path, err := blobs.Path(key)
	if err != nil {
		t.Fatalf("Path %q: %v", key, err)
	}
	out, err := exec.Command("ffprobe",
		"-v", "error",
		"-show_entries", "stream=codec_type",
		"-of", "default=noprint_wrappers=1:nokey=1",
		path,
	).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %q: %v\n%s", key, err, out)
	}
	got := strings.Fields(string(out))
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("%s stream types = %v, want %v", key, got, want)
	}
}

// TestHLSTranscoderRealVideo drives the real ffmpeg exec path: a tiny 320x240
// testsrc fixture is transcoded and the master playlist, the single 240p rung
// (cap-at-source), its variant playlist, and its segments must all land in
// storage with relative playlist URIs.
func TestHLSTranscoderRealVideo(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	prefix := HLSKeyPrefix(videoID)
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	path, err := blobs.Path(srcKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// Simulate replacing a previously-audible source. The silent transcode below
	// must remove this optional derivative instead of advertising stale audio.
	if _, err := blobs.Put(context.Background(), HLSAudioDownloadKey(prefix+"/master.m3u8"), strings.NewReader("stale-audio")); err != nil {
		t.Fatalf("seed stale audio: %v", err)
	}
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi", "-i", "testsrc=duration=2:size=320x240:rate=24", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}

	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false with ffmpeg+ffprobe on PATH")
	}
	res, err := tc.Transcode(context.Background(), videoID, srcKey)
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}

	if res.MasterKey != prefix+"/master.m3u8" {
		t.Errorf("MasterKey = %q, want %q", res.MasterKey, prefix+"/master.m3u8")
	}
	// Cap-at-source: a 240p source plans exactly one 320x240 rung.
	if len(res.Renditions) != 1 {
		t.Fatalf("renditions = %+v, want exactly one (cap-at-source)", res.Renditions)
	}
	r := res.Renditions[0]
	if r.Width != 320 || r.Height != 240 || r.KeyPrefix != prefix+"/240p" {
		t.Errorf("rendition = %+v, want 320x240 at %s/240p", r, prefix)
	}

	read := func(key string) string {
		rc, err := blobs.Open(context.Background(), key)
		if err != nil {
			t.Fatalf("Open %q: %v", key, err)
		}
		defer rc.Close()
		b, err := io.ReadAll(rc)
		if err != nil {
			t.Fatalf("read %q: %v", key, err)
		}
		return string(b)
	}

	master := read(res.MasterKey)
	if !strings.HasPrefix(master, "#EXTM3U") {
		t.Fatalf("master is not an m3u8:\n%s", master)
	}
	if !strings.Contains(master, "RESOLUTION=320x240") || !strings.Contains(master, "240p/playlist.m3u8") {
		t.Errorf("master missing the 240p variant:\n%s", master)
	}

	variant := read(r.KeyPrefix + "/playlist.m3u8")
	if !strings.HasPrefix(variant, "#EXTM3U") || !strings.Contains(variant, "#EXT-X-ENDLIST") {
		t.Errorf("variant playlist malformed:\n%s", variant)
	}
	// Segment URIs must be relative bare filenames so the API can proxy them.
	var segments []string
	for _, line := range strings.Split(variant, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.Contains(line, "/") {
			t.Errorf("segment URI %q must be a bare relative filename", line)
		}
		segments = append(segments, line)
	}
	if len(segments) == 0 {
		t.Fatal("variant playlist lists no segments")
	}
	for _, seg := range segments {
		exists, err := blobs.Exists(context.Background(), r.KeyPrefix+"/"+seg)
		if err != nil || !exists {
			t.Errorf("segment %q not stored under %s (exists=%v err=%v)", seg, r.KeyPrefix, exists, err)
		}
	}

	// Every rendition also has stable progressive MP4 downloads. This fixture
	// is silent, so the muxed asset contains video alone and no audio-only asset
	// should be stored.
	assertStoredStreamTypes(t, blobs, HLSDownloadKey(r.KeyPrefix, true), "video")
	assertStoredStreamTypes(t, blobs, HLSDownloadKey(r.KeyPrefix, false), "video")
	if exists, err := blobs.Exists(context.Background(), HLSAudioDownloadKey(res.MasterKey)); err != nil || exists {
		t.Errorf("silent source audio asset exists=%v err=%v, want absent", exists, err)
	}
}

// TestHLSTranscoderHDLadderMultipleRenditions is the real-ffmpeg regression
// guard for the reported "can't switch resolutions" bug: a genuine HD (1280x720)
// source must produce MULTIPLE distinct renditions (720p/480p/360p) with a
// multivariant master playlist — the shape the player's quality selector needs.
// TestHLSTranscoderRealVideo above only exercises a 240p single-rung source, so
// it can pass while multi-resolution laddering is broken; this closes that gap
// against the real ffmpeg exec path.
func TestHLSTranscoderHDLadderMultipleRenditions(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	path, err := blobs.Path(srcKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A real 720p clip with audio, so the planned ladder is 720p/480p/360p.
	gen := exec.Command("ffmpeg", "-y",
		"-f", "lavfi", "-i", "testsrc=duration=2:size=1280x720:rate=24",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=2",
		"-c:v", "libx264", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}

	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false with ffmpeg+ffprobe on PATH")
	}
	res, err := tc.Transcode(context.Background(), videoID, srcKey)
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}

	// Multiple renditions, tallest-first, at the expected ladder heights.
	if len(res.Renditions) != 3 {
		t.Fatalf("renditions = %+v, want 3 (720p/480p/360p ladder)", res.Renditions)
	}
	wantH := []int{720, 480, 360}
	wantW := []int{1280, 854, 640}
	for i, r := range res.Renditions {
		if r.Height != wantH[i] || r.Width != wantW[i] {
			t.Errorf("rendition[%d] = %dx%d, want %dx%d", i, r.Width, r.Height, wantW[i], wantH[i])
		}
	}

	rc, err := blobs.Open(context.Background(), res.MasterKey)
	if err != nil {
		t.Fatalf("open master: %v", err)
	}
	defer rc.Close()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read master: %v", err)
	}
	master := string(b)
	if n := strings.Count(master, "#EXT-X-STREAM-INF"); n != 3 {
		t.Fatalf("master has %d variant streams, want 3:\n%s", n, master)
	}
	for _, want := range []string{"RESOLUTION=1280x720", "RESOLUTION=854x480", "RESOLUTION=640x360"} {
		if !strings.Contains(master, want) {
			t.Errorf("master missing %q:\n%s", want, master)
		}
	}
	// Every variant playlist + its first segment landed in storage.
	for _, r := range res.Renditions {
		variant, err := blobs.Open(context.Background(), r.KeyPrefix+"/playlist.m3u8")
		if err != nil {
			t.Fatalf("open variant %dp: %v", r.Height, err)
		}
		vb, _ := io.ReadAll(variant)
		_ = variant.Close()
		if !strings.Contains(string(vb), "#EXT-X-ENDLIST") {
			t.Errorf("%dp variant playlist malformed:\n%s", r.Height, string(vb))
		}
		if exists, err := blobs.Exists(context.Background(), r.KeyPrefix+"/seg_00000.ts"); err != nil || !exists {
			t.Errorf("%dp first segment not stored (exists=%v err=%v)", r.Height, exists, err)
		}
		for _, includeAudio := range []bool{true, false} {
			key := HLSDownloadKey(r.KeyPrefix, includeAudio)
			if exists, err := blobs.Exists(context.Background(), key); err != nil || !exists {
				t.Errorf("%dp progressive download %q not stored (exists=%v err=%v)", r.Height, key, exists, err)
			}
		}
	}

	// Probe the top-rung assets to pin their stream composition. The muxed MP4
	// carries video+audio, video-only omits audio, and the root M4A carries only
	// the copied AAC stream.
	top := res.Renditions[0]
	assertStoredStreamTypes(t, blobs, HLSDownloadKey(top.KeyPrefix, true), "video", "audio")
	assertStoredStreamTypes(t, blobs, HLSDownloadKey(top.KeyPrefix, false), "video")
	assertStoredStreamTypes(t, blobs, HLSAudioDownloadKey(res.MasterKey), "audio")
}

// scratchProbe wraps a Backend and, on every Put, records how many of the
// transcoder's scratch rung directories still exist. It is how the
// upload-and-free-as-you-go contract is observed from outside.
type scratchProbe struct {
	*storage.Local
	scratchRoot string
	// rungDirsAt[key] is the number of rung directories still on disk at the
	// moment key was stored.
	rungDirsAt map[string]int
}

func (p *scratchProbe) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	p.rungDirsAt[key] = countScratchRungDirs(p.scratchRoot)
	return p.Local.Put(ctx, key, r)
}

// countScratchRungDirs counts <scratch>/vidra-hls-*/<N>p directories.
func countScratchRungDirs(root string) int {
	matches, _ := filepath.Glob(filepath.Join(root, "vidra-hls-*", "*p"))
	n := 0
	for _, m := range matches {
		if info, err := os.Stat(m); err == nil && info.IsDir() {
			n++
		}
	}
	return n
}

// TestHLSTranscoderFreesScratchAsItUploads pins the peak-scratch contract added
// with incremental storage: each rung's directory is uploaded and deleted as it
// finishes, rather than the whole tree accumulating for one bulk store. Because
// remuxHLSDownloads writes a full video.mp4 AND a full video-only.mp4 per rung on
// top of its segments, holding every rung at once was the largest single
// contributor to peak disk.
//
// Observable consequences, both asserted here:
//   - by the time master.m3u8 is stored, no rung directory remains on disk;
//   - the scratch tree is gone when TranscodeHLS returns, not merely when the
//     deferred cleanup eventually runs (the VP9 encode sits in between).
func TestHLSTranscoderFreesScratchAsItUploads(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	// Pin scratch somewhere observable; os.MkdirTemp("") honours TMPDIR.
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)

	local, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	blobs := &scratchProbe{Local: local, scratchRoot: scratch, rungDirsAt: map[string]int{}}

	videoID := uuid.New()
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	path, err := local.Path(srcKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// 720p so the ladder plans several rungs — a single-rung ladder could not
	// distinguish incremental from bulk storage.
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi",
		"-i", "testsrc=duration=2:size=1280x720:rate=24", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}

	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false with ffmpeg+ffprobe on PATH")
	}
	res, err := tc.Transcode(context.Background(), videoID, srcKey)
	if err != nil {
		t.Fatalf("Transcode: %v", err)
	}
	if len(res.Renditions) < 2 {
		t.Fatalf("test is stale: a 720p source must plan several rungs, got %d", len(res.Renditions))
	}

	if n, ok := blobs.rungDirsAt[res.MasterKey]; !ok {
		t.Fatalf("master playlist %q was never stored", res.MasterKey)
	} else if n != 0 {
		t.Errorf("%d rung directories still on disk when the master playlist was stored; "+
			"every rung should have been uploaded and freed by then", n)
	}

	// A rung's own objects must of course be present while that rung uploads, so
	// the interesting signal is that the count strictly falls: the last rung
	// stored must see fewer directories than the first.
	var maxSeen int
	for _, n := range blobs.rungDirsAt {
		if n > maxSeen {
			maxSeen = n
		}
	}
	if maxSeen > len(res.Renditions) {
		t.Errorf("saw %d scratch rung directories at once, more than the %d planned rungs", maxSeen, len(res.Renditions))
	}

	if n := countScratchRungDirs(scratch); n != 0 {
		t.Errorf("%d scratch rung directories survived TranscodeHLS", n)
	}
	leftover, _ := filepath.Glob(filepath.Join(scratch, "vidra-hls-*"))
	if len(leftover) != 0 {
		t.Errorf("scratch tree %v survived TranscodeHLS; the VP9 encode would have run with it still on disk", leftover)
	}
}

// TestHLSTranscoderStreamsLadderIntoStorage drives the whole TranscodeHLS path
// with SetStreamOutput on: the ladder is PUT through the loopback sidecar
// straight into the backend, so no segment ever lands on scratch, and the
// decode-free post-processing reads its own output back over HTTP.
//
// It asserts the same stored-tree shape the scratch path produces, because the
// streaming mode must be a storage-transport choice and nothing more.
func TestHLSTranscoderStreamsLadderIntoStorage(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	scratch := t.TempDir()
	t.Setenv("TMPDIR", scratch)

	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	prefix := HLSKeyPrefix(videoID)
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	path, err := blobs.Path(srcKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	gen := exec.Command("ffmpeg", "-y", "-f", "lavfi",
		"-i", "testsrc=duration=4:size=640x360:rate=24", path)
	if out, err := gen.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}

	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false with ffmpeg+ffprobe on PATH")
	}
	tc.SetStreamOutput(true)

	res, err := tc.Transcode(context.Background(), videoID, srcKey)
	if err != nil {
		t.Fatalf("Transcode with streaming output: %v", err)
	}
	if res.MasterKey != prefix+"/master.m3u8" {
		t.Errorf("MasterKey = %q, want %q", res.MasterKey, prefix+"/master.m3u8")
	}
	if len(res.Renditions) != 1 {
		t.Fatalf("renditions = %+v, want one (cap-at-source)", res.Renditions)
	}
	if res.Renditions[0].SizeBytes <= 0 {
		t.Errorf("rendition size = %d; a streamed ladder still has to be measured",
			res.Renditions[0].SizeBytes)
	}

	// The canonical tree must be complete in storage: variant playlist, at least
	// one segment, both progressive downloads, and the trick-play pair.
	for _, name := range []string{
		"master.m3u8",
		"360p/playlist.m3u8",
		"360p/" + HLSMuxedDownloadFilename,
		"360p/" + HLSVideoOnlyDownloadFilename,
		"360p/" + HLSIFramePlaylistFilename,
		"360p/" + HLSIFrameMediaFilename,
	} {
		if ok, err := blobs.Exists(context.Background(), prefix+"/"+name); err != nil || !ok {
			t.Errorf("streamed tree is missing %q (err=%v)", name, err)
		}
	}
	keys, err := blobs.ListKeys(context.Background(), prefix+"/360p")
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	var segments int
	for _, k := range keys {
		if strings.HasSuffix(k, ".ts") && strings.Contains(k, "seg_") {
			segments++
		}
	}
	if segments == 0 {
		t.Errorf("no segments stored: %v", keys)
	}

	// The trick-play playlist must have been rewritten IN THE STORE, not only in
	// a scratch copy — this is the read-modify-write path through the sidecar.
	rc, err := blobs.Open(context.Background(), prefix+"/360p/"+HLSIFramePlaylistFilename)
	if err != nil {
		t.Fatalf("Open trick-play playlist: %v", err)
	}
	defer rc.Close()
	body, _ := io.ReadAll(rc)
	if !strings.Contains(string(body), "#EXT-X-I-FRAMES-ONLY") {
		t.Errorf("stored trick-play playlist was never finalised: %q", body)
	}

	// Nothing may be left behind on scratch.
	leftover, _ := filepath.Glob(filepath.Join(scratch, "vidra-hls-*"))
	if len(leftover) != 0 {
		t.Errorf("scratch tree %v survived a streaming transcode", leftover)
	}
}
