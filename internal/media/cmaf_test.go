package media

import (
	"strconv"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/blobsink"
	"github.com/vidra/vidra-core/internal/storage"
)

// ffmpegCMAFMaster is a verbatim capture of what ffmpeg 8.1's dash muxer writes
// for a 2-rung ladder with audio under the arguments cmafMuxerArgs builds. The
// CODECS strings and the rung→representation mapping are read out of it, so it
// is worth having the real bytes rather than an idealised fixture.
const ffmpegCMAFMaster = `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="group_A1",NAME="audio_2",DEFAULT=YES,CHANNELS="2",URI="media_2.m3u8"
#EXT-X-STREAM-INF:BANDWIDTH=931488,RESOLUTION=480x360,CODECS="avc1.4d4015,mp4a.40.2",AUDIO="group_A1"
media_0.m3u8

#EXT-X-STREAM-INF:BANDWIDTH=631488,RESOLUTION=320x240,CODECS="avc1.4d400d,mp4a.40.2",AUDIO="group_A1"
media_1.m3u8

`

// ffmpegCMAFMasterSilent is the same capture for a silent single-rung source:
// no audio rendition at all, and the CODECS string carries video alone.
const ffmpegCMAFMasterSilent = `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-STREAM-INF:BANDWIDTH=501857,RESOLUTION=320x240,CODECS="avc1.4d400d"
media_0.m3u8

`

// cmafPlan is the default ladder plan these tests exercise: a source WITH audio,
// which is the shape that has a shared audio representation to get wrong.
func cmafPlan(rungs []HLSRung) ladderPlan {
	return ladderPlan{rungs: rungs, hasAudio: true}
}

func cmafTestRungs() []HLSRung {
	return []HLSRung{
		{Height: 360, Width: 480, VideoKbps: 800, AudioKbps: 96},
		{Height: 240, Width: 320, VideoKbps: 500, AudioKbps: 64},
	}
}

// TestCMAFLadderIsOneOutputWithStreamQualifiedEncoders pins the property that
// forced the packager seam to change shape. Every rung shares ONE dash output,
// so every rung's encoder options must name their own stream: an unqualified
// -b:v on this vector would apply the last rung's bitrate to all of them and the
// ladder would silently collapse to a single quality that still plays.
func TestCMAFLadderIsOneOutputWithStreamQualifiedEncoders(t *testing.T) {
	rungs := cmafTestRungs()
	args := hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), cmafPlan(rungs))
	joined := strings.Join(args, " ")

	if n := countArg(args, "-f"); n != 1 {
		t.Errorf("-f appears %d times, want exactly 1 — the whole ladder is one dash output", n)
	}
	if got := argValue(t, args, "-f"); got != "dash" {
		t.Errorf("-f %s, want dash", got)
	}
	if n := countArg(args, "-i"); n != 1 {
		t.Errorf("-i appears %d times, want 1 (one decode)", n)
	}
	for i, r := range rungs {
		for _, want := range []string{
			"-c:v:" + strconv.Itoa(i) + " libx264",
			"-profile:v:" + strconv.Itoa(i) + " main",
			"-preset:v:" + strconv.Itoa(i) + " veryfast",
			"-pix_fmt:v:" + strconv.Itoa(i) + " yuv420p",
			"-b:v:" + strconv.Itoa(i) + " " + strconv.Itoa(r.VideoKbps) + "k",
			"-maxrate:v:" + strconv.Itoa(i) + " " + strconv.Itoa(r.VideoKbps) + "k",
			"-bufsize:v:" + strconv.Itoa(i) + " " + strconv.Itoa(2*r.VideoKbps) + "k",
			"-force_key_frames:v:" + strconv.Itoa(i) + " expr:gte(t,n_forced*6)",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("rung %s missing %q\nargs: %s", r.Name(), want, joined)
			}
		}
	}
	// Nothing may be left unqualified: that is exactly the bug this shape exists
	// to prevent.
	for _, leaked := range []string{
		" -c:v ", " -profile:v ", " -preset ", " -pix_fmt ",
		" -b:v ", " -maxrate ", " -bufsize ", " -force_key_frames ",
	} {
		if strings.Contains(" "+joined+" ", leaked) {
			t.Errorf("unqualified%soption would be applied to EVERY rung on this shared output:\n%s", leaked, joined)
		}
	}
}

