//go:build integration

// Excluded from `make ci`; run with: go test -tags integration ./internal/media/
package media

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/google/uuid"

	"github.com/vidra/vidra-core/internal/storage"
)

// cmafFixture transcodes a generated source with the CMAF packager and hands
// back everything the assertions need.
type cmafFixture struct {
	blobs  *storage.Local
	prefix string
	res    HLSResult
	keys   []string
}

func newCMAFFixture(t *testing.T, genArgs []string, stream bool) cmafFixture {
	t.Helper()
	return newPackagedFixture(t, PackagerCMAF, genArgs, stream)
}

// newPackagedFixture transcodes a generated source with the named packager. It
// takes the packager so the geometry tests can run the SAME source through both
// and compare, which is the only way to state "CMAF renders this the way MPEG-TS
// does" as a fact rather than a hope.
func newPackagedFixture(t *testing.T, packager string, genArgs []string, stream bool) cmafFixture {
	t.Helper()
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
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	srcPath, err := blobs.Path(srcKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("ffmpeg", append(append([]string{"-y"}, genArgs...), srcPath)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}

	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false with ffmpeg+ffprobe on PATH")
	}
	if err := tc.SetPackager(packager); err != nil {
		t.Fatalf("SetPackager: %v", err)
	}
	tc.SetStreamOutput(stream)

	res, err := tc.Transcode(context.Background(), videoID, srcKey)
	if err != nil {
		t.Fatalf("%s transcode: %v", packager, err)
	}
	prefix := HLSKeyPrefix(videoID)
	keys, err := blobs.ListKeys(context.Background(), prefix)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	sort.Strings(keys)

	// Nothing may survive on scratch either way.
	if leftover, _ := filepath.Glob(filepath.Join(scratch, "vidra-hls-*")); len(leftover) != 0 {
		t.Errorf("scratch tree %v survived the transcode", leftover)
	}
	return cmafFixture{blobs: blobs, prefix: prefix, res: res, keys: keys}
}

