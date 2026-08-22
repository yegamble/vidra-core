package media

import (
	"fmt"
	"reflect"
	"slices"
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
	args := strings.Join(hlsRungArgs(localSource("/in/src.mp4"), "/out/720p", r, 0), " ")
	for _, want := range []string{
		"-i /in/src.mp4",
		"-c:v libx264",
		"-vf scale=1280:720",
		"-b:v 2800k",
		"-maxrate 2800k",
		"-c:a aac",
		"-b:a 128k",
		"-f hls",
		"-force_key_frames expr:gte(t,n_forced*6)",
		"-hls_time 6",
		"-hls_playlist_type vod",
		"-hls_flags independent_segments",
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

func TestHLSTrickPlayArgs(t *testing.T) {
	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}
	args := strings.Join(hlsTrickPlayArgs(localSource("/in/src.mp4"), "/out/720p", r, 3), " ")
	for _, want := range []string{
		"-i /in/src.mp4",
		"-map 0:v:0",
		"-an",
		"-threads 3",
		"-profile:v main",
		"-vf scale=1280:720,fps=1",
		"-g 1",
		"-keyint_min 1",
		"-sc_threshold 0",
		"-crf 28",
		"-hls_time 1",
		"-hls_flags single_file",
		"-hls_segment_filename /out/720p/iframe.ts",
		"/out/720p/iframe.m3u8",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("trick-play args missing %q\nargs: %s", want, args)
		}
	}
}

func TestMarkIFramesOnlyPlaylistAndPeakBandwidth(t *testing.T) {
	raw := []byte("#EXTM3U\n#EXT-X-VERSION:4\n#EXTINF:1.0,\n#EXT-X-BYTERANGE:1000@0\niframe.ts\n#EXTINF:1.0,\n#EXT-X-BYTERANGE:1500@1000\niframe.ts\n#EXT-X-ENDLIST\n")
	marked, err := markIFramesOnlyPlaylist(raw)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Count(string(marked), "#EXT-X-I-FRAMES-ONLY") != 1 {
		t.Fatalf("marked playlist =\n%s", marked)
	}
	markedAgain, err := markIFramesOnlyPlaylist(marked)
	if err != nil || string(markedAgain) != string(marked) {
		t.Fatalf("marking must be idempotent: err=%v\n%s", err, markedAgain)
	}
	bandwidth, err := trickPlayPeakBandwidth(marked)
	if err != nil {
		t.Fatal(err)
	}
	if bandwidth != 13201 {
		t.Errorf("peak bandwidth = %d, want 13201", bandwidth)
	}
	if _, err := markIFramesOnlyPlaylist([]byte("#EXTM3U\n#EXT-X-ENDLIST\n")); err == nil {
		t.Error("malformed playlist should fail validation")
	}
}

