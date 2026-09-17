package ipfsmirror

import (
	"context"
	"errors"
	"io"
	"path"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/vidra/vidra-core/internal/storage"
)

var errSourceBounds = errors.New("ipfs source exceeds reserved inventory")

// limitedSource is a read-only snapshot for one admitted job. No fresh listing
// or reopened object can spend the same reservation twice. Pacing is shared by
// all its files, so parallel multipart readers cannot multiply the copy rate.
type limitedSource struct {
	base      storage.Backend
	inventory map[string]int64
	rate      int64
	progress  *atomic.Int64
	mu        sync.Mutex
	opened    map[string]bool
	next      time.Time
}

func newLimitedSource(base storage.Backend, inventory map[string]int64, bytesPerSecond, maximumBytes int64, progress *atomic.Int64) (storage.Backend, error) {
	if base == nil || bytesPerSecond <= 0 || maximumBytes < 0 {
		return nil, errSourceBounds
	}
	source := &limitedSource{base: base, inventory: make(map[string]int64, len(inventory)), rate: bytesPerSecond, progress: progress, opened: map[string]bool{}}
	remaining := maximumBytes
	for key, size := range inventory {
		if key == "" || key == "." || strings.HasPrefix(key, "/") || strings.Contains(key, "\\") || path.Clean(key) != key || key == ".." || strings.HasPrefix(key, "../") || size < 0 || size > remaining {
			return nil, errSourceBounds
		}
		source.inventory[key] = size
		remaining -= size
	}
	return source, nil
}
func (s *limitedSource) Put(context.Context, string, io.Reader) (int64, error) {
	return 0, errSourceBounds
}
func (s *limitedSource) Delete(context.Context, string) error { return errSourceBounds }
func (s *limitedSource) Exists(ctx context.Context, key string) (bool, error) {
	_, ok := s.inventory[key]
	return ok, ctx.Err()
}
func (s *limitedSource) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	prefix = strings.TrimSuffix(prefix, "/")
	keys := []string{}
	for key := range s.inventory {
		if key == prefix || strings.HasPrefix(key, prefix+"/") {
			keys = append(keys, key)
		}
	}
	sort.Strings(keys)
	return keys, nil
}
func (s *limitedSource) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	size, ok := s.inventory[key]
	if !ok || s.opened[key] {
		s.mu.Unlock()
		return nil, errSourceBounds
	}
	s.opened[key] = true
	s.mu.Unlock()
	reader, err := s.base.Open(ctx, key)
	if err != nil {
		return nil, err
	}
	r := &limitedReader{source: s, ctx: ctx, reader: reader, remaining: size}
	r.stop = context.AfterFunc(ctx, func() { r.closeUnderlying() })
	return r, nil
}
func (s *limitedSource) wait(ctx context.Context, n int) error {
	s.mu.Lock()
	now := time.Now()
	if s.next.Before(now) {
		s.next = now
	}
	// Each read is capped at32KiB, keeping this multiplication bounded. Round
	// upward rather than granting a free byte when the configured rate is high.
	nanos := int64(n) * int64(time.Second)
	duration := nanos / s.rate
	if nanos%s.rate != 0 {
		duration++
	}
	s.next = s.next.Add(time.Duration(duration))
	delay := time.Until(s.next)
	s.mu.Unlock()
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return ctx.Err()
	}
}

type limitedReader struct {
	source     *limitedSource
	ctx        context.Context
	reader     io.ReadCloser
	remaining  int64
	done       bool
	stop       func() bool
	closeOnce  sync.Once
	closeError error
}

func (r *limitedReader) closeUnderlying() { r.closeOnce.Do(func() { r.closeError = r.reader.Close() }) }
func (r *limitedReader) Close() error     { r.stop(); r.closeUnderlying(); return r.closeError }
func (r *limitedReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	if len(p) == 0 {
		return 0, nil
	}
	if r.done {
		return 0, io.EOF
	}
	if r.remaining == 0 {
		// One bounded lookahead catches a replaced/grown source. Never pass that
		// extra byte to Kubo, which would exceed the admitted content length.
		var extra [1]byte
		n, err := r.reader.Read(extra[:])
		if cancel := r.ctx.Err(); cancel != nil {
			return 0, cancel
		}
		if n != 0 {
			return 0, errSourceBounds
		}
		if err == io.EOF {
			r.done = true
		}
		return 0, err
	}
	length := min(int64(len(p)), r.remaining, 32768)
	if err := r.source.wait(r.ctx, int(length)); err != nil {
		return 0, err
	}
	n, err := r.reader.Read(p[:length])
	r.remaining -= int64(n)
	if r.source.progress != nil {
		r.source.progress.Add(int64(n))
	}
	if cancel := r.ctx.Err(); cancel != nil {
		return n, cancel
	}
	if err == io.EOF {
		if r.remaining != 0 {
			return n, io.ErrUnexpectedEOF
		}
		r.done = true
	}
	return n, err
}
