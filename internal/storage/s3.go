package storage

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// S3Config configures the S3-compatible backend. It works against MinIO, AWS
// S3, Backblaze B2, and DigitalOcean Spaces (all speak the S3 API):
//
//   - MinIO (dev, compose "storage" profile): Endpoint "minio:9000" (or
//     "localhost:9000" from the host), UseSSL=false, ForcePathStyle=true.
//   - AWS S3: Endpoint "s3.<region>.amazonaws.com", Region set, UseSSL=true.
//   - Backblaze B2: Endpoint "s3.<region>.backblazeb2.com" (e.g.
//     s3.us-west-004.backblazeb2.com), the key id/application key as
//     AccessKey/SecretKey, UseSSL=true. B2 supports virtual-host style, but
//     ForcePathStyle=true also works and avoids bucket-name DNS constraints.
//   - DigitalOcean Spaces: Endpoint "<region>.digitaloceanspaces.com" (bucket
//     NOT in the endpoint), Region "<region>", UseSSL=true.
//
// SecretKey (and AccessKey) are credentials: they must never be logged, traced,
// or echoed in errors.
type S3Config struct {
	// Endpoint is the S3 API host[:port] WITHOUT a scheme (UseSSL selects
	// http/https), e.g. "minio:9000" or "s3.us-west-004.backblazeb2.com".
	Endpoint string
	// Bucket is the bucket all objects live in. Keys map 1:1 to object names.
	Bucket string
	// AccessKey / SecretKey are the S3 credentials. Never logged.
	AccessKey string
	SecretKey string
	// Region is the bucket region. Optional for MinIO; required by some
	// providers (AWS, DO Spaces) when creating buckets.
	Region string
	// UseSSL selects https (true, production default) or plain http (MinIO dev).
	UseSSL bool
	// ForcePathStyle addresses the bucket as /<bucket>/<key> instead of
	// <bucket>.<endpoint>. Required by MinIO and safe for most S3-compatibles.
	ForcePathStyle bool
}

// S3 is a Backend stored in an S3-compatible object store via the MinIO Go SDK.
// It deliberately does NOT implement PathProvider — tools that need a local
// file (ffprobe/ffmpeg, clamav) use the temp-download fallback
// (internal/media.objectPath), and HTTP serving gets Range support through the
// seekable reader Open returns (a *minio.Object seeks via ranged GETs).
type S3 struct {
	client *minio.Client
	bucket string
	region string
}

// compile-time interface checks: S3 is a Backend and must never silently
// become a PathProvider (callers rely on the temp-download fallback instead).
var _ Backend = (*S3)(nil)

// NewS3 validates cfg and builds the client. No network calls are made here;
// use EnsureBucket at startup to fail fast on unreachable/missing buckets.
func NewS3(cfg S3Config) (*S3, error) {
	if strings.TrimSpace(cfg.Endpoint) == "" {
		return nil, fmt.Errorf("storage: s3: endpoint is required")
	}
	if strings.Contains(cfg.Endpoint, "://") {
		return nil, fmt.Errorf("storage: s3: endpoint must be host[:port] without a scheme (use the UseSSL flag)")
	}
	if strings.TrimSpace(cfg.Bucket) == "" {
		return nil, fmt.Errorf("storage: s3: bucket is required")
	}
	if strings.TrimSpace(cfg.AccessKey) == "" || strings.TrimSpace(cfg.SecretKey) == "" {
		return nil, fmt.Errorf("storage: s3: access key and secret key are required")
	}
	lookup := minio.BucketLookupAuto
	if cfg.ForcePathStyle {
		lookup = minio.BucketLookupPath
	}
	client, err := minio.New(cfg.Endpoint, &minio.Options{
		Creds:        credentials.NewStaticV4(cfg.AccessKey, cfg.SecretKey, ""),
		Secure:       cfg.UseSSL,
		Region:       cfg.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: s3: build client: %w", err)
	}
	return &S3{client: client, bucket: cfg.Bucket, region: cfg.Region}, nil
}

// EnsureBucket verifies the configured bucket exists, creating it when absent
// (dev convenience for MinIO; production buckets are usually pre-provisioned).
// Call once at startup so a misconfigured store fails fast.
func (s *S3) EnsureBucket(ctx context.Context) error {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return fmt.Errorf("storage: s3: check bucket %q: %w", s.bucket, err)
	}
	if ok {
		return nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
		// Lost a create race (or the credentials may not create but the bucket
		// now exists) — re-check before failing.
		if ok2, err2 := s.client.BucketExists(ctx, s.bucket); err2 == nil && ok2 {
			return nil
		}
		return fmt.Errorf("storage: s3: create bucket %q: %w", s.bucket, err)
	}
	return nil
}

