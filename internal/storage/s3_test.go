package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7/pkg/lifecycle"
)

// validS3Config is a syntactically valid config pointing nowhere; constructing
// a client from it makes no network calls.
func validS3Config() S3Config {
	return S3Config{
		Endpoint:       "127.0.0.1:9",
		Bucket:         "vidra-media",
		AccessKey:      "test-access",
		SecretKey:      "test-secret",
		UseSSL:         false,
		ForcePathStyle: true,
	}
}

func TestNewS3ValidatesConfig(t *testing.T) {
	cases := []struct {
		name   string
		mutate func(*S3Config)
	}{
		{"missing endpoint", func(c *S3Config) { c.Endpoint = "" }},
		{"scheme in endpoint", func(c *S3Config) { c.Endpoint = "http://minio:9000" }},
		{"https scheme in endpoint", func(c *S3Config) { c.Endpoint = "https://s3.example.test" }},
		{"missing bucket", func(c *S3Config) { c.Bucket = " " }},
		{"missing access key", func(c *S3Config) { c.AccessKey = "" }},
		{"missing secret key", func(c *S3Config) { c.SecretKey = "" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := validS3Config()
			tc.mutate(&cfg)
			if _, err := NewS3(cfg); err == nil {
				t.Fatalf("NewS3(%+v) succeeded, want error", cfg)
			}
		})
	}
	if _, err := NewS3(validS3Config()); err != nil {
		t.Fatalf("NewS3(valid) = %v, want nil", err)
	}
}

// TestS3RejectsInvalidKeys proves the S3 backend enforces the same key
// contract as Local — invalid keys are rejected with ErrInvalidKey before any
// network call is attempted (the endpoint points nowhere; a request would fail
// with a different error).
func TestS3RejectsInvalidKeys(t *testing.T) {
	b, err := NewS3(validS3Config())
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	ctx := context.Background()
	for _, key := range []string{"", "/absolute", "../escape", "a/../../escape", "with\x00null"} {
		if _, err := b.Put(ctx, key, strings.NewReader("x")); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Put(%q) err = %v, want ErrInvalidKey", key, err)
		}
		if _, err := b.Open(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Open(%q) err = %v, want ErrInvalidKey", key, err)
		}
		if err := b.Delete(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Delete(%q) err = %v, want ErrInvalidKey", key, err)
		}
		if _, err := b.Exists(ctx, key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("Exists(%q) err = %v, want ErrInvalidKey", key, err)
		}
	}
}

// TestS3IsNotAPathProvider pins the design decision that S3 exposes no local
// filesystem paths: media tools (ffprobe/ffmpeg/clamav) must take the
// temp-download fallback and HTTP serving the seekable-reader path. If S3 ever
// grows a Path method this fails so the decision is revisited consciously.
func TestS3IsNotAPathProvider(t *testing.T) {
	b, err := NewS3(validS3Config())
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	if _, ok := interface{}(b).(PathProvider); ok {
		t.Fatal("*S3 implements PathProvider; the temp-download and seekable-reader fallbacks rely on it NOT doing so")
	}
}

