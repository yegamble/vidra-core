package backup

import (
	"context"
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

	target := NewLocalBackend("./test-backups")
	manager := NewRestoreManager(target, "./temp")

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
