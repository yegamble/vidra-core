package moderation

import (
	"slices"
	"testing"
)

func TestNormalizeReasons(t *testing.T) {
	got := NormalizeReasons([]string{"privacy", "bogus", "privacy", "captions", ""})
	if want := []string{"privacy", "captions"}; !slices.Equal(got, want) {
		t.Errorf("NormalizeReasons = %v, want %v", got, want)
	}
	if got := NormalizeReasons(nil); got == nil || len(got) != 0 {
		t.Errorf("NormalizeReasons(nil) = %#v, want empty non-nil (NOT NULL column)", got)
	}
}

func TestVideoReportDetailValidateRange(t *testing.T) {
	i := func(v int32) *int32 { return &v }
	cases := []struct {
		name       string
		start, end *int32
		dur        *int32
		field      string
	}{
		{"none", nil, nil, nil, ""},
		{"moment", i(5), nil, nil, ""},
		{"range ok", i(0), i(10), i(10), ""},
		{"negative start", i(-1), nil, nil, "start_seconds"},
		{"end without start", nil, i(5), nil, "start_seconds"},
		{"end equals start", i(5), i(5), nil, "end_seconds"},
		{"end before start", i(9), i(5), nil, "end_seconds"},
		{"end past duration", i(1), i(11), i(10), "end_seconds"},
		{"start past duration", i(11), nil, i(10), "start_seconds"},
		{"unknown duration allows any end", i(1), i(99999), nil, ""},
		{"zero duration treated as unknown", i(1), i(5), i(0), ""},
	}
	for _, c := range cases {
		field, _ := VideoReportDetail{StartSeconds: c.start, EndSeconds: c.end}.ValidateRange(c.dur)
		if field != c.field {
			t.Errorf("%s: field = %q, want %q", c.name, field, c.field)
		}
	}
}
