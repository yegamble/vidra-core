// Package integration contains integration tests for the S3 storage backend.
// Run with: TEST_INTEGRATION=true go test ./tests/integration/... -run TestS3Storage -v -timeout 60s
// Requires: docker compose --profile test-integration up -d minio minio-init
package integration

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"athena/internal/storage"
)

// minioConfig returns the S3Config for the test MinIO instance.
// MUST use PathStyle=true — MinIO doesn't support virtual-hosted-style addressing
// when accessed via localhost without a custom domain wildcard.
func minioConfig() storage.S3Config {
	return storage.S3Config{
		Endpoint:  "http://localhost:19100",
		Bucket:    "athena-test",
		AccessKey: "minioadmin",
		SecretKey: "minioadmin",
		Region:    "us-east-1",
		PathStyle: true, // Required for MinIO
	}
}

// newMinioBackend creates an S3Backend connected to the test MinIO instance.
func newMinioBackend(t *testing.T) *storage.S3Backend {
	t.Helper()
	backend, err := storage.NewS3Backend(minioConfig())
	require.NoError(t, err, "failed to create S3 backend for MinIO")
	return backend
}

// uniqueKey generates a unique test object key to avoid test interference.
func uniqueKey(prefix string) string {
	return fmt.Sprintf("test/%s/%d", prefix, time.Now().UnixNano())
}

// TestS3Storage_Upload_Download tests the basic upload/download round-trip.
func TestS3Storage_Upload_Download(t *testing.T) {
	skipIfNoIntegration(t)

	backend := newMinioBackend(t)
	ctx := context.Background()
	key := uniqueKey("upload-download")
	content := "Hello, Athena S3 integration test!"

	// Upload
	err := backend.Upload(ctx, key, strings.NewReader(content), "text/plain")
	require.NoError(t, err, "Upload should succeed")

	// Download
	reader, err := backend.Download(ctx, key)
	require.NoError(t, err, "Download should succeed")
	defer reader.Close()

	downloaded, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, content, string(downloaded), "downloaded content should match uploaded content")

	// Cleanup
	require.NoError(t, backend.Delete(ctx, key))
}

// TestS3Storage_Exists verifies Exists() returns correct values.
func TestS3Storage_Exists(t *testing.T) {
	skipIfNoIntegration(t)

	backend := newMinioBackend(t)
	ctx := context.Background()
	key := uniqueKey("exists")

	// Not yet uploaded
	exists, err := backend.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists, "Exists should return false before upload")

	// Upload
	err = backend.Upload(ctx, key, strings.NewReader("test data"), "text/plain")
	require.NoError(t, err)

	// Now exists
	exists, err = backend.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists, "Exists should return true after upload")

	// Delete and verify
	err = backend.Delete(ctx, key)
	require.NoError(t, err)

	exists, err = backend.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists, "Exists should return false after delete")
}

// TestS3Storage_Delete verifies Delete() removes the object.
func TestS3Storage_Delete(t *testing.T) {
	skipIfNoIntegration(t)

	backend := newMinioBackend(t)
	ctx := context.Background()
	key := uniqueKey("delete")

	// Upload then delete
	err := backend.Upload(ctx, key, strings.NewReader("to be deleted"), "text/plain")
	require.NoError(t, err)

	err = backend.Delete(ctx, key)
	require.NoError(t, err, "Delete should succeed")

	// Verify deleted
	_, err = backend.Download(ctx, key)
	assert.Error(t, err, "Download after delete should return error")
}

// TestS3Storage_DeleteMultiple verifies batch deletion.
func TestS3Storage_DeleteMultiple(t *testing.T) {
	skipIfNoIntegration(t)

	backend := newMinioBackend(t)
	ctx := context.Background()

	keys := []string{
		uniqueKey("multi-delete-1"),
		uniqueKey("multi-delete-2"),
		uniqueKey("multi-delete-3"),
	}

	// Upload all
	for _, key := range keys {
		err := backend.Upload(ctx, key, strings.NewReader("data"), "text/plain")
		require.NoError(t, err, "Upload %s should succeed", key)
	}

	// Delete all at once
	err := backend.DeleteMultiple(ctx, keys)
	require.NoError(t, err, "DeleteMultiple should succeed")

	// Verify all deleted
	for _, key := range keys {
		exists, err := backend.Exists(ctx, key)
		require.NoError(t, err)
		assert.False(t, exists, "key %s should not exist after DeleteMultiple", key)
	}
}

// TestS3Storage_GetMetadata verifies metadata retrieval after upload.
func TestS3Storage_GetMetadata(t *testing.T) {
	skipIfNoIntegration(t)

	backend := newMinioBackend(t)
	ctx := context.Background()
	key := uniqueKey("metadata")
	content := "metadata test content"
	contentType := "text/plain"

	err := backend.Upload(ctx, key, strings.NewReader(content), contentType)
	require.NoError(t, err)
	defer backend.Delete(ctx, key)

	metadata, err := backend.GetMetadata(ctx, key)
	require.NoError(t, err, "GetMetadata should succeed")
	assert.Equal(t, contentType, metadata.ContentType, "content type should match")
	assert.Equal(t, int64(len(content)), metadata.Size, "size should match")
}

