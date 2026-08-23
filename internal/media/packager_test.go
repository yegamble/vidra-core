package media

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/blobsink"
	"github.com/vidra/vidra-core/internal/storage"
)

// TestPackagerSeamSplitsEncodeFromPackaging pins WHERE the seam falls. The two
// halves used to be one function, and the temptation when adding a codec knob is
// to drop it wherever the container settings already are (or vice versa). Each
// half is asserted to own its subject AND to say nothing about the other's.
func TestPackagerSeamSplitsEncodeFromPackaging(t *testing.T) {
	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}
	out := localOutput("/out")
	encode := strings.Join(hlsRungEncodeArgs(r, ""), " ")
	pack := strings.Join(defaultPackager.VariantMuxerArgs(out, rungDest(out, r), r), " ")

	// The encoder half owns the codecs and rate control...
	for _, want := range []string{
		"-c:v libx264", "-profile:v main", "-preset veryfast", "-pix_fmt yuv420p",
		"-b:v 2800k", "-maxrate 2800k", "-bufsize 5600k",
		"-c:a aac", "-b:a 128k", "-ac 2",
		// Deliberately on this side: only the encoder can place a key frame,
		// even though every packaging format needs them at segment boundaries.
		"-force_key_frames expr:gte(t,n_forced*6)",
	} {
		if !strings.Contains(encode, want) {
			t.Errorf("encode args missing %q\nargs: %s", want, encode)
		}
	}
	// ...and nothing about containers or output files.
	for _, unwanted := range []string{"-f ", "-hls_", ".m3u8", ".ts", "-method", "-http_persistent"} {
		if strings.Contains(encode, unwanted) {
			t.Errorf("encode args leaked packaging concern %q\nargs: %s", unwanted, encode)
		}
	}

	// The packager half owns the container, its cutting rules, and the names it
	// writes...
	for _, want := range []string{
		"-f hls", "-hls_time 6", "-hls_playlist_type vod", "-hls_list_size 0",
		"-hls_flags independent_segments",
		"-hls_segment_filename " + filepath.Join("/out", "720p", "seg_%05d.ts"),
		filepath.Join("/out", "720p", "playlist.m3u8"),
	} {
		if !strings.Contains(pack, want) {
			t.Errorf("packager args missing %q\nargs: %s", want, pack)
		}
	}
	// ...and nothing about how the pixels were encoded.
	for _, unwanted := range []string{"-c:v", "-c:a", "-b:v", "-b:a", "-maxrate", "-bufsize", "-preset", "-pix_fmt", "-profile:v", "-force_key_frames", "-vf"} {
		if strings.Contains(pack, unwanted) {
			t.Errorf("packager args leaked encode concern %q\nargs: %s", unwanted, pack)
		}
	}
}

// TestTrickPlaySeamSplitsEncodeFromPackaging is the same boundary for the dense
// I-frame pass, whose GOP settings are encoder-side and whose byte-range
// single-file layout is packager-side.
func TestTrickPlaySeamSplitsEncodeFromPackaging(t *testing.T) {
	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}
	out := localOutput("/out")
	encode := strings.Join(trickPlayEncodeArgs(""), " ")
	pack := strings.Join(defaultPackager.TrickPlayMuxerArgs(out, rungDest(out, r), r), " ")

	for _, want := range []string{"-c:v libx264", "-g 1", "-keyint_min 1", "-sc_threshold 0", "-crf 28"} {
		if !strings.Contains(encode, want) {
			t.Errorf("trick-play encode args missing %q\nargs: %s", want, encode)
		}
	}
	for _, unwanted := range []string{"-f ", "-hls_", ".m3u8", ".ts"} {
		if strings.Contains(encode, unwanted) {
			t.Errorf("trick-play encode args leaked packaging concern %q\nargs: %s", unwanted, encode)
		}
	}
	for _, want := range []string{
		"-f hls", "-hls_time 1", "-hls_playlist_type vod", "-hls_list_size 0",
		// single_file is what makes the byte ranges markIFramesOnlyPlaylist
		// rewrites during finalisation exist at all.
		"-hls_flags single_file",
		"-hls_segment_filename " + filepath.Join("/out", "720p", HLSIFrameMediaFilename),
		filepath.Join("/out", "720p", HLSIFramePlaylistFilename),
	} {
		if !strings.Contains(pack, want) {
			t.Errorf("trick-play packager args missing %q\nargs: %s", want, pack)
		}
	}
	for _, unwanted := range []string{"-c:v", "-g ", "-keyint_min", "-crf", "-preset", "-pix_fmt"} {
		if strings.Contains(pack, unwanted) {
			t.Errorf("trick-play packager args leaked encode concern %q\nargs: %s", unwanted, pack)
		}
	}
}

