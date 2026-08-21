package storage

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// TestLocalDescribeIsTheAbsoluteRoot: the identity string is what a migration
// campaign remembers a store by and compares its live handles against, so it has
// to be stable and canonical rather than whatever spelling the config used.
func TestLocalDescribeIsTheAbsoluteRoot(t *testing.T) {
	dir := t.TempDir()
	b := mustLocal(t, dir)
	abs, err := filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := b.Describe(), "local:"+abs; got != want {
		t.Errorf("Describe() = %q, want %q", got, want)
	}
	if got := Describe(Backend(b)); got != "local:"+abs {
		t.Errorf("Describe(Backend) = %q", got)
	}
}

// TestDescribeIsEmptyForABackendThatCannotSayWhoItIs. Callers must read "" as
// "identity unknown" and refuse anything destructive, so the helper has to be
// total rather than panicking or guessing.
func TestDescribeIsEmptyForABackendThatCannotSayWhoItIs(t *testing.T) {
	if got := Describe(brokenBackend{}); got != "" {
		t.Errorf("Describe(unknown) = %q, want empty", got)
	}
	if got := Describe(nil); got != "" {
		t.Errorf("Describe(nil) = %q, want empty", got)
	}
}

// TestLocalListAllKeysCoversTheWholeStore is the difference between RootLister
// and ObjectLister that the migration depends on: a move that only walked the
// six GC-swept prefixes would leave avatars, banners and the ownership marker
// behind in a store the operator is about to decommission.
func TestLocalListAllKeysCoversTheWholeStore(t *testing.T) {
	ctx := context.Background()
	b := mustLocal(t, t.TempDir())
	want := []string{
		OwnerMarkerKey,
		"avatars/u1.png",
		"streaming-playlists/v1/r0/master.m3u8",
		"thumbnails/v1.jpg",
		"web-videos/v1.mp4",
	}
	for _, key := range want {
		if _, err := b.Put(ctx, key, strings.NewReader(key)); err != nil {
			t.Fatalf("Put %q: %v", key, err)
		}
	}
	got, err := b.ListAllKeys(ctx)
	if err != nil {
		t.Fatalf("ListAllKeys: %v", err)
	}
	sort.Strings(got)
	sort.Strings(want)
	if len(got) != len(want) {
		t.Fatalf("ListAllKeys = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("ListAllKeys[%d] = %q, want %q", i, got[i], want[i])
		}
	}

	// ObjectLister cannot answer this question at all: an empty prefix is an
	// invalid key, not a wildcard. That is precisely why RootLister exists.
	if _, err := b.ListKeys(ctx, ""); err == nil {
		t.Error("ListKeys(\"\") returned no error; the empty prefix must stay invalid so nothing mistakes it for a wildcard")
	}
}

// TestLocalListAllKeysOnAnEmptyStore returns nothing rather than erroring: a
// brand-new destination bucket is the normal case.
func TestLocalListAllKeysOnAnEmptyStore(t *testing.T) {
	keys, err := mustLocal(t, t.TempDir()).ListAllKeys(context.Background())
	if err != nil {
		t.Fatalf("ListAllKeys: %v", err)
	}
	if len(keys) != 0 {
		t.Errorf("ListAllKeys on an empty store = %v", keys)
	}
}