// TestCMAFEncodesAudioOnceForTheWholeLadder pins the demuxed CMAF shape: video
// representations carry no audio and there is exactly one audio representation,
// at the top rung's bitrate. MPEG-TS muxes audio into every rung; repeating that
// here would store the same audio N times and defeat the point of sharing
// segments.
func TestCMAFEncodesAudioOnceForTheWholeLadder(t *testing.T) {
	rungs := cmafTestRungs()
	args := hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), cmafPlan(rungs))
	joined := strings.Join(args, " ")

	if n := strings.Count(joined, "-c:a:"); n != 1 {
		t.Errorf("%d audio encoders on the vector, want exactly 1:\n%s", n, joined)
	}
	if want := "-b:a:0 " + strconv.Itoa(rungs[0].AudioKbps) + "k"; !strings.Contains(joined, want) {
		t.Errorf("audio missing %q (the TOP rung's bitrate):\n%s", want, joined)
	}
	// The lower rung's audio bitrate must not appear at all — there is no
	// per-rung audio to choose.
	if bad := strconv.Itoa(rungs[1].AudioKbps) + "k"; strings.Contains(joined, "-b:a:0 "+bad) {
		t.Errorf("audio encoded at the LOW rung's bitrate %s:\n%s", bad, joined)
	}
	if n := countArg(args, "-map"); n != len(rungs)+1 {
		t.Errorf("%d maps, want one per rung plus one audio (%d)", n, len(rungs)+1)
	}
	// Optional, so a silent source still transcodes.
	if !strings.Contains(joined, "-map 0:a:0?") {
		t.Errorf("audio map is not optional; a silent source would fail the ladder:\n%s", joined)
	}
	// One adaptation set for the video representations, one for audio.
	if got := argValue(t, args, "-adaptation_sets"); got != "id=0,streams=v id=1,streams=a" {
		t.Errorf("-adaptation_sets %q", got)
	}
}

// TestCMAFMuxerAsksForOneSharedSegmentSet pins the flags that make HLS and DASH
// name the SAME files, and the one that makes a broken tree fail loudly.
func TestCMAFMuxerAsksForOneSharedSegmentSet(t *testing.T) {
	args := hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), cmafPlan(cmafTestRungs()))
	joined := strings.Join(args, " ")
	for _, want := range []string{
		// The entire reason this muxer was chosen: HLS playlists over the same
		// segments, from the same pass.
		"-hls_playlist 1",
		"-dash_segment_type mp4",
		"-use_template 1",
		"-use_timeline 1",
		"-format_options movflags=+cmaf",
		"-seg_duration 6",
		"-init_seg_name init-$RepresentationID$.mp4",
		"-media_seg_name chunk-$RepresentationID$-$Number%05d$.m4s",
		// ffmpeg's own master is written somewhere it cannot collide with the
		// one Vidra authors at master.m3u8.
		"-hls_master_name ffmpeg-master.m3u8",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("muxer missing %q:\n%s", want, joined)
		}
	}
	// With io errors ignored the exit code stops meaning anything and a
	// half-written tree would be stored as a success.
	if got := argValue(t, args, "-ignore_io_errors"); got != "0" {
		t.Errorf("-ignore_io_errors %s, want 0 — the exit code must stay trustworthy", got)
	}
	if !strings.HasSuffix(joined, "/out/cmaf/stream.mpd") {
		t.Errorf("vector must end at the manifest:\n%s", joined)
	}
}

// TestCMAFFilterGraphIsIdenticalToTheMPEGTSOne is the guard on the mistake this
// path already made once. CMAF's manifests publish an aspect ratio per
// representation where MPEG-TS playlists publish none, which makes "normalise
// the aspect here" look like a packaging concern; it is not, and acting on it
// corrupts every source whose CODED dimensions are not its DISPLAY dimensions
// (rotated phone video, anamorphic SD) because rung dimensions are planned from
// the coded ones. scale already emits the sample aspect ratio that preserves the
// source's display ratio through each rung's rounding, so the only correct graph
// is the one MPEG-TS uses — byte for byte.
func TestCMAFFilterGraphIsIdenticalToTheMPEGTSOne(t *testing.T) {
	src := localSource("/in/src.mp4")
	for _, rungs := range [][]HLSRung{
		cmafTestRungs(),
		PlanHLSLadder(1280, 720),                       // the 854x480 rung: ratios differ by rounding
		PlanHLSLadder(1920, 1080),                      // four rungs
		PlanHLSLadder(320, 240),                        // single rung, 4:3
		PlanHLSLadderWith(withFPS(30), 1920, 1080, 60), // an fps cap in the chain too
	} {
		cmaf := argValue(t, hlsLadderArgsWith(cmafPackager{}, src, localOutput("/out"), cmafPlan(rungs)), "-filter_complex")
		ts := argValue(t, hlsLadderArgs(src, localOutput("/out"), rungs, 0), "-filter_complex")
		if cmaf != ts {
			t.Errorf("CMAF graph diverges from MPEG-TS:\n cmaf %q\n   ts %q", cmaf, ts)
		}
		for _, banned := range []string{"setsar", "setdar", "aspect"} {
			if strings.Contains(cmaf, banned) {
				t.Errorf("graph carries %q; it overwrites the sample aspect ratio scale computed to "+
					"preserve the SOURCE display ratio, which silently re-renders rotated and "+
					"anamorphic sources at the wrong shape: %q", banned, cmaf)
			}
		}
		trick := argValue(t, hlsTrickPlayLadderArgsWith(cmafPackager{}, src, localOutput("/out"), cmafPlan(rungs)), "-filter_complex")
		tsTrick := argValue(t, hlsTrickPlayLadderArgs(src, localOutput("/out"), rungs, 0), "-filter_complex")
		if trick != tsTrick {
			t.Errorf("CMAF trick-play graph diverges from MPEG-TS:\n cmaf %q\n   ts %q", trick, tsTrick)
		}
	}
}

