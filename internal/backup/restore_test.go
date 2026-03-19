package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewRestoreManager(t *testing.T) {
	target := NewLocalBackend("./backups")
	manager := NewRestoreManager(target, "./temp")

	assert.NotNil(t, manager)
	assert.Equal(t, target, manager.target)
	assert.Equal(t, "./temp", manager.tempDir)
}

func TestRestoreManager_ListBackups(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	target := NewLocalBackend(t.TempDir())
	manager := NewRestoreManager(target, t.TempDir())

	ctx := context.Background()
	backups, err := manager.ListBackups(ctx)

	require.NoError(t, err)
	assert.NotNil(t, backups)
}

func TestRestoreManager_Restore(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	tempDir := t.TempDir()
	target := NewLocalBackend(tempDir)
	manager := NewRestoreManager(target, tempDir)

	assert.NotNil(t, manager)
	assert.Equal(t, tempDir, manager.tempDir)
}

func TestRestoreManager_validateManifest(t *testing.T) {
	tests := []struct {
		name     string
		manifest *Manifest
		wantErr  bool
	}{
		{
			name: "valid manifest",
			manifest: &Manifest{
				Version:       1,
				AppVersion:    "dev",
				SchemaVersion: 61,
				CreatedAt:     time.Now(),
				Contents:      []string{"database.sql"},
			},
			wantErr: false,
		},
		{
			name: "missing contents",
			manifest: &Manifest{
				Version:       1,
				AppVersion:    "dev",
				SchemaVersion: 61,
				CreatedAt:     time.Now(),
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			manager := NewRestoreManager(NewLocalBackend("./backups"), "./temp")
			err := manager.validateManifest(tt.manifest)

			if tt.wantErr {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRestoreRedis_NoCLI(t *testing.T) {
	tmpDir := t.TempDir()
	rdbPath := filepath.Join(tmpDir, "dump.rdb")
	require.NoError(t, os.WriteFile(rdbPath, []byte("REDIS"), 0644))

	t.Setenv("PATH", tmpDir+"/empty-no-binaries")

	manager := NewRestoreManager(NewLocalBackend(tmpDir), tmpDir)
	manager.BackupMgr = &BackupManager{RedisURL: "redis://localhost:6379"}

	err := manager.restoreRedis(context.Background(), rdbPath)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrRedisCLINotFound,
		"expected ErrRedisCLINotFound when redis-cli is not in PATH")
}

func TestRestoreRedis_CopiesRDB(t *testing.T) {
	tmpDir := t.TempDir()
	redisDataDir := filepath.Join(tmpDir, "redis-data")
	require.NoError(t, os.MkdirAll(redisDataDir, 0755))

	rdbPath := filepath.Join(tmpDir, "dump.rdb")
	require.NoError(t, os.WriteFile(rdbPath, []byte("REDIS-CONTENT"), 0644))

	fakeScript := "#!/bin/sh\n" +
		"for arg in \"$@\"; do\n" +
		"  if [ \"$arg\" = \"dir\" ]; then\n" +
		"    printf 'dir\\n" + redisDataDir + "\\n'\n" +
		"    exit 0\n" +
		"  fi\n" +
		"done\n" +
		"exit 0\n"

	fakeBin := filepath.Join(tmpDir, "redis-cli")
	require.NoError(t, os.WriteFile(fakeBin, []byte(fakeScript), 0755))
	origPath := os.Getenv("PATH")
	t.Setenv("PATH", tmpDir+":"+origPath)

	manager := NewRestoreManager(NewLocalBackend(tmpDir), tmpDir)
	manager.BackupMgr = &BackupManager{RedisURL: "redis://localhost:6379"}

	err := manager.restoreRedis(context.Background(), rdbPath)
	require.NoError(t, err, "restoreRedis should succeed and log a warning instead of returning an error")

	destPath := filepath.Join(redisDataDir, "dump.rdb")
	data, readErr := os.ReadFile(destPath)
	require.NoError(t, readErr, "RDB file should have been copied to redis data dir")
	assert.Equal(t, "REDIS-CONTENT", string(data))
}
