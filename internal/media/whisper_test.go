package media

import (
	"strings"
	"testing"
)

// TestWhisperWavArgs pins the ffmpeg argument vector that extracts a 16 kHz mono
// signed-16-bit PCM WAV — the canonical Whisper input.
func TestWhisperWavArgs(t *testing.T) {
	args := whisperWavArgs("/in/video.mp4", "/tmp/out.wav")
	joined := strings.Join(args, " ")

	// Source and destination are present and in place.
	if args[len(args)-1] != "/tmp/out.wav" {
		t.Errorf("last arg = %q, want the output path", args[len(args)-1])
	}
	for _, want := range []string{
		"-i /in/video.mp4", // input
		"-vn",              // drop video
		"-ac 1",            // mono
		"-ar 16000",        // 16 kHz
		"-c:a pcm_s16le",   // signed 16-bit PCM
		"-f wav",           // WAV container
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

// TestFormatVTTTimestamp checks the HH:MM:SS.mmm rendering, including rounding
// and the negative clamp.
func TestFormatVTTTimestamp(t *testing.T) {
	cases := []struct {
		in   float64
		want string
	}{
		{0, "00:00:00.000"},
		{1.5, "00:00:01.500"},
		{61.25, "00:01:01.250"},
		{3661.007, "01:01:01.007"},
		{-3, "00:00:00.000"}, // clamp
	}
	for _, c := range cases {
		if got := formatVTTTimestamp(c.in); got != c.want {
			t.Errorf("formatVTTTimestamp(%v) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestRenderWebVTT renders segments to a valid WebVTT track, skipping empty cues
// and trimming text.
func TestRenderWebVTT(t *testing.T) {
	segs := []whisperSegment{
		{Start: 0, End: 1.5, Text: " Hello world "},
		{Start: 1.5, End: 2, Text: "   "}, // empty after trim → skipped
		{Start: 2, End: 3.25, Text: "Second cue"},
	}
	out := string(renderWebVTT(segs))

	if !isValidWebVTTPrefix(out) {
		t.Fatalf("output must start with the WEBVTT signature, got %q", out)
	}
	want := "WEBVTT\n\n00:00:00.000 --> 00:00:01.500\nHello world\n\n00:00:02.000 --> 00:00:03.250\nSecond cue\n"
	if out != want {
		t.Errorf("render mismatch:\n got %q\nwant %q", out, want)
	}
	if strings.Count(out, "-->") != 2 {
		t.Errorf("expected 2 cues (empty one skipped), got %d", strings.Count(out, "-->"))
	}
}

// TestRenderWebVTTEmpty: no segments still yields a valid (header-only) track.
func TestRenderWebVTTEmpty(t *testing.T) {
	out := string(renderWebVTT(nil))
	if !isValidWebVTTPrefix(out) {
		t.Errorf("empty render %q is not valid WebVTT", out)
	}
}

// isValidWebVTTPrefix mirrors the caption-store's WebVTT signature check (the
// file must begin with "WEBVTT" followed by EOF/newline/space/tab) so the test
// asserts the rendered track would be accepted on upsert.
func isValidWebVTTPrefix(s string) bool {
	if !strings.HasPrefix(s, "WEBVTT") {
		return false
	}
	rest := s[len("WEBVTT"):]
	if rest == "" {
		return true
	}
	switch rest[0] {
	case '\n', '\r', ' ', '\t':
		return true
	}
	return false
}

// TestParseWhisperResponse decodes verbose_json into ordered segments.
func TestParseWhisperResponse(t *testing.T) {
	body := []byte(`{
		"task": "transcribe",
		"language": "english",
		"segments": [
			{"start": 2.0, "end": 3.0, "text": " world"},
			{"start": 0.0, "end": 2.0, "text": " hello"}
		]
	}`)
	segs, err := parseWhisperResponse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(segs) != 2 {
		t.Fatalf("segments = %d, want 2", len(segs))
	}
	// Sorted by start time.
	if segs[0].Text != " hello" || segs[1].Text != " world" {
		t.Errorf("segments not ordered by start: %+v", segs)
	}

	// Rendering the parsed segments produces the expected VTT.
	vtt := string(renderWebVTT(segs))
	if !strings.Contains(vtt, "00:00:00.000 --> 00:00:02.000\nhello") {
		t.Errorf("VTT missing first cue:\n%s", vtt)
	}
}

// TestParseWhisperResponseMalformed: a non-JSON body is an error.
func TestParseWhisperResponseMalformed(t *testing.T) {
	if _, err := parseWhisperResponse([]byte("not json")); err == nil {
		t.Errorf("expected an error for a malformed body")
	}
}