// TestS3Storage_Copy verifies copying objects within the bucket.
func TestS3Storage_Copy(t *testing.T) {
	skipIfNoIntegration(t)

	backend := newMinioBackend(t)
	ctx := context.Background()
	srcKey := uniqueKey("copy-src")
	dstKey := uniqueKey("copy-dst")
	content := "content to copy"

	// Upload source
	err := backend.Upload(ctx, srcKey, strings.NewReader(content), "text/plain")
	require.NoError(t, err)
	defer backend.Delete(ctx, srcKey)

	// Copy
	err = backend.Copy(ctx, srcKey, dstKey)
	require.NoError(t, err, "Copy should succeed")
	defer backend.Delete(ctx, dstKey)

	// Verify copy contents
	reader, err := backend.Download(ctx, dstKey)
	require.NoError(t, err)
	defer reader.Close()

	downloaded, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, content, string(downloaded), "copied content should match original")
}

// TestS3Storage_Upload_LargeFile tests upload with a larger payload (1MB).
func TestS3Storage_Upload_LargeFile(t *testing.T) {
	skipIfNoIntegration(t)

	backend := newMinioBackend(t)
	ctx := context.Background()
	key := uniqueKey("large-file")

	// Generate 1MB of test data
	data := bytes.Repeat([]byte("A"), 1024*1024)

	err := backend.Upload(ctx, key, bytes.NewReader(data), "application/octet-stream")
	require.NoError(t, err, "Large file upload should succeed")
	defer backend.Delete(ctx, key)

	// Verify the download size
	metadata, err := backend.GetMetadata(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, int64(len(data)), metadata.Size, "metadata size should match uploaded size")
}

// TestS3Storage_InvalidCredentials verifies error on bad credentials.
func TestS3Storage_InvalidCredentials(t *testing.T) {
	skipIfNoIntegration(t)

	cfg := minioConfig()
	cfg.AccessKey = "wrong-access-key"
	cfg.SecretKey = "wrong-secret-key"

	backend, err := storage.NewS3Backend(cfg)
	require.NoError(t, err, "NewS3Backend should not fail on construction (credentials validated lazily)")

	ctx := context.Background()
	err = backend.Upload(ctx, "test/key", strings.NewReader("data"), "text/plain")
	assert.Error(t, err, "Upload with invalid credentials should fail")
}

// TestS3Storage_GetSignedURL verifies signed URL generation.
// Note: With MinIO path-style, the signed URL uses localhost:19100 as the host.
func TestS3Storage_GetSignedURL(t *testing.T) {
	skipIfNoIntegration(t)

	backend := newMinioBackend(t)
	ctx := context.Background()
	key := uniqueKey("signed-url")
	content := "signed url test"

	err := backend.Upload(ctx, key, strings.NewReader(content), "text/plain")
	require.NoError(t, err)
	defer backend.Delete(ctx, key)

	signedURL, err := backend.GetSignedURL(ctx, key, 5*time.Minute)
	require.NoError(t, err, "GetSignedURL should succeed")
	assert.NotEmpty(t, signedURL, "signed URL should not be empty")
	assert.Contains(t, signedURL, key, "signed URL should contain the object key")
}

// TestS3Storage_NonExistentBucket verifies error when using a non-existent bucket.
func TestS3Storage_NonExistentBucket(t *testing.T) {
	skipIfNoIntegration(t)

	cfg := minioConfig()
	cfg.Bucket = "this-bucket-does-not-exist"

	backend, err := storage.NewS3Backend(cfg)
	require.NoError(t, err, "NewS3Backend should not fail on construction (bucket validated lazily)")

	ctx := context.Background()
	err = backend.Upload(ctx, "test/key", strings.NewReader("data"), "text/plain")
	assert.Error(t, err, "Upload to non-existent bucket should fail")
}

// TestS3Storage_MinIOACLLimitation documents that MinIO has deprecated bucket ACLs.
// Upload() and UploadPrivate() both succeed, but MinIO does not enforce ACL policies.
// This is a known limitation — ACL enforcement requires a proper S3-compatible service.
func TestS3Storage_MinIOACLLimitation(t *testing.T) {
	skipIfNoIntegration(t)
	t.Log("KNOWN LIMITATION: MinIO has deprecated bucket/object ACLs.")
	t.Log("Upload() and UploadPrivate() both succeed on MinIO but ACLs are not enforced.")
	t.Log("For production ACL enforcement, use AWS S3 or an S3-compatible service that supports ACLs.")

	backend := newMinioBackend(t)
	ctx := context.Background()

	publicKey := uniqueKey("acl-public")
	privateKey := uniqueKey("acl-private")

	// Both should succeed (MinIO ignores ACL)
	err := backend.Upload(ctx, publicKey, strings.NewReader("public"), "text/plain")
	assert.NoError(t, err, "Upload (public ACL) should succeed on MinIO")
	defer backend.Delete(ctx, publicKey)

	err = backend.UploadPrivate(ctx, privateKey, strings.NewReader("private"), "text/plain")
	assert.NoError(t, err, "UploadPrivate (private ACL) should succeed on MinIO")
	defer backend.Delete(ctx, privateKey)
}
