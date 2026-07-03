package storage

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
)

// Local is a Backend backed by a directory on the local filesystem. It is the
// development/single-node default.
type Local struct {
	root string
}

// NewLocal creates a Local backend rooted at dir, creating it if needed.
func NewLocal(dir string) (*Local, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(abs, 0o750); err != nil {
		return nil, err
	}
	return &Local{root: abs}, nil
}

// resolve maps an object key to a filesystem path guaranteed to stay within the
// root. Keys are relative, forward-slash paths: empty, absolute, NUL-bearing, or
// any-".."-segment keys are rejected outright by validateKey (rather than
// silently cleaned) so the contract is predictable and backend-independent. The
// final Rel check is the hard belt-and-braces guarantee that nothing escapes
// the root.
func (l *Local) resolve(key string) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	full := filepath.Join(l.root, filepath.FromSlash(key))
	rel, err := filepath.Rel(l.root, full)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "", ErrInvalidKey
	}
	return full, nil
}

// Path returns the resolved local filesystem path for key, implementing
// storage.PathProvider. It applies the same traversal-safe resolution as the
// read/write methods and does not require the object to exist.
func (l *Local) Path(key string) (string, error) {
	return l.resolve(key)
}

// Put writes r to the object at key, creating parent directories.
func (l *Local) Put(_ context.Context, key string, r io.Reader) (int64, error) {
	full, err := l.resolve(key)
	if err != nil {
		return 0, err
	}
	if err := os.MkdirAll(filepath.Dir(full), 0o750); err != nil {
		return 0, err
	}
	f, err := os.OpenFile(full, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return 0, err
	}
	n, copyErr := io.Copy(f, r)
	closeErr := f.Close()
	if copyErr != nil {
		_ = os.Remove(full) // don't leave a partial object
		return 0, copyErr
	}
	if closeErr != nil {
		return 0, closeErr
	}
	return n, nil
}

// Open returns a reader for the object at key, or ErrNotFound.
func (l *Local) Open(_ context.Context, key string) (io.ReadCloser, error) {
	full, err := l.resolve(key)
	if err != nil {
		return nil, err
	}
	f, err := os.Open(full)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return f, nil
}

// Delete removes the object at key; missing objects are not an error.
func (l *Local) Delete(_ context.Context, key string) error {
	full, err := l.resolve(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// DeletePrefix removes the whole "directory" at key (all objects under it),
// implementing storage.PrefixDeleter. Missing prefixes are not an error. The
// same traversal-safe resolution as every other method applies, so a hostile
// prefix can never escape the root.
func (l *Local) DeletePrefix(_ context.Context, prefix string) error {
	full, err := l.resolve(prefix)
	if err != nil {
		return err
	}
	return os.RemoveAll(full)
}

// ListKeys returns every object key stored under prefix (recursively), as
// forward-slash keys relative to the root, implementing storage.ObjectLister. A
// missing prefix directory returns no keys (not an error). The same
// traversal-safe resolution as every other method applies.
func (l *Local) ListKeys(_ context.Context, prefix string) ([]string, error) {
	full, err := l.resolve(prefix)
	if err != nil {
		return nil, err
	}
	var keys []string
	err = filepath.WalkDir(full, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return nil // missing prefix: no keys
			}
			return werr
		}
		if d.IsDir() {
			return nil
		}
		rel, rerr := filepath.Rel(l.root, p)
		if rerr != nil {
			return rerr
		}
		keys = append(keys, filepath.ToSlash(rel))
		return nil
	})
	if err != nil {
		return nil, err
	}
	return keys, nil
}

// Exists reports whether an object is stored at key.
func (l *Local) Exists(_ context.Context, key string) (bool, error) {
	full, err := l.resolve(key)
	if err != nil {
		return false, err
	}
	if _, err := os.Stat(full); err != nil {
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	return true, nil
}