func TestParseH264CodecString(t *testing.T) {
	got, err := parseH264CodecString([]byte(`{"streams":[{"codec_name":"h264","profile":"Main","level":22}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if got != "avc1.4d4016" {
		t.Errorf("codec = %q, want avc1.4d4016", got)
	}
	if _, err := parseH264CodecString([]byte(`{"streams":[{"codec_name":"hevc","profile":"Main","level":120}]}`)); err == nil {
		t.Error("non-H.264 output should fail")
	}
}

func TestHLSProgressiveMP4Args(t *testing.T) {
	const playlist = "/out/720p/playlist.m3u8"
	tests := []struct {
		name         string
		includeAudio bool
		dst          string
		want         []string
	}{
		{
			name:         "muxed with optional audio",
			includeAudio: true,
			dst:          "/out/720p/video.mp4",
			want: []string{
				"-y", "-i", playlist,
				"-map", "0:v:0",
				"-map", "0:a:0?",
				"-c", "copy",
				"-movflags", "+faststart",
				"/out/720p/video.mp4",
			},
		},
		{
			name:         "video only",
			includeAudio: false,
			dst:          "/out/720p/video-only.mp4",
			want: []string{
				"-y", "-i", playlist,
				"-map", "0:v:0",
				"-c:v", "copy",
				"-an",
				"-movflags", "+faststart",
				"/out/720p/video-only.mp4",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := hlsProgressiveMP4Args(playlist, tt.dst, tt.includeAudio)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("hlsProgressiveMP4Args() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestHLSAudioM4AArgs(t *testing.T) {
	got := hlsAudioM4AArgs("/out/720p/playlist.m3u8", "/out/audio.m4a")
	want := []string{
		"-y", "-i", "/out/720p/playlist.m3u8",
		"-map", "0:a:0",
		"-vn",
		"-c:a", "copy",
		"-movflags", "+faststart",
		"/out/audio.m4a",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("hlsAudioM4AArgs() = %#v, want %#v", got, want)
	}
}

func TestRenderMasterPlaylist(t *testing.T) {
	rungs := []HLSRung{
		{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128},
		{Height: 360, Width: 640, VideoKbps: 800, AudioKbps: 96},
	}
	m := renderMasterPlaylist(rungs, map[int]hlsTrickPlayInfo{
		720: {Bandwidth: 120000, Codec: "avc1.4d401f"},
		360: {Bandwidth: 60000, Codec: "avc1.4d4016"},
	})
	if !strings.HasPrefix(m, "#EXTM3U\n") {
		t.Fatalf("master playlist must start with #EXTM3U:\n%s", m)
	}
	for _, want := range []string{
		"#EXT-X-VERSION:4\n",
		"#EXT-X-INDEPENDENT-SEGMENTS\n",
		"#EXT-X-STREAM-INF:BANDWIDTH=3220800,RESOLUTION=1280x720\n720p/playlist.m3u8\n",
		"#EXT-X-STREAM-INF:BANDWIDTH=985600,RESOLUTION=640x360\n360p/playlist.m3u8\n",
		`#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=120000,RESOLUTION=1280x720,CODECS="avc1.4d401f",URI="720p/iframe.m3u8"` + "\n",
		`#EXT-X-I-FRAME-STREAM-INF:BANDWIDTH=60000,RESOLUTION=640x360,CODECS="avc1.4d4016",URI="360p/iframe.m3u8"` + "\n",
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

func TestHLSDownloadKeys(t *testing.T) {
	const (
		rendition = "streaming-playlists/video-id/720p"
		master    = "streaming-playlists/video-id/master.m3u8"
	)
	if got, want := HLSDownloadKey(rendition, true), rendition+"/"+HLSMuxedDownloadFilename; got != want {
		t.Errorf("HLSDownloadKey(muxed) = %q, want %q", got, want)
	}
	if got, want := HLSDownloadKey(rendition, false), rendition+"/"+HLSVideoOnlyDownloadFilename; got != want {
		t.Errorf("HLSDownloadKey(video-only) = %q, want %q", got, want)
	}
	if got, want := HLSAudioDownloadKey(master), "streaming-playlists/video-id/"+HLSAudioDownloadFilename; got != want {
		t.Errorf("HLSAudioDownloadKey = %q, want %q", got, want)
	}
	if HLSMP4ContentType != "video/mp4" || HLSM4AContentType != "audio/mp4" {
		t.Errorf("download content types = %q, %q", HLSMP4ContentType, HLSM4AContentType)
	}
}

// --- config-parity W10: runtime encode settings ---

func TestPlanHLSLadderWithCustomResolutions(t *testing.T) {
	// Only the enabled rungs are planned; order of the input doesn't matter
	// (planning sorts tallest first); rungs above the source stay skipped.
	st := HLSEncodeSettings{Resolutions: []int{360, 1080, 720}}
	got := rungSizes(PlanHLSLadderWith(st, 1920, 1080, 0))
	want := []string{"1920x1080", "1280x720", "640x360"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("custom ladder = %v, want %v", got, want)
	}
}

func TestPlanHLSLadderWithExtendedRungs(t *testing.T) {
	st := HLSEncodeSettings{Resolutions: []int{2160, 1440, 240, 144}}
	rungs := PlanHLSLadderWith(st, 3840, 2160, 0)
	got := rungSizes(rungs)
	want := []string{"3840x2160", "2560x1440", "426x240", "256x144"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("extended ladder = %v, want %v", got, want)
	}
	if rungs[0].VideoKbps != 16000 || rungs[0].AudioKbps != 160 {
		t.Errorf("2160 rung bitrates = %d/%d, want 16000/160", rungs[0].VideoKbps, rungs[0].AudioKbps)
	}
}

func TestPlanHLSLadderWithInvalidOrEmptyResolutionsFallsBack(t *testing.T) {
	// Defensive: unknown heights are dropped, duplicates collapse, and an
	// empty/all-invalid universe falls back to the default ladder (the registry
	// validator refuses to store one, but a direct DB edit must not break
	// planning).
	for _, res := range [][]int{nil, {}, {999, -1, 0}, {717}} {
		got := rungSizes(PlanHLSLadderWith(HLSEncodeSettings{Resolutions: res}, 1920, 1080, 0))
		want := rungSizes(PlanHLSLadder(1920, 1080))
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Errorf("resolutions %v = %v, want default ladder %v", res, got, want)
		}
	}
	dup := rungSizes(PlanHLSLadderWith(HLSEncodeSettings{Resolutions: []int{720, 720}}, 1920, 1080, 0))
	if strings.Join(dup, ",") != "1280x720" {
		t.Errorf("duplicate rung heights = %v, want a single 1280x720", dup)
	}
}

func TestPlanHLSLadderWithOriginalResolution(t *testing.T) {
	// Source taller than the highest enabled rung: an extra rung at the
	// source's own size tops the ladder, with the bitrates of the smallest
	// canonical rung at least as tall.
	st := HLSEncodeSettings{Resolutions: []int{1080, 720}, OriginalResolution: true}
	rungs := PlanHLSLadderWith(st, 2560, 1440, 0)
	got := rungSizes(rungs)
	want := []string{"2560x1440", "1920x1080", "1280x720"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("original-resolution ladder = %v, want %v", got, want)
	}
	if rungs[0].VideoKbps != 9000 || rungs[0].AudioKbps != 160 {
		t.Errorf("original 1440 rung bitrates = %d/%d, want the canonical 1440 rung's 9000/160", rungs[0].VideoKbps, rungs[0].AudioKbps)
	}

	// Taller than the whole canonical table: clamps to the tallest rung's bitrates.
	huge := PlanHLSLadderWith(st, 7680, 4320, 0)
	if huge[0].Height != 4320 || huge[0].VideoKbps != 16000 {
		t.Errorf("8K original rung = %dp @ %dk, want 4320p @ 16000k", huge[0].Height, huge[0].VideoKbps)
	}

	// Source equal to the highest enabled rung: no extra rung.
	same := rungSizes(PlanHLSLadderWith(st, 1920, 1080, 0))
	if strings.Join(same, ",") != "1920x1080,1280x720" {
		t.Errorf("1080 source with original-resolution on = %v, want no extra rung", same)
	}

	// Off (default): the source's extra height is simply not laddered.
	off := rungSizes(PlanHLSLadderWith(HLSEncodeSettings{Resolutions: []int{1080, 720}}, 2560, 1440, 0))
	if strings.Join(off, ",") != "1920x1080,1280x720" {
		t.Errorf("original-resolution off = %v, want ladder only", off)
	}
}

func TestPlanHLSLadderWithMaxFPS(t *testing.T) {
	st := HLSEncodeSettings{Resolutions: []int{1080, 720}, MaxFPS: 30}
	// Source faster than the cap: every rung carries the cap (uniform —
	// documented deviation from PeerTube's per-rung fps rules).
	for _, r := range PlanHLSLadderWith(st, 1920, 1080, 60) {
		if r.FPS != 30 {
			t.Errorf("rung %dp FPS = %d, want 30 (source 60 > cap 30)", r.Height, r.FPS)
		}
	}
	// Source at/below the cap, or unknown: no filter (never upsample).
	for _, sourceFPS := range []float64{0, 24, 30} {
		for _, r := range PlanHLSLadderWith(st, 1920, 1080, sourceFPS) {
			if r.FPS != 0 {
				t.Errorf("source %v fps: rung %dp FPS = %d, want 0 (no cap applied)", sourceFPS, r.Height, r.FPS)
			}
		}
	}
	// No cap configured: nothing regardless of source rate.
	for _, r := range PlanHLSLadderWith(HLSEncodeSettings{Resolutions: []int{720}}, 1920, 1080, 120) {
		if r.FPS != 0 {
			t.Errorf("uncapped rung FPS = %d, want 0", r.FPS)
		}
	}
	// The single-rung tiny-source fallback also honors the cap.
	tiny := PlanHLSLadderWith(HLSEncodeSettings{Resolutions: []int{1080}, MaxFPS: 30}, 320, 240, 60)
	if len(tiny) != 1 || tiny[0].FPS != 30 {
		t.Errorf("tiny-source fallback = %+v, want one rung with FPS 30", tiny)
	}
}

// --- phase-3 item 6.1: frame-rate-aware bitrates ---

// TestPlanHLSLadderBitratesAreFPSAware is the planning-level pin: a 24/30fps
// source gets the shipped table verbatim, a 50/60fps source gets 1.6x the VIDEO
// budget on every rung, and the audio budget never moves.
func TestPlanHLSLadderBitratesAreFPSAware(t *testing.T) {
	baseline := PlanHLSLadderWith(DefaultHLSEncodeSettings(), 1920, 1080, 24)
	if len(baseline) != 4 {
		t.Fatalf("test is stale: 1080p plans %d rungs, want 4", len(baseline))
	}
	// The 24fps ladder IS the shipped table — this is the anchor every other
	// case is expressed against, so it is pinned by literal value.
	wantVideo := []int{5000, 2800, 1400, 800}
	wantAudio := []int{160, 128, 128, 96}
	for i, r := range baseline {
		if r.VideoKbps != wantVideo[i] || r.AudioKbps != wantAudio[i] {
			t.Errorf("24fps rung %s = %dk/%dk, want %dk/%dk (the table, unscaled)",
				r.Name(), r.VideoKbps, r.AudioKbps, wantVideo[i], wantAudio[i])
		}
	}
	// 30fps is the same standard-rate tier.
	for i, r := range PlanHLSLadderWith(DefaultHLSEncodeSettings(), 1920, 1080, 30) {
		if r.VideoKbps != wantVideo[i] || r.AudioKbps != wantAudio[i] {
			t.Errorf("30fps rung %s = %dk/%dk, want the same as 24fps (%dk/%dk)",
				r.Name(), r.VideoKbps, r.AudioKbps, wantVideo[i], wantAudio[i])
		}
	}
	// 60fps: 1.6x video, identical audio. 5000→8000, 2800→4480, 1400→2240, 800→1280.
	wantHFR := []int{8000, 4480, 2240, 1280}
	for _, sourceFPS := range []float64{50, 59.94, 60, 120} {
		rungs := PlanHLSLadderWith(DefaultHLSEncodeSettings(), 1920, 1080, sourceFPS)
		for i, r := range rungs {
			if r.VideoKbps != wantHFR[i] {
				t.Errorf("%vfps rung %s video = %dk, want %dk (1.6x the %dk table value)",
					sourceFPS, r.Name(), r.VideoKbps, wantHFR[i], wantVideo[i])
			}
			if r.AudioKbps != wantAudio[i] {
				t.Errorf("%vfps rung %s audio = %dk, want %dk — audio is not frame-rate dependent",
					sourceFPS, r.Name(), r.AudioKbps, wantAudio[i])
			}
		}
	}
}

// TestPlanHLSLadderBitratesDegradeToBaselineWhenFPSIsUnknown pins the safe
// direction: a probe that could not read a frame rate (VFR, absent, degenerate
// "0/0") budgets the ladder exactly as it was budgeted before this existed.
func TestPlanHLSLadderBitratesDegradeToBaselineWhenFPSIsUnknown(t *testing.T) {
	if parseFrameRate("0/0") != 0 || parseFrameRate("N/A") != 0 || parseFrameRate("") != 0 {
		t.Fatal("test is stale: the probe no longer reports unknown frame rates as 0")
	}
	unknown := PlanHLSLadderWith(DefaultHLSEncodeSettings(), 1920, 1080, 0)
	known := PlanHLSLadderWith(DefaultHLSEncodeSettings(), 1920, 1080, 30)
	for i := range unknown {
		if unknown[i] != known[i] {
			t.Errorf("unknown-fps rung %s = %+v, want the standard-rate plan %+v",
				unknown[i].Name(), unknown[i], known[i])
		}
	}
}

// TestPlanHLSLadderBitratesFollowTheEFFECTIVEFPS proves the multiplier keys off
// what the ladder will actually EMIT, not off the source: transcoding_max_fps
// appends a uniform fps filter, so a 60fps source capped to 30 really does emit
// standard-rate video and must be budgeted as such.
func TestPlanHLSLadderBitratesFollowTheEFFECTIVEFPS(t *testing.T) {
	capped := PlanHLSLadderWith(HLSEncodeSettings{Resolutions: []int{1080, 720}, MaxFPS: 30}, 1920, 1080, 60)
	for _, r := range capped {
		if r.FPS != 30 {
			t.Fatalf("test is stale: a capped 60fps source must plan FPS=30, got %d", r.FPS)
		}
	}
	if capped[0].VideoKbps != 5000 || capped[1].VideoKbps != 2800 {
		t.Errorf("60fps source capped to 30fps = %dk/%dk, want the standard-rate 5000k/2800k",
			capped[0].VideoKbps, capped[1].VideoKbps)
	}
	// A cap ABOVE the source rate never bites, so the source rate still decides.
	loose := PlanHLSLadderWith(HLSEncodeSettings{Resolutions: []int{1080}, MaxFPS: 120}, 1920, 1080, 60)
	if loose[0].FPS != 0 || loose[0].VideoKbps != 8000 {
		t.Errorf("60fps under a 120fps cap = FPS %d @ %dk, want FPS 0 @ 8000k",
			loose[0].FPS, loose[0].VideoKbps)
	}
	// A cap that bites but stays high-frame-rate keeps the high budget.
	stillHFR := PlanHLSLadderWith(HLSEncodeSettings{Resolutions: []int{1080}, MaxFPS: 50}, 1920, 1080, 60)
	if stillHFR[0].FPS != 50 || stillHFR[0].VideoKbps != 8000 {
		t.Errorf("60fps capped to 50fps = FPS %d @ %dk, want FPS 50 @ 8000k",
			stillHFR[0].FPS, stillHFR[0].VideoKbps)
	}
}

// TestFPSVideoBitrateMultiplier pins the curve itself: flat below the baseline,
// flat above the high-frame-rate threshold, monotonic and continuous between.
func TestFPSVideoBitrateMultiplier(t *testing.T) {
	for _, fps := range []float64{0, 1, 23.976, 25, 29.97, 30, 32} {
		if got := fpsVideoBitrateMultiplier(fps); got != 1 {
			t.Errorf("fpsVideoBitrateMultiplier(%v) = %v, want 1 (standard rate)", fps, got)
		}
	}
	for _, fps := range []float64{40, 48, 50, 59.94, 60, 90, 240} {
		if got := fpsVideoBitrateMultiplier(fps); got != hlsHighFPSVideoMultiplier {
			t.Errorf("fpsVideoBitrateMultiplier(%v) = %v, want %v", fps, got, hlsHighFPSVideoMultiplier)
		}
	}
	// The ramp: strictly increasing, and it meets both ends without a step.
	if got := fpsVideoBitrateMultiplier(36); got != 1.3 {
		t.Errorf("fpsVideoBitrateMultiplier(36) = %v, want 1.3 (the midpoint of the ramp)", got)
	}
	prev := 1.0
	for fps := 32.0; fps <= 40.0; fps += 0.5 {
		got := fpsVideoBitrateMultiplier(fps)
		if got < prev {
			t.Errorf("multiplier fell from %v to %v at %v fps", prev, got, fps)
		}
		if got < 1 || got > hlsHighFPSVideoMultiplier {
			t.Errorf("multiplier %v at %v fps is outside [1, %v]", got, fps, hlsHighFPSVideoMultiplier)
		}
		prev = got
	}
}

// TestPlanHLSLadderFPSAwareBitratesReachEveryDerivedRung proves the scaling is
// not confined to the canonical table lookup: the tiny-source fallback and the
// transcoding_original_resolution rung derive their budgets through their own
// code paths and must scale too.
func TestPlanHLSLadderFPSAwareBitratesReachEveryDerivedRung(t *testing.T) {
	// Tiny-source fallback: inherits the LOWEST enabled rung's budget (360p:
	// 800k) — scaled.
	tiny := PlanHLSLadderWith(DefaultHLSEncodeSettings(), 320, 240, 60)
	if len(tiny) != 1 || tiny[0].VideoKbps != 1280 || tiny[0].AudioKbps != 96 {
		t.Errorf("60fps tiny-source fallback = %+v, want one rung at 1280k/96k", tiny)
	}
	// Original-resolution rung: 1440p's 9000k table value, scaled.
	st := HLSEncodeSettings{Resolutions: []int{1080, 720}, OriginalResolution: true}
	orig := PlanHLSLadderWith(st, 2560, 1440, 60)
	if orig[0].Height != 1440 || orig[0].VideoKbps != 14400 || orig[0].AudioKbps != 160 {
		t.Errorf("60fps original-resolution rung = %dp @ %dk/%dk, want 1440p @ 14400k/160k",
			orig[0].Height, orig[0].VideoKbps, orig[0].AudioKbps)
	}
}

// TestFPSAwareBitratesReachTheEncoderAndBothMasters closes the loop from the
// plan to the bytes: the scaled budget must drive -b:v/-maxrate/-bufsize on both
// packagers and be what BANDWIDTH declares in both master playlists. A ladder
// encoded at 1.6x whose manifest still advertises the 1x rate would have every
// ABR player under-estimate what it is about to download.
func TestFPSAwareBitratesReachTheEncoderAndBothMasters(t *testing.T) {
	rungs := PlanHLSLadderWith(DefaultHLSEncodeSettings(), 1920, 1080, 60)
	top := rungs[0]
	if top.VideoKbps != 8000 {
		t.Fatalf("test is stale: 60fps 1080p top rung = %dk, want 8000k", top.VideoKbps)
	}

	// MPEG-TS encoder args.
	ts := strings.Join(hlsLadderArgs(localSource("/in/src.mp4"), localOutput("/out"), rungs, 0), " ")
	for _, want := range []string{"-b:v 8000k", "-maxrate 8000k", "-bufsize 16000k"} {
		if !strings.Contains(ts, want) {
			t.Errorf("MPEG-TS ladder missing %q\n%s", want, ts)
		}
	}
	// CMAF encoder args (stream-qualified).
	plan := ladderPlan{rungs: rungs, labels: []string{"v0", "v1", "v2", "v3"}, hasAudio: true}
	cmaf := strings.Join(cmafPackager{}.LadderOutputArgs(localOutput("/out"), plan), " ")
	for _, want := range []string{"-b:v:0 8000k", "-maxrate:v:0 8000k", "-bufsize:v:0 16000k"} {
		if !strings.Contains(cmaf, want) {
			t.Errorf("CMAF ladder missing %q\n%s", want, cmaf)
		}
	}

	// Both masters' BANDWIDTH.
	wantBW := (8000 + top.AudioKbps) * 1000 * 11 / 10
	tsMaster := renderMasterPlaylist(rungs, nil)
	if !strings.Contains(tsMaster, "BANDWIDTH="+fmt.Sprint(wantBW)+",") {
		t.Errorf("MPEG-TS master does not declare the scaled top-rung bandwidth %d:\n%s", wantBW, tsMaster)
	}
	layout := cmafLayout{
		videoCodecs: []string{"avc1.4d4028,mp4a.40.2", "avc1.4d401f,mp4a.40.2", "avc1.4d401e,mp4a.40.2", "avc1.4d401e,mp4a.40.2"},
		hasAudio:    true,
		audioRep:    len(rungs),
	}
	cmafMaster, err := renderCMAFMasterPlaylist(rungs, top.AudioKbps, layout, nil)
	if err != nil {
		t.Fatalf("renderCMAFMasterPlaylist: %v", err)
	}
	if !strings.Contains(cmafMaster, "BANDWIDTH="+fmt.Sprint(wantBW)+",") {
		t.Errorf("CMAF master does not declare the scaled top-rung bandwidth %d:\n%s", wantBW, cmafMaster)
	}
}

func TestHLSRungArgsFPSAndThreads(t *testing.T) {
	r := HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128, FPS: 30}
	args := strings.Join(hlsRungArgs(localSource("/in/src.mp4"), "/out/720p", r, 4), " ")
	for _, want := range []string{
		"-vf scale=1280:720,fps=30",
		"-threads 4",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args missing %q\nargs: %s", want, args)
		}
	}
	// Defaults (FPS 0, threads 0) must leave the vector exactly ffmpeg-default.
	def := strings.Join(hlsRungArgs(localSource("/in/src.mp4"), "/out/720p", HLSRung{Height: 720, Width: 1280, VideoKbps: 2800, AudioKbps: 128}, 0), " ")
	if strings.Contains(def, "fps=") || strings.Contains(def, "-threads") {
		t.Errorf("default args must carry no fps filter or -threads\nargs: %s", def)
	}
}