func withFPS(max int) HLSEncodeSettings {
	s := DefaultHLSEncodeSettings()
	s.MaxFPS = max
	return s
}

// TestCMAFDeclaresAnAudioAdaptationSetOnlyWhenThereIsAudio: the dash muxer does
// not drop an adaptation set that ends up with no streams, it writes an empty
// one — and a Representation-less AdaptationSet is invalid DASH.
func TestCMAFDeclaresAnAudioAdaptationSetOnlyWhenThereIsAudio(t *testing.T) {
	rungs := cmafTestRungs()
	withAudio := hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), ladderPlan{rungs: rungs, hasAudio: true})
	if got := argValue(t, withAudio, "-adaptation_sets"); got != "id=0,streams=v id=1,streams=a" {
		t.Errorf("-adaptation_sets %q with audio", got)
	}
	if n := strings.Count(strings.Join(withAudio, " "), "-c:a:"); n != 1 {
		t.Errorf("%d audio encoders with audio, want 1", n)
	}

	silent := hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), ladderPlan{rungs: rungs})
	if got := argValue(t, silent, "-adaptation_sets"); got != "id=0,streams=v" {
		t.Errorf("-adaptation_sets %q for a silent source, want video only", got)
	}
	joined := strings.Join(silent, " ")
	if strings.Contains(joined, "-c:a:") || strings.Contains(joined, "-b:a:") {
		t.Errorf("a silent source still configures an audio encoder: %s", joined)
	}
	// The map stays optional either way: the probe says a stream exists, the
	// encode must not die if it turns out unusable.
	if !strings.Contains(joined, "-map 0:a:0?") {
		t.Errorf("silent vector dropped the optional audio map: %s", joined)
	}
}

// TestCMAFTrickPlayStaysPerRungInTheSharedDirectory pins the trick-play port:
// still one -f hls output per rung (the dash muxer has no I-frame playlists at
// all), now fMP4 byte ranges, all in the shared directory under names that carry
// the representation index.
func TestCMAFTrickPlayStaysPerRungInTheSharedDirectory(t *testing.T) {
	rungs := cmafTestRungs()
	args := hlsTrickPlayLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), cmafPlan(rungs))
	joined := strings.Join(args, " ")
	if n := countArg(args, "-f"); n != len(rungs) {
		t.Errorf("-f appears %d times, want one per rung (%d)", n, len(rungs))
	}
	for i := range rungs {
		for _, want := range []string{
			"-hls_segment_type fmp4",
			"-hls_flags single_file",
			"/out/cmaf/iframe-" + strconv.Itoa(i) + ".mp4",
			"/out/cmaf/iframe-" + strconv.Itoa(i) + ".m3u8",
		} {
			if !strings.Contains(joined, want) {
				t.Errorf("trick-play rung %d missing %q:\n%s", i, want, joined)
			}
		}
	}
	if strings.Contains(joined, "-hls_fmp4_init_filename") {
		t.Error("single_file mode ignores hls_fmp4_init_filename; setting it invites a phantom init object")
	}
}

// TestCMAFArgsComposeWithBothTransports mirrors the MPEG-TS transport test: the
// same packager writes scratch paths for a local output and signed sidecar URLs
// (with the HTTP-only muxer options) for a streaming one. -method PUT has to
// survive on the dash output, or direct-to-object-store CMAF is impossible.
func TestCMAFArgsComposeWithBothTransports(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	sink, err := blobsink.New(blobs, "streaming-playlists/vid1")
	if err != nil {
		t.Fatalf("blobsink.New: %v", err)
	}
	defer func() { _ = sink.Close() }()

	rungs := cmafTestRungs()
	local := strings.Join(hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), localOutput("/out"), cmafPlan(rungs)), " ")
	for _, unwanted := range []string{"-method", "-http_persistent", "http://"} {
		if strings.Contains(local, unwanted) {
			t.Errorf("a scratch destination emitted the HTTP-only %q: %s", unwanted, local)
		}
	}

	streamed := sinkOutput(sink)
	args := strings.Join(hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), streamed, cmafPlan(rungs)), " ")
	for _, want := range []string{"-method PUT", "-http_persistent 0", sink.URL("cmaf/stream.mpd")} {
		if !strings.Contains(args, want) {
			t.Errorf("streaming destination missing %q: %s", want, args)
		}
	}
	trick := strings.Join(hlsTrickPlayLadderArgsWith(cmafPackager{}, localSource("/in/src.mp4"), streamed, cmafPlan(rungs)), " ")
	for _, want := range []string{"-hls_flags single_file-temp_file", "-method PUT", "-http_persistent 0"} {
		if !strings.Contains(trick, want) {
			t.Errorf("streaming trick-play missing %q: %s", want, trick)
		}
	}
}

