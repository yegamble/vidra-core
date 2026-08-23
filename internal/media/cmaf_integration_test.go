//go:build integration

// Excluded from `make ci`; run with: go test -tags integration ./internal/media/
package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
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
	// codecs is the codec plan the fixture transcoded with, so an assertion can
	// walk representation indices the same way the packager laid them out.
	codecs []codecProfile
	// sourceKey is the generated source the fixture transcoded, so a test can
	// re-encode from the very same bytes.
	sourceKey string
}

// rep is the representation index of rung `rung` encoded with codec index
// `codec`, for a fixture's own ladder.
func (f cmafFixture) rep(codec, rung int) int { return codec*len(f.res.Renditions) + rung }

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
	return newFixture(t, fixtureOpts{packager: packager, genArgs: genArgs, stream: stream})
}

// fixtureOpts is one transcode fixture's whole configuration.
type fixtureOpts struct {
	packager string
	genArgs  []string
	stream   bool
	// hevc and av1 are the extra-codec knobs, exactly as an operator sets them.
	hevc bool
	av1  bool
	// hw and hwDevice are TRANSCODING_HW / TRANSCODING_HW_DEVICE. "" is off,
	// which is what every fixture that does not mention them gets.
	hw       string
	hwDevice string
}

func newFixture(t *testing.T, opts fixtureOpts) cmafFixture {
	t.Helper()
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	packager, genArgs, stream := opts.packager, opts.genArgs, opts.stream
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
	// In boot's own order: the hardware choice is recorded before the codec plan
	// is built, because it is the codec plan that the transform is applied to.
	if err := tc.SetHardware(opts.hw, opts.hwDevice); err != nil {
		t.Fatalf("SetHardware(%q, %q): %v", opts.hw, opts.hwDevice, err)
	}
	if err := tc.SetVideoCodecs(opts.hevc, opts.av1); err != nil {
		t.Fatalf("SetVideoCodecs(hevc %v, av1 %v): %v", opts.hevc, opts.av1, err)
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
	return cmafFixture{blobs: blobs, prefix: prefix, res: res, keys: keys, codecs: tc.videoCodecs(), sourceKey: srcKey}
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

// coverArtSourcePath generates a real audio file carrying a cover image — the
// shape a podcast episode or a music track actually has — and writes it to dst.
// It is built in two steps because ffmpeg only sets the attached_pic disposition
// on a stream it is muxing from a separate still input.
func coverArtSourcePath(t *testing.T, dst string) {
	t.Helper()
	dir := t.TempDir()
	audio := filepath.Join(dir, "audio.m4a")
	if out, err := exec.Command("ffmpeg", "-y", "-f", "lavfi",
		"-i", "sine=frequency=440:duration=8:sample_rate=48000",
		"-ac", "1", "-c:a", "aac", "-b:a", "64k", audio).CombinedOutput(); err != nil {
		t.Fatalf("generate audio: %v\n%s", err, out)
	}
	cover := filepath.Join(dir, "cover.png")
	if out, err := exec.Command("ffmpeg", "-y", "-f", "lavfi",
		"-i", "color=c=blue:s=600x600:d=1", "-frames:v", "1", cover).CombinedOutput(); err != nil {
		t.Fatalf("generate cover: %v\n%s", err, out)
	}
	if out, err := exec.Command("ffmpeg", "-y", "-i", audio, "-i", cover,
		"-map", "0:a", "-map", "1:v", "-c:a", "copy", "-c:v", "mjpeg",
		"-disposition:v", "attached_pic", dst).CombinedOutput(); err != nil {
		t.Fatalf("mux cover art: %v\n%s", err, out)
	}
}

// TestCoverArtSourceIsTranscodedAsAudioOnly is the regression for the failure
// the audio-only path was shipped to eliminate and, for its most common input,
// did not. ffprobe reports an attached picture as a codec_type "video" stream
// WITH REAL DIMENSIONS, so the file was never classified audio-only: a whole ABR
// ladder was planned from one still image and the trick-play pass then failed
// with "invalid trick-play segment duration" — five retries and a dead letter.
//
// Every step is asserted end to end against real ffmpeg: the probe's verdict,
// then a successful transcode with the audio-only tree shape.
func TestCoverArtSourceIsTranscodedAsAudioOnly(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	t.Setenv("TMPDIR", t.TempDir())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	srcPath, perr := blobs.Path(srcKey)
	if perr != nil {
		t.Fatalf("Path: %v", perr)
	}
	if merr := os.MkdirAll(filepath.Dir(srcPath), 0o755); merr != nil {
		t.Fatalf("mkdir: %v", merr)
	}
	coverArtSourcePath(t, srcPath)

	// The fixture is only worth anything if ffprobe really does present the
	// picture as video; if a future ffmpeg stops doing that, say so rather than
	// passing for the wrong reason.
	raw, rerr := exec.Command("ffprobe", "-v", "error", "-print_format", "json",
		"-show_streams", srcPath).Output()
	if rerr != nil {
		t.Fatalf("ffprobe: %v", rerr)
	}
	if !strings.Contains(string(raw), `"codec_type": "video"`) {
		t.Skip("this ffmpeg does not mux the cover as a video stream; the fixture would prove nothing")
	}

	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false")
	}
	if serr := tc.SetPackager(PackagerCMAF); serr != nil {
		t.Fatalf("SetPackager: %v", serr)
	}
	md, err := tc.Probe(context.Background(), srcKey)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if md.HasVideo() {
		t.Fatalf("probe read the cover art as video (%dx%d): the ladder would be planned from a still",
			md.Width, md.Height)
	}
	if !md.AudioOnly() {
		t.Fatalf("probe verdict = %+v, want audio-only", md)
	}
	if md.AudioKbps != 64 {
		t.Errorf("probed audio bitrate = %d kbps, want the source's 64", md.AudioKbps)
	}

	res, files, err := tc.TranscodeAll(context.Background(), videoID, srcKey, md, nil)
	if err != nil {
		t.Fatalf("TranscodeAll on an audio-with-cover-art source: %v", err)
	}
	if len(res.Renditions) != 0 || len(files) != 0 {
		t.Errorf("cover-art source produced %d renditions and %d web videos, want none",
			len(res.Renditions), len(files))
	}
	keys, kerr := blobs.ListKeys(context.Background(), HLSKeyPrefix(videoID))
	if kerr != nil {
		t.Fatalf("ListKeys: %v", kerr)
	}
	var rels []string
	segRE := regexp.MustCompile(`^cmaf/chunk-0-[0-9]{5}\.m4s$`)
	for _, k := range keys {
		rel := strings.TrimPrefix(k, HLSKeyPrefix(videoID)+"/")
		if !segRE.MatchString(rel) {
			rels = append(rels, rel)
		}
	}
	sort.Strings(rels)
	want := []string{"audio.m4a", "cmaf/init-0.mp4", "cmaf/media_0.m3u8", "cmaf/stream.mpd", "master.m3u8"}
	if strings.Join(rels, "\n") != strings.Join(want, "\n") {
		t.Errorf("cover-art tree shape:\n got %s\nwant %s", strings.Join(rels, "\n "), strings.Join(want, "\n "))
	}
	// No still image was encoded as a rendition, and no rung directory exists.
	for _, k := range keys {
		if strings.Contains(k, "600p") || strings.HasSuffix(k, "video.mp4") {
			t.Errorf("a rendition was built from the cover art: %s", k)
		}
	}
	// The audio budget follows the 64 kbps source rather than the ceiling.
	master := readObject(t, blobs, res.MasterKey)
	if wantBW := fmt.Sprintf("BANDWIDTH=%d,", 64*1000*11/10); !strings.Contains(string(master), wantBW) {
		t.Errorf("master does not declare the capped 64k budget (%s):\n%s", wantBW, master)
	}
}

// TestCoverArtSourceOnTSIsRefusedPermanently: the same source on the rollback
// packager gets the refusal every other audio-only source gets, and it is marked
// FINAL so the queue does not spend five attempts and a quarter of an hour of
// backoff rediscovering a verdict about the deployment's configuration.
func TestCoverArtSourceOnTSIsRefusedPermanently(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	t.Setenv("TMPDIR", t.TempDir())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	srcPath, perr := blobs.Path(srcKey)
	if perr != nil {
		t.Fatalf("Path: %v", perr)
	}
	if merr := os.MkdirAll(filepath.Dir(srcPath), 0o755); merr != nil {
		t.Fatalf("mkdir: %v", merr)
	}
	coverArtSourcePath(t, srcPath)

	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false")
	}
	if serr := tc.SetPackager(PackagerTS); serr != nil {
		t.Fatalf("SetPackager: %v", serr)
	}
	_, err = tc.Transcode(context.Background(), videoID, srcKey)
	if err == nil {
		t.Fatal("the MPEG-TS packager accepted an audio-with-cover-art source")
	}
	if !IsPermanent(err) {
		t.Errorf("error %q is retryable; a packager verdict cannot change between attempts", err)
	}
	if !strings.Contains(err.Error(), "audio-only requires the cmaf packager") {
		t.Errorf("error = %q, want it to name the packager an operator must switch to", err)
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

// --- phase-3 item 6.3: deriving the web videos instead of re-encoding them ---

// countingFFmpeg returns the path to a wrapper that logs every ffmpeg
// invocation's argument vector and then execs the real binary, plus a reader for
// the log. It is how "how many times did this decode the source?" becomes an
// assertion instead of a claim.
func countingFFmpeg(t *testing.T) (bin string, invocations func() []string) {
	t.Helper()
	real, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	dir := t.TempDir()
	log := filepath.Join(dir, "invocations.log")
	script := filepath.Join(dir, "ffmpeg-counting")
	body := "#!/bin/sh\nprintf '%s\\n' \"$*\" >> " + strconv.Quote(log) + "\nexec " + strconv.Quote(real) + " \"$@\"\n"
	if werr := os.WriteFile(script, []byte(body), 0o755); werr != nil {
		t.Fatalf("write wrapper: %v", werr)
	}
	return script, func() []string {
		b, rerr := os.ReadFile(log)
		if rerr != nil {
			return nil
		}
		var out []string
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line != "" {
				out = append(out, line)
			}
		}
		return out
	}
}

// decodePasses counts the invocations that DECODE the source: every one of them
// builds a filter graph over the input. The remuxes and the audio extraction are
// stream copies and carry none.
func decodePasses(invocations []string) []string {
	var out []string
	for _, inv := range invocations {
		if strings.Contains(inv, "-filter_complex") {
			out = append(out, inv)
		}
	}
	return out
}

// TestFullJobDecodesTheSourceTwiceNotThreeTimes is the measurement behind
// phase-3 item 6.3, taken against real ffmpeg and stated as a counterfactual:
// the SAME source is run both ways, so the number that matters is the
// difference, not an absolute anyone has to trust.
//
// The old shape ran the two targets independently — an HLS ladder decode, a
// trick-play decode, and a third whole decode plus one libx264 encode per rung
// for progressive MP4s the ladder had already produced. TranscodeAll derives
// those instead, at the one moment they are still on scratch.
func TestFullJobDecodesTheSourceTwiceNotThreeTimes(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	t.Setenv("TMPDIR", t.TempDir())

	newTranscoder := func(t *testing.T) (*HLSTranscoder, *storage.Local, uuid.UUID, string, func() []string) {
		t.Helper()
		blobs, err := storage.NewLocal(t.TempDir())
		if err != nil {
			t.Fatalf("NewLocal: %v", err)
		}
		videoID := uuid.New()
		srcKey := "web-videos/" + videoID.String() + ".mp4"
		srcPath, perr := blobs.Path(srcKey)
		if perr != nil {
			t.Fatalf("Path: %v", perr)
		}
		if merr := os.MkdirAll(filepath.Dir(srcPath), 0o755); merr != nil {
			t.Fatalf("mkdir: %v", merr)
		}
		if out, gerr := exec.Command("ffmpeg", append(append([]string{"-y"}, hd720WithAudio...), srcPath)...).CombinedOutput(); gerr != nil {
			t.Fatalf("ffmpeg generate: %v\n%s", gerr, out)
		}
		tc, ok := DetectHLSTranscoder(blobs)
		if !ok {
			t.Fatal("DetectHLSTranscoder = false")
		}
		if serr := tc.SetPackager(PackagerCMAF); serr != nil {
			t.Fatalf("SetPackager: %v", serr)
		}
		bin, invocations := countingFFmpeg(t)
		tc.bin = bin
		return tc, blobs, videoID, srcKey, invocations
	}

	// The old shape: two independent target calls.
	var before int
	t.Run("running the targets independently", func(t *testing.T) {
		tc, _, videoID, srcKey, invocations := newTranscoder(t)
		md, err := tc.Probe(context.Background(), srcKey)
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		if _, err := tc.TranscodeHLS(context.Background(), videoID, srcKey, md, nil); err != nil {
			t.Fatalf("TranscodeHLS: %v", err)
		}
		if _, err := tc.TranscodeWebVideos(context.Background(), videoID, srcKey, md, nil); err != nil {
			t.Fatalf("TranscodeWebVideos: %v", err)
		}
		before = len(decodePasses(invocations()))
		if before != 3 {
			t.Fatalf("independent targets made %d decode passes, want 3 (ladder, trick-play, web-video ladder):\n%s",
				before, strings.Join(decodePasses(invocations()), "\n"))
		}
	})

	// The new shape: one call, one ladder, derived downloads.
	t.Run("running them as one job", func(t *testing.T) {
		tc, blobs, videoID, srcKey, invocations := newTranscoder(t)
		md, err := tc.Probe(context.Background(), srcKey)
		if err != nil {
			t.Fatalf("Probe: %v", err)
		}
		res, files, err := tc.TranscodeAll(context.Background(), videoID, srcKey, md, nil)
		if err != nil {
			t.Fatalf("TranscodeAll: %v", err)
		}
		passes := decodePasses(invocations())
		if len(passes) != 2 {
			t.Fatalf("a full job made %d decode passes, want 2 (ladder, trick-play):\n%s",
				len(passes), strings.Join(passes, "\n"))
		}
		if len(passes) >= before {
			t.Errorf("a full job decodes %d times, no better than the %d it took independently", len(passes), before)
		}
		// And no libx264 encode outside those two passes: every other invocation
		// is a stream copy.
		for _, inv := range invocations() {
			if strings.Contains(inv, "-filter_complex") {
				continue
			}
			if strings.Contains(inv, "libx264") {
				t.Errorf("an invocation outside the ladder passes runs a video encoder:\n%s", inv)
			}
		}

		// The derived files are the real deliverable: one per rung, at the same
		// keys, byte-valid, and byte-IDENTICAL to the ladder's own download for
		// that rung — which is what makes copying them equivalent to encoding them.
		if len(files) != len(res.Renditions) {
			t.Fatalf("derived %d web videos for %d renditions", len(files), len(res.Renditions))
		}
		for i, f := range files {
			rendition := res.Renditions[i]
			wantKey := "web-videos/" + videoID.String() + "/" + strconv.Itoa(rendition.Height) + "p.mp4"
			if f.StorageKey != wantKey {
				t.Errorf("derived key = %q, want %q", f.StorageKey, wantKey)
			}
			if f.Height != rendition.Height || f.Width != rendition.Width {
				t.Errorf("derived %dx%d, want the rung's %dx%d", f.Width, f.Height, rendition.Width, rendition.Height)
			}
			if f.SizeBytes <= 0 || len(f.SHA256) != 64 {
				t.Errorf("derived file = %+v, want a real size and a sha256", f)
			}
			derived := readObject(t, blobs, f.StorageKey)
			ladder := readObject(t, blobs, HLSDownloadKey(rendition.KeyPrefix, true))
			if !bytes.Equal(derived, ladder) {
				t.Errorf("rung %dp: the derived web video is not the ladder's own download (%d vs %d bytes)",
					rendition.Height, len(derived), len(ladder))
			}
			if int64(len(derived)) != f.SizeBytes {
				t.Errorf("rung %dp: recorded size %d, stored %d bytes", rendition.Height, f.SizeBytes, len(derived))
			}
			if got := fmt.Sprintf("%x", sha256.Sum256(derived)); got != f.SHA256 {
				t.Errorf("rung %dp: recorded sha256 %q, stored bytes hash to %q", rendition.Height, f.SHA256, got)
			}
			// Playable: the right codecs at the right size, with audio.
			p, perr := blobs.Path(f.StorageKey)
			if perr != nil {
				t.Fatalf("Path: %v", perr)
			}
			v := ffprobeStream(t, p, "v:0", "stream=codec_name,width,height")
			if v["codec_name"] != "h264" || v["width"] != strconv.Itoa(rendition.Width) || v["height"] != strconv.Itoa(rendition.Height) {
				t.Errorf("rung %dp derived video stream = %v", rendition.Height, v)
			}
			if a := ffprobeStream(t, p, "a:0", "stream=codec_name"); a["codec_name"] != "aac" {
				t.Errorf("rung %dp derived file has no AAC audio: %v", rendition.Height, a)
			}
		}
	})
}

// TestStandaloneWebVideoTargetStillEncodes: a target='web_video' rebuild has no
// ladder to derive from — it is precisely the operator action for when the
// progressive files are missing or wrong — so it must keep its own encode.
func TestStandaloneWebVideoTargetStillEncodes(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	t.Setenv("TMPDIR", t.TempDir())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	srcPath, perr := blobs.Path(srcKey)
	if perr != nil {
		t.Fatalf("Path: %v", perr)
	}
	if merr := os.MkdirAll(filepath.Dir(srcPath), 0o755); merr != nil {
		t.Fatalf("mkdir: %v", merr)
	}
	if out, gerr := exec.Command("ffmpeg", append(append([]string{"-y"},
		"-f", "lavfi", "-i", "testsrc2=duration=4:size=640x360:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest"),
		srcPath)...).CombinedOutput(); gerr != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", gerr, out)
	}
	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false")
	}
	bin, invocations := countingFFmpeg(t)
	tc.bin = bin

	md, err := tc.Probe(context.Background(), srcKey)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	files, err := tc.TranscodeWebVideos(context.Background(), videoID, srcKey, md, nil)
	if err != nil {
		t.Fatalf("TranscodeWebVideos: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("web videos = %+v, want one rung for a 360p source", files)
	}
	passes := decodePasses(invocations())
	if len(passes) != 1 {
		t.Fatalf("standalone web-video rebuild made %d decode passes, want its own single one:\n%s",
			len(passes), strings.Join(passes, "\n"))
	}
	if !strings.Contains(passes[0], "libx264") {
		t.Errorf("standalone rebuild did not encode:\n%s", passes[0])
	}
	p, perr := blobs.Path(files[0].StorageKey)
	if perr != nil {
		t.Fatalf("Path: %v", perr)
	}
	if got := ffprobeStream(t, p, "v:0", "stream=codec_name"); got["codec_name"] != "h264" {
		t.Errorf("standalone rebuild produced %v", got)
	}
}

