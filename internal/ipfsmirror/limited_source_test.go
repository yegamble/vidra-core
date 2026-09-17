package ipfsmirror

import (
	"context"
	"errors"
	"io"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

func TestLimitedSourceFreezesInventoryAndRejectsWrites(t *testing.T) {
	ctx := context.Background()
	base, _ := storage.NewLocal(t.TempDir())
	for key, data := range map[string]string{"tree/a": "abc", "tree/b": "de", "tree/new": "secret"} {
		if _, err := base.Put(ctx, key, strings.NewReader(data)); err != nil {
			t.Fatal(err)
		}
	}
	inventory := map[string]int64{"tree/a": 3, "tree/b": 2}
	var progress atomic.Int64
	source, err := newLimitedSource(base, inventory, 1000000, 5, &progress)
	if err != nil {
		t.Fatal(err)
	}
	inventory["tree/new"] = 6
	keys, err := source.(storage.ObjectLister).ListKeys(ctx, "tree/")
	if err != nil || !reflect.DeepEqual(keys, []string{"tree/a", "tree/b"}) {
		t.Fatalf("inventory=%v %v", keys, err)
	}
	for _, key := range keys {
		r, err := source.Open(ctx, key)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.ReadAll(r); err != nil {
			t.Fatal(err)
		}
		r.Close()
	}
	if progress.Load() != 5 {
		t.Fatalf("progress=%d", progress.Load())
	}
	if _, err := source.Open(ctx, "tree/new"); err == nil {
		t.Fatal("opened unreserved object")
	}
	if _, err := source.Open(ctx, "tree/a"); err == nil {
		t.Fatal("read same reservation twice")
	}
	if _, err := source.Put(ctx, "tree/a", strings.NewReader("bad")); err == nil {
		t.Fatal("write allowed")
	}
	if err := source.Delete(ctx, "tree/a"); err == nil {
		t.Fatal("delete allowed")
	}
}

func TestLimitedSourceRejectsInvalidBudgetAndChangedLength(t *testing.T) {
	base, _ := storage.NewLocal(t.TempDir())
	ctx := context.Background()
	for _, inventory := range []map[string]int64{{"a": -1}, {"a": 11}, {"../a": 1}, {"a/../b": 1}, {"/a": 1}} {
		if _, err := newLimitedSource(base, inventory, 1000, 10, nil); err == nil {
			t.Fatal("invalid inventory accepted")
		}
	}
	for _, tc := range []struct {
		name, data string
		size       int64
	}{{"short", "ab", 3}, {"grown", "abcd", 3}, {"empty grew", "a", 0}} {
		t.Run(tc.name, func(t *testing.T) {
			base.Put(ctx, "a", strings.NewReader(tc.data))
			source, err := newLimitedSource(base, map[string]int64{"a": tc.size}, 1000000, 10, nil)
			if err != nil {
				t.Fatal(err)
			}
			r, err := source.Open(ctx, "a")
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			got, err := io.ReadAll(r)
			if err == nil || int64(len(got)) > tc.size {
				t.Fatalf("changed source accepted: %q %v", got, err)
			}
		})
	}
}

func TestLimitedSourcePacesAcrossFilesAndCancelsWait(t *testing.T) {
	base, _ := storage.NewLocal(t.TempDir())
	ctx := context.Background()
	base.Put(ctx, "a", strings.NewReader(strings.Repeat("x", 100)))
	base.Put(ctx, "b", strings.NewReader(strings.Repeat("x", 100)))
	source, err := newLimitedSource(base, map[string]int64{"a": 100, "b": 100}, 1000, 200, nil)
	if err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	var wg sync.WaitGroup
	for _, key := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := source.Open(ctx, key)
			if err != nil {
				t.Error(err)
				return
			}
			defer r.Close()
			if _, err := io.ReadAll(r); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if time.Since(started) < 180*time.Millisecond {
		t.Fatal("parallel files bypassed shared byte rate")
	}
	source, err = newLimitedSource(base, map[string]int64{"a": 100}, 1, 100, nil)
	if err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer cancel()
	r, err := source.Open(canceled, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := io.ReadAll(r); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("wait ignored cancellation: %v", err)
	}
}

type blockedSource struct {
	storage.Backend
	reader *blockedReadCloser
}

func (s blockedSource) Open(context.Context, string) (io.ReadCloser, error) { return s.reader, nil }

type blockedReadCloser struct {
	once   sync.Once
	closed chan struct{}
}

func (r *blockedReadCloser) Read([]byte) (int, error) { <-r.closed; return 0, io.ErrClosedPipe }
func (r *blockedReadCloser) Close() error             { r.once.Do(func() { close(r.closed) }); return nil }
func TestLimitedSourceCancellationClosesBlockedRead(t *testing.T) {
	base := blockedSource{reader: &blockedReadCloser{closed: make(chan struct{})}}
	source, err := newLimitedSource(base, map[string]int64{"a": 1}, 1000000, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	r, err := source.Open(ctx, "a")
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	done := make(chan error, 1)
	go func() { _, err := io.ReadAll(r); done <- err }()
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("blocked source not closed")
	}
}