// TestSniffSize covers the types Put actually receives in this codebase. A wrong
// answer here fails the upload (the SDK reads exactly the length it is told), so
// anything not provably exact must report SizeUnknown.
func TestSniffSize(t *testing.T) {
	t.Run("bytes.Reader", func(t *testing.T) {
		if got := sniffSize(bytes.NewReader([]byte("hello"))); got != 5 {
			t.Errorf("sniffSize = %d, want 5", got)
		}
	})
	t.Run("bytes.Reader partially consumed reports remaining", func(t *testing.T) {
		r := bytes.NewReader([]byte("hello"))
		if _, err := r.Read(make([]byte, 2)); err != nil {
			t.Fatalf("Read: %v", err)
		}
		if got := sniffSize(r); got != 3 {
			t.Errorf("sniffSize = %d, want 3", got)
		}
	})
	t.Run("bytes.Buffer", func(t *testing.T) {
		if got := sniffSize(bytes.NewBufferString("abcd")); got != 4 {
			t.Errorf("sniffSize = %d, want 4", got)
		}
	})
	t.Run("strings.Reader", func(t *testing.T) {
		if got := sniffSize(strings.NewReader("abc")); got != 3 {
			t.Errorf("sniffSize = %d, want 3", got)
		}
	})
	t.Run("regular file", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "obj.bin")
		if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() { _ = f.Close() }()
		if got := sniffSize(f); got != 10 {
			t.Errorf("sniffSize = %d, want 10", got)
		}
	})
	t.Run("regular file at offset reports remaining", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "obj.bin")
		if err := os.WriteFile(path, []byte("0123456789"), 0o600); err != nil {
			t.Fatalf("WriteFile: %v", err)
		}
		f, err := os.Open(path)
		if err != nil {
			t.Fatalf("Open: %v", err)
		}
		defer func() { _ = f.Close() }()
		if _, err := f.Seek(4, io.SeekStart); err != nil {
			t.Fatalf("Seek: %v", err)
		}
		if got := sniffSize(f); got != 6 {
			t.Errorf("sniffSize = %d, want 6", got)
		}
	})
	t.Run("opaque reader is unknown", func(t *testing.T) {
		if got := sniffSize(io.LimitReader(strings.NewReader("abc"), 2)); got != SizeUnknown {
			t.Errorf("sniffSize = %d, want SizeUnknown", got)
		}
	})
	t.Run("a SizedReader answers for itself", func(t *testing.T) {
		if got := sniffSize(sizedReader{Reader: strings.NewReader("abcdefg"), size: 7}); got != 7 {
			t.Errorf("sniffSize = %d, want 7", got)
		}
	})
	t.Run("a SizedReader that declines is unknown", func(t *testing.T) {
		// SizeUnknown from the reader must propagate as SizeUnknown, not as a
		// negative length handed to the SDK.
		if got := sniffSize(sizedReader{Reader: strings.NewReader("abc"), size: SizeUnknown}); got != SizeUnknown {
			t.Errorf("sniffSize = %d, want SizeUnknown", got)
		}
	})
	t.Run("the concrete cases beat the interface", func(t *testing.T) {
		// *bytes.Reader and *strings.Reader BOTH satisfy SizedReader, and their
		// Size() reports the length they started with rather than what is left.
		// If the interface case were matched first, a partially-consumed reader
		// would over-report and the upload would stall waiting for bytes that
		// are not coming. This pins the type switch's ordering.
		br := bytes.NewReader([]byte("hello"))
		sr := strings.NewReader("hello")
		for _, r := range []io.Reader{br, sr} {
			if _, err := r.Read(make([]byte, 2)); err != nil {
				t.Fatalf("Read: %v", err)
			}
			if _, ok := r.(SizedReader); !ok {
				t.Fatalf("%T no longer satisfies SizedReader; this pin no longer proves anything", r)
			}
			if got := sniffSize(r); got != 3 {
				t.Errorf("sniffSize(%T) = %d, want 3 (remaining, not original length)", r, got)
			}
		}
	})
}

// sizedReader is a reader that knows its own remaining length — the shape
// S3.Open's reader has, without needing a store to open anything from.
type sizedReader struct {
	io.Reader
	size int64
}

func (s sizedReader) Size() int64 { return s.size }

// fakeS3Object serves one object over HTTP the way an S3-compatible store does,
// which is enough for the SDK to build a *minio.Object against it. It exists so
// the size-carrying reader S3.Open returns is provable WITHOUT a live MinIO —
// the integration suite needs a container, and this contract is the reason the
// storage migration does not upload every thumbnail as a multipart.
func fakeS3Object(t *testing.T, body []byte) *S3 {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
		w.Header().Set("ETag", `"5d41402abc4b2a76b9719d911017c592"`)
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Header().Set("Accept-Ranges", "bytes")
		http.ServeContent(w, r, "obj.bin", time.Time{}, bytes.NewReader(body))
	}))
	t.Cleanup(srv.Close)
	cfg := validS3Config()
	cfg.Endpoint = strings.TrimPrefix(srv.URL, "http://")
	// A region pins the client to it, so no bucket-location round trip is made
	// against a fake that would not answer one.
	cfg.Region = "us-east-1"
	b, err := NewS3(cfg)
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	return b
}

