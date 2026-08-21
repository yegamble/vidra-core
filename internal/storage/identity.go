package storage

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/minio/minio-go/v7"
)

// Describer is an optional capability implemented by backends that can state
// WHICH store they are, in a short, stable, credential-free string.
//
// It exists for storage migration, which has to survive an operator swapping
// STORAGE_* environment variables and restarting the process: the campaign
// remembers the two stores by these strings, and every copy or delete is refused
// unless the handles the process is holding still describe the stores the
// campaign expects. Without that check, a swapped environment turns "copy source
// to target" into "copy target back over source", which is not a bug an operator
// gets to notice before the data is gone.
//
// The string is an IDENTITY, not a URL: nothing dials it, and it must never
// carry an access key, a secret, or a signature — it is written to the database,
// returned by an admin endpoint, and logged.
type Describer interface {
	// Describe returns this backend's identity string, e.g.
	// "s3://minio:9000/vidra-media" or "local:/var/lib/vidra/media".
	Describe() string
}

// Describe returns b's identity string, or "" for a backend that cannot state
// one. Callers must treat "" as "identity unknown" and refuse anything
// destructive — an unanswerable question about which store this is has exactly
// one safe answer.
func Describe(b Backend) string {
	if b == nil {
		return ""
	}
	if d, ok := b.(Describer); ok {
		return d.Describe()
	}
	return ""
}

// Describe implements Describer for the local backend: the absolute root, which
// is the whole of a local store's identity.
func (l *Local) Describe() string { return "local:" + l.root }

// Describe implements Describer for the S3 backend. Endpoint and bucket only —
// the access key and secret are what authorise use of that store, never what
// names it, and this value is persisted and displayed.
func (s *S3) Describe() string {
	return "s3://" + s.client.EndpointURL().Host + "/" + s.bucket
}

// RootLister is an optional capability implemented by backends that can
// enumerate EVERY object they hold, not merely those under one prefix.
//
// It is deliberately separate from ObjectLister. Prefix listing serves media
// garbage collection, which must only ever look at a fixed set of KNOWN prefixes
// — listing the whole store is precisely the thing that sweep is forbidden to
// do. Storage migration needs the opposite: a move that copied only the prefixes
// this release happens to know about would silently leave avatars, banners,
// instance images and message attachments behind in a store the operator is
// about to decommission. Two different questions, two different capabilities.
//
// ObjectLister cannot answer this one, because an empty prefix is an invalid key
// (see validateKey) rather than a wildcard.
type RootLister interface {
	// ListAllKeys returns every object key the backend holds, as forward-slash
	// keys relative to its root (i.e. the same keys Put accepts). An empty store
	// returns no keys, not an error.
	ListAllKeys(ctx context.Context) ([]string, error)
}

// ListAllKeys walks the entire storage root, implementing RootLister. Directories
// themselves are not objects and are skipped; everything else — including dotted
// paths such as the ownership marker — is a key, because a migration that
// silently skipped a file would produce a target that is not a copy of the
// source.
func (l *Local) ListAllKeys(_ context.Context) ([]string, error) {
	var keys []string
	err := filepath.WalkDir(l.root, func(p string, d os.DirEntry, werr error) error {
		if werr != nil {
			if os.IsNotExist(werr) {
				return nil
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

// ListAllKeys lists the whole bucket, implementing RootLister.
func (s *S3) ListAllKeys(ctx context.Context) ([]string, error) {
	var keys []string
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{Recursive: true}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("storage: s3: list bucket: %w", obj.Err)
		}
		// A zero-byte "directory marker" some tools write is not an object any
		// caller can Open, and its key would fail validateKey on the target.
		if obj.Key == "" || strings.HasSuffix(obj.Key, "/") {
			continue
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
}
