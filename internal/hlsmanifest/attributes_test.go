package hlsmanifest

import (
	"reflect"
	"strings"
	"testing"
)

func TestAttributesPreserveQuotedCommasAndRawFields(t *testing.T) {
	input := `TYPE=AUDIO,NAME="English, descriptive",CODECS="avc1.640028,mp4a.40.2",URI="audio.m3u8"`
	want := []Attribute{
		{Name: "TYPE", Value: "AUDIO", Raw: "TYPE=AUDIO"},
		{Name: "NAME", Value: "English, descriptive", Raw: `NAME="English, descriptive"`, Quoted: true},
		{Name: "CODECS", Value: "avc1.640028,mp4a.40.2", Raw: `CODECS="avc1.640028,mp4a.40.2"`, Quoted: true},
		{Name: "URI", Value: "audio.m3u8", Raw: `URI="audio.m3u8"`, Quoted: true},
	}
	got, err := ParseAttributes(input)
	if err != nil || !reflect.DeepEqual(got, want) {
		t.Fatalf("attributes = %#v, %v; want %#v", got, err, want)
	}
	var raw []string
	for _, field := range got {
		raw = append(raw, field.Raw)
	}
	if strings.Join(raw, ",") != input {
		t.Fatal("parser changed the retained raw attribute list")
	}
}

func TestAttributesKeepSyntaxSeparateFromReferencePolicy(t *testing.T) {
	got, err := ParseAttributes(`NAME="",URI="https://source.invalid/a?x=y",BANDWIDTH=1000`)
	if err != nil || len(got) != 3 || got[0].Value != "" || !got[0].Quoted || got[1].Value != "https://source.invalid/a?x=y" {
		t.Fatalf("opaque values changed: %#v, %v", got, err)
	}
}

func TestAttributesRejectAmbiguityWithoutPartialOutput(t *testing.T) {
	for _, input := range []string{
		"", "URI=", "=a", "uri=a", "URI=a,URI=b", "URI=a,", ",URI=a",
		`URI="unterminated`, `NAME="quoted"junk`, `NAME="one\"two"`,
		"URI=a=b", "URI=a b", "URI=a\tb", "URI=a\r", "URI=a\n", "URI=a\x00",
	} {
		t.Run(input, func(t *testing.T) {
			got, err := ParseAttributes(input)
			if err == nil || err.Error() != "invalid HLS attribute list" || got != nil {
				t.Fatalf("ambiguous syntax accepted: %#v, %v", got, err)
			}
		})
	}
}