func (f cmafFixture) read(t *testing.T, rel string) string {
	t.Helper()
	rc, err := f.blobs.Open(context.Background(), f.prefix+"/"+rel)
	if err != nil {
		t.Fatalf("Open %q: %v", rel, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", rel, err)
	}
	return string(b)
}

// rels is the stored tree as paths relative to the video's prefix.
func (f cmafFixture) rels() []string {
	out := make([]string, 0, len(f.keys))
	for _, k := range f.keys {
		out = append(out, strings.TrimPrefix(k, f.prefix+"/"))
	}
	return out
}

// hd720WithAudio generates a real 720p clip with audio, so the ladder plans
// 720p/480p/360p and there is a genuine shared audio representation.
var hd720WithAudio = []string{
	"-f", "lavfi", "-i", "testsrc2=duration=8:size=1280x720:rate=25",
	"-f", "lavfi", "-i", "sine=frequency=440:duration=8",
	"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest",
}

// TestCMAFTranscodeProducesOneSharedSegmentSet is the end-to-end proof, through
// real ffmpeg, that the format does what it is for: ONE set of segments, both
// manifests over it, and a tree whose shape is pinned rather than described.
func TestCMAFTranscodeProducesOneSharedSegmentSet(t *testing.T) {
	f := newCMAFFixture(t, hd720WithAudio, false)

	if f.res.Format != HLSFormatCMAF {
		t.Errorf("HLSResult.Format = %q, want %q — serving reads this to decide the tree's shape", f.res.Format, HLSFormatCMAF)
	}
	if f.res.MasterKey != f.prefix+"/master.m3u8" {
		t.Errorf("MasterKey = %q, want the same key MPEG-TS uses", f.res.MasterKey)
	}
	if len(f.res.Renditions) != 3 {
		t.Fatalf("renditions = %+v, want the 720p/480p/360p ladder", f.res.Renditions)
	}

	// The tree shape, pinned exactly. Segment COUNTS vary with the source, so
	// the per-segment names are collapsed to their pattern; everything else is
	// literal.
	segRE := regexp.MustCompile(`^cmaf/chunk-[0-9]+-[0-9]{5}\.m4s$`)
	var shape []string
	segments := map[string]int{}
	for _, rel := range f.rels() {
		if segRE.MatchString(rel) {
			rep := strings.Split(strings.TrimPrefix(rel, "cmaf/chunk-"), "-")[0]
			segments["cmaf/chunk-"+rep+"-*.m4s"]++
			continue
		}
		shape = append(shape, rel)
	}
	wantShape := []string{
		"360p/video-only.mp4",
		"360p/video.mp4",
		"480p/video-only.mp4",
		"480p/video.mp4",
		"720p/video-only.mp4",
		"720p/video.mp4",
		"audio.m4a",
		"cmaf/iframe-0.m3u8",
		"cmaf/iframe-0.mp4",
		"cmaf/iframe-1.m3u8",
		"cmaf/iframe-1.mp4",
		"cmaf/iframe-2.m3u8",
		"cmaf/iframe-2.mp4",
		"cmaf/init-0.mp4",
		"cmaf/init-1.mp4",
		"cmaf/init-2.mp4",
		"cmaf/init-3.mp4",
		"cmaf/media_0.m3u8",
		"cmaf/media_1.m3u8",
		"cmaf/media_2.m3u8",
		"cmaf/media_3.m3u8",
		"cmaf/stream.mpd",
		"master.m3u8",
	}
	sort.Strings(shape)
	if strings.Join(shape, "\n") != strings.Join(wantShape, "\n") {
		t.Errorf("stored tree shape:\n got %s\nwant %s", strings.Join(shape, "\n "), strings.Join(wantShape, "\n "))
	}
	// Four representations (three video rungs + one shared audio), each with
	// segments. Rep 3 is the audio one.
	if len(segments) != 4 {
		t.Errorf("segment families = %v, want one per representation (4)", segments)
	}
	for family, n := range segments {
		if n == 0 {
			t.Errorf("%s has no segments", family)
		}
	}
	// ffmpeg's own master playlist is not part of the tree.
	for _, rel := range f.rels() {
		if strings.HasSuffix(rel, cmafFFmpegMasterFilename) {
			t.Errorf("ffmpeg's discarded master playlist was stored at %q", rel)
		}
	}
}

// TestCMAFManifestsNameTheSameFiles is the claim that makes DASH free: the MPD's
// SegmentTemplate and the HLS media playlists must resolve to the SAME stored
// objects. Anything less and the "shared" segment set is two sets in a trench
// coat, at double the storage.
func TestCMAFManifestsNameTheSameFiles(t *testing.T) {
	f := newCMAFFixture(t, hd720WithAudio, false)

	stored := map[string]bool{}
	for _, rel := range f.rels() {
		stored[rel] = true
	}

	mpd := f.read(t, "cmaf/"+cmafManifestFilename)
	if !strings.Contains(mpd, `type="static"`) {
		t.Errorf("a VOD MPD must be static, not live:\n%s", mpd)
	}
	// Expand the MPD's templates for every Representation and check each
	// resolves to a stored object that the matching HLS playlist also lists.
	repRE := regexp.MustCompile(`<Representation id="([0-9]+)"`)
	reps := repRE.FindAllStringSubmatch(mpd, -1)
	if len(reps) != 4 {
		t.Fatalf("MPD has %d representations, want 4 (three video rungs + shared audio):\n%s", len(reps), mpd)
	}
	initTmpl := attrOf(t, mpd, "initialization")
	mediaTmpl := attrOf(t, mpd, "media")
	if initTmpl != cmafInitSegmentPattern || mediaTmpl != cmafMediaSegmentPattern {
		t.Fatalf("MPD templates = (%q, %q), want the names Go reproduces", initTmpl, mediaTmpl)
	}

	for _, m := range reps {
		rep, err := strconv.Atoi(m[1])
		if err != nil {
			t.Fatalf("representation id %q: %v", m[1], err)
		}
		// DASH side: the init segment the template expands to.
		initName := cmafInitSegmentName(rep)
		if !stored["cmaf/"+initName] {
			t.Errorf("MPD representation %d names init %q, which is not stored", rep, initName)
		}
		// HLS side: the same object, via EXT-X-MAP.
		playlist := f.read(t, "cmaf/"+cmafMediaPlaylistName(rep))
		if !strings.Contains(playlist, `#EXT-X-MAP:URI="`+initName+`"`) {
			t.Errorf("representation %d: HLS playlist does not map the MPD's init %q:\n%s", rep, initName, playlist)
		}
		// And every segment the playlist lists must be a stored object matching
		// the MPD's media template for this representation.
		var listed int
		for _, line := range strings.Split(playlist, "\n") {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			listed++
			if strings.Contains(line, "/") {
				t.Errorf("segment URI %q must be a bare relative filename", line)
			}
			if !stored["cmaf/"+line] {
				t.Errorf("representation %d lists segment %q, which is not stored", rep, line)
			}
			wantPrefix := "chunk-" + strconv.Itoa(rep) + "-"
			if !strings.HasPrefix(line, wantPrefix) || !strings.HasSuffix(line, ".m4s") {
				t.Errorf("segment %q does not match the MPD's media template for representation %d", line, rep)
			}
		}
		if listed == 0 {
			t.Errorf("representation %d lists no segments:\n%s", rep, playlist)
		}
	}
}

// TestCMAFSegmentsAreValidMedia proves the segments are real: concatenating a
// representation's init segment with all of its media segments must ffprobe as
// the codec, dimensions and duration the ladder planned. This is the check that
// would catch a broken init segment or a mis-mapped representation — both of
// which produce a tree that looks perfect and plays nothing.
func TestCMAFSegmentsAreValidMedia(t *testing.T) {
	f := newCMAFFixture(t, hd720WithAudio, false)
	mpd := f.read(t, "cmaf/"+cmafManifestFilename)

	for i, r := range f.res.Renditions {
		joined := f.concatRepresentation(t, i)
		probe := ffprobeEntries(t, joined, "stream=codec_name,width,height:format=duration")
		if probe["codec_name"] != "h264" {
			t.Errorf("rung %dp is %q, want h264", r.Height, probe["codec_name"])
		}
		if probe["width"] != strconv.Itoa(r.Width) || probe["height"] != strconv.Itoa(r.Height) {
			t.Errorf("rung %dp concatenates to %sx%s, want %dx%d — a representation is mapped to the wrong rung",
				r.Height, probe["width"], probe["height"], r.Width, r.Height)
		}
		if d, err := strconv.ParseFloat(probe["duration"], 64); err != nil || d < 7.5 || d > 8.5 {
			t.Errorf("rung %dp concatenates to %s seconds, want the source's 8", r.Height, probe["duration"])
		}
	}

	// The shared audio representation, once, for the whole ladder.
	audio := f.concatRepresentation(t, len(f.res.Renditions))
	probe := ffprobeEntries(t, audio, "stream=codec_name,channels:format=duration")
	if probe["codec_name"] != "aac" || probe["channels"] != "2" {
		t.Errorf("shared audio = %s/%s channels, want aac/2", probe["codec_name"], probe["channels"])
	}
	if d, err := strconv.ParseFloat(probe["duration"], 64); err != nil || d < 7.5 || d > 8.6 {
		t.Errorf("shared audio concatenates to %s seconds, want the source's 8", probe["duration"])
	}

	// CMAF, not merely fMP4: the cmfc brand is what -format_options
	// movflags=+cmaf buys, and it is the difference between a CMAF tree and an
	// fMP4 one that happens to look similar.
	initPath, err := f.blobs.Path(f.prefix + "/cmaf/" + cmafInitSegmentName(0))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	brands := ffprobeEntries(t, initPath, "format_tags=compatible_brands")
	if !strings.Contains(brands["compatible_brands"], "cmfc") {
		t.Errorf("init segment brands = %q, want the cmfc CMAF brand", brands["compatible_brands"])
	}
	// One display aspect ratio for the whole video adaptation set. Without it the
	// dash muxer refuses to write the manifest at all ("Conflicting stream aspect
	// ratios values in Adaptation Set 1"), because the ladder's even-rounded
	// widths put 854x480 next to 1280x720.
	pars := regexp.MustCompile(`par="([^"]*)"`).FindAllStringSubmatch(mpd, -1)
	if len(pars) == 0 {
		t.Fatalf("MPD publishes no aspect ratio:\n%s", mpd)
	}
	for _, p := range pars {
		if p[1] != "16:9" {
			t.Errorf("adaptation set par = %q, want the source's 16:9:\n%s", p[1], mpd)
		}
	}
}

// concatRepresentation writes init+segments of one representation into a single
// file and returns its path — the decision-doc technique for proving a CMAF
// representation is decodable media.
func (f cmafFixture) concatRepresentation(t *testing.T, rep int) string {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "rep"+strconv.Itoa(rep)+".mp4")
	out, err := os.Create(dst)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	defer func() { _ = out.Close() }()

	names := []string{cmafInitSegmentName(rep)}
	for _, rel := range f.rels() {
		if strings.HasPrefix(rel, "cmaf/chunk-"+strconv.Itoa(rep)+"-") {
			names = append(names, path.Base(rel))
		}
	}
	sort.Strings(names[1:])
	if len(names) < 2 {
		t.Fatalf("representation %d has no segments to concatenate", rep)
	}
	for _, name := range names {
		rc, err := f.blobs.Open(context.Background(), f.prefix+"/cmaf/"+name)
		if err != nil {
			t.Fatalf("Open %q: %v", name, err)
		}
		if _, err := io.Copy(out, rc); err != nil {
			t.Fatalf("copy %q: %v", name, err)
		}
		_ = rc.Close()
	}
	return dst
}

