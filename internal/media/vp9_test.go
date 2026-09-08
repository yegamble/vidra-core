package media

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestVP9WebMArgs(t *testing.T) {
	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}
	args := vp9WebMArgs(localSource("/in.mp4"), "/out.webm", r)
	joined := strings.Join(args, " ")

	if !strings.Contains(joined, "-c:v libvpx-vp9") {
		t.Errorf("expected VP9 video codec: %q", joined)
	}
	if !strings.Contains(joined, "-c:a libopus") {
		t.Errorf("expected Opus audio codec: %q", joined)
	}
	if !strings.Contains(joined, "scale=1280:720") {
		t.Errorf("expected scale to rung dims: %q", joined)
	}
	if !strings.Contains(joined, "-b:v 2800k") {
		t.Errorf("expected rung video bitrate: %q", joined)
	}
	if !strings.Contains(joined, "-f webm") {
		t.Errorf("expected webm container: %q", joined)
	}
	if args[len(args)-1] != "/out.webm" {
		t.Errorf("args should end with output path, got %q", args[len(args)-1])
	}
	// Audio map is optional so silent sources still encode.
	if !strings.Contains(joined, "0:a:0?") {
		t.Errorf("expected optional audio map: %q", joined)
	}
}

// TestVP9WebMKeyIsGenerationAddressed: the alternate sits inside the tree it was
// produced with, so it moves with every transcode generation (0136). Generation 0
// is the legacy in-place layout; anything later is under its own rN directory,
// which is what encodeVP9 actually writes.
func TestVP9WebMKeyIsGenerationAddressed(t *testing.T) {
	id := uuid.New()
	legacy := "streaming-playlists/" + id.String()
	if got, want := VP9WebMKey(legacy), legacy+"/vp9.webm"; got != want {
		t.Errorf("VP9WebMKey(generation 0) = %q, want %q", got, want)
	}
	gen := HLSPrefixForGeneration(id, 3)
	if got, want := VP9WebMKey(gen), legacy+"/r3/vp9.webm"; got != want {
		t.Errorf("VP9WebMKey(generation 3) = %q, want %q", got, want)
	}
	if VP9WebMFilename != "vp9.webm" {
		t.Errorf("VP9WebMFilename = %q, want vp9.webm (the ipfs mirror excludes by this name)", VP9WebMFilename)
	}
}

// TestVP9WebMArgsHonourTheFPSCap pins the one output that has to apply
// transcoding_max_fps itself. The VP9 alternate encodes from the SOURCE, not
// from the ladder's filter graph, so it inherits nothing: before this it emitted
// 60fps for a 60fps source under a 30fps cap — and did it at the standard-rate
// bitrate, because planning had already decided the output was 30fps and
// budgeted accordingly.
func TestVP9WebMArgsHonourTheFPSCap(t *testing.T) {
	capped := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128, FPS: 30}
	args := strings.Join(vp9WebMArgs(localSource("/in/src.mp4"), "/out/vp9.webm", capped), " ")
	if !strings.Contains(args, "-vf scale=1280:720,fps=30") {
		t.Errorf("VP9 alternate ignores the fps cap:\n%s", args)
	}
	// Uncapped stays exactly what it always was: no filter beyond the scale.
	uncapped := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}
	plain := strings.Join(vp9WebMArgs(localSource("/in/src.mp4"), "/out/vp9.webm", uncapped), " ")
	if !strings.Contains(plain, "-vf scale=1280:720 ") {
		t.Errorf("uncapped VP9 args changed shape:\n%s", plain)
	}
	if strings.Contains(plain, "fps=") {
		t.Errorf("uncapped VP9 args grew an fps filter:\n%s", plain)
	}
}

// TestVP9AlternateIsBudgetedForTheRateItEmits closes the loop with the planner:
// the rung the alternate is encoded at is the top rung, so its bitrate and its
// frame rate must describe the same output.
func TestVP9AlternateIsBudgetedForTheRateItEmits(t *testing.T) {
	// 60fps source, 30fps cap: standard-rate budget AND a 30fps output.
	capped := PlanHLSLadderWith(HLSEncodeSettings{Resolutions: []int{1080}, MaxFPS: 30}, 1920, 1080, 60)[0]
	args := strings.Join(vp9WebMArgs(localSource("/in/src.mp4"), "/out/vp9.webm", capped), " ")
	if !strings.Contains(args, "fps=30") || !strings.Contains(args, "-b:v 5000k") {
		t.Errorf("capped alternate = the wrong pairing of rate and budget:\n%s", args)
	}
	// 60fps source, no cap: high-frame-rate budget AND no fps filter.
	free := PlanHLSLadderWith(HLSEncodeSettings{Resolutions: []int{1080}}, 1920, 1080, 60)[0]
	freeArgs := strings.Join(vp9WebMArgs(localSource("/in/src.mp4"), "/out/vp9.webm", free), " ")
	if strings.Contains(freeArgs, "fps=") || !strings.Contains(freeArgs, "-b:v 8000k") {
		t.Errorf("uncapped alternate = the wrong pairing of rate and budget:\n%s", freeArgs)
	}
}
