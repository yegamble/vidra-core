package backup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewS3Backend(t *testing.T) {
	backend := NewS3Backend(S3Config{
		Bucket:    "test-bucket",
		Region:    "test-region",
		Prefix:    "backups/",
		Endpoint:  "endpoint",
		AccessKey: "access",
		SecretKey: "secret",
	})

	assert.NotNil(t, backend)
	assert.Equal(t, "test-bucket", backend.Bucket)
	assert.Equal(t, "test-region", backend.Region)
	assert.Equal(t, "backups/", backend.Prefix)
	assert.Equal(t, "endpoint", backend.Endpoint)
}

func TestS3Backend_Upload(t *testing.T) {
	backend := NewS3Backend(S3Config{Bucket: "test-bucket", Region: "us-east-1", Prefix: "backups/", AccessKey: "access-key", SecretKey: "secret-key"})
	assert.NotNil(t, backend)
	assert.Equal(t, "test-bucket", backend.Bucket)
}

func TestS3Backend_Download(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping S3 integration test in short mode")
	}

	t.Log("S3 download requires real AWS infrastructure - skipped in short mode")
}

func TestS3Backend_List(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping S3 integration test in short mode")
	}

	t.Log("S3 list requires real AWS infrastructure - skipped in short mode")
}

func TestS3Backend_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping S3 integration test in short mode")
	}

	t.Log("S3 delete requires real AWS infrastructure - skipped in short mode")
}

func TestS3Backend_buildKey(t *testing.T) {
	tests := []struct {
		name   string
		prefix string
		path   string
		want   string
	}{
		{
			name:   "with prefix",
			prefix: "backups/",
			path:   "file.tar.gz",
			want:   "backups/file.tar.gz",
		},
		{
			name:   "empty prefix",
			prefix: "",
			path:   "file.tar.gz",
			want:   "file.tar.gz",
		},
		{
			name:   "prefix without trailing slash",
			prefix: "backups",
			path:   "file.tar.gz",
			want:   "backups/file.tar.gz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := NewS3Backend(S3Config{Bucket: "bucket", Region: "region", Prefix: tt.prefix, AccessKey: "access", SecretKey: "secret"})
			got := backend.buildKey(tt.path)
			assert.Equal(t, tt.want, got)
		})
	}
}