// TestCMAFMasterAndPlaylistsAreServable pins the manifests Vidra authors over
// ffmpeg's: version 7 with CODECS everywhere (Safari will not start an fMP4
// variant without one), independent segments, an audio group, trick-play
// entries, and media playlists that are VOD with no wall-clock date-times.
func TestCMAFMasterAndPlaylistsAreServable(t *testing.T) {
	f := newCMAFFixture(t, hd720WithAudio, false)

	master := f.read(t, "master.m3u8")
	for _, want := range []string{
		"#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n",
		"#EXT-X-MEDIA:TYPE=AUDIO,",
		`URI="cmaf/media_3.m3u8"`,
		"RESOLUTION=1280x720", "RESOLUTION=854x480", "RESOLUTION=640x360",
		"cmaf/media_0.m3u8\n", "cmaf/media_1.m3u8\n", "cmaf/media_2.m3u8\n",
		"#EXT-X-I-FRAME-STREAM-INF:",
		`URI="cmaf/iframe-0.m3u8"`,
	} {
		if !strings.Contains(master, want) {
			t.Errorf("master missing %q:\n%s", want, master)
		}
	}
	if n := strings.Count(master, "#EXT-X-STREAM-INF:"); n != 3 {
		t.Errorf("master has %d variants, want 3:\n%s", n, master)
	}
	// Every variant carries a CODECS attribute with BOTH codecs, taken from
	// ffmpeg's own manifest rather than probed.
	codecsRE := regexp.MustCompile(`#EXT-X-STREAM-INF:[^\n]*CODECS="(avc1\.[0-9a-f]{6},mp4a\.40\.2)"`)
	if got := codecsRE.FindAllString(master, -1); len(got) != 3 {
		t.Errorf("only %d of 3 variants carry a video+audio CODECS attribute:\n%s", len(got), master)
	}
	if strings.Contains(master, "AUDIO=") != strings.Contains(master, "#EXT-X-MEDIA:") {
		t.Errorf("the audio group is referenced without being declared, or vice versa:\n%s", master)
	}

	for rep := 0; rep < 4; rep++ {
		playlist := f.read(t, "cmaf/"+cmafMediaPlaylistName(rep))
		if strings.Contains(playlist, "PROGRAM-DATE-TIME") {
			t.Errorf("representation %d still carries wall-clock date-times:\n%s", rep, playlist)
		}
		if !strings.Contains(playlist, "#EXT-X-PLAYLIST-TYPE:VOD") {
			t.Errorf("representation %d is not declared VOD:\n%s", rep, playlist)
		}
		if !strings.Contains(playlist, "#EXT-X-ENDLIST") {
			t.Errorf("representation %d has no ENDLIST:\n%s", rep, playlist)
		}
	}

	// Trick-play, per rung, as byte ranges into a single fMP4.
	for i := range f.res.Renditions {
		iframe := f.read(t, "cmaf/"+cmafIFramePlaylistName(i))
		for _, want := range []string{"#EXT-X-I-FRAMES-ONLY", "#EXT-X-BYTERANGE:", "#EXT-X-MAP:URI=\"" + cmafIFrameMediaName(i) + "\""} {
			if !strings.Contains(iframe, want) {
				t.Errorf("trick-play %d missing %q:\n%s", i, want, iframe)
			}
		}
	}
}

