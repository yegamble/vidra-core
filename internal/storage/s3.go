package storage

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"github.com/minio/minio-go/v7/pkg/lifecycle"
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
var _ PrefixDeleter = (*S3)(nil)
var _ PrefixDeleter = (*Local)(nil)
var _ ObjectLister = (*S3)(nil)
var _ ObjectLister = (*Local)(nil)
var _ SizedPutter = (*S3)(nil)
var _ Presigner = (*S3)(nil)

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
//
// created reports whether THIS call made the bucket. That is not bookkeeping: a
// bucket this process just created cannot hold anyone else's objects, which is
// the one case where claiming ownership of a store (see OwnerMarkerKey) needs no
// operator confirmation. A lost create race reports created=false, because the
// winner may not have been us.
func (s *S3) EnsureBucket(ctx context.Context) (created bool, err error) {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return false, fmt.Errorf("storage: s3: check bucket %q: %w", s.bucket, err)
	}
	if ok {
		return false, nil
	}
	if err := s.client.MakeBucket(ctx, s.bucket, minio.MakeBucketOptions{Region: s.region}); err != nil {
		// Lost a create race (or the credentials may not create but the bucket
		// now exists) — re-check before failing.
		if ok2, err2 := s.client.BucketExists(ctx, s.bucket); err2 == nil && ok2 {
			return false, nil
		}
		return false, fmt.Errorf("storage: s3: create bucket %q: %w", s.bucket, err)
	}
	return true, nil
}

// IsEmpty reports whether the bucket holds no objects at all. It is one list
// request capped at a single key — the cheapest question that distinguishes "a
// store nobody has used yet" from "a store with somebody's data in it", which is
// the distinction the ownership marker turns on.
func (s *S3) IsEmpty(ctx context.Context) (bool, error) {
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Recursive: true,
		MaxKeys:   1,
	}) {
		if obj.Err != nil {
			return false, fmt.Errorf("storage: s3: list bucket %q: %w", s.bucket, obj.Err)
		}
		return false, nil
	}
	return true, nil
}

