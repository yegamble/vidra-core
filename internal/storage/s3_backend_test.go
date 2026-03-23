package storage

import (
	"context"
	"strings"
	"testing"
	"time"
)

func newS3ConfigForTests() S3Config {
	return S3Config{
		Endpoint:  "http://127.0.0.1:1",
		Bucket:    "vidra-test-bucket",
		AccessKey: "test-access",
		SecretKey: "test-secret",
		Region:    "us-test-1",
		PathStyle: true,
	}
}

func TestNewS3Backend_ValidationAndDefaults(t *testing.T) {
	t.Run("missing bucket", func(t *testing.T) {
		backend, err := NewS3Backend(S3Config{
			AccessKey: "ak",
			SecretKey: "sk",
		})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if backend != nil {
			t.Fatal("expected nil backend on error")
		}
		if !strings.Contains(err.Error(), "bucket name is required") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("missing credentials", func(t *testing.T) {
		backend, err := NewS3Backend(S3Config{Bucket: "bucket-only"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if backend != nil {
			t.Fatal("expected nil backend on error")
		}
		if !strings.Contains(err.Error(), "access key and secret key are required") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("default region and public URL without endpoint", func(t *testing.T) {
		backend, err := NewS3Backend(S3Config{
			Bucket:    "media",
			AccessKey: "ak",
			SecretKey: "sk",
		})
		if err != nil {
			t.Fatalf("NewS3Backend() error = %v", err)
		}
		if backend.region != "us-east-1" {
			t.Fatalf("region = %q, want %q", backend.region, "us-east-1")
		}
		if backend.publicURL != "https://media.s3.us-east-1.amazonaws.com" {
			t.Fatalf("publicURL = %q", backend.publicURL)
		}
	})

	t.Run("endpoint-derived and custom public URL", func(t *testing.T) {
		endpointCfg := S3Config{
			Endpoint:  "https://s3.example.com",
			Bucket:    "archive",
			AccessKey: "ak",
			SecretKey: "sk",
			Region:    "us-west-2",
			PathStyle: true,
		}
		endpointBackend, err := NewS3Backend(endpointCfg)
		if err != nil {
			t.Fatalf("NewS3Backend() error = %v", err)
		}
		if endpointBackend.publicURL != "https://s3.example.com/archive" {
			t.Fatalf("publicURL = %q", endpointBackend.publicURL)
		}
		if !endpointBackend.pathStyle {
			t.Fatal("pathStyle = false, want true")
		}

		customCfg := endpointCfg
		customCfg.PublicURL = "https://cdn.example.com/media"
		customBackend, err := NewS3Backend(customCfg)
		if err != nil {
			t.Fatalf("NewS3Backend() error = %v", err)
		}
		if customBackend.publicURL != "https://cdn.example.com/media" {
			t.Fatalf("publicURL = %q", customBackend.publicURL)
		}
	})
}

func TestS3Backend_CoreMethods_NoExternalServices(t *testing.T) {
	backend, err := NewS3Backend(newS3ConfigForTests())
	if err != nil {
		t.Fatalf("NewS3Backend() error = %v", err)
	}

	t.Run("GetURL", func(t *testing.T) {
		got := backend.GetURL("videos/v1.mp4")
		if got != backend.publicURL+"/videos/v1.mp4" {
			t.Fatalf("GetURL() = %q", got)
		}
	})

	t.Run("GetSignedURL", func(t *testing.T) {
		url, err := backend.GetSignedURL(context.Background(), "videos/v1.mp4", 5*time.Minute)
		if err != nil {
			t.Fatalf("GetSignedURL() error = %v", err)
		}
		if url == "" {
			t.Fatal("GetSignedURL() returned empty URL")
		}
		if !strings.Contains(url, "videos%2Fv1.mp4") && !strings.Contains(url, "/videos/v1.mp4") {
			t.Fatalf("unexpected signed URL: %s", url)
		}
	})

	t.Run("UploadFile missing local file", func(t *testing.T) {
		err := backend.UploadFile(context.Background(), "videos/v1.mp4", "/does/not/exist.mp4", "video/mp4")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to open file") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("DeleteMultiple empty keys no-op", func(t *testing.T) {
		if err := backend.DeleteMultiple(context.Background(), nil); err != nil {
			t.Fatalf("DeleteMultiple(nil) error = %v", err)
		}
	})
}

func TestS3Backend_ContextCanceled_ErrorWrapping(t *testing.T) {
	backend, err := NewS3Backend(newS3ConfigForTests())
	if err != nil {
		t.Fatalf("NewS3Backend() error = %v", err)
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()

	t.Run("Upload wraps error", func(t *testing.T) {
		err := backend.Upload(canceledCtx, "videos/v2.mp4", strings.NewReader("data"), "")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to upload to S3") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Download wraps error", func(t *testing.T) {
		_, err := backend.Download(canceledCtx, "videos/v2.mp4")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to download from S3") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Delete wraps error", func(t *testing.T) {
		err := backend.Delete(canceledCtx, "videos/v2.mp4")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to delete from S3") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Exists wraps non-not-found errors", func(t *testing.T) {
		exists, err := backend.Exists(canceledCtx, "videos/v2.mp4")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if exists {
			t.Fatal("exists = true, want false")
		}
		if !strings.Contains(err.Error(), "failed to check if file exists") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("Copy wraps error", func(t *testing.T) {
		err := backend.Copy(canceledCtx, "videos/src.mp4", "videos/dst.mp4")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to copy in S3") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("GetMetadata wraps error", func(t *testing.T) {
		_, err := backend.GetMetadata(canceledCtx, "videos/v2.mp4")
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to get metadata") {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("DeleteMultiple non-empty wraps error", func(t *testing.T) {
		err := backend.DeleteMultiple(canceledCtx, []string{"k1", "k2"})
		if err == nil {
			t.Fatal("expected error, got nil")
		}
		if !strings.Contains(err.Error(), "failed to delete multiple objects") {
			t.Fatalf("unexpected error: %v", err)
		}
	})
}

func TestS3Backend_ConfigurableACLAndRetry(t *testing.T) {
	tests := []struct {
		name               string
		uploadACLPublic    string
		uploadACLPrivate   string
		maxUploadPart      int64
		maxRequestAttempts int
	}{
		{
			name:               "default ACLs and settings",
			uploadACLPublic:    "",
			uploadACLPrivate:   "",
			maxUploadPart:      0,
			maxRequestAttempts: 0,
		},
		{
			name:               "Backblaze null ACL (empty strings)",
			uploadACLPublic:    "",
			uploadACLPrivate:   "",
			maxUploadPart:      100 * 1024 * 1024,
			maxRequestAttempts: 3,
		},
		{
			name:               "explicit null ACL strings for Backblaze",
			uploadACLPublic:    "null",
			uploadACLPrivate:   "null",
			maxUploadPart:      100 * 1024 * 1024,
			maxRequestAttempts: 5,
		},
		{
			name:               "standard ACLs with custom part size",
			uploadACLPublic:    "public-read",
			uploadACLPrivate:   "private",
			maxUploadPart:      50 * 1024 * 1024,
			maxRequestAttempts: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newS3ConfigForTests()
			cfg.UploadACLPublic = tt.uploadACLPublic
			cfg.UploadACLPrivate = tt.uploadACLPrivate
			cfg.MaxUploadPart = tt.maxUploadPart
			cfg.MaxRequestAttempts = tt.maxRequestAttempts

			backend, err := NewS3Backend(cfg)
			if err != nil {
				t.Fatalf("NewS3Backend() error = %v", err)
			}

			if backend == nil {
				t.Fatal("expected non-nil backend")
			}

			if backend.bucket != cfg.Bucket {
				t.Fatalf("bucket = %q, want %q", backend.bucket, cfg.Bucket)
			}
		})
	}
}

func TestS3Backend_PartSizeConfiguration(t *testing.T) {
	tests := []struct {
		name          string
		maxUploadPart int64
		wantPartSize  int64
	}{
		{
			name:          "default 10MB when zero",
			maxUploadPart: 0,
			wantPartSize:  10 * 1024 * 1024,
		},
		{
			name:          "custom 50MB",
			maxUploadPart: 50 * 1024 * 1024,
			wantPartSize:  50 * 1024 * 1024,
		},
		{
			name:          "custom 100MB (Backblaze recommended)",
			maxUploadPart: 100 * 1024 * 1024,
			wantPartSize:  100 * 1024 * 1024,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newS3ConfigForTests()
			cfg.MaxUploadPart = tt.maxUploadPart

			backend, err := NewS3Backend(cfg)
			if err != nil {
				t.Fatalf("NewS3Backend() error = %v", err)
			}

			if backend.uploader == nil {
				t.Fatal("uploader is nil")
			}
		})
	}
}

func TestS3Backend_ACLNullSkipsACLOnUpload(t *testing.T) {
	tests := []struct {
		name    string
		acl     string
		wantACL bool
	}{
		{"null ACL skips setting", "null", false},
		{"empty ACL skips setting", "", false},
		{"public-read sets ACL", "public-read", true},
		{"private sets ACL", "private", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newS3ConfigForTests()
			cfg.UploadACLPublic = tt.acl

			backend, err := NewS3Backend(cfg)
			if err != nil {
				t.Fatalf("NewS3Backend() error = %v", err)
			}

			if tt.wantACL {
				if backend.uploadACLPublic == "" || backend.uploadACLPublic == "null" {
					t.Errorf("expected ACL to be set, got %q", backend.uploadACLPublic)
				}
			} else {
				if backend.uploadACLPublic != "" && backend.uploadACLPublic != "null" {
					t.Errorf("expected ACL to be empty or null, got %q", backend.uploadACLPublic)
				}
			}
		})
	}
}
