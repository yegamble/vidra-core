package storage

import (
	"context"
	"errors"
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