// TestParseCMAFMasterPlaylist covers the read of ffmpeg's own manifest — the
// step that supplies every CODECS string and, more importantly, VERIFIES the
// rung→representation mapping. A master that pointed each variant at the wrong
// media playlist would still play; it would just play the wrong resolutions, so
// a mismatch has to be an error and not a shrug.
func TestParseCMAFMasterPlaylist(t *testing.T) {
	t.Run("two rungs plus shared audio", func(t *testing.T) {
		got, err := parseCMAFMasterPlaylist([]byte(ffmpegCMAFMaster), 2)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		want := []string{"avc1.4d4015,mp4a.40.2", "avc1.4d400d,mp4a.40.2"}
		for i, w := range want {
			if got.videoCodecs[i] != w {
				t.Errorf("videoCodecs[%d] = %q, want %q", i, got.videoCodecs[i], w)
			}
		}
		if !got.hasAudio || got.audioRep != 2 {
			t.Errorf("audio = (has %v, rep %d), want (true, 2)", got.hasAudio, got.audioRep)
		}
		if got.audioChannels != "2" {
			t.Errorf("audioChannels = %q, want 2", got.audioChannels)
		}
		if reps := got.representations(); len(reps) != 3 {
			t.Errorf("representations = %v, want 3", reps)
		}
	})

	t.Run("a silent source has no audio representation", func(t *testing.T) {
		got, err := parseCMAFMasterPlaylist([]byte(ffmpegCMAFMasterSilent), 1)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if got.hasAudio {
			t.Error("silent source reported an audio representation")
		}
		if got.videoCodecs[0] != "avc1.4d400d" {
			t.Errorf("videoCodecs[0] = %q", got.videoCodecs[0])
		}
		if reps := got.representations(); len(reps) != 1 {
			t.Errorf("representations = %v, want 1", reps)
		}
	})

	t.Run("a layout that is not the one the ladder asked for is an error", func(t *testing.T) {
		cases := map[string]struct {
			playlist string
			rungs    int
		}{
			"a rung has no representation": {ffmpegCMAFMaster, 3},
			"a representation is outside the ladder": {
				"#EXTM3U\n#EXT-X-STREAM-INF:CODECS=\"avc1\"\nmedia_5.m3u8\n", 2},
			"a representation appears twice": {
				"#EXTM3U\n#EXT-X-STREAM-INF:CODECS=\"avc1\"\nmedia_0.m3u8\n" +
					"#EXT-X-STREAM-INF:CODECS=\"avc1\"\nmedia_0.m3u8\n", 2},
			"a variant has no CODECS": {
				"#EXTM3U\n#EXT-X-STREAM-INF:BANDWIDTH=1\nmedia_0.m3u8\n", 1},
			"audio landed on a video representation index": {
				"#EXTM3U\n#EXT-X-MEDIA:TYPE=AUDIO,URI=\"media_0.m3u8\"\n" +
					"#EXT-X-STREAM-INF:CODECS=\"avc1\"\nmedia_0.m3u8\n", 1},
			"a variant URI is not a media playlist": {
				"#EXTM3U\n#EXT-X-STREAM-INF:CODECS=\"avc1\"\nsomething-else.m3u8\n", 1},
		}
		for name, tc := range cases {
			if _, err := parseCMAFMasterPlaylist([]byte(tc.playlist), tc.rungs); err == nil {
				t.Errorf("%s: parse succeeded", name)
			}
		}
	})
}

// TestM3U8AttrHandlesQuotedCommas guards the parser against the attribute that
// actually matters: a multi-codec CODECS value contains a comma INSIDE its
// quotes, and a naive split on commas would truncate it to the video codec and
// silently drop the audio one from every variant of the master.
func TestM3U8AttrHandlesQuotedCommas(t *testing.T) {
	line := `#EXT-X-STREAM-INF:BANDWIDTH=931488,RESOLUTION=480x360,CODECS="avc1.4d4015,mp4a.40.2",AUDIO="group_A1"`
	cases := map[string]string{
		"BANDWIDTH":  "931488",
		"RESOLUTION": "480x360",
		"CODECS":     "avc1.4d4015,mp4a.40.2",
		"AUDIO":      "group_A1",
	}
	for attr, want := range cases {
		got, ok := m3u8Attr(line, attr)
		if !ok || got != want {
			t.Errorf("m3u8Attr(%s) = (%q, %v), want %q", attr, got, ok, want)
		}
	}
	if _, ok := m3u8Attr(line, "MISSING"); ok {
		t.Error("m3u8Attr reported a missing attribute as present")
	}
}