// BucketExists reports whether the configured bucket is there, and is the
// cheapest AUTHENTICATED round trip this client can make: one HeadBucket, no
// object read, no write, and nothing created. `vidra doctor` uses it as the
// object-store reachability probe — EnsureBucket would be the wrong call there,
// because a diagnostic that CREATES a bucket when it cannot find one turns a
// typo in STORAGE_S3_BUCKET into a new, empty, silently-wrong store.
//
// An error means the store could not be asked (DNS, TLS, credentials, region);
// false with no error means the credentials work and the bucket is not there.
func (s *S3) BucketExists(ctx context.Context) (bool, error) {
	ok, err := s.client.BucketExists(ctx, s.bucket)
	if err != nil {
		return false, fmt.Errorf("storage: s3: check bucket %q: %w", s.bucket, err)
	}
	return ok, nil
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

// unknownSizePartSize is the multipart part size used when an object's length
// cannot be determined. It is minio's own minPartSize (16 MiB) and also sits
// comfortably above the 5 MiB floor S3-compatible stores impose on non-final
// parts (Backblaze B2 in particular).
//
// It exists because the SDK's default for an unknown length is catastrophic for
// a media server: OptimalPartInfo(-1, 0) assumes the 5 TiB maximum object,
// divides by the 10,000-part limit, and allocates a 553,648,128-byte (528 MiB)
// buffer — on every call, for every object, however small. Naming an explicit
// part size caps that at 16 MiB.
const unknownSizePartSize = 16 * 1024 * 1024

// Put streams r to the object at key, overwriting any existing object. When r's
// length can be determined without consuming it (the common case in this
// codebase: *os.File from storeTree, *bytes.Reader from generated thumbnails and
// captions) the size is passed through to PutSized, which avoids multipart
// entirely for small objects. Otherwise the upload streams with a bounded part
// size.
func (s *S3) Put(ctx context.Context, key string, r io.Reader) (int64, error) {
	return s.PutSized(ctx, key, r, sniffSize(r))
}

// PutSized streams exactly size bytes from r to the object at key, implementing
// storage.SizedPutter. size must be exact; pass storage.SizeUnknown when it is
// not known, in which case the upload streams in unknownSizePartSize chunks.
func (s *S3) PutSized(ctx context.Context, key string, r io.Reader, size int64) (int64, error) {
	if err := validateKey(key); err != nil {
		return 0, err
	}
	opts := minio.PutObjectOptions{}
	if size < 0 {
		// Normalise any negative value to the SDK's "unknown" sentinel and bound
		// the streaming buffer (see unknownSizePartSize).
		size = -1
		opts.PartSize = unknownSizePartSize
	}
	info, err := s.client.PutObject(ctx, s.bucket, key, r, size, opts)
	if err != nil {
		return 0, fmt.Errorf("storage: s3: put %q: %w", key, err)
	}
	return info.Size, nil
}

// BucketRetention describes what a bucket does with objects that are overwritten
// or deleted. It exists because that answer is not uniform across S3-compatible
// stores and gets the operator billed when they assume it is.
//
// On a versioned bucket a DELETE does not free anything: it writes a delete
// marker (Backblaze calls it a hide marker) and the previous version keeps
// existing and keeps costing money. Every Delete and DeletePrefix this codebase
// performs is that kind of delete — the upload chunks removed after an original
// is assembled, the superseded HLS generation removed before a re-transcode, and
// everything internal/mediagc collects. On such a bucket none of it is reclaimed
// unless a lifecycle rule expires non-current versions.
//
// This matters most on Backblaze B2, whose buckets are versioned BY DEFAULT.
type BucketRetention struct {
	// VersioningEnabled reports whether the bucket retains previous versions.
	// Only meaningful when VersioningKnown is true.
	VersioningEnabled bool
	// VersioningKnown is false when the store did not answer the versioning
	// query at all (not every S3-compatible implements it). An unknown answer is
	// not a failure — it is reported as unknown so an operator is not told
	// something false about their bill.
	VersioningKnown bool
	// NoncurrentExpiryRule is the ID of an ENABLED lifecycle rule that expires
	// non-current versions, or "" when the bucket has none. A rule with a blank
	// ID reports as "(unnamed)" so the caller can still say one exists.
	NoncurrentExpiryRule string
	// LifecycleKnown is false when the store would not answer the lifecycle
	// query. A bucket with no lifecycle configuration at all is KNOWN and empty,
	// which is different from unknown.
	LifecycleKnown bool
}

// ReclaimsOnDelete reports whether a delete against this bucket actually frees
// the bytes. True when versioning is known-off, or known-on with a non-current
// expiry rule. False means deletes accumulate billable hidden versions. The
// second return is false when there was not enough information to say.
func (r BucketRetention) ReclaimsOnDelete() (reclaims, known bool) {
	if !r.VersioningKnown {
		return false, false
	}
	if !r.VersioningEnabled {
		return true, true
	}
	if !r.LifecycleKnown {
		return false, false
	}
	return r.NoncurrentExpiryRule != "", true
}

// Retention reports how the configured bucket treats overwritten and deleted
// objects. It makes two read-only calls and creates nothing. A store that does
// not implement either query is reported as unknown rather than as an error:
// the point is to warn an operator whose bill is quietly growing, not to fail a
// diagnostic on a store that simply answers fewer questions.
func (s *S3) Retention(ctx context.Context) (BucketRetention, error) {
	var out BucketRetention
	if v, err := s.client.GetBucketVersioning(ctx, s.bucket); err == nil {
		out.VersioningKnown = true
		out.VersioningEnabled = v.Enabled()
	}
	cfg, err := s.client.GetBucketLifecycle(ctx, s.bucket)
	switch {
	case err == nil:
		out.LifecycleKnown = true
		out.NoncurrentExpiryRule = noncurrentExpiryRuleID(cfg)
	case isNoLifecycleConfiguration(err):
		// "This bucket has no lifecycle configuration" is a definitive answer,
		// not a failure to answer: the bucket is known to expire nothing.
		out.LifecycleKnown = true
	}
	if !out.VersioningKnown && !out.LifecycleKnown {
		return out, fmt.Errorf("storage: s3: bucket %q answered neither versioning nor lifecycle", s.bucket)
	}
	return out, nil
}

// noncurrentExpiryRuleID returns the ID of the first ENABLED rule that expires
// non-current versions — either after a number of days or by keeping only the N
// newest. A disabled rule is ignored: it is configuration that is not running.
func noncurrentExpiryRuleID(cfg *lifecycle.Configuration) string {
	if cfg == nil {
		return ""
	}
	for _, r := range cfg.Rules {
		if !strings.EqualFold(r.Status, "Enabled") {
			continue
		}
		if r.NoncurrentVersionExpiration.IsDaysNull() && r.NoncurrentVersionExpiration.NewerNoncurrentVersions == 0 {
			continue
		}
		if strings.TrimSpace(r.ID) == "" {
			return "(unnamed)"
		}
		return r.ID
	}
	return ""
}

// isNoLifecycleConfiguration reports the "there is no lifecycle here" response,
// which S3-compatibles return as an error rather than an empty document.
func isNoLifecycleConfiguration(err error) bool {
	resp := minio.ToErrorResponse(err)
	return resp.Code == "NoSuchLifecycleConfiguration" || resp.StatusCode == http.StatusNotFound
}

// PresignGet returns a time-limited URL that serves the object at key by plain
// HTTP GET, implementing storage.Presigner. The returned URL carries a SigV4
// signature and is therefore a credential for that object — callers must not log
// it. Backblaze B2, MinIO, AWS S3 and DigitalOcean Spaces all support presigned
// GETs on the S3 API.
func (s *S3) PresignGet(ctx context.Context, key string, ttl time.Duration) (string, error) {
	if err := validateKey(key); err != nil {
		return "", err
	}
	u, err := s.client.PresignedGetObject(ctx, s.bucket, key, ttl, nil)
	if err != nil {
		// The SDK error can echo the request URL, which for a presign attempt may
		// carry signature material — report the key only.
		return "", fmt.Errorf("storage: s3: presign %q failed", key)
	}
	return u.String(), nil
}

// sniffSize reports how many bytes r will yield, or SizeUnknown when that cannot
// be established without consuming it. Only types whose remaining length is
// exact and free to query are recognised — a wrong answer would fail the upload,
// so anything uncertain must report unknown.
func sniffSize(r io.Reader) int64 {
	switch v := r.(type) {
	case *bytes.Reader:
		return int64(v.Len())
	case *bytes.Buffer:
		return int64(v.Len())
	case *strings.Reader:
		return int64(v.Len())
	case *os.File:
		info, err := v.Stat()
		if err != nil || !info.Mode().IsRegular() {
			// Pipes, devices and sockets report a meaningless size.
			return SizeUnknown
		}
		// The caller may have already read part of the file, so the remaining
		// length is size-minus-offset, not size.
		offset, err := v.Seek(0, io.SeekCurrent)
		if err != nil {
			return SizeUnknown
		}
		if remaining := info.Size() - offset; remaining >= 0 {
			return remaining
		}
		return SizeUnknown
	default:
		return SizeUnknown
	}
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

// DeletePrefix removes every object under prefix (and any object stored at
// exactly prefix, matching Local's RemoveAll semantics), implementing
// storage.PrefixDeleter: a recursive list feeds each key to RemoveObject.
// Missing prefixes are not an error.
func (s *S3) DeletePrefix(ctx context.Context, prefix string) error {
	if err := validateKey(prefix); err != nil {
		return err
	}
	bare := strings.TrimSuffix(prefix, "/")
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    bare + "/", // trailing slash scopes strictly to the "directory"
		Recursive: true,
	}) {
		if obj.Err != nil {
			return fmt.Errorf("storage: s3: list %q: %w", prefix, obj.Err)
		}
		if err := s.client.RemoveObject(ctx, s.bucket, obj.Key, minio.RemoveObjectOptions{}); err != nil && !isS3NotFound(err) {
			return fmt.Errorf("storage: s3: delete %q: %w", obj.Key, err)
		}
	}
	return s.Delete(ctx, bare)
}

// ListKeys returns every object key stored under prefix (recursively),
// implementing storage.ObjectLister. Missing prefixes return no keys. Keys are
// the object names as stored (the same forward-slash keys Put accepts).
func (s *S3) ListKeys(ctx context.Context, prefix string) ([]string, error) {
	if err := validateKey(prefix); err != nil {
		return nil, err
	}
	bare := strings.TrimSuffix(prefix, "/")
	var keys []string
	for obj := range s.client.ListObjects(ctx, s.bucket, minio.ListObjectsOptions{
		Prefix:    bare + "/", // trailing slash scopes strictly to the "directory"
		Recursive: true,
	}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("storage: s3: list %q: %w", prefix, obj.Err)
		}
		keys = append(keys, obj.Key)
	}
	return keys, nil
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
