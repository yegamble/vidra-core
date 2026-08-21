package media

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
)

func testLadder() []HLSRung {
	return PlanHLSLadderWith(DefaultHLSEncodeSettings(), 1920, 1080, 30)
}

// splitArgsByOutput chops a multi-output ffmpeg vector into one slice per
// output. An output ends at the first argument that is not a flag and is not a
// flag's value — in these vectors that is the trailing filename, which always
// lives under root.
func splitArgsByOutput(t *testing.T, args []string, root string) [][]string {
	t.Helper()
	var out [][]string
	cur := []string{}
	for _, a := range args {
		cur = append(cur, a)
		if strings.HasPrefix(a, root) && !strings.HasSuffix(a, ".ts") {
			out = append(out, cur)
			cur = []string{}
		}
	}
	if len(cur) != 0 {
		t.Fatalf("trailing args with no output file: %v", cur)
	}
	return out
}

// TestSplitChain pins the filter-graph prologue: n branches, correctly labelled,
// and no pointless split for a single rung.
func TestSplitChain(t *testing.T) {
	t.Run("single rung reads the input directly", func(t *testing.T) {
		chain, labels := splitChain(1)
		if chain != "" {
			t.Errorf("chain = %q, want empty for one branch", chain)
		}
		if len(labels) != 1 || labels[0] != "0:v" {
			t.Errorf("labels = %v, want [0:v]", labels)
		}
	})
	t.Run("multiple rungs fork one decode", func(t *testing.T) {
		chain, labels := splitChain(4)
		if chain != "[0:v]split=4[b0][b1][b2][b3]" {
			t.Errorf("chain = %q", chain)
		}
		if len(labels) != 4 {
			t.Fatalf("labels = %v, want 4", labels)
		}
		for i, l := range labels {
			if l != fmt.Sprintf("b%d", i) {
				t.Errorf("labels[%d] = %q", i, l)
			}
		}
	})
}

// TestPerOutputThreads guards the CPU-budget contract. transcoding_threads is
// documented as a PER-JOB value; before decode-once the rungs ran one after
// another so the whole budget belonged to the single running encoder. Now they
// run concurrently in one process, so handing each the full number would
// silently multiply a deliberately-restrained setting by the ladder height.
func TestPerOutputThreads(t *testing.T) {
	cases := []struct {
		threads, outputs, want int
		why                    string
	}{
		{0, 4, 0, "ffmpeg's own default must stay the default"},
		{8, 4, 2, "the budget is divided across concurrent encoders"},
		{4, 4, 1, "an exact division leaves one each"},
		{2, 4, 1, "a budget smaller than the ladder floors at one, never zero"},
		{1, 8, 1, "never zero"},
		{6, 1, 6, "a single output keeps the whole budget"},
		{6, 0, 6, "no outputs cannot divide"},
	}
	for _, tc := range cases {
		if got := perOutputThreads(tc.threads, tc.outputs); got != tc.want {
			t.Errorf("perOutputThreads(%d, %d) = %d, want %d — %s",
				tc.threads, tc.outputs, got, tc.want, tc.why)
		}
	}
}

// TestHLSLadderArgsDecodesOnce is the core contract: one input, one filter
// graph, and one output per rung. The old shape was one whole ffmpeg invocation
// (and therefore one whole decode of the source) per rung.
func TestHLSLadderArgsDecodesOnce(t *testing.T) {
	rungs := testLadder()
	if len(rungs) < 2 {
		t.Fatalf("need a multi-rung ladder to test, got %d", len(rungs))
	}
	root := filepath.Join("/out", "tree")
	args := hlsLadderArgs(localSource("/in/src.mp4"), localOutput(root), rungs, 0)

	if n := countArg(args, "-i"); n != 1 {
		t.Errorf("-i appears %d times, want exactly 1 (the whole point is one decode)", n)
	}
	if n := countArg(args, "-filter_complex"); n != 1 {
		t.Errorf("-filter_complex appears %d times, want 1", n)
	}
	graph := argValue(t, args, "-filter_complex")
	if !strings.HasPrefix(graph, fmt.Sprintf("[0:v]split=%d", len(rungs))) {
		t.Errorf("graph does not fork the decode into one branch per rung: %q", graph)
	}
	outputs := splitArgsByOutput(t, args, root)
	if len(outputs) != len(rungs) {
		t.Fatalf("got %d outputs, want one per rung (%d)", len(outputs), len(rungs))
	}
	for i, r := range rungs {
		got := strings.Join(outputs[i], " ")
		if !strings.Contains(got, "-map [v"+fmt.Sprint(i)+"]") {
			t.Errorf("rung %s does not consume its own filter branch: %q", r.Name(), got)
		}
		// Audio is mapped from the input, optionally, so a silent source still
		// encodes — the filter graph only carries video.
		if !strings.Contains(got, "-map 0:a:0?") {
			t.Errorf("rung %s drops the optional audio map", r.Name())
		}
		wantPlaylist := filepath.Join(root, r.Name(), "playlist.m3u8")
		if !strings.HasSuffix(got, wantPlaylist) {
			t.Errorf("rung %s output = %q, want it to end at %q", r.Name(), got, wantPlaylist)
		}
	}
}

