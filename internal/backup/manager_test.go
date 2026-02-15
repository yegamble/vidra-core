package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBackupManagerCreation(t *testing.T) {
	target := NewLocalBackend(t.TempDir())
	manager := NewBackupManager(target, "1.0.0", 61, "postgres://localhost/test", "redis://localhost")

	assert.NotNil(t, manager)
	assert.Equal(t, "1.0.0", manager.AppVersion)
	assert.Equal(t, int64(61), manager.SchemaVersion)
	assert.Equal(t, "postgres://localhost/test", manager.DatabaseURL)
	assert.Equal(t, "redis://localhost", manager.RedisURL)
}

func TestBackupManagerValidation(t *testing.T) {
	target := NewLocalBackend(t.TempDir())

	tests := []struct {
		name          string
		appVersion    string
		schemaVersion int64
		wantErr       bool
	}{
		{
			name:          "valid configuration",
			appVersion:    "1.0.0",
			schemaVersion: 61,
			wantErr:       false,
		},
		{
			name:          "empty app version",
			appVersion:    "",
			schemaVersion: 61,
			wantErr:       true,
		},
		{
			name:          "negative schema version",
			appVersion:    "1.0.0",
			schemaVersion: -1,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewBackupManager(target, tt.appVersion, tt.schemaVersion, "postgres://localhost/test", "redis://localhost")
			err := manager.Validate()

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestBackupManagerJobCreation(t *testing.T) {
	target := NewLocalBackend(t.TempDir())
	manager := NewBackupManager(target, "1.0.0", 61, "postgres://localhost/test", "redis://localhost")

	ctx := context.Background()
	job, err := manager.CreateJob(ctx)
	require.NoError(t, err)

	assert.NotEmpty(t, job.ID)
	assert.Equal(t, StatusPending, job.Status)
	assert.Equal(t, int64(61), job.SchemaVersion)
}

func TestBackupManagerCreateBackup(t *testing.T) {
	t.Skip("Integration test - requires actual database")

	tempDir := t.TempDir()
	target := NewLocalBackend(tempDir)

	manager := &BackupManager{
		Target:        target,
		AppVersion:    "1.0.0",
		SchemaVersion: 61,
		DatabaseURL:   "postgres://user:pass@localhost:5432/athena",
		RedisURL:      "redis://localhost:6379",
	}

	ctx := context.Background()
	result, err := manager.CreateBackup(ctx)

	require.NoError(t, err)
	assert.NotNil(t, result)
	assert.True(t, result.Success)
	assert.NotEmpty(t, result.BackupPath)
	assert.Equal(t, int64(61), result.SchemaVersion)
}

func TestBackupManagerManifestWriting(t *testing.T) {
	tempDir := t.TempDir()
	target := NewLocalBackend(tempDir)

	manager := &BackupManager{
		Target:        target,
		AppVersion:    "1.0.0",
		SchemaVersion: 61,
	}

	manifest := NewManifest("1.0.0", 61)
	manifestPath := filepath.Join(tempDir, "manifest.json")

	err := manager.writeManifest(manifest, manifestPath)
	require.NoError(t, err)

	data, err := os.ReadFile(manifestPath)
	require.NoError(t, err)

	var readManifest Manifest
	err = json.Unmarshal(data, &readManifest)
	require.NoError(t, err)
	assert.Equal(t, "1.0.0", readManifest.AppVersion)
	assert.Equal(t, int64(61), readManifest.SchemaVersion)
}

func TestBackupManagerArchiveCreation(t *testing.T) {
	tempDir := t.TempDir()
	target := NewLocalBackend(tempDir)

	manager := &BackupManager{
		Target:        target,
		AppVersion:    "1.0.0",
		SchemaVersion: 61,
	}

	testFile1 := filepath.Join(tempDir, "test1.txt")
	testFile2 := filepath.Join(tempDir, "test2.txt")
	require.NoError(t, os.WriteFile(testFile1, []byte("content1"), 0644))
	require.NoError(t, os.WriteFile(testFile2, []byte("content2"), 0644))

	archivePath := filepath.Join(tempDir, "test.tar.gz")
	err := manager.createArchive(archivePath, tempDir, []string{"test1.txt", "test2.txt"})
	require.NoError(t, err)

	stat, err := os.Stat(archivePath)
	require.NoError(t, err)
	assert.Greater(t, stat.Size(), int64(0))
}
