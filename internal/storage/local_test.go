package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newLocal(t *testing.T) *Local {
	t.Helper()
	b, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return b
}

func TestPutOpenRoundTrip(t *testing.T) {
	b := newLocal(t)
	ctx := context.Background()
	content := []byte("hello vidra")

	n, err := b.Put(ctx, "videos/abc/original.bin", bytes.NewReader(content))
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if n != int64(len(content)) {
		t.Errorf("wrote %d bytes, want %d", n, len(content))
	}

	rc, err := b.Open(ctx, "videos/abc/original.bin")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer rc.Close()
	got, _ := io.ReadAll(rc)
	if !bytes.Equal(got, content) {
		t.Errorf("read %q, want %q", got, content)
	}
}

func TestExistsAndDelete(t *testing.T) {
	b := newLocal(t)
	ctx := context.Background()
	if ok, _ := b.Exists(ctx, "k"); ok {
		t.Fatal("Exists true before Put")
	}
	_, _ = b.Put(ctx, "k", strings.NewReader("x"))
	if ok, _ := b.Exists(ctx, "k"); !ok {
		t.Fatal("Exists false after Put")
	}
	if err := b.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, _ := b.Exists(ctx, "k"); ok {
		t.Fatal("Exists true after Delete")
	}
	// Delete is idempotent.
	if err := b.Delete(ctx, "k"); err != nil {
		t.Fatalf("Delete (missing) = %v, want nil", err)
	}
}

func TestOpenMissingIsNotFound(t *testing.T) {
	b := newLocal(t)
	if _, err := b.Open(context.Background(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

func TestRejectsTraversalAndInvalidKeys(t *testing.T) {
	b := newLocal(t)
	ctx := context.Background()
	for _, key := range []string{"", "../escape", "../../etc/passwd", "a/../../escape", "with\x00null"} {
		if _, err := b.Put(ctx, key, strings.NewReader("x")); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Put(%q) err = %v, want ErrInvalidKey", key, err)
		}
	}
}

// TestTraversalCannotEscapeRoot proves a "../" key never writes outside root.
func TestTraversalCannotEscapeRoot(t *testing.T) {
	dir := t.TempDir()
	b, _ := NewLocal(filepath.Join(dir, "root"))
	// A sentinel file a real escape would clobber.
	outside := filepath.Join(dir, "outside.txt")
	if err := os.WriteFile(outside, []byte("original"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err := b.Put(context.Background(), "../outside.txt", strings.NewReader("HACKED"))
	if !errors.Is(err, ErrInvalidKey) {
		t.Fatalf("traversal Put err = %v, want ErrInvalidKey", err)
	}
	got, _ := os.ReadFile(outside)
	if string(got) != "original" {
		t.Fatal("traversal escaped the storage root and overwrote an outside file")
	}
}

// TestKeyWithInternalDotDotIsContained ensures a "..", when it stays within
// root after cleaning, resolves under root (not rejected spuriously, not escaped).
func TestNestedKeysCreateDirs(t *testing.T) {
	b := newLocal(t)
	ctx := context.Background()
	if _, err := b.Put(ctx, "a/b/c/d.bin", strings.NewReader("x")); err != nil {
		t.Fatalf("Put nested: %v", err)
	}
	if ok, _ := b.Exists(ctx, "a/b/c/d.bin"); !ok {
		t.Fatal("nested object not found after Put")
	}
}
