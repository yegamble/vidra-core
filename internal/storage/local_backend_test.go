package storage_test

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"vidra-core/internal/storage"
)

func newTestLocalBackend(t *testing.T) (*storage.LocalBackend, string) {
	t.Helper()
	dir := t.TempDir()
	return storage.NewLocalBackend(dir), dir
}

func TestLocalBackend_Upload(t *testing.T) {
	b, dir := newTestLocalBackend(t)
	ctx := context.Background()

	content := []byte("hello local storage")
	err := b.Upload(ctx, "videos/test/file.txt", bytes.NewReader(content), "text/plain")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "videos/test/file.txt"))
	require.NoError(t, err)
	assert.Equal(t, content, data)
}

func TestLocalBackend_UploadFile(t *testing.T) {
	b, dir := newTestLocalBackend(t)
	ctx := context.Background()

	// Create source file
	src := filepath.Join(t.TempDir(), "source.mp4")
	require.NoError(t, os.WriteFile(src, []byte("video data"), 0o600))

	err := b.UploadFile(ctx, "videos/test/video.mp4", src, "video/mp4")
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, "videos/test/video.mp4"))
	require.NoError(t, err)
	assert.Equal(t, []byte("video data"), data)
}

func TestLocalBackend_Download(t *testing.T) {
	b, dir := newTestLocalBackend(t)
	ctx := context.Background()

	// Pre-write a file
	key := "videos/test/data.txt"
	dest := filepath.Join(dir, key)
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o750))
	require.NoError(t, os.WriteFile(dest, []byte("download me"), 0o600))

	rc, err := b.Download(ctx, key)
	require.NoError(t, err)
	defer rc.Close()

	data, err := io.ReadAll(rc)
	require.NoError(t, err)
	assert.Equal(t, []byte("download me"), data)
}

func TestLocalBackend_Download_NotFound(t *testing.T) {
	b, _ := newTestLocalBackend(t)
	ctx := context.Background()

	_, err := b.Download(ctx, "nonexistent/key.txt")
	assert.Error(t, err)
}

func TestLocalBackend_GetURL(t *testing.T) {
	b, dir := newTestLocalBackend(t)
	url := b.GetURL("videos/test/file.mp4")
	// Should return an absolute path under the base dir
	assert.True(t, strings.HasPrefix(url, dir) || filepath.IsAbs(url))
	assert.Contains(t, url, "file.mp4")
}

func TestLocalBackend_GetSignedURL(t *testing.T) {
	b, _ := newTestLocalBackend(t)
	ctx := context.Background()

	url, err := b.GetSignedURL(ctx, "videos/test/file.mp4", time.Hour)
	require.NoError(t, err)
	assert.NotEmpty(t, url)
}

func TestLocalBackend_Delete(t *testing.T) {
	b, dir := newTestLocalBackend(t)
	ctx := context.Background()

	// Pre-write a file
	key := "videos/test/delete.txt"
	dest := filepath.Join(dir, key)
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o750))
	require.NoError(t, os.WriteFile(dest, []byte("delete me"), 0o600))

	err := b.Delete(ctx, key)
	require.NoError(t, err)

	_, err = os.Stat(dest)
	assert.True(t, os.IsNotExist(err))
}

func TestLocalBackend_Exists(t *testing.T) {
	b, dir := newTestLocalBackend(t)
	ctx := context.Background()

	key := "videos/test/exists.txt"
	exists, err := b.Exists(ctx, key)
	require.NoError(t, err)
	assert.False(t, exists)

	dest := filepath.Join(dir, key)
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o750))
	require.NoError(t, os.WriteFile(dest, []byte("present"), 0o600))

	exists, err = b.Exists(ctx, key)
	require.NoError(t, err)
	assert.True(t, exists)
}

func TestLocalBackend_Copy(t *testing.T) {
	b, dir := newTestLocalBackend(t)
	ctx := context.Background()

	// Pre-write source
	srcKey := "videos/test/source.txt"
	dest := filepath.Join(dir, srcKey)
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o750))
	require.NoError(t, os.WriteFile(dest, []byte("copy me"), 0o600))

	dstKey := "videos/test/destination.txt"
	err := b.Copy(ctx, srcKey, dstKey)
	require.NoError(t, err)

	data, err := os.ReadFile(filepath.Join(dir, dstKey))
	require.NoError(t, err)
	assert.Equal(t, []byte("copy me"), data)
}

func TestLocalBackend_GetMetadata(t *testing.T) {
	b, dir := newTestLocalBackend(t)
	ctx := context.Background()

	key := "videos/test/meta.txt"
	content := []byte("metadata test")
	dest := filepath.Join(dir, key)
	require.NoError(t, os.MkdirAll(filepath.Dir(dest), 0o750))
	require.NoError(t, os.WriteFile(dest, content, 0o600))

	meta, err := b.GetMetadata(ctx, key)
	require.NoError(t, err)
	assert.Equal(t, key, meta.Key)
	assert.Equal(t, int64(len(content)), meta.Size)
	assert.False(t, meta.LastModified.IsZero())
}

func TestLocalBackend_GetMetadata_NotFound(t *testing.T) {
	b, _ := newTestLocalBackend(t)
	ctx := context.Background()

	_, err := b.GetMetadata(ctx, "nonexistent/key.txt")
	assert.Error(t, err)
}

func TestLocalBackend_PathTraversal(t *testing.T) {
	b, _ := newTestLocalBackend(t)
	ctx := context.Background()

	traversalKeys := []string{
		"../../etc/passwd",
		"../secret",
		"videos/../../outside",
	}

	for _, key := range traversalKeys {
		t.Run(key, func(t *testing.T) {
			// Upload should reject traversal
			err := b.Upload(ctx, key, strings.NewReader("bad"), "text/plain")
			assert.Error(t, err, "Upload should reject path traversal key %q", key)

			// Download should reject traversal
			_, err = b.Download(ctx, key)
			assert.Error(t, err, "Download should reject path traversal key %q", key)

			// Delete should reject traversal
			err = b.Delete(ctx, key)
			assert.Error(t, err, "Delete should reject path traversal key %q", key)

			// GetURL should return empty string for traversal keys
			url := b.GetURL(key)
			assert.Empty(t, url, "GetURL should return empty for path traversal key %q", key)
		})
	}
}
