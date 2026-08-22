package media

import "testing"

func TestParseFFProbe(t *testing.T) {
	const out = `{
	  "streams": [
	    {"codec_type": "audio", "width": 0, "height": 0},
	    {"codec_type": "video", "width": 1920, "height": 1080}
	  ],
	  "format": {"duration": "12.840000"}
	}`
	m, err := parseFFProbe([]byte(out))
	if err != nil {
		t.Fatalf("parseFFProbe: %v", err)
	}
	if m.DurationSeconds != 13 { // 12.84 rounds to 13
		t.Errorf("duration = %d, want 13", m.DurationSeconds)
	}
	if m.Width != 1920 || m.Height != 1080 {
		t.Errorf("dimensions = %dx%d, want 1920x1080", m.Width, m.Height)
	}
}

func TestParseFFProbeAudioOnly(t *testing.T) {
	const out = `{"streams":[{"codec_type":"audio"}],"format":{"duration":"3.0"}}`
	m, err := parseFFProbe([]byte(out))
	if err != nil {
		t.Fatalf("parseFFProbe: %v", err)
	}
	if m.DurationSeconds != 3 {
		t.Errorf("duration = %d, want 3", m.DurationSeconds)
	}
	if m.Width != 0 || m.Height != 0 {
		t.Errorf("dimensions = %dx%d, want 0x0 (no video stream)", m.Width, m.Height)
	}
}

func TestParseFFProbeMissingAndInvalid(t *testing.T) {
	// No format/streams -> all zero, no error.
	m, err := parseFFProbe([]byte(`{}`))
	if err != nil {
		t.Fatalf("parseFFProbe empty: %v", err)
	}
	if m != (Metadata{}) {
		t.Errorf("metadata = %+v, want zero", m)
	}
	// Non-JSON -> error.
	if _, err := parseFFProbe([]byte("not json")); err == nil {
		t.Fatal("parseFFProbe(non-json) = nil error, want error")
	}
}

// --- config-parity W10: frame-rate probing for transcoding_max_fps ---

func TestParseFFProbeFrameRate(t *testing.T) {
	const out = `{
	  "streams": [
	    {"codec_type": "video", "width": 1920, "height": 1080, "avg_frame_rate": "30000/1001", "r_frame_rate": "60/1"}
	  ],
	  "format": {"duration": "1.0"}
	}`
	m, err := parseFFProbe([]byte(out))
	if err != nil {
		t.Fatalf("parseFFProbe: %v", err)
	}
	if m.FPS < 29.9 || m.FPS > 30.0 {
		t.Errorf("FPS = %v, want ~29.97 (avg_frame_rate preferred over r_frame_rate)", m.FPS)
	}

	// Degenerate avg ("0/0") falls back to r_frame_rate.
	const fallback = `{"streams":[{"codec_type":"video","width":640,"height":360,"avg_frame_rate":"0/0","r_frame_rate":"25/1"}]}`
	m, err = parseFFProbe([]byte(fallback))
	if err != nil {
		t.Fatalf("parseFFProbe fallback: %v", err)
	}
	if m.FPS != 25 {
		t.Errorf("FPS = %v, want 25 (r_frame_rate fallback)", m.FPS)
	}
}

func TestParseFrameRate(t *testing.T) {
	cases := map[string]float64{
		"30000/1001": 29.97002997002997,
		"25/1":       25,
		"24":         24,
		"0/0":        0,
		"N/A":        0,
		"":           0,
		"junk":       0,
		"-30/1":      0,
		"30/0":       0,
		"100000/1":   0, // absurd rates read as unknown
	}
	for in, want := range cases {
		if got := parseFrameRate(in); got != want {
			t.Errorf("parseFrameRate(%q) = %v, want %v", in, got, want)
		}
	}
}

// --- phase-3 item 6: probe verdicts the ladder turns on -----------------------

// ffprobeCoverArt is a verbatim capture of ffprobe 8.1 on an audio file carrying
// a cover image (ffmpeg -i sine -i png -disposition:v attached_pic). Note what it
// says: codec_type "video", 600x600, a plausible r_frame_rate. EVERY field that
// would normally identify real video looks real. Only the disposition tells the
// truth, which is why the fixture is the real bytes.
const ffprobeCoverArt = `{
  "streams": [
    {"codec_type": "audio", "bit_rate": "64276",
     "avg_frame_rate": "0/0", "r_frame_rate": "0/0",
     "disposition": {"attached_pic": 0}},
    {"codec_type": "video", "codec_name": "mjpeg", "width": 600, "height": 600,
     "avg_frame_rate": "0/0", "r_frame_rate": "90000/1",
     "disposition": {"attached_pic": 1}}
  ],
  "format": {"duration": "8.0"}
}`