func TestIsHLSRungHeightAndCanonicalSet(t *testing.T) {
	for _, h := range HLSCanonicalRungHeights {
		if !IsHLSRungHeight(h) {
			t.Errorf("IsHLSRungHeight(%d) = false, want true", h)
		}
	}
	for _, h := range []int{0, 1, 480 + 1, 4320} {
		if IsHLSRungHeight(h) {
			t.Errorf("IsHLSRungHeight(%d) = true, want false", h)
		}
	}
	// No 0p audio-only rung in v1 (ledgered).
	if IsHLSRungHeight(0) {
		t.Error("audio-only 0p rung must not be canonical in v1")
	}
	// The default ladder is the original hardcoded one.
	if got := fmt.Sprint(DefaultHLSResolutionHeights); got != "[1080 720 480 360]" {
		t.Errorf("DefaultHLSResolutionHeights = %v", got)
	}
}

// TestMetadataAudioOnly pins the classification the whole audio-only path turns
// on. It is deliberately narrower than "no video": a file with neither video nor
// audio is not media at all and must keep failing, and a source WITH video is
// never audio-only however little else it has.
func TestMetadataAudioOnly(t *testing.T) {
	cases := []struct {
		name string
		md   Metadata
		want bool
		why  string
	}{
		{"audio with no video", Metadata{HasAudio: true, DurationSeconds: 90}, true,
			"a podcast episode: the shape that used to dead-letter"},
		{"neither audio nor video", Metadata{DurationSeconds: 90}, false,
			"an unprobeable file must keep failing, not package as silence"},
		{"video with audio", Metadata{Width: 1280, Height: 720, HasAudio: true}, false, ""},
		{"video without audio", Metadata{Width: 1280, Height: 720}, false,
			"a silent video is a video"},
		{"audio beside a half-described video stream", Metadata{Width: 640, HasAudio: true}, true,
			"a stream ffprobe could not fully describe cannot be laddered"},
	}
	for _, tc := range cases {
		if got := tc.md.AudioOnly(); got != tc.want {
			t.Errorf("%s: AudioOnly() = %v, want %v — %s", tc.name, got, tc.want, tc.why)
		}
	}
}

