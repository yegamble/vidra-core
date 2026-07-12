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
