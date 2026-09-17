package storage

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocalInventorySizesScopeAndUnknownFiles(t *testing.T) {
	root := t.TempDir()
	b, err := NewLocal(root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for key, data := range map[string]string{"tree/master.m3u8": "manifest", "tree/720/seg.ts": "segment", "tree-other/private": "private"} {
		if _, err := b.Put(ctx, key, strings.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	objects, err := b.ListObjects(ctx, "tree/")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]int64{}
	for _, o := range objects {
		got[o.Key] = o.Size
	}
	if len(got) != 2 || got["tree/master.m3u8"] != 8 || got["tree/720/seg.ts"] != 7 {
		t.Fatalf("wrong inventory: %+v", got)
	}
	if _, err := b.ListObjects(ctx, "../tree"); err == nil {
		t.Fatal("traversal accepted")
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := b.ListObjects(canceled, "tree"); err == nil {
		t.Fatal("canceled inventory continued")
	}
	if err := os.Symlink(filepath.Join(root, "tree-other/private"), filepath.Join(root, "tree/unknown")); err != nil {
		t.Fatal(err)
	}
	if _, err := b.ListObjects(ctx, "tree"); err == nil {
		t.Fatal("nonregular file given authoritative size")
	}
}