// TestLadderRungIsEncodeArgsThenPackagerArgs proves the composition itself: each
// rung's block on the multi-output vector is EXACTLY its stream maps, then the
// encoder's arguments, then the packager's — nothing hand-assembled in between.
// This is what makes a second packaging format a second Packager rather than a
// second ladder builder.
func TestLadderRungIsEncodeArgsThenPackagerArgs(t *testing.T) {
	rungs := testLadder()
	root := "/out"
	out := localOutput(root)
	outputs := splitArgsByOutput(t, hlsLadderArgs(localSource("/in/src.mp4"), out, rungs, 0), root)
	if len(outputs) != len(rungs) {
		t.Fatalf("got %d output blocks, want %d", len(outputs), len(rungs))
	}
	for i, r := range rungs {
		want := append(
			append([]string{"-map", "[v" + strconv.Itoa(i) + "]", "-map", "0:a:0?"},
				hlsRungEncodeArgs(r, "")...),
			defaultPackager.VariantMuxerArgs(out, rungDest(out, r), r)...)
		// The first block carries the shared prologue (-y, the single -i, the
		// filter graph); every later block is nothing but its own output.
		if got := outputTail(t, outputs[i], want); !reflect.DeepEqual(got, want) {
			t.Errorf("rung %s block is not maps+encode+package:\n got %#v\nwant %#v", r.Name(), got, want)
		}
		if i > 0 && len(outputs[i]) != len(want) {
			t.Errorf("rung %s block carries %d unexpected leading args: %#v",
				r.Name(), len(outputs[i])-len(want), outputs[i][:len(outputs[i])-len(want)])
		}
	}
}

// outputTail is the last len(want) arguments of block, for comparing an output's
// own arguments against a composed expectation without the shared prologue.
func outputTail(t *testing.T, block, want []string) []string {
	t.Helper()
	if len(block) < len(want) {
		t.Fatalf("output block is shorter than the composed expectation:\n got %#v\nwant %#v", block, want)
	}
	return block[len(block)-len(want):]
}

// TestTrickPlayLadderRungIsEncodeArgsThenPackagerArgs is the same composition
// proof for the trick-play pass.
func TestTrickPlayLadderRungIsEncodeArgsThenPackagerArgs(t *testing.T) {
	rungs := testLadder()
	root := "/out"
	out := localOutput(root)
	outputs := splitArgsByOutput(t, hlsTrickPlayLadderArgs(localSource("/in/src.mp4"), out, rungs, 0), root)
	if len(outputs) != len(rungs) {
		t.Fatalf("got %d output blocks, want %d", len(outputs), len(rungs))
	}
	for i, r := range rungs {
		want := append(
			append([]string{"-map", "[t" + strconv.Itoa(i) + "]", "-an"},
				trickPlayEncodeArgs("")...),
			defaultPackager.TrickPlayMuxerArgs(out, rungDest(out, r), r)...)
		if got := outputTail(t, outputs[i], want); !reflect.DeepEqual(got, want) {
			t.Errorf("trick-play rung %s block is not maps+encode+package:\n got %#v\nwant %#v", r.Name(), got, want)
		}
		if i > 0 && len(outputs[i]) != len(want) {
			t.Errorf("trick-play rung %s block carries %d unexpected leading args: %#v",
				r.Name(), len(outputs[i])-len(want), outputs[i][:len(outputs[i])-len(want)])
		}
	}
}