func readObject(t *testing.T, blobs *storage.Local, key string) []byte {
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
	return b
}

// TestFullJobDerivesWebVideosOnBothPackagers proves the derivation lives in the
// packager seam rather than in CMAF: the MPEG-TS rollback path produces the same
// per-rung download at the same point in finalisation, so a deployment rolled
// back to it keeps both the saving and the files.
func TestFullJobDerivesWebVideosOnBothPackagers(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	for _, packager := range []string{PackagerTS, PackagerCMAF} {
		t.Run(packager, func(t *testing.T) {
			t.Setenv("TMPDIR", t.TempDir())
			blobs, err := storage.NewLocal(t.TempDir())
			if err != nil {
				t.Fatalf("NewLocal: %v", err)
			}
			videoID := uuid.New()
			srcKey := "web-videos/" + videoID.String() + ".mp4"
			srcPath, perr := blobs.Path(srcKey)
			if perr != nil {
				t.Fatalf("Path: %v", perr)
			}
			if merr := os.MkdirAll(filepath.Dir(srcPath), 0o755); merr != nil {
				t.Fatalf("mkdir: %v", merr)
			}
			if out, gerr := exec.Command("ffmpeg", append(append([]string{"-y"},
				"-f", "lavfi", "-i", "testsrc2=duration=4:size=854x480:rate=25",
				"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
				"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest"),
				srcPath)...).CombinedOutput(); gerr != nil {
				t.Fatalf("ffmpeg generate: %v\n%s", gerr, out)
			}
			tc, ok := DetectHLSTranscoder(blobs)
			if !ok {
				t.Fatal("DetectHLSTranscoder = false")
			}
			if serr := tc.SetPackager(packager); serr != nil {
				t.Fatalf("SetPackager: %v", serr)
			}
			bin, invocations := countingFFmpeg(t)
			tc.bin = bin

			md, err := tc.Probe(context.Background(), srcKey)
			if err != nil {
				t.Fatalf("Probe: %v", err)
			}
			res, files, err := tc.TranscodeAll(context.Background(), videoID, srcKey, md, nil)
			if err != nil {
				t.Fatalf("TranscodeAll: %v", err)
			}
			if n := len(decodePasses(invocations())); n != 2 {
				t.Errorf("%s made %d decode passes, want 2", packager, n)
			}
			if len(files) == 0 || len(files) != len(res.Renditions) {
				t.Fatalf("%s derived %d web videos for %d renditions", packager, len(files), len(res.Renditions))
			}
			for i, f := range files {
				derived := readObject(t, blobs, f.StorageKey)
				ladder := readObject(t, blobs, HLSDownloadKey(res.Renditions[i].KeyPrefix, true))
				if !bytes.Equal(derived, ladder) {
					t.Errorf("%s rung %dp: derived bytes differ from the ladder's own download",
						packager, res.Renditions[i].Height)
				}
			}
		})
	}
}