// TestCMAFProgressiveDownloadsStillWork is the compatibility guard for the
// download routes, which are the reason a CMAF video still has per-rung
// directories at all. internal/httpapi/downloads.go resolves these keys through
// media.HLSDownloadKey / HLSAudioDownloadKey, so their names and their stream
// composition must be exactly what MPEG-TS produced.
func TestCMAFProgressiveDownloadsStillWork(t *testing.T) {
	f := newCMAFFixture(t, hd720WithAudio, false)

	for _, r := range f.res.Renditions {
		muxed := HLSDownloadKey(r.KeyPrefix, true)
		videoOnly := HLSDownloadKey(r.KeyPrefix, false)
		assertStoredStreamTypes(t, f.blobs, muxed, "video", "audio")
		assertStoredStreamTypes(t, f.blobs, videoOnly, "video")

		p, err := f.blobs.Path(muxed)
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		probe := ffprobeEntries(t, p, "stream=width,height")
		if probe["width"] != strconv.Itoa(r.Width) || probe["height"] != strconv.Itoa(r.Height) {
			t.Errorf("%s is %sx%s, want %dx%d", muxed, probe["width"], probe["height"], r.Width, r.Height)
		}
	}
	assertStoredStreamTypes(t, f.blobs, HLSAudioDownloadKey(f.res.MasterKey), "audio")

	// Rendition sizes must still total the stored tree: the shared segment set
	// is attributed to the top rung, so the SUM is what has to be exact.
	var total int64
	for _, r := range f.res.Renditions {
		if r.SizeBytes <= 0 {
			t.Errorf("rendition %dp has size %d", r.Height, r.SizeBytes)
		}
		total += r.SizeBytes
	}
	var stored int64
	for _, key := range f.keys {
		p, err := f.blobs.Path(key)
		if err != nil {
			t.Fatalf("Path %q: %v", key, err)
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %q: %v", key, err)
		}
		stored += info.Size()
	}
	if total != stored {
		t.Errorf("rendition sizes total %d, stored tree is %d — summing video_renditions.size_bytes must be the tree size", total, stored)
	}
}

// TestCMAFSilentSourceTranscodes covers the shape a silent upload produces: no
// audio representation at all, no audio group in the master, no audio-only
// download, and a muxed progressive MP4 that is still valid video.
func TestCMAFSilentSourceTranscodes(t *testing.T) {
	f := newCMAFFixture(t, []string{
		"-f", "lavfi", "-i", "testsrc2=duration=4:size=320x240:rate=25",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
	}, false)

	if len(f.res.Renditions) != 1 {
		t.Fatalf("renditions = %+v, want one (cap-at-source)", f.res.Renditions)
	}
	for _, rel := range f.rels() {
		if strings.HasPrefix(rel, "cmaf/init-1.") || strings.HasPrefix(rel, "cmaf/media_1.") {
			t.Errorf("a silent source produced a second representation: %q", rel)
		}
		if rel == HLSAudioDownloadFilename {
			t.Errorf("a silent source produced an audio-only download")
		}
	}
	master := f.read(t, "master.m3u8")
	if strings.Contains(master, "EXT-X-MEDIA") || strings.Contains(master, "AUDIO=") {
		t.Errorf("silent master advertises audio that does not exist:\n%s", master)
	}
	assertStoredStreamTypes(t, f.blobs, HLSDownloadKey(f.res.Renditions[0].KeyPrefix, true), "video")
	assertStoredStreamTypes(t, f.blobs, HLSDownloadKey(f.res.Renditions[0].KeyPrefix, false), "video")
}

