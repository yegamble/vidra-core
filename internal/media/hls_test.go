package media

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func rungSizes(rungs []HLSRung) []string {
	var out []string
	for _, r := range rungs {
		out = append(out, fmt.Sprintf("%dx%d", r.Width, r.Height))
	}
	return out
}

func TestPlanHLSLadderFullLadder(t *testing.T) {
	got := rungSizes(PlanHLSLadder(1920, 1080))
	want := []string{"1920x1080", "1280x720", "854x480", "640x360"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("1920x1080 ladder = %v, want %v", got, want)
	}
}

func TestPlanHLSLadderCapsAtSourceHeight(t *testing.T) {
	// 720p source: the 1080 rung must be skipped (no upscaling).
	got := rungSizes(PlanHLSLadder(1280, 720))
	want := []string{"1280x720", "854x480", "640x360"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("1280x720 ladder = %v, want %v", got, want)
	}
}

func TestPlanHLSLadderTinySourceSingleRungNoUpscale(t *testing.T) {
	// 240p source is below every ladder rung: one rung at the source's own size.
	rungs := PlanHLSLadder(320, 240)
	if len(rungs) != 1 {
		t.Fatalf("320x240 planned %d rungs, want 1 (%v)", len(rungs), rungSizes(rungs))
	}
	if rungs[0].Width != 320 || rungs[0].Height != 240 {
		t.Errorf("rung = %dx%d, want 320x240", rungs[0].Width, rungs[0].Height)
	}
	if rungs[0].VideoKbps != 800 || rungs[0].AudioKbps != 96 {
		t.Errorf("tiny source should inherit the lowest rung's bitrates, got %+v", rungs[0])
	}
}

func TestPlanHLSLadderPortraitAndOddDimensions(t *testing.T) {
	// Portrait 1080x1920: every rung qualifies (cap is by height); widths follow
	// the 9:16 aspect and must come out even.
	rungs := PlanHLSLadder(1080, 1920)
	if len(rungs) != 4 {
		t.Fatalf("planned %d rungs, want 4 (%v)", len(rungs), rungSizes(rungs))
	}
	for _, r := range rungs {
		if r.Width%2 != 0 || r.Height%2 != 0 {
			t.Errorf("rung %dx%d has odd dimension (must be even for yuv420p)", r.Width, r.Height)
		}
	}
	// Odd source dimensions round to even in the single-rung fallback.
	odd := PlanHLSLadder(321, 241)
	if len(odd) != 1 || odd[0].Width%2 != 0 || odd[0].Height%2 != 0 {
		t.Errorf("321x241 = %v, want one even-dimensioned rung", rungSizes(odd))
	}
}

func TestPlanHLSLadderUnknownDimensions(t *testing.T) {
	if rungs := PlanHLSLadder(0, 0); rungs != nil {
		t.Errorf("unknown dimensions should plan nothing, got %v", rungSizes(rungs))
	}
}

func TestHLSRungArgs(t *testing.T) {
	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}
	args := strings.Join(hlsRungArgs("/in/src.mp4", "/out/720p", r), " ")
	for _, want := range []string{
		"-i /in/src.mp4",
		"-c:v libx264",
		"-vf scale=1280:720",
		"-b:v 2800k",
		"-maxrate 2800k",
		"-c:a aac",
		"-b:a 128k",
		"-f hls",
		"-hls_time 4",
		"-hls_playlist_type vod",
		"-hls_segment_filename /out/720p/seg_%05d.ts",
		"/out/720p/playlist.m3u8",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q\nargs: %s", want, args)
		}
	}
	// The audio map must be optional so silent sources still transcode.
	if !strings.Contains(args, "-map 0:a:0?") {
		t.Errorf("audio map must be optional (0:a:0?)\nargs: %s", args)
	}
}

func TestRenderMasterPlaylist(t *testing.T) {
	rungs := []HLSRung{
		{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128},
		{Height: 360, Width: 640, VideoKbps: 800, AudioKbps: 96},
	}
	m := renderMasterPlaylist(rungs)
	if !strings.HasPrefix(m, "#EXTM3U\n") {
		t.Fatalf("master playlist must start with #EXTM3U:\n%s", m)
	}
	for _, want := range []string{
		"#EXT-X-STREAM-INF:BANDWIDTH=3220800,RESOLUTION=1280x720\n720p/playlist.m3u8\n",
		"#EXT-X-STREAM-INF:BANDWIDTH=985600,RESOLUTION=640x360\n360p/playlist.m3u8\n",
	} {
		if !strings.Contains(m, want) {
			t.Errorf("master playlist missing %q\n%s", want, m)
		}
	}
	// Every URI must be relative so the playlist works behind the API proxy.
	for _, line := range strings.Split(m, "\n") {
		if line != "" && !strings.HasPrefix(line, "#") && strings.HasPrefix(line, "/") {
			t.Errorf("variant URI %q must be relative", line)
		}
	}
}

func TestHLSKeyPrefix(t *testing.T) {
	id := uuid.MustParse("6ba7b810-9dad-11d1-80b4-00c04fd430c8")
	if got, want := HLSKeyPrefix(id), "streaming-playlists/6ba7b810-9dad-11d1-80b4-00c04fd430c8"; got != want {
		t.Errorf("HLSKeyPrefix = %q, want %q", got, want)
	}
}