// TestFullJobDerivesWebVideosFromAStreamedLadder is the case most likely to be
// wrong by construction: with SetStreamOutput no segment ever touches the disk,
// so "the bytes are still local" has to be true for a reason rather than by
// luck. It is — the progressive MP4s are ALWAYS written to scratch, because
// +faststart rewinds to move the moov atom and an HTTP PUT body cannot be
// rewound — and this pins it.
func TestFullJobDerivesWebVideosFromAStreamedLadder(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	t.Setenv("TMPDIR", t.TempDir())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	srcPath, perr := blobs.Path(srcKey)
	if perr != nil {
		t.Fatalf("Path: %v", perr)
	}
	if merr := os.MkdirAll(filepath.Dir(srcPath), 0o755); merr != nil {
		t.Fatalf("mkdir: %v", merr)
	}
	if out, gerr := exec.Command("ffmpeg", append(append([]string{"-y"},
		"-f", "lavfi", "-i", "testsrc2=duration=4:size=854x480:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-c:a", "aac", "-shortest"),
		srcPath)...).CombinedOutput(); gerr != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", gerr, out)
	}
	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false")
	}
	if serr := tc.SetPackager(PackagerCMAF); serr != nil {
		t.Fatalf("SetPackager: %v", serr)
	}
	tc.SetStreamOutput(true)
	md, err := tc.Probe(context.Background(), srcKey)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	res, files, err := tc.TranscodeAll(context.Background(), videoID, srcKey, md, nil)
	if err != nil {
		t.Fatalf("TranscodeAll: %v", err)
	}
	if len(files) == 0 || len(files) != len(res.Renditions) {
		t.Fatalf("streamed job derived %d web videos for %d renditions", len(files), len(res.Renditions))
	}
	for i, f := range files {
		derived := readObject(t, blobs, f.StorageKey)
		ladder := readObject(t, blobs, HLSDownloadKey(res.Renditions[i].KeyPrefix, true))
		if !bytes.Equal(derived, ladder) {
			t.Errorf("streamed rung %dp: derived bytes differ from the ladder's own download",
				res.Renditions[i].Height)
		}
	}
}

// TestAudioOnlyFullJobDerivesNoWebVideos: there are no rungs, so there are no
// progressive video derivatives to make. The job must still succeed — its
// deliverable is the audio tree.
func TestAudioOnlyFullJobDerivesNoWebVideos(t *testing.T) {
	if _, err := exec.LookPath("ffprobe"); err != nil {
		t.Skip("ffprobe not installed")
	}
	t.Setenv("TMPDIR", t.TempDir())
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	videoID := uuid.New()
	srcKey := "web-videos/" + videoID.String() + ".mp4"
	srcPath, perr := blobs.Path(srcKey)
	if perr != nil {
		t.Fatalf("Path: %v", perr)
	}
	if merr := os.MkdirAll(filepath.Dir(srcPath), 0o755); merr != nil {
		t.Fatalf("mkdir: %v", merr)
	}
	if out, gerr := exec.Command("ffmpeg", append(append([]string{"-y"}, audioOnlySource...), srcPath)...).CombinedOutput(); gerr != nil {
		t.Fatalf("ffmpeg generate: %v\n%s", gerr, out)
	}
	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false")
	}
	if serr := tc.SetPackager(PackagerCMAF); serr != nil {
		t.Fatalf("SetPackager: %v", serr)
	}
	md, err := tc.Probe(context.Background(), srcKey)
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	res, files, err := tc.TranscodeAll(context.Background(), videoID, srcKey, md, nil)
	if err != nil {
		t.Fatalf("TranscodeAll on an audio-only source: %v", err)
	}
	if len(files) != 0 || len(res.Renditions) != 0 {
		t.Errorf("audio-only full job produced %d web videos and %d renditions, want none", len(files), len(res.Renditions))
	}
	if res.MasterKey == "" {
		t.Error("audio-only full job produced no master playlist")
	}
	keys, kerr := blobs.ListKeys(context.Background(), "web-videos/"+videoID.String())
	if kerr != nil {
		t.Fatalf("ListKeys: %v", kerr)
	}
	if len(keys) != 0 {
		t.Errorf("audio-only full job wrote progressive video objects: %v", keys)
	}
}

// --- phase-3 item 5: HEVC and AV1 representations ----------------------------

// requireEncoders skips when this ffmpeg cannot encode what the test is about.
// The image ships libx265 and libsvtav1; a developer's Homebrew or distro build
// may not, and a skipped test is honest where a failed one would be a lie about
// the code.
func requireEncoders(t *testing.T, names ...string) {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	out, err := exec.Command(bin, "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Skipf("ffmpeg -encoders: %v", err)
	}
	have := ffmpegEncoderNames(out)
	for _, n := range names {
		if !have[n] {
			t.Skipf("this ffmpeg has no %q encoder", n)
		}
	}
}

// assertCMAFRepresentation concatenates one representation and asserts it decodes
// as the codec, tag and size expected. This is the check that catches a
// mis-mapped representation and a broken init segment, both of which produce a
// tree that looks perfect and plays nothing.
func assertCMAFRepresentation(t *testing.T, f cmafFixture, rep int, wantCodec, wantTag string, r HLSRendition) {
	t.Helper()
	joined := f.concatRepresentation(t, rep)
	probe := ffprobeEntries(t, joined, "stream=codec_name,codec_tag_string,width,height:format=duration")
	if probe["codec_name"] != wantCodec {
		t.Errorf("representation %d is %q, want %q", rep, probe["codec_name"], wantCodec)
	}
	if wantTag != "" && probe["codec_tag_string"] != wantTag {
		t.Errorf("representation %d sample entry is %q, want %q", rep, probe["codec_tag_string"], wantTag)
	}
	if probe["width"] != strconv.Itoa(r.Width) || probe["height"] != strconv.Itoa(r.Height) {
		t.Errorf("representation %d concatenates to %sx%s, want the %dp rung's %dx%d — it is mapped to the wrong rung",
			rep, probe["width"], probe["height"], r.Height, r.Width, r.Height)
	}
	if d, err := strconv.ParseFloat(probe["duration"], 64); err != nil || d < 7.0 || d > 9.0 {
		t.Errorf("representation %d concatenates to %s seconds, want the source's 8", rep, probe["duration"])
	}
}