// TestOraclesAreRecomposedFromTheSamePieces guards the ONE property that gives
// hlsRungArgs and hlsTrickPlayArgs their value. They are dead in production and
// exist only as the reference TestHLSLadderRungSettingsMatchSingleRungForm
// asserts the ladder against; that anti-drift guard is worth nothing if the
// oracle is a hand-maintained second copy of the settings. Both must stay
// literally built from the same encode-args and packager calls the ladder uses.
func TestOraclesAreRecomposedFromTheSamePieces(t *testing.T) {
	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128, FPS: 30}
	const dir = "/out/720p"

	variantTail := append(
		append([]string{}, hlsRungEncodeArgs(r, "scale=1280:720,fps=30")...),
		defaultPackager.VariantMuxerArgs(localOutput(""), dirRef(dir), r)...)
	got := hlsRungArgs(localSource("/in/src.mp4"), dir, r, 0)
	if !reflect.DeepEqual(got[len(got)-len(variantTail):], variantTail) {
		t.Errorf("hlsRungArgs is no longer recomposed from the shared pieces:\n got %#v\nwant tail %#v", got, variantTail)
	}

	trickTail := append(
		append([]string{}, trickPlayEncodeArgs("scale=1280:720,fps=1")...),
		defaultPackager.TrickPlayMuxerArgs(localOutput(""), dirRef(dir), r)...)
	got = hlsTrickPlayArgs(localSource("/in/src.mp4"), dir, r, 0)
	if !reflect.DeepEqual(got[len(got)-len(trickTail):], trickTail) {
		t.Errorf("hlsTrickPlayArgs is no longer recomposed from the shared pieces:\n got %#v\nwant tail %#v", got, trickTail)
	}
}

// TestPackagerArgsComposeWithBothTransports pins that packaging layers OVER the
// output transport seam rather than replacing it: the same packager writes plain
// scratch paths for a local output and signed sidecar URLs (with the HTTP-only
// muxer options and flag adaptation) for a streaming one.
func TestPackagerArgsComposeWithBothTransports(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	sink, err := blobsink.New(blobs, "streaming-playlists/vid1")
	if err != nil {
		t.Fatalf("blobsink.New: %v", err)
	}
	defer func() { _ = sink.Close() }()

	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}

	local := localOutput("/out")
	localArgs := strings.Join(defaultPackager.VariantMuxerArgs(local, rungDest(local, r), r), " ")
	if !strings.Contains(localArgs, "-hls_flags independent_segments ") {
		t.Errorf("a scratch destination must keep the plain flags: %s", localArgs)
	}
	for _, unwanted := range []string{"-method", "-http_persistent", "-temp_file", "http://"} {
		if strings.Contains(localArgs, unwanted) {
			t.Errorf("a scratch destination emitted the HTTP-only %q: %s", unwanted, localArgs)
		}
	}

	streamed := sinkOutput(sink)
	streamArgs := strings.Join(defaultPackager.VariantMuxerArgs(streamed, rungDest(streamed, r), r), " ")
	for _, want := range []string{
		// Over HTTP the muxer's write-to-.tmp-then-rename dance would PUT a
		// temp-named object the sink stores verbatim.
		"-hls_flags independent_segments-temp_file",
		"-method PUT",
		"-http_persistent 0",
		sink.URL("720p/seg_%05d.ts"),
		sink.URL("720p/playlist.m3u8"),
	} {
		if !strings.Contains(streamArgs, want) {
			t.Errorf("streaming destination missing %q: %s", want, streamArgs)
		}
	}

	trickStreamArgs := strings.Join(defaultPackager.TrickPlayMuxerArgs(streamed, rungDest(streamed, r), r), " ")
	for _, want := range []string{"-hls_flags single_file-temp_file", "-method PUT", "-http_persistent 0"} {
		if !strings.Contains(trickStreamArgs, want) {
			t.Errorf("streaming trick-play missing %q: %s", want, trickStreamArgs)
		}
	}
}

