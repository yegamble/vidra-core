package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Re-rendering a file with its own values must return the exact bytes it was
// parsed from. Everything else in this package rests on that: the generated env
// file is the template plus a handful of changed values, never a reformat.
func TestParseRenderRoundTrip(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("testdata", "template.env.example"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	f := mustParse(t, raw)
	if got := f.Render(f.Values(), nil, nil); string(got) != string(raw) {
		t.Errorf("round-trip changed the file\n--- got ---\n%s--- want ---\n%s", got, raw)
	}
}

func TestParseRecognisesOnlyActiveAssignments(t *testing.T) {
	f := mustParse(t, []byte(strings.Join([]string{
		"# a comment",
		"ACTIVE=1",
		"# COMMENTED=2",       // documentation, not an assignment
		"  INDENTED=3",        // ditto: compose would not see it either
		"WITH_EQUALS=a=b&c=d", // only the first = splits
		"WITH_SPACES=Example Video",
		"EMPTY=",
		"",
		"not an assignment at all",
		"=novalue",
		"LOWER_case_1=ok",
	}, "\n")+"\n"))

	want := []string{"ACTIVE", "WITH_EQUALS", "WITH_SPACES", "EMPTY", "LOWER_case_1"}
	if strings.Join(f.Keys(), ",") != strings.Join(want, ",") {
		t.Errorf("keys = %v, want %v", f.Keys(), want)
	}
	for key, wantValue := range map[string]string{
		"WITH_EQUALS": "a=b&c=d",
		"WITH_SPACES": "Example Video",
		"EMPTY":       "",
	} {
		if got, _ := f.Value(key); got != wantValue {
			t.Errorf("%s = %q, want %q", key, got, wantValue)
		}
	}
	for _, key := range []string{"COMMENTED", "INDENTED"} {
		if f.Has(key) {
			t.Errorf("%s is a comment line and must not be an assignment", key)
		}
	}
}

// This file gets REWRITTEN, so a duplicate is not "last wins" — silently
// dropping one of two conflicting lines is how an operator loses a value.
func TestParseRejectsDuplicateKeys(t *testing.T) {
	_, err := ParseEnvFile([]byte("JWT_SECRET=a\n# ...\nJWT_SECRET=b\n"))
	if err == nil || !strings.Contains(err.Error(), "assigned twice") {
		t.Fatalf("err = %v, want the duplicate to be refused", err)
	}
}

func TestRenderOnlyRewritesChangedLines(t *testing.T) {
	f := mustParse(t, []byte("A=1\n# keep me\nB=2\n"))
	got := f.Render(map[string]string{"A": "1", "B": "changed"}, nil, nil)
	if string(got) != "A=1\n# keep me\nB=changed\n" {
		t.Errorf("got %q", got)
	}
	// A key absent from the values map keeps the value it was parsed with.
	if got := f.Render(map[string]string{}, nil, nil); string(got) != "A=1\n# keep me\nB=2\n" {
		t.Errorf("got %q", got)
	}
}

func TestRenderAppendsCarriedKeysUnderAHeader(t *testing.T) {
	f := mustParse(t, []byte("A=1\n"))
	got := string(f.Render(map[string]string{"A": "1", "B": "2"}, []string{"B"}, nil))
	if !strings.HasPrefix(got, "A=1\n") {
		t.Errorf("template lines were disturbed: %q", got)
	}
	if !strings.Contains(got, "Carried over from the previous env file") || !strings.HasSuffix(got, "\nB=2\n") {
		t.Errorf("carried key not appended under its header: %q", got)
	}
	// Round-trips: what was carried parses back as an ordinary assignment.
	back := mustParse(t, []byte(got))
	if v, ok := back.Value("B"); !ok || v != "2" {
		t.Errorf("carried key did not survive a re-parse: %q %v", v, ok)
	}
}

// The component keys a template does not define get their OWN block, not the
// carried one: they were computed by this run, and a header telling an operator
// they are the previous file's leftovers is an invitation to delete them.
func TestRenderAppendsManagedKeysUnderTheirOwnHeader(t *testing.T) {
	f := mustParse(t, []byte("A=1\n"))
	got := string(f.Render(
		map[string]string{"A": "1", "OLD": "kept", "VIDRA_COMPOSE_PROFILES": "core frontend ipfs"},
		[]string{"OLD"},
		[]string{"VIDRA_COMPOSE_PROFILES"},
	))
	if !strings.HasPrefix(got, "A=1\n") {
		t.Errorf("template lines were disturbed: %q", got)
	}
	if !strings.Contains(got, "Carried over from the previous env file\n") || !strings.Contains(got, "\nOLD=kept\n") {
		t.Errorf("carried block missing: %q", got)
	}
	if !strings.Contains(got, "Component selection (managed by `vidra setup`)") || !strings.HasSuffix(got, "\nVIDRA_COMPOSE_PROFILES=core frontend ipfs\n") {
		t.Errorf("managed block missing or misplaced: %q", got)
	}
	if strings.Index(got, "OLD=kept") > strings.Index(got, "VIDRA_COMPOSE_PROFILES=") {
		t.Errorf("the managed block must come after the carried one: %q", got)
	}
	// Both blocks round-trip as ordinary assignments, which is what makes a
	// re-run see exactly what this one wrote.
	back := mustParse(t, []byte(got))
	for _, tc := range []struct{ key, want string }{{"OLD", "kept"}, {"VIDRA_COMPOSE_PROFILES", "core frontend ipfs"}} {
		if v, ok := back.Value(tc.key); !ok || v != tc.want {
			t.Errorf("%s did not survive a re-parse: %q %v", tc.key, v, ok)
		}
	}
}

func TestParseNormalisesLineEndingsAndEmptyInput(t *testing.T) {
	f := mustParse(t, []byte("A=1\r\n# c\r\n"))
	if v, _ := f.Value("A"); v != "1" {
		t.Errorf("A = %q, want the trailing CR stripped", v)
	}
	if got := string(f.Render(f.Values(), nil, nil)); got != "A=1\n# c\n" {
		t.Errorf("got %q", got)
	}
	empty := mustParse(t, nil)
	if len(empty.Keys()) != 0 || len(empty.Render(nil, nil, nil)) != 0 {
		t.Error("an empty file should parse and render as empty")
	}
	// A file with no trailing newline gains one rather than losing its last line.
	f = mustParse(t, []byte("A=1"))
	if got := string(f.Render(f.Values(), nil, nil)); got != "A=1\n" {
		t.Errorf("got %q", got)
	}
}