// TestS3OpenReaderCarriesTheSize proves S3.Open keeps the length its own Stat
// already paid for, so a copy INTO another store gets the single-PUT path
// instead of a multipart upload with a 16 MiB part buffer.
func TestS3OpenReaderCarriesTheSize(t *testing.T) {
	body := []byte("0123456789")
	b := fakeS3Object(t, body)
	rc, err := b.Open(context.Background(), "thumbnails/x.jpg")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()

	sized, ok := rc.(SizedReader)
	if !ok {
		t.Fatalf("S3 Open returned %T, which is not a SizedReader; every S3→S3 copy would upload with an unknown length", rc)
	}
	if got := sized.Size(); got != int64(len(body)) {
		t.Fatalf("Size = %d, want %d", got, len(body))
	}
	// The whole point: this is the value the Put path derives from the reader.
	if got := sniffSize(rc); got != int64(len(body)) {
		t.Errorf("sniffSize = %d, want %d", got, len(body))
	}

	// Size is REMAINING, not the object's length: a partly-read reader must not
	// over-report, or the destination waits for bytes that are already gone.
	if _, err := io.ReadFull(rc, make([]byte, 4)); err != nil {
		t.Fatalf("ReadFull: %v", err)
	}
	if got := sized.Size(); got != 6 {
		t.Errorf("Size after reading 4 bytes = %d, want 6", got)
	}
}

// TestS3OpenReaderStaysSeekable pins the capability the size wrapper must not
// cost: serveStoredObject and blobsink type-assert this reader to io.ReadSeeker
// for http.ServeContent, and losing Seek would turn every Range request into a
// silent full-body 200.
func TestS3OpenReaderStaysSeekable(t *testing.T) {
	b := fakeS3Object(t, []byte("0123456789"))
	rc, err := b.Open(context.Background(), "web-videos/x.mp4")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer func() { _ = rc.Close() }()
	rs, ok := rc.(io.ReadSeeker)
	if !ok {
		t.Fatal("S3 Open reader does not implement io.ReadSeeker; Range serving would silently degrade to full-body 200s")
	}
	if _, err := rs.Seek(4, io.SeekStart); err != nil {
		t.Fatalf("Seek: %v", err)
	}
	got, err := io.ReadAll(rs)
	if err != nil {
		t.Fatalf("ReadAll: %v", err)
	}
	if string(got) != "456789" {
		t.Errorf("read after seek = %q, want %q", got, "456789")
	}
}

// TestPutSizedValidatesKeys proves the sized path enforces the same key contract
// as Put — it is a separate entry point, so it needs its own guard.
func TestPutSizedValidatesKeys(t *testing.T) {
	b, err := NewS3(validS3Config())
	if err != nil {
		t.Fatalf("NewS3: %v", err)
	}
	ctx := context.Background()
	for _, key := range []string{"", "/absolute", "../escape", "with\x00null"} {
		if _, err := b.PutSized(ctx, key, strings.NewReader("x"), 1); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("PutSized(%q) err = %v, want ErrInvalidKey", key, err)
		}
	}
}

// TestPutSizedHelperFallsBack proves the package helper degrades to plain Put on
// a backend without the capability, and routes to PutSized when present.
func TestPutSizedHelperFallsBack(t *testing.T) {
	local, err := NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("NewLocal: %v", err)
	}
	if _, ok := interface{}(local).(SizedPutter); ok {
		t.Fatal("*Local implements SizedPutter; the helper's fallback path would go untested")
	}
	n, err := PutSized(context.Background(), local, "a/b.bin", strings.NewReader("hello"), 5)
	if err != nil {
		t.Fatalf("PutSized: %v", err)
	}
	if n != 5 {
		t.Errorf("PutSized wrote %d bytes, want 5", n)
	}
}

func TestValidateKeyTable(t *testing.T) {
	for _, key := range []string{"a", "a/b/c.bin", "web-videos/x.mp4", "a/./b", "a..b/c", "..a/b"} {
		if err := validateKey(key); err != nil {
			t.Errorf("validateKey(%q) = %v, want nil", key, err)
		}
	}
	for _, key := range []string{"", "/abs", "../up", "a/../../b", "a/..", "bad\x00key"} {
		if err := validateKey(key); !errors.Is(err, ErrInvalidKey) {
			t.Errorf("validateKey(%q) = %v, want ErrInvalidKey", key, err)
		}
	}
}

