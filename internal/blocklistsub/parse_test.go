package blocklistsub

import (
	"reflect"
	"strings"
	"testing"
)

func TestParseFormats(t *testing.T) {
	cases := []struct {
		name, format, body string
		block              []string
		ignored, invalid   int
	}{
		{"mastodon header + severities", FormatMastodonCSV,
			"#domain,#severity,#reject_media,#reject_reports,#public_comment,#obfuscate\r\nbad.example,suspend,false,false,spam,false\r\nmeh.example,silence,,,,\r\nfine.example,noop\r\n",
			[]string{"bad.example"}, 2, 0},
		{"csv reordered header", FormatMastodonCSV, "severity,domain\nsuspend,a.example\nsuspend,B.Example.\n",
			[]string{"a.example", "b.example"}, 0, 0},
		{"csv no header, BOM, comments, short row, dup", FormatMastodonCSV,
			"\xef\xbb\xbf# a comment\n\nx.example\nx.example,suspend\n,suspend\n",
			[]string{"x.example"}, 0, 1},
		{"csv obfuscated + wildcard + ip + port refused", FormatMastodonCSV,
			"exa***.com,suspend\n*.evil.example,suspend\n10.0.0.1,suspend\nhost.example:8080,suspend\nlocalhost,suspend\nok.example,suspend\n",
			[]string{"ok.example"}, 0, 5},
		{"domain list", FormatDomainList, "# hosts\nOne.example  # trailing\n\ntwo.example\t\nnot a domain line\n-bad-.example\n",
			[]string{"one.example", "two.example"}, 0, 2},
		{"json strings", FormatJSON, `["a.example","B.example","not valid", 7]`, []string{"a.example", "b.example"}, 0, 2},
		{"json mastodon api objects", FormatJSON,
			`[{"domain":"a.example","severity":"suspend","comment":"x"},{"domain":"b.example","severity":"silence"},{"host":"c.example"}]`,
			[]string{"a.example", "c.example"}, 1, 0},
		{"json envelope", FormatJSON, `{"updated":"x","domains":["a.example"]}`, []string{"a.example"}, 0, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := Parse(c.format, []byte(c.body))
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if !reflect.DeepEqual(got.Block, c.block) || got.IgnoredSeverity != c.ignored || got.Invalid != c.invalid {
				t.Errorf("got block=%v ignored=%d invalid=%d; want block=%v ignored=%d invalid=%d",
					got.Block, got.IgnoredSeverity, got.Invalid, c.block, c.ignored, c.invalid)
			}
		})
	}
}

func TestParseRejectsWrongShape(t *testing.T) {
	for _, body := range []string{`<html>nope</html>`, `{"a":1}`, `"str"`, ``, `[`} {
		if _, err := Parse(FormatJSON, []byte(body)); err == nil {
			t.Errorf("json %q: want ErrUnparseable", body)
		}
	}
	if _, err := Parse("yaml", nil); err == nil {
		t.Error("unknown format must error")
	}
}

func TestParseBoundsEntries(t *testing.T) {
	var b strings.Builder
	for i := 0; i < maxEntries+500; i++ {
		b.WriteString("h")
		b.WriteString(strings.Repeat("a", i%5))
		b.WriteString(string(rune('a' + i%26)))
		b.WriteString(".example\n")
	}
	got, err := Parse(FormatDomainList, []byte(b.String()))
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Block) > maxEntries {
		t.Errorf("unbounded: %d", len(got.Block))
	}
}

// FuzzParse: no input may panic, and every returned domain must satisfy the
// strict validator and be unique.
func FuzzParse(f *testing.F) {
	for _, s := range []string{"#domain,#severity\na.example,suspend", "a.example\n", `["a.example"]`, `{"domains":[{"domain":"x.y"}]}`, "\"", ",,,\n\"a", "\xff\xfe"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		for _, format := range []string{FormatMastodonCSV, FormatDomainList, FormatJSON} {
			got, err := Parse(format, []byte(body))
			if err != nil {
				continue
			}
			seen := map[string]bool{}
			for _, d := range got.Block {
				if c, ok := cleanDomain(d); !ok || c != d || seen[d] {
					t.Fatalf("%s: bad/duplicate domain %q", format, d)
				}
				seen[d] = true
			}
		}
	})
}

func TestParseFoldsUnicodeToPunycode(t *testing.T) {
	got, err := Parse(FormatDomainList, []byte("bücher.example\nxn--bcher-kva.example\n"))
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Block, []string{"xn--bcher-kva.example"}) {
		t.Errorf("block = %v, want the single punycode form", got.Block)
	}
}