// TestFixCMAFMediaPlaylist covers the two repairs a VOD media playlist needs.
// The date-times are the interesting one: ffmpeg stamps every segment with the
// wall-clock instant it was packaged, which a player reads as a live-edge date
// for a recording that may be years old.
func TestFixCMAFMediaPlaylist(t *testing.T) {
	raw := `#EXTM3U
#EXT-X-VERSION:6
#EXT-X-TARGETDURATION:6
#EXT-X-MEDIA-SEQUENCE:1
#EXT-X-MAP:URI="init-0.mp4"
#EXTINF:6.000000,
#EXT-X-PROGRAM-DATE-TIME:2026-08-22T06:35:07.000-0400
chunk-0-00001.m4s
#EXTINF:2.000000,
#EXT-X-PROGRAM-DATE-TIME:2026-08-22T06:35:13.000-0400
chunk-0-00002.m4s
#EXT-X-ENDLIST
`
	fixed, err := fixCMAFMediaPlaylist([]byte(raw))
	if err != nil {
		t.Fatalf("fix: %v", err)
	}
	got := string(fixed)
	if strings.Contains(got, "PROGRAM-DATE-TIME") {
		t.Errorf("date-times survived:\n%s", got)
	}
	if !strings.Contains(got, "#EXT-X-PLAYLIST-TYPE:VOD\n#EXT-X-MAP:") {
		t.Errorf("VOD must be declared with the playlist-wide tags, before the first segment:\n%s", got)
	}
	// Everything else is untouched, in order.
	for _, want := range []string{"#EXTM3U\n", "#EXT-X-MAP:URI=\"init-0.mp4\"", "chunk-0-00001.m4s", "chunk-0-00002.m4s", "#EXT-X-ENDLIST"} {
		if !strings.Contains(got, want) {
			t.Errorf("fixed playlist lost %q:\n%s", want, got)
		}
	}
	if n := strings.Count(got, "#EXT-X-PLAYLIST-TYPE:VOD"); n != 1 {
		t.Errorf("VOD declared %d times, want 1", n)
	}

	// Idempotent: a playlist that already declares VOD gains nothing.
	twice, err := fixCMAFMediaPlaylist(fixed)
	if err != nil {
		t.Fatalf("fix twice: %v", err)
	}
	if string(twice) != got {
		t.Errorf("fixCMAFMediaPlaylist is not idempotent:\n%s", string(twice))
	}

	for name, bad := range map[string]string{
		"not an m3u8":  "<html>",
		"no EXT-X-MAP": "#EXTM3U\n#EXTINF:6,\nchunk-0-00001.m4s\n",
	} {
		if _, err := fixCMAFMediaPlaylist([]byte(bad)); err == nil {
			t.Errorf("%s: fix succeeded", name)
		}
	}
}

