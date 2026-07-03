package peertubeimport

import (
	"errors"
	"testing"
)

func TestParseConflictPolicy(t *testing.T) {
	cases := map[string]ConflictPolicy{
		"":       PolicySkip,
		"skip":   PolicySkip,
		"SKIP":   PolicySkip,
		" merge": PolicyMerge,
		"rename": PolicyRename,
		"fail":   PolicyFail,
	}
	for in, want := range cases {
		got, err := ParseConflictPolicy(in)
		if err != nil {
			t.Errorf("ParseConflictPolicy(%q) unexpected error: %v", in, err)
			continue
		}
		if got != want {
			t.Errorf("ParseConflictPolicy(%q) = %q, want %q", in, got, want)
		}
	}
	if _, err := ParseConflictPolicy("bogus"); err == nil {
		t.Error("expected error for invalid policy")
	}
}

func TestDedupeName(t *testing.T) {
	// "alice" and "alice-2" taken → next is "alice-3".
	taken := map[string]bool{"alice": true, "alice-2": true}
	got, err := dedupeName("alice", func(c string) (bool, error) { return taken[c], nil })
	if err != nil {
		t.Fatal(err)
	}
	if got != "alice-3" {
		t.Errorf("dedupeName = %q, want alice-3", got)
	}

	// A free base is returned unchanged.
	got, err = dedupeName("bob", func(c string) (bool, error) { return false, nil })
	if err != nil {
		t.Fatal(err)
	}
	if got != "bob" {
		t.Errorf("dedupeName = %q, want bob", got)
	}

	// A lookup error propagates.
	sentinel := errors.New("boom")
	if _, err := dedupeName("x", func(string) (bool, error) { return false, sentinel }); !errors.Is(err, sentinel) {
		t.Errorf("expected lookup error to propagate, got %v", err)
	}
}
