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
		DatabaseURL:   "postgres://user:pass@localhost:5432/vidra",
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

func TestShouldExcludePath_TraversalEdgeCases(t *testing.T) {
	excludeDirs := []string{"videos", "thumbnails"}

	tests := []struct {
		name     string
		path     string
		excluded bool
	}{
		{"dot-slash videos still excluded", "./videos", true},
		{"dot-slash nested still excluded", "./videos/file.mp4", true},
		{"trailing slash excluded", "videos/", true},
		{"double dot does not escape to parent", "../videos", false},
		{"traversal through excluded dir", "videos/../secrets", false},

		{"deeply nested in excluded dir", "videos/sub/deep/file.mp4", true},
		{"deeply nested in thumbnails", "thumbnails/2026/02/thumb.jpg", true},

		{"prefix match is not excluded", "videos2", false},
		{"suffix match is not excluded", "myvideos", false},
		{"substring match is not excluded", "old-videos-backup", false},

		{"empty path not excluded", "", false},
		{"dot path not excluded", ".", false},
		{"empty exclude list", "videos", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dirs := excludeDirs
			if tt.name == "empty exclude list" {
				dirs = []string{}
			}
			result := shouldExcludePath(tt.path, dirs)
			assert.Equal(t, tt.excluded, result, "path: %q", tt.path)
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

func TestRunDumpCommand_Success(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "out.txt")

	fakeScript := "#!/bin/sh\nprintf 'data' > \"$1\"\nexit 0\n"
	fakeBin := filepath.Join(tmpDir, "fake-dump")
	require.NoError(t, os.WriteFile(fakeBin, []byte(fakeScript), 0755))
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	err := runDumpCommand(context.Background(), fakeBin, []string{outputPath}, outputPath, 5)
	require.NoError(t, err)

	info, statErr := os.Stat(outputPath)
	require.NoError(t, statErr)
	assert.Greater(t, info.Size(), int64(0))
}

func TestRunDumpCommand_CommandFails(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "out.txt")

	fakeScript := "#!/bin/sh\necho 'simulated failure' >&2\nexit 1\n"
	fakeBin := filepath.Join(tmpDir, "fail-cmd")
	require.NoError(t, os.WriteFile(fakeBin, []byte(fakeScript), 0755))
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	err := runDumpCommand(context.Background(), fakeBin, []string{}, outputPath, 5)
	assert.Error(t, err)
}

func TestDumpRedis_FailsWithoutRedis(t *testing.T) {
	tmpDir := t.TempDir()
	outputPath := filepath.Join(tmpDir, "dump.rdb")

	manager := &BackupManager{
		RedisURL: "redis://127.0.0.1:19999", // port nobody listens on
	}
	ctx := context.Background()
	err := manager.dumpRedis(ctx, outputPath)

	assert.Error(t, err, "dumpRedis should fail when Redis is unreachable")

	if info, statErr := os.Stat(outputPath); statErr == nil {
		assert.Greater(t, info.Size(), int64(0),
			"output file must not be an empty placeholder; size was %d", info.Size())
	}
}

func TestDumpRedis_UsesRDBFlag(t *testing.T) {
	tmpDir := t.TempDir()

	fakeScript := "#!/bin/sh\n" +
		"prev=\"\"\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$prev\" = \"--rdb\" ]; then\n" +
		"    printf 'REDIS' > \"$arg\"\n" +
		"    exit 0\n" +
		"  fi\n" +
		"  prev=\"$arg\"\n" +
		"done\n" +
		"echo 'error: --rdb flag not provided' >&2\n" +
		"exit 1\n"

	fakeBin := filepath.Join(tmpDir, "redis-cli")
	require.NoError(t, os.WriteFile(fakeBin, []byte(fakeScript), 0755))

	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	outputPath := filepath.Join(tmpDir, "dump.rdb")
	manager := &BackupManager{RedisURL: "redis://localhost:6379"}

	err := manager.dumpRedis(context.Background(), outputPath)
	require.NoError(t, err)

	data, readErr := os.ReadFile(outputPath)
	require.NoError(t, readErr)
	assert.Greater(t, len(data), 0, "RDB file must not be empty after a successful dump")
}