// TestCMAFHEVCLadderIsTaggedHVC1 is the end-to-end proof for the one detail that
// makes HEVC usable at all. ffmpeg writes HEVC into fMP4 as `hev1` unless told
// otherwise, warns, and carries on; Safari — the browser HEVC exists for here —
// refuses hev1, and ffmpeg's own master then computes an EMPTY codec string for
// that variant, so the tree is unplayable everywhere rather than only on Safari.
// The assertion is on the SAMPLE ENTRY in the stored init segment, not on the
// argument vector, because that is the artefact a player reads.
func TestCMAFHEVCLadderIsTaggedHVC1(t *testing.T) {
	requireEncoders(t, "libx265")
	f := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: hd720WithAudio, hevc: true})

	if len(f.res.Renditions) != 3 {
		t.Fatalf("renditions = %+v, want the 720p/480p/360p ladder", f.res.Renditions)
	}
	// Renditions are RESOLUTIONS, not representations: a second codec must not
	// invent three more video_renditions rows for the same three resolutions.
	for i, r := range f.res.Renditions {
		if r.KeyPrefix != f.prefix+"/"+strconv.Itoa(r.Height)+"p" {
			t.Errorf("rendition %d key prefix = %q", i, r.KeyPrefix)
		}
	}

	// Six video representations (3 rungs x 2 codecs) plus the ONE shared audio
	// one: enabling a codec must not duplicate the audio.
	for rep := 0; rep < 7; rep++ {
		if !f.stored(t, "cmaf/"+cmafInitSegmentName(rep)) {
			t.Errorf("representation %d has no init segment", rep)
		}
	}
	if f.stored(t, "cmaf/"+cmafInitSegmentName(7)) {
		t.Errorf("an eighth representation exists; audio was encoded per codec")
	}

	for i, r := range f.res.Renditions {
		assertCMAFRepresentation(t, f, f.rep(0, i), "h264", "avc1", r)
		assertCMAFRepresentation(t, f, f.rep(1, i), "hevc", "hvc1", r)
	}

	master := f.read(t, "master.m3u8")
	if n := strings.Count(master, "#EXT-X-STREAM-INF:"); n != 6 {
		t.Errorf("master has %d variants, want 6 (3 rungs x 2 codecs):\n%s", n, master)
	}
	// `hvc1` alone or `hvc1.<profile/level>`: older ffmpeg builds abbreviate.
	hvc1 := regexp.MustCompile(`#EXT-X-STREAM-INF:[^\n]*CODECS="hvc1(\.[^",]+)?,mp4a\.40\.2"`)
	if got := hvc1.FindAllString(master, -1); len(got) != 3 {
		t.Errorf("only %d of 3 HEVC variants carry an hvc1 CODECS attribute:\n%s", len(got), master)
	}
	if strings.Contains(master, "hev1") {
		t.Errorf("the master advertises hev1, which Safari refuses:\n%s", master)
	}
	if strings.Contains(master, `CODECS=",`) {
		t.Errorf("a variant has an empty video codec — the exact symptom of a missing hvc1 tag:\n%s", master)
	}

	// The MPD: one video adaptation set per codec, plus audio, and the HEVC set's
	// representations declare hvc1 too.
	mpd := f.read(t, "cmaf/"+cmafManifestFilename)
	if n := strings.Count(mpd, "<AdaptationSet"); n != 3 {
		t.Errorf("MPD has %d adaptation sets, want 3 (h264, hevc, audio):\n%s", n, mpd)
	}
	if n := len(regexp.MustCompile(`codecs="hvc1(\.[^"]*)?"`).FindAllString(mpd, -1)); n != 3 {
		t.Errorf("MPD declares %d hvc1 representations, want 3:\n%s", n, mpd)
	}
	if n := strings.Count(mpd, `contentType="video"`); n != 2 {
		t.Errorf("MPD has %d video adaptation sets, want one per codec:\n%s", n, mpd)
	}
	if n := strings.Count(mpd, `contentType="audio"`); n != 1 {
		t.Errorf("MPD has %d audio adaptation sets, want 1:\n%s", n, mpd)
	}

	// The compatibility floor is untouched: downloads, the audio file and
	// trick-play all still come off the H.264 representations.
	for _, r := range f.res.Renditions {
		assertStoredStreamTypes(t, f.blobs, HLSDownloadKey(r.KeyPrefix, true), "video", "audio")
		p, err := f.blobs.Path(HLSDownloadKey(r.KeyPrefix, false))
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if got := ffprobeEntries(t, p, "stream=codec_name"); got["codec_name"] != "h264" {
			t.Errorf("the %dp download is %q; downloads are the compatibility floor and must stay H.264", r.Height, got["codec_name"])
		}
	}
	assertStoredStreamTypes(t, f.blobs, HLSAudioDownloadKey(f.res.MasterKey), "audio")
	if n := strings.Count(master, "#EXT-X-I-FRAME-STREAM-INF:"); n != 3 {
		t.Errorf("%d trick-play entries, want one per rung (H.264 only):\n%s", n, master)
	}
	for rep := 3; rep < 7; rep++ {
		if f.stored(t, "cmaf/"+cmafIFramePlaylistName(rep)) {
			t.Errorf("trick-play was encoded for representation %d; it is H.264-only", rep)
		}
	}
}

// TestCMAFAV1Ladder is the same proof for AV1, whose interesting property is the
// rate control: SVT-AV1 REFUSES -maxrate with -b:v and the whole ffmpeg process
// dies before writing a byte, so a tree existing at all is the assertion.
func TestCMAFAV1Ladder(t *testing.T) {
	requireEncoders(t, "libsvtav1")
	f := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: hd720WithAudio, av1: true})

	if len(f.res.Renditions) != 3 {
		t.Fatalf("renditions = %+v", f.res.Renditions)
	}
	for i, r := range f.res.Renditions {
		assertCMAFRepresentation(t, f, f.rep(0, i), "h264", "avc1", r)
		assertCMAFRepresentation(t, f, f.rep(1, i), "av1", "av01", r)
	}
	master := f.read(t, "master.m3u8")
	av01 := regexp.MustCompile(`#EXT-X-STREAM-INF:[^\n]*CODECS="av01(\.[^",]+)?,mp4a\.40\.2"`)
	if got := av01.FindAllString(master, -1); len(got) != 3 {
		t.Errorf("only %d of 3 AV1 variants carry an av01 CODECS attribute:\n%s", len(got), master)
	}
	mpd := f.read(t, "cmaf/"+cmafManifestFilename)
	if n := len(regexp.MustCompile(`codecs="av01(\.[^"]*)?"`).FindAllString(mpd, -1)); n != 3 {
		t.Errorf("MPD declares %d av01 representations, want 3:\n%s", n, mpd)
	}

	// AV1 is budgeted at 0.55 of H.264's and declared with capped CRF's own peak
	// allowance — the manifests must agree with the encoder, because a variant
	// that under-declares its peak is chosen by an ABR player that cannot carry
	// it, and one that over-declares is passed over by a player that could.
	audioKbps := hlsRungBitrates[f.res.Renditions[0].Height].AudioKbps
	for i, r := range f.res.Renditions {
		rung := HLSRung{Height: r.Height, Width: r.Width, VideoKbps: hlsRungBitrates[r.Height].VideoKbps}
		want := cmafVariantBandwidth(av1Profile.videoKbps(rung), audioKbps, av1Profile)
		if !strings.Contains(master, "BANDWIDTH="+strconv.Itoa(want)+",") {
			t.Errorf("AV1 rung %d does not declare its own budget %d:\n%s", i, want, master)
		}
		// And it is still the cheapest variant at that resolution, which is the
		// entire point of shipping it: a wider peak allowance must not price AV1
		// above the codec it is supposed to undercut.
		h264 := cmafVariantBandwidth(h264Profile.videoKbps(rung), audioKbps, h264Profile)
		if want >= h264 {
			t.Errorf("AV1 rung %d declares %d against H.264's %d — the efficient codec must declare less", i, want, h264)
		}
	}
}

