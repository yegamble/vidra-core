package storage

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
)

func mustLocal(t *testing.T, dir string) *Local {
	t.Helper()
	b, err := NewLocal(dir)
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	return b
}

func readAll(t *testing.T, b Backend, key string) string {
	t.Helper()
	rc, err := b.Open(context.Background(), key)
	if err != nil {
		t.Fatalf("Open %q: %v", key, err)
	}
	defer func() { _ = rc.Close() }()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("read %q: %v", key, err)
	}
	return string(data)
}

// TestFallbackServesFromEitherStore is the dual-read property the migration
// window depends on: while a move is in flight, some objects are only in the
// store the instance came FROM, and a viewer must not be able to tell.
func TestFallbackServesFromEitherStore(t *testing.T) {
	ctx := context.Background()
	primary := mustLocal(t, filepath.Join(t.TempDir(), "new"))
	secondary := mustLocal(t, filepath.Join(t.TempDir(), "old"))

	if _, err := primary.Put(ctx, "web-videos/copied.mp4", strings.NewReader("in the new store")); err != nil {
		t.Fatal(err)
	}
	if _, err := secondary.Put(ctx, "web-videos/not-yet.mp4", strings.NewReader("only in the old store")); err != nil {
		t.Fatal(err)
	}

	f := NewFallback(primary, secondary, nil)
	if got := readAll(t, f, "web-videos/copied.mp4"); got != "in the new store" {
		t.Errorf("primary read = %q", got)
	}
	if got := readAll(t, f, "web-videos/not-yet.mp4"); got != "only in the old store" {
		t.Errorf("fallback read = %q", got)
	}
	for _, key := range []string{"web-videos/copied.mp4", "web-videos/not-yet.mp4"} {
		ok, err := f.Exists(ctx, key)
		if err != nil || !ok {
			t.Errorf("Exists(%q) = %v, %v; want true, nil", key, ok, err)
		}
	}
	if _, err := f.Open(ctx, "web-videos/nowhere.mp4"); !errors.Is(err, ErrNotFound) {
		t.Errorf("missing-everywhere Open err = %v, want ErrNotFound", err)
	}
	if ok, err := f.Exists(ctx, "web-videos/nowhere.mp4"); ok || err != nil {
		t.Errorf("Exists(missing) = %v, %v; want false, nil", ok, err)
	}
}

// TestFallbackWritesOnlyToPrimaryAndDeletesBoth pins the asymmetry: new bytes
// belong to the store the instance serves from, but a DELETE has to reach the
// other copy too or the fallback read would resurrect deleted media.
func TestFallbackWritesOnlyToPrimaryAndDeletesBoth(t *testing.T) {
	ctx := context.Background()
	primary := mustLocal(t, filepath.Join(t.TempDir(), "new"))
	secondary := mustLocal(t, filepath.Join(t.TempDir(), "old"))
	f := NewFallback(primary, secondary, nil)

	if _, err := f.Put(ctx, "thumbnails/a.jpg", strings.NewReader("fresh")); err != nil {
		t.Fatal(err)
	}
	if ok, _ := primary.Exists(ctx, "thumbnails/a.jpg"); !ok {
		t.Error("Put did not reach the primary")
	}
	if ok, _ := secondary.Exists(ctx, "thumbnails/a.jpg"); ok {
		t.Error("Put reached the secondary; new bytes must not be written into the store being migrated away from")
	}

	// A stale copy in the secondary is exactly what makes the mirrored delete
	// necessary: without it, Open would keep serving the deleted object.
	if _, err := secondary.Put(ctx, "thumbnails/a.jpg", strings.NewReader("stale")); err != nil {
		t.Fatal(err)
	}
	if err := f.Delete(ctx, "thumbnails/a.jpg"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if ok, _ := secondary.Exists(ctx, "thumbnails/a.jpg"); ok {
		t.Error("Delete left the counterpart copy behind; the fallback read would resurrect it")
	}
	if _, err := f.Open(ctx, "thumbnails/a.jpg"); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted object still readable: %v", err)
	}
}

