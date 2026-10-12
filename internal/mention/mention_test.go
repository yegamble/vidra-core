package mention

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		body string
		want []Mention
	}{
		{"local", "hi @Alice!", []Mention{{Username: "alice"}}},
		{"remote", "cc @bob@Example.COM please", []Mention{{Username: "bob", Host: "example.com"}}},
		{"start and newline", "@a\n@b", []Mention{{Username: "a"}, {Username: "b"}}},
		{"punctuation before", "(@a) and \"@b\"", []Mention{{Username: "a"}, {Username: "b"}}},
		{"email is not a mention", "mail me at joe@example.com", nil},
		{"url is not a mention", "see https://example.com/@alice and https://x.test/u@alice", nil},
		{"mid-word is not a mention", "foo@bar", nil},
		{"dotted and dashed names", "@a.b-c_d x", []Mention{{Username: "a.b-c_d"}}},
		{"trailing dot is sentence punctuation", "thanks @alice.", []Mention{{Username: "alice"}}},
		{"trailing dot on host", "ask @bob@example.com.", []Mention{{Username: "bob", Host: "example.com"}}},
		{"dedupe case-insensitively", "@Al @al @AL@h.test @al@h.test", []Mention{{Username: "al"}, {Username: "al", Host: "h.test"}}},
		{"lone at", "@ @@ @!", nil},
		{"host needs a dot or is dropped", "@bob@localhost", []Mention{{Username: "bob"}}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := Parse(tc.body); !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("Parse(%q) = %+v, want %+v", tc.body, got, tc.want)
			}
		})
	}
}

func TestParseCapsMentions(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "@user%d ", i)
	}
	got := Parse(b.String())
	if len(got) != MaxPerComment {
		t.Fatalf("got %d mentions, want cap %d", len(got), MaxPerComment)
	}
	if got[0].Username != "user0" {
		t.Fatalf("cap must keep the FIRST mentions, got %+v", got[0])
	}
}

func TestLocalUsernames(t *testing.T) {
	ms := Parse("@a @b@far.test @c@home.test")
	got := LocalUsernames(ms, "Home.Test")
	if !reflect.DeepEqual(got, []string{"a", "c"}) {
		t.Fatalf("got %v", got)
	}
}