// TestCMAFMultiCodecLadder runs all three codecs at once and pins the whole
// tree: nine video representations plus one audio, every one of them decodable,
// the master ordered compat-first, and the MPD grouped one set per codec.
func TestCMAFMultiCodecLadder(t *testing.T) {
	requireEncoders(t, "libx265", "libsvtav1")
	f := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: hd720WithAudio, hevc: true, av1: true})

	rungs := len(f.res.Renditions)
	if rungs != 3 {
		t.Fatalf("renditions = %+v", f.res.Renditions)
	}
	want := []struct{ codec, tag string }{{"h264", "avc1"}, {"hevc", "hvc1"}, {"av1", "av01"}}
	for c, w := range want {
		for i, r := range f.res.Renditions {
			assertCMAFRepresentation(t, f, f.rep(c, i), w.codec, w.tag, r)
		}
	}
	// The audio representation sits after every video one, exactly once.
	audioRep := rungs * len(want)
	probe := ffprobeEntries(t, f.concatRepresentation(t, audioRep), "stream=codec_name,channels")
	if probe["codec_name"] != "aac" || probe["channels"] != "2" {
		t.Errorf("representation %d = %v, want the single shared aac/2 rendition", audioRep, probe)
	}
	if f.stored(t, "cmaf/"+cmafInitSegmentName(audioRep+1)) {
		t.Errorf("a representation exists past the shared audio one")
	}

	// Master ordering, pinned exactly: every H.264 variant, then every HEVC one,
	// then every AV1 one. A client that takes the first playable variant must
	// land on the codec everything can play.
	master := f.read(t, "master.m3u8")
	var variants []string
	for _, line := range strings.Split(master, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") {
			variants = append(variants, line)
		}
	}
	var wantOrder []string
	for c := range want {
		for i := range f.res.Renditions {
			wantOrder = append(wantOrder, "cmaf/"+cmafMediaPlaylistName(f.rep(c, i)))
		}
	}
	if strings.Join(variants, ",") != strings.Join(wantOrder, ",") {
		t.Errorf("variant order:\n got %v\nwant %v", variants, wantOrder)
	}
	// And the first STREAM-INF in the file is H.264.
	if i := strings.Index(master, "#EXT-X-STREAM-INF:"); i < 0 || !regexp.MustCompile(`CODECS="avc1[.,"]`).MatchString(master[i:i+200]) {
		t.Errorf("the first variant is not H.264:\n%s", master)
	}

	// SCORE is what actually steers an Apple client, and it must rank the way the
	// registry says: resolution first, efficient codec second. Without it,
	// AVFoundation's fallback is close to "the highest BANDWIDTH it can play" —
	// which picks H.264, because an efficient codec declares LESS.
	scores := masterVariantScores(t, master)
	if len(scores) != rungs*len(want) {
		t.Fatalf("only %d of %d variants carry a SCORE:\n%s", len(scores), rungs*len(want), master)
	}
	for c, prof := range f.codecs {
		for i := range f.res.Renditions {
			if got, expect := scores[f.rep(c, i)], cmafVariantScore(i, rungs, prof); got != expect {
				t.Errorf("%s rung %d SCORE = %v, want %v", prof.Name, i, got, expect)
			}
		}
	}
	// The two properties, checked on the rendered manifest rather than on the
	// formula: the top rung of every codec outranks every lower rung, and at one
	// resolution hevc > av1 > h264.
	for i := 1; i < rungs; i++ {
		for c := range f.codecs {
			if scores[f.rep(c, 0)] <= scores[f.rep(c, i)] {
				t.Errorf("codec %d: rung 0 does not outrank rung %d", c, i)
			}
		}
	}
	for i := range f.res.Renditions {
		h264, hevc, av1 := scores[f.rep(0, i)], scores[f.rep(1, i)], scores[f.rep(2, i)]
		if !(hevc > av1 && av1 > h264) {
			t.Errorf("rung %d ranks h264 %v, hevc %v, av1 %v — want hevc > av1 > h264", i, h264, hevc, av1)
		}
	}

	// AVERAGE-BANDWIDTH comes from the bytes the tree actually stored, so every
	// variant has one and none of them exceeds its own declared peak.
	declared := declaredBandwidths(t, master)
	averages := averageBandwidths(t, master)
	if len(averages) != rungs*len(want) {
		t.Errorf("only %d of %d variants declare AVERAGE-BANDWIDTH:\n%s", len(averages), rungs*len(want), master)
	}
	for rep, avg := range averages {
		if avg <= 0 || avg > declared[rep] {
			t.Errorf("variant %d: AVERAGE-BANDWIDTH=%d against BANDWIDTH=%d", rep, avg, declared[rep])
		}
	}

	mpd := f.read(t, "cmaf/"+cmafManifestFilename)
	if n := strings.Count(mpd, `contentType="video"`); n != 3 {
		t.Errorf("MPD has %d video adaptation sets, want one per codec:\n%s", n, mpd)
	}
	if n := strings.Count(mpd, "<Representation "); n != rungs*len(want)+1 {
		t.Errorf("MPD has %d representations, want %d:\n%s", n, rungs*len(want)+1, mpd)
	}
	// Every segment lives under names the serving route already reaches — the
	// allow-list regex takes any representation index, and this is what proves it
	// rather than assuming it.
	reachable := regexp.MustCompile(
		`^(stream\.mpd|media_[0-9]+\.m3u8|init-[0-9]+\.mp4|chunk-[0-9]+-[0-9]+\.m4s|iframe-[0-9]+\.m3u8|iframe-[0-9]+\.mp4)$`)
	for _, rel := range f.rels() {
		if !strings.HasPrefix(rel, cmafDirName+"/") {
			continue
		}
		if name := strings.TrimPrefix(rel, cmafDirName+"/"); !reachable.MatchString(name) {
			t.Errorf("stored object %q is not reachable through the HLS file route's allow-list", rel)
		}
	}
	// Rendition sizes still total the stored tree with three codecs' segments in
	// it: the shared directory is attributed to the top rung whatever is in it.
	var total, stored int64
	for _, r := range f.res.Renditions {
		total += r.SizeBytes
	}
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
		t.Errorf("rendition sizes total %d, stored tree is %d", total, stored)
	}
}

// TestCMAFMultiCodecGeometryMatchesMPEGTS re-runs the geometry proof with every
// codec on. The filter graph now forks each rung's scaled frames to several
// encoders, and the trap it must not fall into is unchanged: the corrective
// sample aspect ratio scale computes has to survive the fork, for EVERY codec,
// or a rotated or anamorphic source is rendered at the wrong shape in the
// representations that H.264-only tests never look at.
func TestCMAFMultiCodecGeometryMatchesMPEGTS(t *testing.T) {
	requireEncoders(t, "libx265")
	anamorphic := []string{
		"-f", "lavfi", "-i", "testsrc2=duration=4:size=720x576:rate=25",
		"-f", "lavfi", "-i", "sine=frequency=440:duration=4",
		"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-shortest", "-aspect", "16:9",
	}
	multi := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: anamorphic, hevc: true})
	ts := newPackagedFixture(t, PackagerTS, anamorphic, false)

	if len(multi.res.Renditions) != len(ts.res.Renditions) {
		t.Fatalf("ladders differ: multi-codec %d rungs, ts %d", len(multi.res.Renditions), len(ts.res.Renditions))
	}
	for i, r := range multi.res.Renditions {
		tsPath, err := ts.blobs.Path(HLSDownloadKey(ts.res.Renditions[i].KeyPrefix, false))
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		wantDAR := probedDAR(t, tsPath)
		for c := range multi.codecs {
			if got := probedDAR(t, multi.concatRepresentation(t, multi.rep(c, i))); got != wantDAR {
				t.Errorf("rung %dp codec %s: segments display at %s, MPEG-TS at %s",
					r.Height, multi.codecs[c].Name, got, wantDAR)
			}
		}
	}
	// Each adaptation set publishes ONE ratio, and it is the same one — the muxer
	// refuses a set that mixes them, so this doubles as proof the fork did not
	// introduce a conflict inside either codec's set.
	mpd := multi.read(t, "cmaf/"+cmafManifestFilename)
	pars := regexp.MustCompile(`par="([^"]*)"`).FindAllStringSubmatch(mpd, -1)
	if len(pars) != len(multi.codecs) {
		t.Fatalf("MPD declares %d video aspect ratios, want one per codec (%d):\n%s", len(pars), len(multi.codecs), mpd)
	}
	tsTop, err := ts.blobs.Path(HLSDownloadKey(ts.res.Renditions[0].KeyPrefix, false))
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	want := probedDAR(t, tsTop)
	for _, p := range pars {
		if p[1] != want {
			t.Errorf("adaptation set par = %q, want the source's %q:\n%s", p[1], want, mpd)
		}
	}
}

// TestCMAFMultiCodecStreamedLadder runs a two-codec ladder straight into the
// object store. The dash muxer now PUTs three times as many objects through the
// loopback sidecar and finalisation reads more of them back over HTTP; the tree
// has to come out the same either way, because streaming is a transport choice
// and nothing more.
func TestCMAFMultiCodecStreamedLadder(t *testing.T) {
	requireEncoders(t, "libx265")
	f := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: hd720WithAudio, hevc: true, stream: true})

	if len(f.res.Renditions) != 3 {
		t.Fatalf("renditions = %+v", f.res.Renditions)
	}
	for rep := 0; rep < 7; rep++ {
		if !f.stored(t, "cmaf/"+cmafInitSegmentName(rep)) {
			t.Errorf("streamed tree is missing representation %d's init segment", rep)
		}
	}
	for _, rel := range f.rels() {
		if strings.HasSuffix(rel, ".tmp") || strings.HasSuffix(rel, cmafFFmpegMasterFilename) {
			t.Errorf("a non-tree object reached the store: %q", rel)
		}
	}
	assertCMAFRepresentation(t, f, f.rep(1, 0), "hevc", "hvc1", f.res.Renditions[0])
	if pl := f.read(t, "cmaf/"+cmafMediaPlaylistName(f.rep(1, 0))); strings.Contains(pl, "PROGRAM-DATE-TIME") ||
		!strings.Contains(pl, "#EXT-X-PLAYLIST-TYPE:VOD") {
		t.Errorf("an HEVC media playlist was never finalised:\n%s", pl)
	}
	assertStoredStreamTypes(t, f.blobs, HLSDownloadKey(f.res.Renditions[0].KeyPrefix, true), "video", "audio")
}

// TestMPEGTSRefusesExtraCodecs is the rollback contract, stated where an operator
// meets it. Rolling TRANSCODING_PACKAGER back to `ts` while HEVC is still on must
// stop the process — quietly writing H.264-only trees while the operator believes
// they are shipping H.265 is the failure this refuses to have.
func TestMPEGTSRefusesExtraCodecs(t *testing.T) {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		t.Skip("ffmpeg not installed")
	}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	tc, ok := DetectHLSTranscoder(blobs)
	if !ok {
		t.Fatal("DetectHLSTranscoder = false")
	}
	if err := tc.SetPackager(PackagerTS); err != nil {
		t.Fatalf("SetPackager: %v", err)
	}
	if err := tc.SetVideoCodecs(true, false); err == nil {
		t.Fatal("the MPEG-TS packager accepted HEVC")
	}
	// And with the codecs off it is exactly the transcoder it always was.
	if err := tc.SetVideoCodecs(false, false); err != nil {
		t.Fatalf("SetVideoCodecs(false, false): %v", err)
	}
}