// TestAudioOnlyLadderPlansNothingToEncode: the planner is unchanged, and that is
// the point. "No rungs" was never wrong — reading it as "unprobeable source" was.
func TestAudioOnlyLadderPlansNothingToEncode(t *testing.T) {
	if rungs := PlanHLSLadderWith(DefaultHLSEncodeSettings(), 0, 0, 0); rungs != nil {
		t.Errorf("audio-only source planned %v, want no rungs", rungSizes(rungs))
	}
}

// TestAudioOnlyKbpsFor pins the audio-only budget rule: the smallest canonical
// step whose nominal rate, allowed a 10% overshoot, still covers the source.
// Without it every podcast was re-encoded at the 160 kbps ceiling — a 64 kbps
// mono episode came out two and a half times larger carrying no more information
// than it arrived with, because lossy-to-lossy cannot recover what the first
// encoder threw away.
func TestAudioOnlyKbpsFor(t *testing.T) {
	cases := []struct {
		sourceKbps, want int
		why              string
	}{
		{0, 160, "an unknown source rate takes the ceiling: guessing low would degrade the whole content"},
		{64, 64, "a 64k podcast stays 64k"},
		{64276 / 1000, 64, "and still does when the probe reports container overhead (64276 bps)"},
		{70, 64, "70 is inside the 10% allowance on the 64 step (70.4)"},
		{71, 96, "71 is outside it, so the next step covers the source"},
		{96, 96, ""},
		{128, 128, ""},
		{130, 128, "a 128k stream probing high is still the 128 step (140.8)"},
		{160, 160, ""},
		{176, 160, "the top step's own allowance"},
		{192, 160, "above everything the ladder allocates: clamped to the ceiling, never exceeded"},
		{320, 160, "a 320k master is not re-encoded at 320k"},
		{32, 64, "below the lowest step: the lowest step covers it"},
	}
	for _, tc := range cases {
		if got := audioOnlyKbpsFor(tc.sourceKbps); got != tc.want {
			t.Errorf("audioOnlyKbpsFor(%d) = %d, want %d — %s", tc.sourceKbps, got, tc.want, tc.why)
		}
	}
	// It never allocates a rate no video rendition could have, and never exceeds
	// the ceiling.
	for source := 0; source <= 400; source++ {
		got := audioOnlyKbpsFor(source)
		if got > hlsAudioOnlyKbps {
			t.Fatalf("audioOnlyKbpsFor(%d) = %d, above the ceiling", source, got)
		}
		if !slices.Contains(hlsAudioSteps, got) {
			t.Fatalf("audioOnlyKbpsFor(%d) = %d, which is not a canonical audio step", source, got)
		}
	}
}

