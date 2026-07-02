package media

import "testing"

// FuzzParseFFProbe asserts the ffprobe-output parser never panics on arbitrary
// bytes and, when it succeeds, returns only non-negative, sanely-bounded metadata
// — even for hostile input a crafted upload could make ffprobe emit (e.g. a huge
// duration that would overflow float->int to a negative second count).
func FuzzParseFFProbe(f *testing.F) {
	f.Add([]byte(`{"format":{"duration":"12.7"},"streams":[{"codec_type":"video","width":1920,"height":1080}]}`))
	f.Add([]byte(`{}`))
	f.Add([]byte(``))
	f.Add([]byte(`not json`))
	f.Add([]byte(`{"format":{"duration":"1e19"}}`)) // overflows float->int if unclamped
	f.Add([]byte(`{"format":{"duration":"-5"}}`))   // negative
	f.Add([]byte(`{"format":{"duration":"NaN"}}`))  // not a number
	f.Add([]byte(`{"format":{"duration":"Inf"}}`))  // infinite
	f.Add([]byte(`{"streams":[{"codec_type":"video","width":-1,"height":-1}]}`))

	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := parseFFProbe(b)
		if err != nil {
			return // a parse error is a valid outcome
		}
		if m.DurationSeconds < 0 || m.Width < 0 || m.Height < 0 {
			t.Fatalf("negative metadata field from %q: %+v", b, m)
		}
		if m.DurationSeconds >= maxProbeDurationSeconds {
			t.Fatalf("duration %d not clamped below %d for %q", m.DurationSeconds, maxProbeDurationSeconds, b)
		}
	})
}
