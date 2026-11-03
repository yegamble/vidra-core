package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vidra/vidra-core/internal/storage"
)

type fallbackSource struct {
	storage.Backend
	err error
}

func (f *fallbackSource) Open(context.Context, string) (io.ReadCloser, error) {
	if f.err != nil {
		return nil, f.err
	}
	return io.NopCloser(strings.NewReader("primary")), nil
}

func TestSourceFallbackReadsOnlyAfterDefiniteAbsence(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "file.mp4"), []byte("local"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, cause := range []error{nil, storage.ErrNotFound, fmt.Errorf("wrapped: %w", storage.ErrNotFound),
		errors.New("403 denied"), errors.New("503 unavailable"), context.Canceled, storage.ErrInvalidKey} {
		t.Run(fmt.Sprint(cause), func(t *testing.T) {
			primary := &fallbackSource{err: cause}
			source, err := newSourceLocalFallback(primary, root)
			if err != nil {
				t.Fatal(err)
			}
			rc, err := source.Open(context.Background(), "file.mp4")
			if cause != nil && !errors.Is(cause, storage.ErrNotFound) {
				if !errors.Is(err, cause) || rc != nil {
					t.Fatalf("primary failure hidden: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			defer rc.Close()
			body, err := io.ReadAll(rc)
			want := "primary"
			if cause != nil {
				want = "local"
			}
			if err != nil || string(body) != want || sourceCopyPrimary(source) != primary {
				t.Fatalf("source route/identity changed: %q %v", body, err)
			}
		})
	}
}

func TestSourceFallbackIsConfinedReadOnlyAndDoesNotCreateRoots(t *testing.T) {
	root := t.TempDir()
	missing := filepath.Join(root, "missing")
	if _, err := newSourceLocalFallback(&fallbackSource{}, missing); err == nil {
		t.Fatal("accepted nonexistent source root")
	}
	if _, err := os.Stat(missing); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("source constructor created a directory")
	}
	source, err := newSourceLocalFallback(&fallbackSource{err: storage.ErrNotFound}, root)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for _, key := range []string{"", "../secret", "/secret", "x\x00y"} {
		if _, err := source.Open(ctx, key); !errors.Is(err, storage.ErrInvalidKey) {
			t.Errorf("unsafe key accepted: %q %v", key, err)
		}
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("outside"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(root, "escape")); err != nil {
		t.Fatal(err)
	}
	if rc, err := source.Open(ctx, "escape"); err == nil || rc != nil {
		t.Fatal("source symlink escaped its root")
	}
	if _, err := source.Open(ctx, "absent"); !errors.Is(err, storage.ErrNotFound) {
		t.Fatal("missing-both sentinel lost")
	}
	if _, err := source.Put(ctx, "new", strings.NewReader("no")); err == nil {
		t.Fatal("source accepted a write")
	}
	if err := source.Delete(ctx, "absent"); err == nil {
		t.Fatal("source accepted a delete")
	}
	if ok, err := source.Exists(ctx, "absent"); ok || err != nil {
		t.Fatalf("missing-both Exists: %v %v", ok, err)
	}
}