// TestFallbackDeleteSurvivesASecondaryFailure: the authoritative delete
// succeeded, so the caller's request must succeed. The campaign's own
// delete-source pass is the durable cleanup.
func TestFallbackDeleteSurvivesASecondaryFailure(t *testing.T) {
	ctx := context.Background()
	primary := mustLocal(t, t.TempDir())
	if _, err := primary.Put(ctx, "thumbnails/a.jpg", strings.NewReader("x")); err != nil {
		t.Fatal(err)
	}
	f := NewFallback(primary, brokenBackend{}, nil)
	if err := f.Delete(ctx, "thumbnails/a.jpg"); err != nil {
		t.Errorf("Delete = %v, want nil: a failing secondary must not fail the request", err)
	}
}

// TestFallbackImplementsNoOptionalCapability is the guard behind the wiring
// rule in cmd/api. Every optional capability is a claim about ONE store, and the
// consumer that most wants listing is media GC — which DELETES what it lists. If
// someone gives Fallback one of these methods, this test goes red before a GC
// sweep enumerates a merged view of two buckets.
func TestFallbackImplementsNoOptionalCapability(t *testing.T) {
	var b Backend = NewFallback(mustLocal(t, t.TempDir()), mustLocal(t, t.TempDir()), nil)
	if _, ok := b.(ObjectLister); ok {
		t.Error("Fallback must not implement ObjectLister: media GC deletes what it lists")
	}
	if _, ok := b.(RootLister); ok {
		t.Error("Fallback must not implement RootLister: a merged enumeration is not one store's contents")
	}
	if _, ok := b.(PrefixDeleter); ok {
		t.Error("Fallback must not implement PrefixDeleter")
	}
	if _, ok := b.(PathProvider); ok {
		t.Error("Fallback must not implement PathProvider: the object may be in the other store")
	}
	if _, ok := b.(Presigner); ok {
		t.Error("Fallback must not implement Presigner: a URL for the wrong store 404s with no fallback left")
	}
	if _, ok := b.(SizedPutter); ok {
		t.Error("Fallback must not implement SizedPutter")
	}
	if _, ok := b.(Describer); ok {
		t.Error("Fallback must not implement Describer: a pair of stores has no single identity")
	}
}

// TestFallbackPropagatesRealPrimaryErrors: only a definite "not there" may fall
// through. A timeout or a permission problem must surface, or a broken primary
// hides behind stale copies in the store being decommissioned.
func TestFallbackPropagatesRealPrimaryErrors(t *testing.T) {
	ctx := context.Background()
	secondary := mustLocal(t, t.TempDir())
	if _, err := secondary.Put(ctx, "thumbnails/a.jpg", strings.NewReader("stale")); err != nil {
		t.Fatal(err)
	}
	f := NewFallback(brokenBackend{}, secondary, nil)
	if _, err := f.Open(ctx, "thumbnails/a.jpg"); !errors.Is(err, errBroken) {
		t.Errorf("Open err = %v, want the primary's error", err)
	}
	if _, err := f.Exists(ctx, "thumbnails/a.jpg"); !errors.Is(err, errBroken) {
		t.Errorf("Exists err = %v, want the primary's error", err)
	}
}

var errBroken = errors.New("storage: test backend is broken")

// brokenBackend fails every call with a non-ErrNotFound error.
type brokenBackend struct{}

func (brokenBackend) Put(context.Context, string, io.Reader) (int64, error) { return 0, errBroken }
func (brokenBackend) Open(context.Context, string) (io.ReadCloser, error)   { return nil, errBroken }
func (brokenBackend) Delete(context.Context, string) error                  { return errBroken }
func (brokenBackend) Exists(context.Context, string) (bool, error)          { return false, errBroken }
