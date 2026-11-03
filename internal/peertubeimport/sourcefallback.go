package peertubeimport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"strings"

	"github.com/vidra/vidra-core/internal/storage"
)

var errSourceReadOnly = errors.New("peertubeimport: source storage is read-only")

// sourceLocalFallback is source-only: unlike storage.Fallback, neither store
// can be mutated through it. It claims no path, listing or presign capability.
type sourceLocalFallback struct {
	primary storage.Backend
	root    string
}

func newSourceLocalFallback(primary storage.Backend, root string) (*sourceLocalFallback, error) {
	dir, err := os.OpenRoot(root)
	if err != nil {
		return nil, fmt.Errorf("peertubeimport: open local source root: %w", err)
	}
	if err := dir.Close(); err != nil {
		return nil, err
	}
	return &sourceLocalFallback{primary: primary, root: root}, nil
}

func (s *sourceLocalFallback) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	rc, err := s.primary.Open(ctx, key)
	if !errors.Is(err, storage.ErrNotFound) {
		return rc, err
	}
	return s.openLocal(ctx, key)
}

func (s *sourceLocalFallback) openLocal(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if !fs.ValidPath(key) || strings.ContainsRune(key, 0) {
		return nil, storage.ErrInvalidKey
	}
	// OpenInRoot confines symlink resolution too, and never creates a directory.
	f, err := os.OpenInRoot(s.root, key)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, storage.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err == nil && !info.Mode().IsRegular() {
		err = errors.New("peertubeimport: local source is not a regular file")
	}
	if err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (s *sourceLocalFallback) Put(context.Context, string, io.Reader) (int64, error) {
	return 0, errSourceReadOnly
}

func (s *sourceLocalFallback) Delete(context.Context, string) error { return errSourceReadOnly }

func (s *sourceLocalFallback) Exists(ctx context.Context, key string) (bool, error) {
	rc, err := s.Open(ctx, key)
	if errors.Is(err, storage.ErrNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, rc.Close()
}

// The copy accelerator needs the actual S3 identity, not a claim that a local
// fallback object lives in S3. Missing objects are handled separately per key.
func sourceCopyPrimary(source storage.Backend) storage.Backend {
	if mixed, ok := source.(*sourceLocalFallback); ok {
		return mixed.primary
	}
	return source
}