// TestFinalizeReportsPackagingFailureThroughTheCallback pins the failure
// contract the packaging stage owes its caller: the job's per-resolution
// progress projection must be marked failed BEFORE the error is returned, and
// the operator-facing error string must keep naming the rung, the step and the
// source. It uses a missing ffmpeg binary so no encode is needed.
func TestFinalizeReportsPackagingFailureThroughTheCallback(t *testing.T) {
	scratch := t.TempDir()
	r := HLSRung{Height: 240, Width: 320, VideoKbps: 500, AudioKbps: 64}
	if err := os.MkdirAll(filepath.Join(scratch, r.Name()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}

	var packagingFailed, storeFailed int
	_, err = defaultPackager.Finalize(context.Background(), packageRequest{
		sourceKey: "web-videos/abc.mp4",
		out:       localOutput(scratch),
		scratch:   scratch,
		prefix:    "streaming-playlists/abc",
		rungs:     []HLSRung{r},
		tools: packageTools{
			blobs:   blobs,
			ffmpeg:  filepath.Join(scratch, "no-such-ffmpeg"),
			ffprobe: filepath.Join(scratch, "no-such-ffprobe"),
		},
		onPackagingFailed: func() { packagingFailed++ },
		onStoreFailed:     func([]HLSRung) { storeFailed++ },
	})
	if err == nil {
		t.Fatal("Finalize succeeded with no ffmpeg on hand")
	}
	if want := `media: ffmpeg hls 240p muxed download for "web-videos/abc.mp4"`; !strings.Contains(err.Error(), want) {
		t.Errorf("error = %q, want it to contain %q", err, want)
	}
	if packagingFailed != 1 {
		t.Errorf("onPackagingFailed called %d times, want 1", packagingFailed)
	}
	if storeFailed != 0 {
		t.Errorf("onStoreFailed called %d times for a pre-store failure, want 0", storeFailed)
	}
}

// TestFinalizeToleratesAbsentProgressCallbacks keeps the callbacks optional: a
// caller with no progress projection must not crash the packaging stage.
func TestFinalizeToleratesAbsentProgressCallbacks(t *testing.T) {
	scratch := t.TempDir()
	r := HLSRung{Height: 240, Width: 320, VideoKbps: 500, AudioKbps: 64}
	if err := os.MkdirAll(filepath.Join(scratch, r.Name()), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if _, err := defaultPackager.Finalize(context.Background(), packageRequest{
		sourceKey: "web-videos/abc.mp4",
		out:       localOutput(scratch),
		scratch:   scratch,
		prefix:    "streaming-playlists/abc",
		rungs:     []HLSRung{r},
		tools: packageTools{
			blobs:   blobs,
			ffmpeg:  filepath.Join(scratch, "no-such-ffmpeg"),
			ffprobe: filepath.Join(scratch, "no-such-ffprobe"),
		},
	}); err == nil {
		t.Fatal("Finalize succeeded with no ffmpeg on hand")
	}
}

// TestHLSTranscoderUsesTheTSPackagerByDefault pins the wiring: an unconfigured
// transcoder packages MPEG-TS, which is every deployment today.
func TestHLSTranscoderUsesTheTSPackagerByDefault(t *testing.T) {
	blobs, err := storage.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	tc := NewHLSTranscoder(blobs)
	if got := tc.packager().Name(); got != "ts" {
		t.Errorf("default packager = %q, want %q", got, "ts")
	}
	tools := tc.packageTools()
	if tools.ffmpeg != "ffmpeg" || tools.ffprobe != "ffprobe" || tools.blobs == nil {
		t.Errorf("package tools = %+v, want the transcoder's own binaries and backend", tools)
	}
}