// TestParseFFProbeCoverArtIsNotVideo is the regression for the shape most
// podcast and music uploads actually have. Taking the attached picture as video
// meant the file was never classified audio-only, a whole ABR ladder was planned
// from one still, and trick-play then failed on a stream with no timeline —
// five retries and a dead letter, which is exactly the outcome the audio-only
// path exists to eliminate.
func TestParseFFProbeCoverArtIsNotVideo(t *testing.T) {
	m, err := parseFFProbe([]byte(ffprobeCoverArt))
	if err != nil {
		t.Fatalf("parseFFProbe: %v", err)
	}
	if m.HasVideo() {
		t.Errorf("cover art was read as video: %dx%d", m.Width, m.Height)
	}
	if !m.AudioOnly() {
		t.Errorf("metadata = %+v, want the audio-only verdict", m)
	}
	if !m.HasAudio || m.DurationSeconds != 8 {
		t.Errorf("metadata = %+v, want audio and an 8s duration", m)
	}
	// The picture's timebase must not leak out as a frame rate either — it would
	// buy the (non-existent) ladder the high-frame-rate budget.
	if m.FPS != 0 {
		t.Errorf("FPS = %v, want 0 — 90000/1 is a timebase, not a frame rate", m.FPS)
	}
	// Real video ALONGSIDE the cover art still wins: the flag rejects the
	// picture, it does not reject the file.
	const withRealVideo = `{"streams":[
	  {"codec_type":"video","width":600,"height":600,"r_frame_rate":"90000/1","disposition":{"attached_pic":1}},
	  {"codec_type":"video","width":1920,"height":1080,"avg_frame_rate":"30/1","disposition":{"attached_pic":0}},
	  {"codec_type":"audio","disposition":{"attached_pic":0}}],
	  "format":{"duration":"8.0"}}`
	v, err := parseFFProbe([]byte(withRealVideo))
	if err != nil {
		t.Fatalf("parseFFProbe: %v", err)
	}
	if v.Width != 1920 || v.Height != 1080 || v.FPS != 30 || v.AudioOnly() {
		t.Errorf("metadata = %+v, want the real 1920x1080@30 stream", v)
	}
}

// TestParseFFProbeFrameRateFallback pins both halves of the r_frame_rate
// fallback: the case it exists for, and the case that made it dangerous once the
// frame rate started driving the bitrate budget.
func TestParseFFProbeFrameRateFallback(t *testing.T) {
	cases := []struct {
		name, avg, r string
		want         float64
		why          string
	}{
		{"the average when there is one", "30000/1001", "30/1", 30000.0 / 1001,
			"avg_frame_rate is always preferred"},
		{"single-GOP MPEG-TS capture", "0/0", "60/1", 60,
			"the real case the fallback exists for: ffprobe cannot average, r_frame_rate is genuine"},
		{"short clip with no average", "0/0", "25/1", 25, ""},
		{"a plausible rate at the boundary", "0/0", "120/1", 120,
			"120 is inclusive: it is a real high-frame-rate capture rate"},
		{"an MPEG-TS timebase", "0/0", "90000/1", 0,
			"not a frame rate at all; unknown must degrade to the standard-rate budget"},
		{"an MP4 millisecond timebase", "0/0", "1000/1", 0, ""},
		{"just above the plausibility bound", "0/0", "121/1", 0, ""},
		{"neither known", "0/0", "N/A", 0, ""},
	}
	for _, tc := range cases {
		got := streamFrameRate(tc.avg, tc.r)
		if got != tc.want {
			t.Errorf("%s: streamFrameRate(%q, %q) = %v, want %v — %s",
				tc.name, tc.avg, tc.r, got, tc.want, tc.why)
		}
	}
	// And the guard really does keep the budget at baseline.
	if fpsVideoBitrateMultiplier(streamFrameRate("0/0", "90000/1")) != 1 {
		t.Error("a timebase read as a frame rate would buy every rung the high-frame-rate budget")
	}
}

// TestParseFFProbeAudioBitrate pins the hint the audio-only budget reads.
func TestParseFFProbeAudioBitrate(t *testing.T) {
	m, err := parseFFProbe([]byte(ffprobeCoverArt))
	if err != nil {
		t.Fatalf("parseFFProbe: %v", err)
	}
	if m.AudioKbps != 64 {
		t.Errorf("AudioKbps = %d, want 64 (64276 bps rounded to whole kbps)", m.AudioKbps)
	}
	for _, tc := range []struct {
		raw  string
		want int
	}{
		{"", 0}, {"N/A", 0}, {"0", 0}, {"-1", 0},
		{"64276", 64}, {"128000", 128}, {"127600", 128},
		{"2000000000", 0}, // absurd: refused rather than believed
	} {
		if got := parseBitrateKbps(tc.raw); got != tc.want {
			t.Errorf("parseBitrateKbps(%q) = %d, want %d", tc.raw, got, tc.want)
		}
	}
}
