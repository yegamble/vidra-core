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
	manager := NewBackupManager(target, "1.0.0", 61, "postgres://localhost/test", "redis://localhost", "/tmp/storage")

	assert.NotNil(t, manager)
	assert.Equal(t, "1.0.0", manager.AppVersion)
	assert.Equal(t, int64(61), manager.SchemaVersion)
	assert.Equal(t, "postgres://localhost/test", manager.DatabaseURL)
	assert.Equal(t, "redis://localhost", manager.RedisURL)
	assert.Equal(t, "/tmp/storage", manager.StoragePath)
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
			manager := NewBackupManager(target, tt.appVersion, tt.schemaVersion, "postgres://localhost/test", "redis://localhost", "/tmp/storage")
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
	manager := NewBackupManager(target, "1.0.0", 61, "postgres://localhost/test", "redis://localhost", "/tmp/storage")

	ctx := context.Background()
	job, err := manager.CreateJob(ctx)
	require.NoError(t, err)

	assert.NotEmpty(t, job.ID)
	assert.Equal(t, StatusPending, job.Status)
	assert.Equal(t, int64(61), job.SchemaVersion)
}

func TestBackupManagerCreateBackup(t *testing.T) {
	if testing.Short() {
		t.Skip("Integration test - requires actual database and pg_dump")
	}

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

func TestBackupManager_SelectiveBackup(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tempDir := t.TempDir()
	target := NewLocalBackend(tempDir)

	manager := NewBackupManager(target, "1.0.0", 61, "", "", "")
	manager.Components = BackupComponents{
		IncludeDatabase: true,
		IncludeRedis:    false,
		IncludeStorage:  false,
	}

	result, err := manager.CreateBackup(context.Background())

	assert.Error(t, err)
	assert.Nil(t, result)
}

func TestShouldExcludePath(t *testing.T) {
	excludeDirs := []string{"videos", "thumbnails"}

	tests := []struct {
		name     string
		path     string
		excluded bool
	}{
		{"exclude videos directory", "videos", true},
		{"exclude file in videos directory", "videos/file.mp4", true},
		{"exclude thumbnails directory", "thumbnails", true},
		{"exclude file in thumbnails directory", "thumbnails/thumb.jpg", true},
		{"include other directory", "documents", false},
		{"include file in other directory", "documents/file.txt", false},
		{"include nested path not in excluded dir", "data/videos.txt", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shouldExcludePath(tt.path, excludeDirs)
			assert.Equal(t, tt.excluded, result)
		})
	}
}

func TestBackupManager_ComponentsDefaultValues(t *testing.T) {
	target := NewLocalBackend(t.TempDir())
	manager := NewBackupManager(target, "1.0.0", 61, "postgres://localhost/test", "", "")

	assert.True(t, manager.Components.IncludeDatabase)
	assert.True(t, manager.Components.IncludeRedis)
	assert.True(t, manager.Components.IncludeStorage)
	assert.Empty(t, manager.Components.ExcludeDirs)
}