// TestCMAFStreamsLadderIntoStorage runs the whole thing with SetStreamOutput on:
// the dash muxer PUTs through the loopback sidecar, the manifests coalesce
// (including the .mpd, which dashenc rewrites once per segment), and the
// post-processing reads its own output back over HTTP. The stored tree must be
// the same one the scratch path produces — streaming is a transport choice and
// nothing more.
func TestCMAFStreamsLadderIntoStorage(t *testing.T) {
	f := newCMAFFixture(t, hd720WithAudio, true)

	if f.res.Format != HLSFormatCMAF {
		t.Errorf("Format = %q", f.res.Format)
	}
	if len(f.res.Renditions) != 3 {
		t.Fatalf("renditions = %+v, want 3", f.res.Renditions)
	}
	stored := map[string]bool{}
	for _, rel := range f.rels() {
		stored[rel] = true
	}
	for _, want := range []string{
		"master.m3u8", "audio.m4a",
		"cmaf/stream.mpd", "cmaf/media_0.m3u8", "cmaf/media_3.m3u8",
		"cmaf/init-0.mp4", "cmaf/init-3.mp4",
		"cmaf/iframe-0.m3u8", "cmaf/iframe-0.mp4",
		"720p/video.mp4", "720p/video-only.mp4",
	} {
		if !stored[want] {
			t.Errorf("streamed tree is missing %q", want)
		}
	}
	if stored["cmaf/"+cmafFFmpegMasterFilename] {
		t.Error("ffmpeg's discarded master survived a streamed transcode; it is flushed into the store before finalisation runs and must be deleted there")
	}
	// The dash muxer writes local files through a .tmp-then-rename dance. The HLS
	// muxer needs -hls_flags -temp_file to suppress it over HTTP (an object store
	// has no rename, so the sink would keep the temp-named object verbatim) and
	// the dash muxer has no such flag — so this asserts it does not need one.
	for _, rel := range f.rels() {
		if strings.HasSuffix(rel, ".tmp") {
			t.Errorf("a temp-named object reached the store: %q", rel)
		}
	}
	// The manifest fixups must have landed IN THE STORE, not only in a scratch
	// copy — this is the read-modify-write path through the sidecar.
	if pl := f.read(t, "cmaf/media_0.m3u8"); strings.Contains(pl, "PROGRAM-DATE-TIME") || !strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Errorf("streamed media playlist was never finalised:\n%s", pl)
	}
	if ip := f.read(t, "cmaf/iframe-0.m3u8"); !strings.Contains(ip, "#EXT-X-I-FRAMES-ONLY") {
		t.Errorf("streamed trick-play playlist was never finalised:\n%s", ip)
	}
	// And the segments are still real media.
	probe := ffprobeEntries(t, f.concatRepresentation(t, 0), "stream=codec_name,width,height")
	if probe["codec_name"] != "h264" || probe["width"] != "1280" || probe["height"] != "720" {
		t.Errorf("streamed top rung concatenates to %v, want h264 1280x720", probe)
	}
	// The riskiest read-back of all: the muxed progressive download is remuxed
	// from TWO inputs (the rung's representation AND the shared audio one), both
	// fetched back through the sidecar over HTTP. If either read failed the asset
	// would be silently video-only.
	assertStoredStreamTypes(t, f.blobs, HLSDownloadKey(f.res.Renditions[0].KeyPrefix, true), "video", "audio")
	assertStoredStreamTypes(t, f.blobs, HLSDownloadKey(f.res.Renditions[0].KeyPrefix, false), "video")
	assertStoredStreamTypes(t, f.blobs, HLSAudioDownloadKey(f.res.MasterKey), "audio")
}

// TestPackagerRollbackStillProducesTheMPEGTSTree is the rollback proof: the same
// transcoder, the same source, TRANSCODING_PACKAGER=ts, and the old tree comes
// back exactly as it was — no CMAF directory, no MPD, and the format recorded on
// the result says so.
func TestPackagerRollbackStillProducesTheMPEGTSTree(t *testing.T) {
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
	srcPath, err := blobs.Path(srcKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("ffmpeg", append(append([]string{"-y"}, hd720WithAudio...), srcPath)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}
	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false")
	}
	if err := tc.SetPackager(PackagerTS); err != nil {
		t.Fatalf("SetPackager: %v", err)
	}
	res, err := tc.Transcode(context.Background(), videoID, srcKey)
	if err != nil {
		t.Fatalf("MPEG-TS transcode: %v", err)
	}
	if res.Format != HLSFormatTS {
		t.Errorf("Format = %q, want %q", res.Format, HLSFormatTS)
	}
	prefix := HLSKeyPrefix(videoID)
	keys, err := blobs.ListKeys(context.Background(), prefix)
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	var tsSegments int
	for _, k := range keys {
		rel := strings.TrimPrefix(k, prefix+"/")
		if strings.HasPrefix(rel, cmafDirName+"/") || strings.HasSuffix(rel, ".mpd") || strings.HasSuffix(rel, ".m4s") {
			t.Errorf("the MPEG-TS rollback produced a CMAF artefact: %q", rel)
		}
		if strings.HasPrefix(rel, "720p/seg_") && strings.HasSuffix(rel, ".ts") {
			tsSegments++
		}
	}
	if tsSegments == 0 {
		t.Errorf("no MPEG-TS segments: %v", keys)
	}
	master := readKey(t, blobs, res.MasterKey)
	if !strings.Contains(master, "#EXT-X-VERSION:4") || !strings.Contains(master, "720p/playlist.m3u8") {
		t.Errorf("the MPEG-TS master is not the one it has always been:\n%s", master)
	}
	assertStoredStreamTypes(t, blobs, HLSDownloadKey(prefix+"/720p", true), "video", "audio")
}

func readKey(t *testing.T, blobs *storage.Local, key string) string {
	t.Helper()
	rc, err := blobs.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open %q: %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	b, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return string(b)
}

// ffprobeEntries runs ffprobe with -show_entries and returns the flat key=value
// pairs it prints.
func ffprobeEntries(t *testing.T, path, entries string) map[string]string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error",
		"-show_entries", entries, "-of", "default=noprint_wrappers=1", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %q: %v\n%s", path, err, out)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		k, v, ok := strings.Cut(strings.TrimSpace(line), "=")
		if ok {
			got[strings.TrimPrefix(k, "TAG:")] = v
		}
	}
	return got
}

// attrOf reads the first occurrence of an XML attribute out of a manifest.
func attrOf(t *testing.T, doc, name string) string {
	t.Helper()
	re := regexp.MustCompile(name + `="([^"]*)"`)
	m := re.FindStringSubmatch(doc)
	if m == nil {
		t.Fatalf("no %s attribute in:\n%s", name, doc)
	}
	return m[1]
}

// --- geometry: CMAF must render exactly what MPEG-TS renders --------------

