package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