// --- the declared BANDWIDTH has to bound what is delivered -------------------

// spikySource is 30 seconds of flat grey followed by 6 seconds of dense moving
// detail — one segment's worth of hard content at the end of a clip the encoder
// has spent the whole of coasting.
//
// It is the shape that catches an UNCAPPED encoder and nothing else does. A
// uniformly hard clip does not: the encoder never builds up the slack it later
// spends, so the average and the peak are the same number and any rate control at
// all looks fine. This one lets an unbounded encoder arrive at the last segment
// with everything still to spend.
//
// The hard tail is a detailed moving PATTERN rather than per-pixel random noise,
// deliberately. Random noise is the incompressible limit — not video, and no
// ladder's declared bitrate survives it for any codec; on such a clip H.264 only
// "passes" because its budget is nearly twice AV1's. testsrc2 is genuinely
// adversarial while still being something a camera could produce.
var spikySource = []string{
	"-f", "lavfi", "-i", "color=c=gray:s=854x480:r=25:d=30,format=yuv420p",
	"-f", "lavfi", "-i", "testsrc2=s=854x480:r=25:d=6",
	"-f", "lavfi", "-i", "sine=frequency=440:duration=36",
	"-filter_complex", "[0:v][1:v]concat=n=2:v=1:a=0[v]",
	"-map", "[v]", "-map", "2:a",
	"-c:v", "libx264", "-preset", "ultrafast", "-crf", "16", "-pix_fmt", "yuv420p",
	"-c:a", "aac", "-shortest",
}

// bandwidthTolerance is how far over its declared BANDWIDTH a variant's worst
// segment may land.
//
// What it covers is the part of the overshoot that belongs to the ENCODER BUILD
// rather than to this code: real container overhead on a real segment, and a rate
// controller that converges over a window instead of per segment. The
// codec-specific part is not in here — each codec's own target is chosen to fit
// its own budget (see av1CRF) so all three land in the same band.
//
// Measured across the two ffmpeg builds this project runs against — 8.1 (the
// shipped Alpine image) and 6.1 (the distribution build on the CI runner and on
// a Debian/Ubuntu host):
//
//	           ffmpeg 8.1        ffmpeg 6.1
//	H.264      1.030x  1.031x    1.061x  1.077x
//	HEVC       0.945x  0.928x    0.987x  1.006x
//	AV1        0.842x  0.622x    0.822x  0.669x
//
// 1.10 admits all of it. It does NOT mask AV1, which is the failure this test
// exists for: AV1's worst measurement is 0.842x, so it would have to regress by
// a third before this number hid anything — and the uncapped encode it replaced
// ran at 2.93x on the shipped build. The widest column is H.264's, whose ~10%
// container allowance predates codec profiles entirely.
const bandwidthTolerance = 1.10

// TestCMAFDeclaredBandwidthBoundsWhatIsDelivered is the regression for the AV1
// rate-control bug, and it is stated for every codec because the property is not
// AV1's alone: a master playlist's BANDWIDTH is a PROMISE about the worst segment,
// an ABR player picks on it, and a player cannot un-fetch a segment already in
// flight. A variant that delivers multiples of its declared peak drains the
// buffer and stalls — and hls.js's abandon-rules rescue cannot fire, because its
// expected-length estimate is taken from max(loaded, …) and collapses the very
// delay it is watching for.
func TestCMAFDeclaredBandwidthBoundsWhatIsDelivered(t *testing.T) {
	requireEncoders(t, "libx265", "libsvtav1")
	f := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: spikySource, hevc: true, av1: true})

	rungs := len(f.res.Renditions)
	if rungs == 0 {
		t.Fatal("no renditions")
	}
	audioPeak := f.peakRepBitrate(t, rungs*len(f.codecs))
	if audioPeak <= 0 {
		t.Fatal("the shared audio representation measured no bitrate")
	}

	master := f.read(t, "master.m3u8")
	declared := declaredBandwidths(t, master)
	if len(declared) != rungs*len(f.codecs) {
		t.Fatalf("master declares %d variants, want %d", len(declared), rungs*len(f.codecs))
	}
	for c, prof := range f.codecs {
		for i, r := range f.res.Renditions {
			rep := f.rep(c, i)
			// What a client actually receives for this variant in its worst
			// moment: the representation's largest segment, plus the audio
			// rendition it plays alongside it.
			peak := f.peakRepBitrate(t, rep) + audioPeak
			want := declared[rep]
			ratio := float64(peak) / float64(want)
			name := strconv.Itoa(r.Height) + "p"
			t.Logf("%s %s: declared %d, worst segment delivers %d (%.3fx)", prof.Name, name, want, peak, ratio)
			if ratio > bandwidthTolerance {
				t.Errorf("%s %s delivers %.3fx its declared BANDWIDTH (%d against %d) — an ABR player picks on the declared number and cannot un-fetch the segment",
					prof.Name, name, ratio, peak, want)
			}
		}
	}

	// AVERAGE-BANDWIDTH is the other half of an honest declaration, and on this
	// fixture it is dramatically lower than the peak — 30 seconds of flat grey.
	// That is exactly the information a steady-state estimator wants and exactly
	// what a budget-derived number cannot express.
	for rep, avg := range averageBandwidths(t, master) {
		if avg <= 0 {
			t.Errorf("variant %d declares no AVERAGE-BANDWIDTH", rep)
			continue
		}
		if avg > declared[rep] {
			t.Errorf("variant %d declares an average (%d) above its peak (%d)", rep, avg, declared[rep])
		}
	}

	// THE COUNTERFACTUAL, measured rather than remembered: the same AV1 rung,
	// same budget, same source, encoded the way this pipeline used to — plain VBR
	// with no ceiling, which is all libsvtav1 accepts alongside -b:v.
	//
	// Its MAGNITUDE is a property of the SVT-AV1 build and not of this code, so
	// only its DIRECTION is asserted. Measured: 4.0x apart on SVT-AV1 4.1
	// (ffmpeg 8.1, the shipped image), where the uncapped encode reaches 2.93x its
	// declared BANDWIDTH; 1.4x apart on SVT-AV1 1.7 (ffmpeg 6.1), whose plain VBR
	// happens to track its target far more closely. The bug got WORSE as the
	// encoder got better, which is exactly the kind of thing a hard-coded
	// expectation would have hidden.
	//
	// The build-independent guard against losing the ceiling is not here at all —
	// it is TestEveryCodecIsRateCapped, which reads the argument vector and needs
	// no encoder to run. This measures the outcome on whatever build is present;
	// that pins the code.
	top := f.res.Renditions[0]
	budget := av1Profile.videoKbps(HLSRung{VideoKbps: hlsRungBitrates[top.Height].VideoKbps})
	uncapped := uncappedAV1PeakBitrate(t, f, top, budget)
	capped := f.peakRepBitrate(t, f.rep(len(f.codecs)-1, 0))
	t.Logf("counterfactual at %dp: uncapped AV1 peaks at %d bps, capped CRF at %d (%.1fx apart)",
		top.Height, uncapped, capped, float64(uncapped)/float64(capped))
	if uncapped <= capped {
		t.Errorf("plain VBR peaked at %d and capped CRF at %d — the ceiling is no longer doing anything, or the fixture no longer distinguishes them", uncapped, capped)
	}
}

// peakRepBitrate is the worst segment bit rate of one representation: for every
// segment its stored bytes over its own EXTINF duration, maximised.
//
// Per-segment is the right granularity because a segment is the unit a player
// fetches: it commits to the whole thing on one bandwidth estimate.
func (f cmafFixture) peakRepBitrate(t *testing.T, rep int) int {
	t.Helper()
	playlist := f.read(t, "cmaf/"+cmafMediaPlaylistName(rep))
	var peak int
	var duration float64
	for _, line := range strings.Split(playlist, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "#EXTINF:") {
			d, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimPrefix(line, "#EXTINF:"), ","), 64)
			if err != nil {
				t.Fatalf("representation %d: bad EXTINF %q", rep, line)
			}
			duration = d
			continue
		}
		if line == "" || strings.HasPrefix(line, "#") || duration <= 0 {
			continue
		}
		p, err := f.blobs.Path(f.prefix + "/cmaf/" + line)
		if err != nil {
			t.Fatalf("Path %q: %v", line, err)
		}
		info, err := os.Stat(p)
		if err != nil {
			t.Fatalf("stat %q: %v", line, err)
		}
		if bps := int(float64(info.Size()) * 8 / duration); bps > peak {
			peak = bps
		}
		duration = 0
	}
	return peak
}