// Ladder rung dimensions are planned from ffprobe's CODED width/height, which
// for a great many real uploads is NOT the shape the video is displayed at: a
// phone shoots 1920x1080 with a 90-degree display matrix, a DVD rip is 720x576
// with a 64:45 sample aspect. The MPEG-TS path survives that because ffmpeg's
// scale filter sets each output's sample aspect ratio to whatever preserves the
// SOURCE's display ratio — nothing in the pipeline has to understand rotation or
// anamorphic pixels for the result to be right.
//
// Anything CMAF adds to that filter chain breaks it silently: the tree is
// well-formed, every manifest validates, and the video is simply the wrong shape
// forever. So these fixtures do not assert an expected ratio in the abstract.
// They transcode the SAME source both ways and require the geometry to match,
// which is the only claim that stays true for source shapes nobody thought of.

// probedDAR returns the display aspect ratio ffprobe reports for a file, as an
// exact rational reduced from its coded size and sample aspect ratio.
func probedDAR(t *testing.T, path string) string {
	t.Helper()
	p := ffprobeEntries(t, path, "stream=width,height,sample_aspect_ratio,display_aspect_ratio")
	w, err := strconv.Atoi(p["width"])
	if err != nil {
		t.Fatalf("width %q: %v", p["width"], err)
	}
	h, err := strconv.Atoi(p["height"])
	if err != nil {
		t.Fatalf("height %q: %v", p["height"], err)
	}
	sarN, sarD := 1, 1
	if sar := p["sample_aspect_ratio"]; sar != "" && sar != "N/A" && sar != "0:1" {
		if n, d, ok := strings.Cut(sar, ":"); ok {
			sarN, _ = strconv.Atoi(n)
			sarD, _ = strconv.Atoi(d)
		}
	}
	if sarN <= 0 || sarD <= 0 {
		sarN, sarD = 1, 1
	}
	num, den := w*sarN, h*sarD
	g := gcd(num, den)
	return strconv.Itoa(num/g) + ":" + strconv.Itoa(den/g)
}

func gcd(a, b int) int {
	for b != 0 {
		a, b = b, a%b
	}
	if a == 0 {
		return 1
	}
	return a
}

// assertCMAFGeometryMatchesMPEGTS transcodes one source both ways and requires
// every rung's decoded display aspect ratio to agree.
func assertCMAFGeometryMatchesMPEGTS(t *testing.T, genArgs []string) {
	t.Helper()
	cmaf := newPackagedFixture(t, PackagerCMAF, genArgs, false)
	ts := newPackagedFixture(t, PackagerTS, genArgs, false)

	if len(cmaf.res.Renditions) != len(ts.res.Renditions) {
		t.Fatalf("ladders differ: cmaf %d rungs, ts %d", len(cmaf.res.Renditions), len(ts.res.Renditions))
	}
	for i, r := range cmaf.res.Renditions {
		// The MPEG-TS reference: its progressive download is a stream copy of the
		// rung, so it carries the encoded aspect signalling verbatim.
		tsPath, err := ts.blobs.Path(HLSDownloadKey(ts.res.Renditions[i].KeyPrefix, false))
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		wantDAR := probedDAR(t, tsPath)

		// The CMAF side, read from the segments themselves rather than a
		// derivative, so a wrong filter cannot hide behind a remux.
		gotDAR := probedDAR(t, cmaf.concatRepresentation(t, i))
		if gotDAR != wantDAR {
			t.Errorf("rung %dp: CMAF segments display at %s, MPEG-TS at %s — the ladder is rendering this source at the wrong shape",
				r.Height, gotDAR, wantDAR)
		}
		// And the progressive download, which is what /download serves.
		cmafPath, err := cmaf.blobs.Path(HLSDownloadKey(r.KeyPrefix, false))
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if got := probedDAR(t, cmafPath); got != wantDAR {
			t.Errorf("rung %dp download: CMAF %s, MPEG-TS %s", r.Height, got, wantDAR)
		}
	}

	// The MPD must publish that same ratio, once, for the whole video adaptation
	// set — the muxer refuses to write one that mixes ratios, so this doubles as
	// proof there was never a conflict to "fix".
	mpd := cmaf.read(t, "cmaf/"+cmafManifestFilename)
	pars := regexp.MustCompile(`par="([^"]*)"`).FindAllStringSubmatch(mpd, -1)
	if len(pars) != 1 {
		t.Fatalf("MPD declares %d video aspect ratios, want exactly 1:\n%s", len(pars), mpd)
	}
	tsTop, err := ts.blobs.Path(HLSDownloadKey(ts.res.Renditions[0].KeyPrefix, false))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if want := probedDAR(t, tsTop); pars[0][1] != want {
		t.Errorf("MPD par = %q, want the source's %q", pars[0][1], want)
	}
}

// TestCMAFGeometryRotatedSource is the case that motivated all of this: a
// portrait clip recorded as landscape-plus-display-matrix. ffmpeg autorotates
// before the filter graph, so the graph sees 1080x1920 and scale's corrective
// SAR carries the portrait shape through a ladder whose rung dimensions were
// planned from the landscape coded size. Pinning the ladder to those coded
// dimensions instead renders it landscape — a 3.16x horizontal stretch, baked
// into the segments.
func TestCMAFGeometryRotatedSource(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	src := rotatedSource(t)
	assertCMAFGeometryMatchesMPEGTS(t, []string{"-i", src, "-c", "copy"})
}