// validateKey enforces the Backend key contract for backends that don't
// resolve keys to filesystem paths: keys are non-empty, relative, forward-slash
// object paths — empty, absolute, NUL-bearing, or any-".."-segment keys are
// rejected with ErrInvalidKey. Object stores can't traverse, but keeping the
// exact same rejection rules as Local keeps behavior backend-independent.
func validateKey(key string) error {
	if key == "" || strings.ContainsRune(key, 0) || strings.HasPrefix(key, "/") {
		return ErrInvalidKey
	}
	for _, seg := range strings.Split(key, "/") {
		if seg == ".." {
			return ErrInvalidKey
		}
	}
	return nil
}

// Put streams r to the object at key, overwriting any existing object.
func (s *S3) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	if err := validateKey(key); err != nil {
		return 0, err
	}
	// Size -1 = unknown; the SDK streams in multipart chunks so large originals
	// never need to be buffered wholly in memory.
	info, err := s.client.PutObject(ctx, s.bucket, key, r, -1, minio.PutObjectOptions{})
	if err != nil {
		return 0, fmt.Errorf("storage: s3: put %q: %w", key, err)
	}
	return info.Size, nil
}

// Open returns a reader for the object at key, or ErrNotFound. The returned
// reader is an io.ReadSeeker (seeks translate to ranged GETs), so callers like
// http.ServeContent get Range/206 support without a local file.
func (s *S3) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	if err := validateKey(key); err != nil {
		return nil, err
	}
	obj, err := s.client.GetObject(ctx, s.bucket, key, minio.GetObjectOptions{})
	if err != nil {
		return nil, fmt.Errorf("storage: s3: open %q: %w", key, err)
	}
	// GetObject is lazy — force the first request so a missing object surfaces
	// here as ErrNotFound (matching Local) rather than on the first Read.
	if _, err := obj.Stat(); err != nil {
		_ = obj.Close()
		if isS3NotFound(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: s3: open %q: %w", key, err)
	}
	return obj, nil
}

// Delete removes the object at key; missing objects are not an error.
func (s *S3) Delete(ctx context.Context, key string) error {
	if err := validateKey(key); err != nil {
		return err
	}
	if err := s.client.RemoveObject(ctx, s.bucket, key, minio.RemoveObjectOptions{}); err != nil {
		if isS3NotFound(err) {
			return nil
		}
		return fmt.Errorf("storage: s3: delete %q: %w", key, err)
	}
	return nil
}

// Exists reports whether an object is stored at key.
func (s *S3) Exists(ctx context.Context, key string) (bool, error) {
	if err := validateKey(key); err != nil {
		return false, err
	}
	if _, err := s.client.StatObject(ctx, s.bucket, key, minio.StatObjectOptions{}); err != nil {
		if isS3NotFound(err) {
			return false, nil
		}
		return false, fmt.Errorf("storage: s3: stat %q: %w", key, err)
	}
	return true, nil
}

// isS3NotFound reports whether err is the S3 "object does not exist" error.
func isS3NotFound(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == minio.NoSuchKey || resp.StatusCode == http.StatusNotFound
}