// TestHLSLadderRungSettingsMatchSingleRungForm is the anti-drift guard: the
// ladder must encode each rung exactly as the standalone command did. The
// single-rung form has golden assertions of its own, so proving equivalence here
// means the ladder inherits them.
func TestHLSLadderRungSettingsMatchSingleRungForm(t *testing.T) {
	rungs := testLadder()
	root := "/out"
	outputs := splitArgsByOutput(t, hlsLadderArgs(localSource("/in/src.mp4"), localOutput(root), rungs, 0), root)

	for i, r := range rungs {
		single := strings.Join(hlsRungArgs(localSource("/in/src.mp4"), filepath.Join(root, r.Name()), r, 0), " ")
		ladder := strings.Join(outputs[i], " ")
		// Everything from the encoder settings onward must be identical; the
		// difference is confined to how the frames arrive (‑vf vs a graph branch).
		for _, want := range []string{
			fmt.Sprintf("-b:v %dk", r.VideoKbps),
			fmt.Sprintf("-maxrate %dk", r.VideoKbps),
			fmt.Sprintf("-bufsize %dk", 2*r.VideoKbps),
			fmt.Sprintf("-b:a %dk", r.AudioKbps),
			"-c:v libx264", "-profile:v main", "-preset veryfast", "-pix_fmt yuv420p",
			"-force_key_frames expr:gte(t,n_forced*6)",
			"-f hls", "-hls_time 6", "-hls_playlist_type vod",
			"-hls_list_size 0", "-hls_flags independent_segments",
		} {
			if !strings.Contains(single, want) {
				t.Fatalf("test is stale: single-rung form no longer contains %q", want)
			}
			if !strings.Contains(ladder, want) {
				t.Errorf("rung %s: ladder dropped %q that the single-rung form has", r.Name(), want)
			}
		}
		// The scale must move into the graph, not stay as a -vf on the output.
		if strings.Contains(ladder, "-vf ") {
			t.Errorf("rung %s: ladder output still carries -vf; the graph already scaled it", r.Name())
		}
	}
}

// TestHLSLadderScalesEachBranchLikeTheSingleRungForm proves the graph applies
// the same scale (and fps cap) the standalone -vf did.
func TestHLSLadderScalesEachBranchLikeTheSingleRungForm(t *testing.T) {
	settings := DefaultHLSEncodeSettings()
	settings.MaxFPS = 30
	rungs := PlanHLSLadderWith(settings, 1920, 1080, 60)
	graph := argValue(t, hlsLadderArgs(localSource("/in/src.mp4"), localOutput("/out"), rungs, 0), "-filter_complex")
	for _, r := range rungs {
		if r.FPS == 0 {
			t.Fatal("test is stale: a 60fps source under a 30fps cap must plan capped rungs")
		}
		want := fmt.Sprintf("scale=%d:%d,fps=%d", r.Width, r.Height, r.FPS)
		if !strings.Contains(graph, want) {
			t.Errorf("graph missing %q for rung %s: %q", want, r.Name(), graph)
		}
	}
}

// TestHLSLadderDividesThreadBudget proves the per-job thread setting is shared
// across the now-concurrent encoders rather than handed to each in full.
func TestHLSLadderDividesThreadBudget(t *testing.T) {
	rungs := testLadder()
	args := hlsLadderArgs(localSource("/in/src.mp4"), localOutput("/out"), rungs, 8)
	want := perOutputThreads(8, len(rungs))
	if n := countArg(args, "-threads"); n != len(rungs) {
		t.Errorf("-threads appears %d times, want once per output (%d)", n, len(rungs))
	}
	for _, a := range argValues(args, "-threads") {
		if a != fmt.Sprint(want) {
			t.Errorf("-threads %s, want %d (the job budget split across %d encoders)", a, want, len(rungs))
		}
	}
	if got := countArg(hlsLadderArgs(localSource("/in/src.mp4"), localOutput("/out"), rungs, 0), "-threads"); got != 0 {
		t.Errorf("-threads emitted %d times for the ffmpeg-default setting, want 0", got)
	}
}