// uncappedAV1PeakBitrate re-encodes one rung from the fixture's own source with
// plain VBR — no ceiling — and returns its worst segment bit rate.
func uncappedAV1PeakBitrate(t *testing.T, f cmafFixture, r HLSRendition, budgetKbps int) int {
	t.Helper()
	src, err := f.blobs.Path(f.sourceKey)
	if err != nil {
		t.Fatalf("Path: %v", err)
	}
	dir := t.TempDir()
	args := []string{
		"-y", "-i", src,
		"-filter_complex", fmt.Sprintf("[0:v]scale=%d:%d[v0]", r.Width, r.Height),
		"-map", "[v0]",
		"-c:v:0", "libsvtav1", "-preset:v:0", "8", "-pix_fmt:v:0", "yuv420p",
		"-b:v:0", strconv.Itoa(budgetKbps) + "k",
		"-force_key_frames:v:0", "expr:gte(t,n_forced*6)",
		"-f", "dash", "-seg_duration", "6", "-dash_segment_type", "mp4",
		"-use_template", "1", "-use_timeline", "1", "-ignore_io_errors", "0",
		"-init_seg_name", cmafInitSegmentPattern, "-media_seg_name", cmafMediaSegmentPattern,
		"-adaptation_sets", "id=0,streams=v",
		filepath.Join(dir, cmafManifestFilename),
	}
	if out, cerr := exec.Command("ffmpeg", args...).CombinedOutput(); cerr != nil {
		t.Fatalf("uncapped AV1 encode: %v\n%s", cerr, out)
	}
	segments, err := filepath.Glob(filepath.Join(dir, "chunk-0-*.m4s"))
	if err != nil || len(segments) == 0 {
		t.Fatalf("uncapped AV1 encode produced no segments: %v", err)
	}
	var peak int
	for _, p := range segments {
		info, serr := os.Stat(p)
		if serr != nil {
			t.Fatalf("stat: %v", serr)
		}
		// Every segment but the last is a full hlsSegmentSeconds.
		if bps := int(info.Size()) * 8 / hlsSegmentSeconds; bps > peak {
			peak = bps
		}
	}
	return peak
}

// declaredBandwidths / averageBandwidths read one attribute off every
// EXT-X-STREAM-INF, keyed by the representation its URI names.
func declaredBandwidths(t *testing.T, master string) map[int]int {
	t.Helper()
	return masterVariantAttr(t, master, "BANDWIDTH")
}

func averageBandwidths(t *testing.T, master string) map[int]int {
	t.Helper()
	return masterVariantAttr(t, master, "AVERAGE-BANDWIDTH")
}

// masterVariantScores reads the SCORE off every variant, keyed by representation.
func masterVariantScores(t *testing.T, master string) map[int]float64 {
	t.Helper()
	out := map[int]float64{}
	pending, ok := "", false
	for _, line := range strings.Split(master, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			pending, ok = m3u8Attr(line, "SCORE")
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			if !ok {
				continue
			}
			rep, isRep := cmafRepOfMediaPlaylist(strings.TrimPrefix(line, cmafDirName+"/"))
			if !isRep {
				continue
			}
			v, err := strconv.ParseFloat(pending, 64)
			if err != nil {
				t.Fatalf("variant %d has a non-numeric SCORE %q", rep, pending)
			}
			out[rep] = v
			ok = false
		}
	}
	return out
}

func masterVariantAttr(t *testing.T, master, attr string) map[int]int {
	t.Helper()
	out := map[int]int{}
	pending, ok := "", false
	for _, line := range strings.Split(master, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "#EXT-X-STREAM-INF:"):
			pending, ok = m3u8Attr(line, attr)
		case line == "" || strings.HasPrefix(line, "#"):
		default:
			if !ok {
				continue
			}
			rep, isRep := cmafRepOfMediaPlaylist(strings.TrimPrefix(line, cmafDirName+"/"))
			if !isRep {
				continue
			}
			n, err := strconv.Atoi(pending)
			if err != nil {
				t.Fatalf("variant %d has a non-numeric %s %q", rep, attr, pending)
			}
			out[rep] = n
			ok = false
		}
	}
	return out
}

// stored reports whether one path relative to the video's prefix is in the tree.
func (f cmafFixture) stored(t *testing.T, rel string) bool {
	t.Helper()
	for _, r := range f.rels() {
		if r == rel {
			return true
		}
	}
	return false
}

// ffprobeStream is ffprobeEntries restricted to ONE stream, so a file with both
// video and audio does not have its later streams overwrite the earlier ones in
// the flat key/value output.
func ffprobeStream(t *testing.T, path, selector, entries string) map[string]string {
	t.Helper()
	out, err := exec.Command("ffprobe", "-v", "error", "-select_streams", selector,
		"-show_entries", entries, "-of", "default=noprint_wrappers=1", path).CombinedOutput()
	if err != nil {
		t.Fatalf("ffprobe %q %s: %v\n%s", path, selector, err, out)
	}
	got := map[string]string{}
	for _, line := range strings.Split(string(out), "\n") {
		if k, v, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			got[k] = v
		}
	}
	return got
}

// --- phase-3 item 7: hardware transcoding ------------------------------------

// requireHardware skips unless this machine can actually run the backend. It
// checks BOTH halves of the availability question the detection helper asks —
// the encoder is in this ffmpeg, and the device is plausibly here — because a
// build with h264_vaapi on a host with no GPU produces a failure that says
// nothing about the code under test.
func requireHardware(t *testing.T, hw string) {
	t.Helper()
	bin, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not installed")
	}
	out, err := exec.Command(bin, "-hide_banner", "-encoders").Output()
	if err != nil {
		t.Skipf("ffmpeg -encoders: %v", err)
	}
	probe := HardwareProbe{
		Encoders:    ffmpegEncoderNames(out),
		GOOS:        runtime.GOOS,
		RenderNodes: RenderNodes("/dev/dri"),
	}
	for _, a := range DetectHardware(probe) {
		if a.Backend != hw {
			continue
		}
		if !a.Available {
			t.Skipf("this machine cannot run the %s backend: %s", hw, a.Why)
		}
		return
	}
	t.Skipf("no such backend %q", hw)
}

// TestHardwareCMAFLadderPreservesCodecIdentity is the end-to-end proof of the
// invariant the whole feature rests on: a rung encoded on the GPU is the SAME
// representation as one encoded on the CPU.
//
// The assertions are on the stored artefacts — the concatenated segments, the
// master ffmpeg wrote, the MPD — rather than on the argument vector, because the
// vector being right is the unit tests' job and this is the question they cannot
// answer: does a real hardware encoder, given those arguments, produce avc1 in an
// avc1 sample entry at the rung's dimensions. If any of that moved, turning the
// knob on would need every tree re-encoded to turn it back off.
//
// VideoToolbox is the backend this runs on because it is the one a developer's
// machine has. The other three are proven at the argument-vector level and by the
// boot probe; nothing here is macOS-specific except which backend is available.
func TestHardwareCMAFLadderPreservesCodecIdentity(t *testing.T) {
	requireHardware(t, HardwareVideoToolbox)
	f := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: hd720WithAudio, hw: HardwareVideoToolbox})

	if len(f.res.Renditions) != 3 {
		t.Fatalf("renditions = %+v, want the 720p/480p/360p ladder", f.res.Renditions)
	}
	// The plan really did go to the GPU — otherwise this test proves that software
	// encoding works, which is not news.
	if got := hardwareBackendOf(f.codecs); got != HardwareVideoToolbox {
		t.Fatalf("the fixture encoded with backend %q, want videotoolbox", got)
	}
	if enc := f.codecs[0].Encoder; enc != "h264_videotoolbox" {
		t.Fatalf("H.264 encoder = %q, want h264_videotoolbox", enc)
	}

	// Three video representations plus the ONE shared audio one, and nothing else:
	// a backend must not change how many representations a ladder has.
	for rep := 0; rep < 4; rep++ {
		if !f.stored(t, "cmaf/"+cmafInitSegmentName(rep)) {
			t.Errorf("representation %d has no init segment", rep)
		}
	}
	if f.stored(t, "cmaf/"+cmafInitSegmentName(4)) {
		t.Error("a fifth representation exists")
	}

	// Every rung decodes as H.264 in an avc1 sample entry at its own dimensions —
	// the concat-and-ffprobe technique, so a broken init segment or a mis-mapped
	// representation cannot hide behind a manifest that reads correctly.
	for i, r := range f.res.Renditions {
		assertCMAFRepresentation(t, f, i, "h264", "avc1", r)
	}

	// The manifests: identical in shape to the software ones. CODECS is computed
	// by ffmpeg from what it actually wrote, so `avc1.<profile><level>` here is the
	// muxer agreeing that the GPU produced ordinary H.264.
	master := f.read(t, "master.m3u8")
	if n := strings.Count(master, "#EXT-X-STREAM-INF:"); n != 3 {
		t.Errorf("master has %d variants, want 3:\n%s", n, master)
	}
	codecsRE := regexp.MustCompile(`#EXT-X-STREAM-INF:[^\n]*CODECS="(avc1\.[0-9a-f]{6},mp4a\.40\.2)"`)
	if got := codecsRE.FindAllString(master, -1); len(got) != 3 {
		t.Errorf("only %d of 3 variants declare avc1 — a hardware rung must be indistinguishable from a software one:\n%s", len(got), master)
	}
	for _, unwanted := range []string{"hvc1", "hev1", "av01", `CODECS=",`} {
		if strings.Contains(master, unwanted) {
			t.Errorf("the master advertises %q:\n%s", unwanted, master)
		}
	}
	for _, want := range []string{"RESOLUTION=1280x720", "RESOLUTION=854x480", "RESOLUTION=640x360",
		"#EXT-X-MEDIA:TYPE=AUDIO,", "#EXT-X-I-FRAME-STREAM-INF:"} {
		if !strings.Contains(master, want) {
			t.Errorf("master missing %q:\n%s", want, master)
		}
	}
	// Every media playlist is a complete VOD playlist, which is what makes the
	// tree servable at all.
	for rep := 0; rep < 4; rep++ {
		playlist := f.read(t, "cmaf/"+cmafMediaPlaylistName(rep))
		for _, want := range []string{"#EXT-X-PLAYLIST-TYPE:VOD", "#EXT-X-ENDLIST"} {
			if !strings.Contains(playlist, want) {
				t.Errorf("representation %d missing %q:\n%s", rep, want, playlist)
			}
		}
	}

	mpd := f.read(t, "cmaf/"+cmafManifestFilename)
	if n := strings.Count(mpd, `contentType="video"`); n != 1 {
		t.Errorf("MPD has %d video adaptation sets, want 1:\n%s", n, mpd)
	}
	if n := strings.Count(mpd, `codecs="avc1.`); n != 3 {
		t.Errorf("MPD declares %d avc1 representations, want 3:\n%s", n, mpd)
	}

	// The derived assets — the compatibility floor — all still come off the
	// hardware-encoded H.264 representations, and are still H.264.
	for _, r := range f.res.Renditions {
		assertStoredStreamTypes(t, f.blobs, HLSDownloadKey(r.KeyPrefix, true), "video", "audio")
		p, err := f.blobs.Path(HLSDownloadKey(r.KeyPrefix, false))
		if err != nil {
			t.Fatalf("Path: %v", err)
		}
		if got := ffprobeEntries(t, p, "stream=codec_name"); got["codec_name"] != "h264" {
			t.Errorf("the %dp download is %q, want h264", r.Height, got["codec_name"])
		}
	}
	assertStoredStreamTypes(t, f.blobs, HLSAudioDownloadKey(f.res.MasterKey), "audio")
	// Trick-play stays libx264 — its own pass, at one frame per second, where a
	// hardware encoder buys nothing — so it must still be there, once per rung.
	if n := strings.Count(master, "#EXT-X-I-FRAME-STREAM-INF:"); n != 3 {
		t.Errorf("%d trick-play entries, want one per rung:\n%s", n, master)
	}
}