// rotatedSource writes a 1920x1080 clip carrying a 90-degree display matrix and
// returns its path.
func rotatedSource(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	flat := filepath.Join(dir, "flat.mp4")
	if out, err := exec.Command("ffmpeg", "-y", "-f", "lavfi",
		"-i", "testsrc2=duration=4:size=1920x1080:rate=25",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", flat).CombinedOutput(); err != nil {
		t.Fatalf("generate: %v\n%s", err, out)
	}
	rotated := filepath.Join(dir, "rotated.mp4")
	if out, err := exec.Command("ffmpeg", "-y", "-display_rotation", "90",
		"-i", flat, "-c", "copy", rotated).CombinedOutput(); err != nil {
		t.Fatalf("rotate: %v\n%s", err, out)
	}
	probe := ffprobeEntries(t, rotated, "stream_side_data=rotation")
	if probe["rotation"] == "" {
		t.Skip("this ffmpeg does not write a display matrix; the fixture would prove nothing")
	}
	return rotated
}

// TestCMAFGeometryAnamorphicSource: 720x576 with a 64:45 sample aspect displays
// at 16:9, not the 5:4 its coded size suggests. Same failure mode, no rotation
// involved, and the shape of every PAL-sourced upload in an archive.
func TestCMAFGeometryAnamorphicSource(t *testing.T) {
	assertCMAFGeometryMatchesMPEGTS(t, []string{
		"-f", "lavfi", "-i", "testsrc2=duration=4:size=720x576:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", "-aspect", "16:9",
	})
}

// TestCMAFGeometrySingleRungSource: with one rung there is nothing to keep
// consistent — no adaptation set can conflict with itself — so any aspect filter
// here is pure downside. The source is deliberately anamorphic (320x240 coded,
// displayed 16:9, i.e. sample aspect 4:3) so that pinning to the rung's coded
// ratio would visibly squash it; a square-pixel fixture would pass either way
// and prove nothing.
func TestCMAFGeometrySingleRungSource(t *testing.T) {
	assertCMAFGeometryMatchesMPEGTS(t, []string{
		"-f", "lavfi", "-i", "testsrc2=duration=4:size=320x240:rate=25",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-aspect", "16:9",
	})
}

// TestCMAFSilentSourceDeclaresNoAudioAdaptationSet: the dash muxer writes an
// EMPTY <AdaptationSet contentType="audio"/> when the set is declared and no
// stream lands in it, and an adaptation set with no Representation is invalid
// DASH — a manifest some players reject outright.
func TestCMAFSilentSourceDeclaresNoAudioAdaptationSet(t *testing.T) {
	f := newCMAFFixture(t, []string{
		"-f", "lavfi", "-i", "testsrc2=duration=4:size=320x240:rate=25",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
	}, false)
	mpd := f.read(t, "cmaf/"+cmafManifestFilename)
	if strings.Contains(mpd, `contentType="audio"`) {
		t.Errorf("silent source declared an audio adaptation set:\n%s", mpd)
	}
	if n := strings.Count(mpd, "<AdaptationSet"); n != 1 {
		t.Errorf("silent MPD has %d adaptation sets, want 1 (video):\n%s", n, mpd)
	}
	if n := strings.Count(mpd, "<Representation "); n != 1 {
		t.Errorf("silent MPD has %d representations, want 1:\n%s", n, mpd)
	}
}

// --- phase-3 item 6.2: audio-only sources ------------------------------------

// audioOnlySource generates a real audio-only file (in an MP4 container, which
// is what an audio-only upload of an accepted extension actually looks like).
var audioOnlySource = []string{
	"-f", "lavfi", "-i", "sine=frequency=440:duration=13:sample_rate=48000",
	"-ac", "2", "-c:a", "aac", "-b:a", "128k",
}