// TestRenderCMAFMasterPlaylist pins the manifest Vidra authors in place of
// ffmpeg's. Version 7 and a CODECS attribute on every variant are not polish:
// Safari will not start an fMP4 variant it has no codec string for.
func TestRenderCMAFMasterPlaylist(t *testing.T) {
	rungs := cmafTestRungs()
	layout, err := parseCMAFMasterPlaylist([]byte(ffmpegCMAFMaster), len(rungs))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := renderCMAFMasterPlaylist(rungs, rungs[0].AudioKbps, layout, map[int]hlsTrickPlayInfo{
		360: {Bandwidth: 120000, Codec: "avc1.4d4015"},
	})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	for _, want := range []string{
		"#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n",
		`#EXT-X-MEDIA:TYPE=AUDIO,GROUP-ID="audio",NAME="audio",DEFAULT=YES,AUTOSELECT=YES,CHANNELS="2",URI="cmaf/media_2.m3u8"`,
		`#EXT-X-STREAM-INF:BANDWIDTH=985600,RESOLUTION=480x360,CODECS="avc1.4d4015,mp4a.40.2",AUDIO="audio"` + "\ncmaf/media_0.m3u8\n",
		// 655600, not 620400: the low rung's declared peak includes the SHARED
		// audio bitrate (the top rung's 96k), because that is the audio it plays.
		`#EXT-X-STREAM-INF:BANDWIDTH=655600,RESOLUTION=320x240,CODECS="avc1.4d400d,mp4a.40.2",AUDIO="audio"` + "\ncmaf/media_1.m3u8\n",
		`#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=120000,RESOLUTION=480x360,CODECS="avc1.4d4015",URI="cmaf/iframe-0.m3u8"`,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("master missing %q:\n%s", want, got)
		}
	}
	// Every URI is relative and inside the shared directory, so the playlist
	// works from any base path and the file route resolves it as one rendition.
	for _, line := range strings.Split(got, "\n") {
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if !strings.HasPrefix(line, "cmaf/") {
			t.Errorf("variant URI %q must live under the shared directory", line)
		}
	}

	// Every variant's BANDWIDTH must count the audio it actually plays. Audio is
	// encoded ONCE at the top rung's bitrate, so a variant declaring its own
	// (lower) AudioKbps under-declares its peak — which is what an ABR player
	// uses to decide whether the connection can carry it.
	t.Run("bandwidth counts the shared audio, not the rung's own", func(t *testing.T) {
		for i, r := range rungs {
			want := (r.VideoKbps + rungs[0].AudioKbps) * 1000 * 11 / 10
			if !strings.Contains(got, "BANDWIDTH="+strconv.Itoa(want)+",RESOLUTION="+strconv.Itoa(r.Width)) {
				t.Errorf("variant %d does not declare the shared-audio peak %d:\n%s", i, want, got)
			}
			if i > 0 && r.AudioKbps != rungs[0].AudioKbps && strings.Contains(got, "BANDWIDTH="+strconv.Itoa(r.Bandwidth())+",") {
				t.Errorf("variant %d declares its own AudioKbps, which is not the audio it plays:\n%s", i, got)
			}
		}
	})

	// An incomplete codec list is chosen by a player and then fails, which is the
	// specific Safari behaviour the CODECS attribute exists to avoid.
	t.Run("a variant that references audio must name an audio codec", func(t *testing.T) {
		broken := layout
		broken.videoCodecs = []string{"avc1.4d4015", "avc1.4d400d"}
		if _, err := renderCMAFMasterPlaylist(rungs, rungs[0].AudioKbps, broken, nil); err == nil {
			t.Error("rendered a master whose variants reference the audio group but declare no audio codec")
		}
	})

	t.Run("a silent source advertises no audio group", func(t *testing.T) {
		single := rungs[1:]
		silent, err := parseCMAFMasterPlaylist([]byte(ffmpegCMAFMasterSilent), 1)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		got, err := renderCMAFMasterPlaylist(single, single[0].AudioKbps, silent, nil)
		if err != nil {
			t.Fatalf("render: %v", err)
		}
		if strings.Contains(got, "EXT-X-MEDIA") || strings.Contains(got, "AUDIO=") {
			t.Errorf("silent master advertises an audio rendition that does not exist:\n%s", got)
		}
		if !strings.Contains(got, `CODECS="avc1.4d400d"`) {
			t.Errorf("silent master lost its CODECS:\n%s", got)
		}
		// No audio to carry, so none is declared in the peak either.
		if want := single[0].VideoKbps * 1000 * 11 / 10; !strings.Contains(got, "BANDWIDTH="+strconv.Itoa(want)+",") {
			t.Errorf("silent variant declares bandwidth for audio it does not have, want %d:\n%s", want, got)
		}
	})
}

// TestCMAFProgressiveDownloadsRemuxFromTwoRepresentations pins the consequence
// of demuxed CMAF for the download routes: a video representation has no audio,
// so the muxed asset the download endpoint serves must be assembled from the
// rung's representation AND the shared audio one — while staying a stream copy.
func TestCMAFProgressiveDownloadsRemuxFromTwoRepresentations(t *testing.T) {
	const video = "/out/cmaf/media_0.m3u8"
	const audio = "/out/cmaf/media_2.m3u8"

	muxed := strings.Join(cmafProgressiveMP4Args(video, audio, "/out/240p/video.mp4", true), " ")
	for _, want := range []string{
		"-i " + video, "-i " + audio,
		"-map 0:v:0", "-map 1:a:0",
		"-c copy", "-movflags +faststart", "/out/240p/video.mp4",
	} {
		if !strings.Contains(muxed, want) {
			t.Errorf("muxed download missing %q: %s", want, muxed)
		}
	}

	videoOnly := strings.Join(cmafProgressiveMP4Args(video, audio, "/out/240p/video-only.mp4", false), " ")
	if strings.Contains(videoOnly, audio) || !strings.Contains(videoOnly, "-an") {
		t.Errorf("video-only download must not touch the audio representation: %s", videoOnly)
	}

	// A silent source has no audio representation; the muxed asset then contains
	// video alone, which is what the MPEG-TS optional audio map produces too.
	silent := strings.Join(cmafProgressiveMP4Args(video, "", "/out/240p/video.mp4", true), " ")
	if strings.Contains(silent, "-map 1:a:0") || !strings.Contains(silent, "-an") {
		t.Errorf("a silent source must still produce a valid muxed MP4: %s", silent)
	}
	if !strings.Contains(silent, "-c:v copy") {
		t.Errorf("the download remux must stay a stream copy: %s", silent)
	}
}

