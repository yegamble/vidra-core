package backup

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLocalBackendCreation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping local backend test in short mode")
	}

	tmpDir := t.TempDir()
	backend := NewLocalBackend(tmpDir)

	assert.NotNil(t, backend)
	assert.Equal(t, tmpDir, backend.BasePath)
}

func TestLocalBackendUpload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping local backend test in short mode")
	}

	tmpDir := t.TempDir()
	backend := NewLocalBackend(tmpDir)

	testData := []byte("test backup data")
	reader := bytes.NewReader(testData)

	ctx := context.Background()
	err := backend.Upload(ctx, reader, "test-backup.tar.gz")
	require.NoError(t, err)

	filePath := filepath.Join(tmpDir, "test-backup.tar.gz")
	_, err = os.Stat(filePath)
	require.NoError(t, err)

	content, err := os.ReadFile(filePath)
	require.NoError(t, err)
	assert.Equal(t, testData, content)
}

func TestLocalBackendDownload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping local backend test in short mode")
	}

	tmpDir := t.TempDir()
	backend := NewLocalBackend(tmpDir)

	testData := []byte("test backup data for download")
	filePath := filepath.Join(tmpDir, "test-download.tar.gz")
	err := os.WriteFile(filePath, testData, 0600)
	require.NoError(t, err)

	ctx := context.Background()
	reader, err := backend.Download(ctx, "test-download.tar.gz")
	require.NoError(t, err)
	defer reader.Close()

	content, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, testData, content)
}

func TestLocalBackendList(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping local backend test in short mode")
	}

	tmpDir := t.TempDir()
	backend := NewLocalBackend(tmpDir)

	files := []string{
		"backup-2026-01-01.tar.gz",
		"backup-2026-01-02.tar.gz",
		"backup-2026-01-03.tar.gz",
	}

	for _, filename := range files {
		filePath := filepath.Join(tmpDir, filename)
		err := os.WriteFile(filePath, []byte("test"), 0600)
		require.NoError(t, err)
	}

	ctx := context.Background()
	entries, err := backend.List(ctx, "backup-")
	require.NoError(t, err)

	assert.Len(t, entries, 3)
	for _, entry := range entries {
		assert.Contains(t, entry.Path, "backup-")
		assert.Greater(t, entry.Size, int64(0))
	}
}

func TestLocalBackendDelete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping local backend test in short mode")
	}

	tmpDir := t.TempDir()
	backend := NewLocalBackend(tmpDir)

	filePath := filepath.Join(tmpDir, "test-delete.tar.gz")
	err := os.WriteFile(filePath, []byte("test"), 0600)
	require.NoError(t, err)

	ctx := context.Background()
	err = backend.Delete(ctx, "test-delete.tar.gz")
	require.NoError(t, err)

	_, err = os.Stat(filePath)
	assert.True(t, os.IsNotExist(err))
}
