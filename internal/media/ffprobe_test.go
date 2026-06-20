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