// TestAudioOnlyPlanBudgetFollowsTheSource joins the rule to the plan and to what
// the manifest declares, so the encoder and the BANDWIDTH cannot disagree.
func TestAudioOnlyPlanBudgetFollowsTheSource(t *testing.T) {
	plan := ladderPlan{hasAudio: true, sourceAudioKbps: 64}
	if got := plan.audioBitrateKbps(); got != 64 {
		t.Errorf("audio-only plan budget = %d, want the source's own 64", got)
	}
	args := strings.Join(cmafPackager{}.LadderOutputArgs(localOutput("/out"), plan), " ")
	if !strings.Contains(args, "-b:a:0 64k") {
		t.Errorf("encoder does not use the capped budget:\n%s", args)
	}
	// -ac 2 stays: a single-channel HLS rendition is a compatibility question
	// this rule deliberately does not answer.
	if !strings.Contains(args, "-ac:a:0 2") {
		t.Errorf("audio-only output stopped emitting stereo:\n%s", args)
	}
	master, err := renderCMAFAudioOnlyMasterPlaylist(plan.audioBitrateKbps(),
		cmafLayout{hasAudio: true, audioCodecs: "mp4a.40.2"})
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if want := fmt.Sprintf("BANDWIDTH=%d,", 64*1000*11/10); !strings.Contains(master, want) {
		t.Errorf("master declares a bandwidth the encoder did not produce, want %q:\n%s", want, master)
	}
	// An unknown source rate still takes the ceiling.
	if got := (ladderPlan{hasAudio: true}).audioBitrateKbps(); got != hlsAudioOnlyKbps {
		t.Errorf("unknown-source audio-only budget = %d, want the ceiling %d", got, hlsAudioOnlyKbps)
	}
}

// TestMetadataAudioOnlyRejectsCoverArtNotDimensions guards against the fix being
// weakened back into a dimension check. Cover art is rejected by its DISPOSITION;
// its width and height are perfectly real, which is exactly why the file used to
// be laddered from a still image.
func TestMetadataAudioOnlyRejectsCoverArtNotDimensions(t *testing.T) {
	m, err := parseFFProbe([]byte(ffprobeCoverArt))
	if err != nil {
		t.Fatalf("parseFFProbe: %v", err)
	}
	if !m.AudioOnly() {
		t.Fatalf("metadata = %+v, want audio-only", m)
	}
	// Prove the fixture really does carry real dimensions, so this test cannot
	// pass for the wrong reason if the fixture is ever softened.
	if !strings.Contains(ffprobeCoverArt, `"width": 600`) || !strings.Contains(ffprobeCoverArt, `"height": 600`) {
		t.Error("fixture no longer describes a cover image with real dimensions")
	}
}