// TestPackagerSelection pins the operator-facing names and that an unknown one
// is refused rather than silently falling back — a typo that quietly re-packaged
// a whole deployment would be found by a viewer, not by boot.
func TestPackagerSelection(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	tc := NewHLSTranscoder(blobs)
	for _, name := range []string{PackagerTS, PackagerCMAF} {
		if err := tc.SetPackager(name); err != nil {
			t.Fatalf("SetPackager(%q): %v", name, err)
		}
		if got := tc.packager().Name(); got != name {
			t.Errorf("packager = %q, want %q", got, name)
		}
		if !IsPackagerName(name) {
			t.Errorf("IsPackagerName(%q) = false", name)
		}
	}
	for _, bad := range []string{"", "CMAF", "dash", "hls-ts", "fmp4"} {
		if err := tc.SetPackager(bad); err == nil {
			t.Errorf("SetPackager(%q) was accepted", bad)
		}
		if IsPackagerName(bad) {
			t.Errorf("IsPackagerName(%q) = true", bad)
		}
	}
	// A rejected name must not have changed the packager.
	if got := tc.packager().Name(); got != PackagerCMAF && got != PackagerTS {
		t.Errorf("packager = %q after a rejected name", got)
	}
}

// --- phase-3 item 6.2: audio-only sources ------------------------------------

// ffmpegCMAFMasterAudioOnly is a verbatim capture of what ffmpeg 8.1's dash
// muxer writes for a source with only an audio stream, under the very arguments
// cmafMuxerArgs builds for one. Note the SHAPE: with no video to group
// renditions against, there is no EXT-X-MEDIA at all and the audio is published
// as an ordinary variant. That is correct HLS — and it is why the audio arrives
// through the branch that means "video rung" in the ladder reader, which is what
// makes a separate reader necessary rather than tidy.
const ffmpegCMAFMasterAudioOnly = `#EXTM3U
#EXT-X-VERSION:7
#EXT-X-STREAM-INF:BANDWIDTH=161750,CODECS="mp4a.40.2"
media_0.m3u8

`

// TestCMAFAudioOnlyLadderArgs pins the encode vector for a source with no video:
// no filter graph, no video maps, no video encoders, a REQUIRED audio map, and
// exactly one adaptation set — the audio one.
func TestCMAFAudioOnlyLadderArgs(t *testing.T) {
	plan := ladderPlan{hasAudio: true}
	if !plan.audioOnly() {
		t.Fatal("a plan with no rungs must be audio-only")
	}
	args := hlsLadderArgsWith(cmafPackager{}, localSource("/in/src.m4a"), localOutput("/out"), plan)
	joined := strings.Join(args, " ")

	// An empty -filter_complex is a syntax error and a split into zero branches is
	// meaningless; there is simply no graph.
	if countArg(args, "-filter_complex") != 0 {
		t.Errorf("audio-only vector carries a filter graph:\n%s", joined)
	}
	// The audio map is REQUIRED here. On a video ladder a missing audio stream is
	// a silent video; here it is an empty output the muxer would happily write.
	if !strings.Contains(joined, "-map 0:a:0 ") {
		t.Errorf("audio-only vector does not map audio unconditionally:\n%s", joined)
	}
	if strings.Contains(joined, "-map 0:a:0?") {
		t.Errorf("audio-only vector maps audio OPTIONALLY, so an unusable stream would package as an empty tree:\n%s", joined)
	}
	if strings.Contains(joined, "libx264") || strings.Contains(joined, "-b:v") {
		t.Errorf("audio-only vector carries a video encoder:\n%s", joined)
	}
	// One adaptation set, and it is audio: an empty video set is as invalid as an
	// empty audio set.
	if got := argValue(t, args, "-adaptation_sets"); got != "id=0,streams=a" {
		t.Errorf("-adaptation_sets = %q, want id=0,streams=a", got)
	}
	// The audio budget comes from the standalone audio-only value, because there
	// is no rung to read one off.
	if want := "-b:a:0 " + strconv.Itoa(hlsAudioOnlyKbps) + "k"; !strings.Contains(joined, want) {
		t.Errorf("audio-only vector missing %q:\n%s", want, joined)
	}
	// It is still the same muxer writing the same tree.
	if !strings.HasSuffix(joined, "/out/cmaf/stream.mpd") {
		t.Errorf("audio-only vector does not write the shared CMAF directory:\n%s", joined)
	}
}

// TestCMAFAudioOnlyScratchDirsHaveNoRungDirectories: nothing per-rung is written
// for a source with no rungs, so no per-rung directory may be created either.
func TestCMAFAudioOnlyScratchDirsHaveNoRungDirectories(t *testing.T) {
	dirs := cmafPackager{}.ScratchDirs(nil)
	if len(dirs) != 1 || dirs[0] != cmafDirName {
		t.Errorf("audio-only scratch dirs = %v, want just %q", dirs, cmafDirName)
	}
}

// TestCMAFAudioOnlyTrickPlayIsNotAttempted: there is nothing to scrub through.
func TestCMAFAudioOnlyTrickPlayIsNotAttempted(t *testing.T) {
	args := cmafPackager{}.TrickPlayOutputArgs(localOutput("/out"), ladderPlan{hasAudio: true})
	if len(args) != 0 {
		t.Errorf("audio-only trick-play output args = %v, want none", args)
	}
}