// TestNoncurrentExpiryRuleID pins which lifecycle rules count as "this bucket
// actually reclaims". A rule that is present but DISABLED must not count — that
// is configuration which is not running, and treating it as protection is how an
// operator ends up billed for something a green check told them was handled.
func TestNoncurrentExpiryRuleID(t *testing.T) {
	days := func(n int) lifecycle.NoncurrentVersionExpiration {
		return lifecycle.NoncurrentVersionExpiration{NoncurrentDays: lifecycle.ExpirationDays(n)}
	}
	cases := []struct {
		name string
		cfg  *lifecycle.Configuration
		want string
	}{
		{"nil config", nil, ""},
		{"no rules", &lifecycle.Configuration{}, ""},
		{
			"enabled noncurrent-days rule counts",
			&lifecycle.Configuration{Rules: []lifecycle.Rule{
				{ID: "expire-old", Status: "Enabled", NoncurrentVersionExpiration: days(7)},
			}},
			"expire-old",
		},
		{
			"keep-newest-N counts too",
			&lifecycle.Configuration{Rules: []lifecycle.Rule{
				{ID: "keep-2", Status: "Enabled", NoncurrentVersionExpiration: lifecycle.NoncurrentVersionExpiration{NewerNoncurrentVersions: 2}},
			}},
			"keep-2",
		},
		{
			"a disabled rule does not count",
			&lifecycle.Configuration{Rules: []lifecycle.Rule{
				{ID: "expire-old", Status: "Disabled", NoncurrentVersionExpiration: days(7)},
			}},
			"",
		},
		{
			"a current-version expiry is not a noncurrent expiry",
			&lifecycle.Configuration{Rules: []lifecycle.Rule{
				{ID: "expire-current", Status: "Enabled", Expiration: lifecycle.Expiration{Days: 30}},
			}},
			"",
		},
		{
			"an unnamed enabled rule still reports",
			&lifecycle.Configuration{Rules: []lifecycle.Rule{
				{ID: "  ", Status: "Enabled", NoncurrentVersionExpiration: days(3)},
			}},
			"(unnamed)",
		},
		{
			"a disabled rule does not mask an enabled one",
			&lifecycle.Configuration{Rules: []lifecycle.Rule{
				{ID: "off", Status: "Disabled", NoncurrentVersionExpiration: days(7)},
				{ID: "on", Status: "Enabled", NoncurrentVersionExpiration: days(7)},
			}},
			"on",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := noncurrentExpiryRuleID(tc.cfg); got != tc.want {
				t.Errorf("noncurrentExpiryRuleID = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBucketRetentionReclaimsOnDelete pins the three-way answer. "Unknown" must
// never collapse into "fine": a store that would not answer is a store whose
// billing behaviour we cannot vouch for.
func TestBucketRetentionReclaimsOnDelete(t *testing.T) {
	cases := []struct {
		name            string
		r               BucketRetention
		reclaims, known bool
	}{
		{"nothing known", BucketRetention{}, false, false},
		{
			"versioning off reclaims, lifecycle irrelevant",
			BucketRetention{VersioningKnown: true},
			true, true,
		},
		{
			"versioned with an expiry rule reclaims",
			BucketRetention{VersioningKnown: true, VersioningEnabled: true, LifecycleKnown: true, NoncurrentExpiryRule: "r"},
			true, true,
		},
		{
			"versioned with a known-empty lifecycle does not reclaim",
			BucketRetention{VersioningKnown: true, VersioningEnabled: true, LifecycleKnown: true},
			false, true,
		},
		{
			"versioned with an unknown lifecycle is undetermined, not safe",
			BucketRetention{VersioningKnown: true, VersioningEnabled: true},
			false, false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reclaims, known := tc.r.ReclaimsOnDelete()
			if reclaims != tc.reclaims || known != tc.known {
				t.Errorf("ReclaimsOnDelete() = (%v, %v), want (%v, %v)", reclaims, known, tc.reclaims, tc.known)
			}
		})
	}
}