// TestCMAFAudioOnlySourceProducesAnAudioRendition is the end-to-end proof that a
// source with no video is packaged rather than dead-lettered. Before this, the
// ladder planner returned nothing for it, TranscodeHLS read that as "no
// probeable video dimensions", and the queue retried five times and gave up —
// so a podcast episode could be uploaded and published but never transcoded.
func TestCMAFAudioOnlySourceProducesAnAudioRendition(t *testing.T) {
	f := newCMAFFixture(t, audioOnlySource, false)

	if f.res.Format != HLSFormatCMAF {
		t.Errorf("Format = %q, want %q", f.res.Format, HLSFormatCMAF)
	}
	if f.res.MasterKey != f.prefix+"/master.m3u8" {
		t.Errorf("MasterKey = %q, want the same key every other tree uses", f.res.MasterKey)
	}
	// No rendition rows: a video_renditions row describes a RESOLUTION, and this
	// has none. Serving gates on the playlist row, not on rendition rows.
	if len(f.res.Renditions) != 0 {
		t.Errorf("renditions = %+v, want none for an audio-only source", f.res.Renditions)
	}

	// The tree shape, pinned. No rung directories, no video.mp4, no
	// video-only.mp4, no trick-play — and exactly one representation.
	segRE := regexp.MustCompile(`^cmaf/chunk-0-[0-9]{5}\.m4s$`)
	var shape []string
	segments := 0
	for _, rel := range f.rels() {
		if segRE.MatchString(rel) {
			segments++
			continue
		}
		shape = append(shape, rel)
	}
	sort.Strings(shape)
	wantShape := []string{
		"audio.m4a",
		"cmaf/init-0.mp4",
		"cmaf/media_0.m3u8",
		"cmaf/stream.mpd",
		"master.m3u8",
	}
	if strings.Join(shape, "\n") != strings.Join(wantShape, "\n") {
		t.Errorf("stored tree shape:\n got %s\nwant %s", strings.Join(shape, "\n "), strings.Join(wantShape, "\n "))
	}
	if segments == 0 {
		t.Error("the audio representation has no segments")
	}

	// The MPD: exactly one adaptation set, and it is the audio one. A declared-
	// but-empty video set would be invalid DASH for the same reason a declared-
	// but-empty audio set is on a silent video.
	mpd := f.read(t, "cmaf/"+cmafManifestFilename)
	if n := strings.Count(mpd, "<AdaptationSet"); n != 1 {
		t.Errorf("audio-only MPD has %d adaptation sets, want 1:\n%s", n, mpd)
	}
	if !strings.Contains(mpd, `contentType="audio"`) {
		t.Errorf("audio-only MPD declares no audio adaptation set:\n%s", mpd)
	}
	if strings.Contains(mpd, `contentType="video"`) {
		t.Errorf("audio-only MPD declares a video adaptation set:\n%s", mpd)
	}
	if n := strings.Count(mpd, "<Representation "); n != 1 {
		t.Errorf("audio-only MPD has %d representations, want 1:\n%s", n, mpd)
	}

	// The master: RFC 8216's audio-only presentation — ONE variant naming the
	// audio media playlist directly, an audio codec in CODECS, and no RESOLUTION.
	master := f.read(t, "master.m3u8")
	if n := strings.Count(master, "#EXT-X-STREAM-INF:"); n != 1 {
		t.Errorf("audio-only master has %d variants, want 1:\n%s", n, master)
	}
	if strings.Contains(master, "RESOLUTION=") {
		t.Errorf("audio-only variant declares a RESOLUTION:\n%s", master)
	}
	if !strings.Contains(master, `CODECS="mp4a.`) {
		t.Errorf("audio-only variant names no audio codec:\n%s", master)
	}
	if strings.Contains(master, "#EXT-X-MEDIA:") {
		t.Errorf("audio-only master published a rendition GROUP; RFC 8216 wants a variant:\n%s", master)
	}
	if strings.Contains(master, "#EXT-X-I-FRAME-STREAM-INF") {
		t.Errorf("audio-only master advertises trick-play:\n%s", master)
	}
	if !strings.Contains(master, "\ncmaf/media_0.m3u8\n") {
		t.Errorf("audio-only variant does not point at the audio media playlist:\n%s", master)
	}

	// The media playlist is VOD and carries no wall-clock date-times, exactly as
	// a video tree's is.
	media0 := f.read(t, "cmaf/media_0.m3u8")
	if !strings.Contains(media0, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Errorf("audio media playlist is not declared VOD:\n%s", media0)
	}
	if strings.Contains(media0, "#EXT-X-PROGRAM-DATE-TIME") {
		t.Errorf("audio media playlist kept its wall-clock date-times:\n%s", media0)
	}

	// The audio download really is playable audio.
	audioKey := HLSAudioDownloadKey(f.res.MasterKey)
	p, err := f.blobs.Path(audioKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if got := ffprobeEntries(t, p, "stream=codec_type"); got["codec_type"] != "audio" {
		t.Errorf("audio.m4a first stream is %q, want audio", got["codec_type"])
	}
}

// TestCMAFAudioOnlyStreamedLadder proves the same source survives the streaming
// transport, where every object is PUT into the store as ffmpeg writes it and
// finalisation reads its own output back over HTTP.
func TestCMAFAudioOnlyStreamedLadder(t *testing.T) {
	f := newCMAFFixture(t, audioOnlySource, true)
	if len(f.res.Renditions) != 0 {
		t.Errorf("renditions = %+v, want none", f.res.Renditions)
	}
	for _, want := range []string{"master.m3u8", "audio.m4a", "cmaf/media_0.m3u8", "cmaf/init-0.mp4"} {
		if !slicesContains(f.rels(), want) {
			t.Errorf("streamed audio-only tree is missing %q: %v", want, f.rels())
		}
	}
	for _, rel := range f.rels() {
		if strings.HasSuffix(rel, cmafFFmpegMasterFilename) {
			t.Errorf("ffmpeg's discarded master was stored at %q", rel)
		}
	}
}

// TestTSPackagerRefusesAudioOnlySource pins the rollback path's contract. MPEG-TS
// cannot express an audio-only tree — audio is muxed INTO a rendition directory
// named for its height, and the file route only reaches "<NNN>p" — so it must
// say so plainly rather than fail somewhere deeper with a confusing message.
func TestTSPackagerRefusesAudioOnlySource(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	srcPath, err := blobs.Path(srcKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(srcPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if out, err := exec.Command("ffmpeg", append(append([]string{"-y"}, audioOnlySource...), srcPath)...).CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", err, out)
	}
	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false")
	}
	if err := tc.SetPackager(PackagerTS); err != nil {
		t.Fatalf("SetPackager: %v", err)
	}
	_, err = tc.Transcode(context.Background(), videoID, srcKey)
	if err == nil {
		t.Fatal("the MPEG-TS packager accepted an audio-only source")
	}
	if !strings.Contains(err.Error(), "audio-only requires the cmaf packager") {
		t.Errorf("error = %q, want it to name the packager an operator must switch to", err)
	}
	// Nothing may have been written for a source that was refused.
	keys, kerr := blobs.ListKeys(context.Background(), HLSKeyPrefix(videoID))
	if kerr != nil {
		t.Fatalf("ListKeys: %v", kerr)
	}
	if len(keys) != 0 {
		t.Errorf("a refused audio-only transcode stored %v", keys)
	}
}

func slicesContains(hay []string, needle string) bool {
	for _, s := range hay {
		if s == needle {
			return true
		}
	}
	return false
}