// TestParseCMAFAudioOnlyMasterPlaylist reads ffmpeg's real audio-only master and
// pins what may go wrong with it.
func TestParseCMAFAudioOnlyMasterPlaylist(t *testing.T) {
	layout, err := parseCMAFMasterPlaylist([]byte(ffmpegCMAFMasterAudioOnly), 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if !layout.audioOnly() || !layout.hasAudio {
		t.Errorf("layout = %+v, want an audio-only layout with audio", layout)
	}
	if layout.audioRep != 0 {
		t.Errorf("audioRep = %d, want 0 (it is the only stream)", layout.audioRep)
	}
	if layout.audioCodecs != "mp4a.40.2" {
		t.Errorf("audioCodecs = %q, want the muxer's own string", layout.audioCodecs)
	}

	for _, tc := range []struct {
		name, playlist string
	}{
		{"no variant at all", "#EXTM3U\n#EXT-X-VERSION:7\n"},
		{"a variant that is not a media playlist", "#EXTM3U\n#EXT-X-STREAM-INF:CODECS=\"mp4a.40.2\"\nsomething.m3u8\n"},
		{"a variant that is not representation 0", "#EXTM3U\n#EXT-X-STREAM-INF:CODECS=\"mp4a.40.2\"\nmedia_3.m3u8\n"},
		{"more than one variant", "#EXTM3U\n#EXT-X-STREAM-INF:CODECS=\"mp4a.40.2\"\nmedia_0.m3u8\n" +
			"#EXT-X-STREAM-INF:CODECS=\"mp4a.40.2\"\nmedia_1.m3u8\n"},
		{"a variant that names no audio codec", "#EXTM3U\n#EXT-X-STREAM-INF:CODECS=\"avc1.4d400d\"\nmedia_0.m3u8\n"},
	} {
		if _, err := parseCMAFMasterPlaylist([]byte(tc.playlist), 0); err == nil {
			t.Errorf("%s: parsed without error", tc.name)
		}
	}
}

// TestRenderCMAFAudioOnlyMasterPlaylist pins RFC 8216's audio-only presentation:
// ONE variant naming the audio media playlist directly, an audio codec in
// CODECS, and no RESOLUTION. Deliberately not an EXT-X-MEDIA rendition group — a
// group describes alternates OF a video presentation, and a master whose only
// audio is in a group no variant references is one no client will start.
func TestRenderCMAFAudioOnlyMasterPlaylist(t *testing.T) {
	layout, err := parseCMAFMasterPlaylist([]byte(ffmpegCMAFMasterAudioOnly), 0)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	got, err := renderCMAFMasterPlaylist(nil, hlsAudioOnlyKbps, layout, nil)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	want := "#EXTM3U\n#EXT-X-VERSION:7\n#EXT-X-INDEPENDENT-SEGMENTS\n" +
		"#EXT-X-STREAM-INF:BANDWIDTH=" + strconv.Itoa(hlsAudioOnlyKbps*1000*11/10) + ",CODECS=\"mp4a.40.2\"\n" +
		"cmaf/media_0.m3u8\n"
	if got != want {
		t.Errorf("audio-only master:\n got %q\nwant %q", got, want)
	}
	// A layout with no audio codec cannot produce a startable master, so it must
	// fail rather than emit one.
	if _, err := renderCMAFMasterPlaylist(nil, hlsAudioOnlyKbps, cmafLayout{hasAudio: true}, nil); err == nil {
		t.Error("rendered an audio-only master with no audio codec")
	}
}

// TestLadderPlanAudioBudget pins where the single shared audio representation's
// bitrate comes from in each shape.
func TestLadderPlanAudioBudget(t *testing.T) {
	rungs := cmafTestRungs()
	if got := cmafPlan(rungs).audioBitrateKbps(); got != rungs[0].AudioKbps {
		t.Errorf("video plan audio budget = %d, want the top rung's %d", got, rungs[0].AudioKbps)
	}
	if got := (ladderPlan{hasAudio: true}).audioBitrateKbps(); got != hlsAudioOnlyKbps {
		t.Errorf("audio-only plan audio budget = %d, want %d", got, hlsAudioOnlyKbps)
	}
}

// TestPackagerAudioOnlyCapability pins which formats can express an audio-only
// tree, and that the capability is stated rather than inferred at a call site.
func TestPackagerAudioOnlyCapability(t *testing.T) {
	var cmaf, ts Packager = cmafPackager{}, tsPackager{}
	if !cmaf.SupportsAudioOnly() {
		t.Error("CMAF must support audio-only: its audio is already its own representation")
	}
	if ts.SupportsAudioOnly() {
		t.Error("MPEG-TS must not claim audio-only support: its variants are rendition directories named for a height")
	}
}