// TestTrickPlayLadderArgs mirrors the HLS ladder contract for the dense I-frame
// pass: one decode, one output per rung, no audio, byte-range single file.
func TestTrickPlayLadderArgs(t *testing.T) {
	rungs := testLadder()
	root := "/out"
	args := hlsTrickPlayLadderArgs(localSource("/in/src.mp4"), localOutput(root), rungs, 0)
	if n := countArg(args, "-i"); n != 1 {
		t.Errorf("-i appears %d times, want 1", n)
	}
	graph := argValue(t, args, "-filter_complex")
	for _, r := range rungs {
		want := fmt.Sprintf("scale=%d:%d,fps=1", r.Width, r.Height)
		if !strings.Contains(graph, want) {
			t.Errorf("graph missing trick-play chain %q: %q", want, graph)
		}
	}
	outputs := splitArgsByOutput(t, args, root)
	if len(outputs) != len(rungs) {
		t.Fatalf("got %d outputs, want %d", len(outputs), len(rungs))
	}
	for i, r := range rungs {
		got := strings.Join(outputs[i], " ")
		if !strings.Contains(got, "-an") {
			t.Errorf("trick-play rung %s carries audio", r.Name())
		}
		if !strings.Contains(got, "-hls_flags single_file") {
			t.Errorf("trick-play rung %s lost single_file; markIFramesOnlyPlaylist needs byte ranges", r.Name())
		}
		if !strings.Contains(got, "-g 1") || !strings.Contains(got, "-keyint_min 1") {
			t.Errorf("trick-play rung %s is not all-IDR: %q", r.Name(), got)
		}
	}
}

// TestWebVideoLadderArgs mirrors the contract for the progressive MP4 pass.
func TestWebVideoLadderArgs(t *testing.T) {
	rungs := testLadder()
	root := "/out"
	args := webVideoLadderArgs(localSource("/in/src.mp4"), root, rungs, 0)
	if n := countArg(args, "-i"); n != 1 {
		t.Errorf("-i appears %d times, want 1", n)
	}
	outputs := splitArgsByOutput(t, args, root)
	if len(outputs) != len(rungs) {
		t.Fatalf("got %d outputs, want %d", len(outputs), len(rungs))
	}
	for i, r := range rungs {
		got := strings.Join(outputs[i], " ")
		if !strings.Contains(got, "-movflags +faststart") {
			t.Errorf("web video rung %s lost +faststart", r.Name())
		}
		if !strings.Contains(got, "-map 0:a:0?") {
			t.Errorf("web video rung %s drops the optional audio map", r.Name())
		}
		if !strings.HasSuffix(got, filepath.Join(root, r.Name()+".mp4")) {
			t.Errorf("web video rung %s output = %q", r.Name(), got)
		}
	}
}

// TestSingleRungLadderOmitsSplit proves a one-rung ladder does not emit a
// pointless split filter.
func TestSingleRungLadderOmitsSplit(t *testing.T) {
	rungs := PlanHLSLadderWith(DefaultHLSEncodeSettings(), 640, 360, 30)
	if len(rungs) != 1 {
		t.Fatalf("test is stale: a 360p source should plan exactly one rung, got %d", len(rungs))
	}
	graph := argValue(t, hlsLadderArgs(localSource("/in/src.mp4"), localOutput("/out"), rungs, 0), "-filter_complex")
	if strings.Contains(graph, "split") {
		t.Errorf("single-rung graph contains a split: %q", graph)
	}
	if !strings.HasPrefix(graph, "[0:v]") {
		t.Errorf("single-rung graph should read [0:v] directly: %q", graph)
	}
}

func countArg(args []string, flag string) int {
	n := 0
	for _, a := range args {
		if a == flag {
			n++
		}
	}
	return n
}

func argValues(args []string, flag string) []string {
	var out []string
	for i, a := range args {
		if a == flag && i+1 < len(args) {
			out = append(out, args[i+1])
		}
	}
	return out
}

func argValue(t *testing.T, args []string, flag string) string {
	t.Helper()
	v := argValues(args, flag)
	if len(v) != 1 {
		t.Fatalf("%s appears %d times, want exactly 1", flag, len(v))
	}
	return v[0]
}
