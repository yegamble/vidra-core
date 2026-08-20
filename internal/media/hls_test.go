package media

import (
	"fmt"
	"reflect"
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