// TestHardwareCMAFGeometryMatchesSoftware runs the SAME sources through a
// hardware ladder and a software one and requires every rung's decoded display
// aspect ratio to agree.
//
// Geometry is where a hardware backend is most likely to go wrong quietly. The
// scale filter sets each rung's sample aspect ratio to whatever preserves the
// source's display shape through that rung's rounding; an encoder that dropped
// or overrode that signalling produces a tree that plays perfectly and is
// stretched. The software CMAF ladder is the reference because the existing
// geometry tests already prove IT equal to MPEG-TS, so agreeing with it closes
// the chain.
func TestHardwareCMAFGeometryMatchesSoftware(t *testing.T) {
	requireHardware(t, HardwareVideoToolbox)
	for _, tc := range []struct {
		name    string
		genArgs []string
	}{
		{"square pixels", hd720WithAudio},
		// Anamorphic SD: coded dimensions that are not display dimensions, which is
		// the shape that exposes a dropped SAR.
		{"anamorphic", []string{
			"-f", "lavfi", "-i", "testsrc2=duration=4:size=720x576:rate=25",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p",
			"-vf", "setsar=64/45", "-an",
		}},
		// A single-rung source: the branch with no split at all, where the hardware
		// filter tail is appended directly to the scale.
		{"single rung", []string{
			"-f", "lavfi", "-i", "testsrc2=duration=4:size=320x240:rate=25",
			"-c:v", "libx264", "-preset", "ultrafast", "-pix_fmt", "yuv420p", "-an",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			soft := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: tc.genArgs})
			hard := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: tc.genArgs, hw: HardwareVideoToolbox})

			if len(soft.res.Renditions) != len(hard.res.Renditions) {
				t.Fatalf("ladders differ: software %d rungs, hardware %d",
					len(soft.res.Renditions), len(hard.res.Renditions))
			}
			for i, r := range soft.res.Renditions {
				if h := hard.res.Renditions[i]; h.Width != r.Width || h.Height != r.Height {
					t.Errorf("rung %d is %dx%d on hardware, %dx%d on software",
						i, h.Width, h.Height, r.Width, r.Height)
				}
				want := probedDAR(t, soft.concatRepresentation(t, i))
				if got := probedDAR(t, hard.concatRepresentation(t, i)); got != want {
					t.Errorf("rung %dp: hardware segments display at %s, software at %s — the ladder renders this source at the wrong shape on the GPU",
						r.Height, got, want)
				}
			}
			// And the MPD publishes ONE aspect ratio for the whole video adaptation
			// set, the same one the software tree publishes.
			pars := func(f cmafFixture) []string {
				m := regexp.MustCompile(`par="([^"]*)"`).FindAllStringSubmatch(f.read(t, "cmaf/"+cmafManifestFilename), -1)
				out := make([]string, 0, len(m))
				for _, g := range m {
					out = append(out, g[1])
				}
				return out
			}
			if got, want := pars(hard), pars(soft); strings.Join(got, ",") != strings.Join(want, ",") {
				t.Errorf("MPD par: hardware %v, software %v", got, want)
			}
		})
	}
}

// TestHardwareHEVCLadderIsStillTaggedHVC1 proves the tag survives the transform.
// It is the detail that makes HEVC usable at all — ffmpeg writes hev1 into fMP4
// unless told otherwise, Safari refuses it, and the master's codec string then
// comes out EMPTY — and the transform touches the encoder, which is exactly the
// kind of change that could have dropped a muxer-level option on the way past.
func TestHardwareHEVCLadderIsStillTaggedHVC1(t *testing.T) {
	requireHardware(t, HardwareVideoToolbox)
	requireEncoders(t, "hevc_videotoolbox")
	f := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: hd720WithAudio, hevc: true, hw: HardwareVideoToolbox})

	if enc := f.codecs[1].Encoder; enc != "hevc_videotoolbox" {
		t.Fatalf("HEVC encoder = %q, want hevc_videotoolbox", enc)
	}
	for i, r := range f.res.Renditions {
		assertCMAFRepresentation(t, f, f.rep(0, i), "h264", "avc1", r)
		assertCMAFRepresentation(t, f, f.rep(1, i), "hevc", "hvc1", r)
	}
	master := f.read(t, "master.m3u8")
	hvc1 := regexp.MustCompile(`#EXT-X-STREAM-INF:[^\n]*CODECS="hvc1\.[^",]+,mp4a\.40\.2"`)
	if got := hvc1.FindAllString(master, -1); len(got) != 3 {
		t.Errorf("only %d of 3 HEVC variants carry an hvc1 CODECS attribute:\n%s", len(got), master)
	}
	if strings.Contains(master, "hev1") {
		t.Errorf("the master advertises hev1, which Safari refuses:\n%s", master)
	}
	if strings.Contains(master, `CODECS=",`) {
		t.Errorf("a variant has an empty video codec — the symptom of a lost hvc1 tag:\n%s", master)
	}
}

// TestHardwareLadderWithASoftwareCodecBesideIt is the mixed case: H.264 and HEVC
// on the GPU, AV1 on the CPU, in ONE ffmpeg process off one decode. It is the
// filter graph's hardest shape — the per-codec fork, with a `null` link for the
// codec that stayed in system memory — and the thing it proves is that all three
// representations come out of it decodable.
func TestHardwareLadderWithASoftwareCodecBesideIt(t *testing.T) {
	requireHardware(t, HardwareVideoToolbox)
	requireEncoders(t, "hevc_videotoolbox", "libsvtav1")
	f := newFixture(t, fixtureOpts{packager: PackagerCMAF, genArgs: hd720WithAudio, hevc: true, av1: true, hw: HardwareVideoToolbox})

	// AV1 stayed on libsvtav1 whatever the backend says.
	if enc := f.codecs[2].Encoder; enc != "libsvtav1" {
		t.Fatalf("AV1 encoder = %q, want libsvtav1 — hardware AV1 encode is never selected by this knob", enc)
	}
	for i, r := range f.res.Renditions {
		assertCMAFRepresentation(t, f, f.rep(0, i), "h264", "avc1", r)
		assertCMAFRepresentation(t, f, f.rep(1, i), "hevc", "hvc1", r)
		assertCMAFRepresentation(t, f, f.rep(2, i), "av1", "av01", r)
	}
	mpd := f.read(t, "cmaf/"+cmafManifestFilename)
	if n := strings.Count(mpd, `contentType="video"`); n != 3 {
		t.Errorf("MPD has %d video adaptation sets, want one per codec:\n%s", n, mpd)
	}
	if n := strings.Count(mpd, `contentType="audio"`); n != 1 {
		t.Errorf("MPD has %d audio adaptation sets, want 1 — a codec must not duplicate the audio:\n%s", n, mpd)
	}
}

// TestHardwareAudioOnlySourceStillTranscodes covers the plan with no video at
// all. An audio-only ladder has no filter graph, so it also has no place to put a
// hardware upload — and a backend that initialised a device it then never used
// would fail a podcast upload on a host whose GPU is busy.
func TestHardwareAudioOnlySourceStillTranscodes(t *testing.T) {
	requireHardware(t, HardwareVideoToolbox)
	f := newFixture(t, fixtureOpts{packager: PackagerCMAF, hw: HardwareVideoToolbox, genArgs: []string{
		"-f", "lavfi", "-i", "sine=frequency=440:duration=5", "-c:a", "aac",
	}})
	if len(f.res.Renditions) != 0 {
		t.Errorf("renditions = %+v, want none for an audio-only source", f.res.Renditions)
	}
	if !f.stored(t, "cmaf/"+cmafInitSegmentName(0)) {
		t.Error("the audio representation has no init segment")
	}
	assertStoredStreamTypes(t, f.blobs, HLSAudioDownloadKey(f.res.MasterKey), "audio")
}
