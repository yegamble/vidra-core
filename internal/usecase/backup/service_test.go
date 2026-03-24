package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"vidra-core/internal/backup"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewService(t *testing.T) {
	target := backup.NewLocalBackend("./backups")
	manager := &backup.BackupManager{
		Target:        target,
		AppVersion:    "test",
		SchemaVersion: 1,
	}
	svc := NewService(target, "./temp", manager)

	assert.NotNil(t, svc)
}

func TestService_TriggerBackupWithComponents(t *testing.T) {
	tests := []struct {
		name       string
		manager    *backup.BackupManager
		components backup.BackupComponents
		wantErr    error
	}{
		{
			name:       "nil manager returns ErrInvalidConfiguration",
			manager:    nil,
			components: backup.NewBackupComponents(),
			wantErr:    backup.ErrInvalidConfiguration,
		},
		{
			name: "valid manager returns nil (goroutine launched)",
			manager: &backup.BackupManager{
				Target:        backup.NewLocalBackend(t.TempDir()),
				AppVersion:    "test",
				SchemaVersion: 1,
			},
			components: backup.NewBackupComponents(),
			wantErr:    nil,
		},
		{
			name: "selective components accepted",
			manager: &backup.BackupManager{
				Target:        backup.NewLocalBackend(t.TempDir()),
				AppVersion:    "test",
				SchemaVersion: 1,
			},
			components: backup.BackupComponents{
				IncludeDatabase: true,
				IncludeRedis:    false,
				IncludeStorage:  false,
			},
			wantErr: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			target := backup.NewLocalBackend(t.TempDir())
			svc := NewService(target, t.TempDir(), tt.manager)

			err := svc.TriggerBackupWithComponents(context.Background(), tt.components)

			if tt.wantErr != nil {
				assert.ErrorIs(t, err, tt.wantErr)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestService_TriggerBackup(t *testing.T) {
	target := backup.NewLocalBackend(t.TempDir())
	manager := &backup.BackupManager{
		Target:        target,
		AppVersion:    "test",
		SchemaVersion: 1,
	}
	svc := NewService(target, t.TempDir(), manager)

	err := svc.TriggerBackup(context.Background())
	assert.NoError(t, err)
}

func TestService_DeleteBackup(t *testing.T) {
	dir := t.TempDir()
	target := backup.NewLocalBackend(dir)
	svc := NewService(target, t.TempDir(), nil)

	err := svc.DeleteBackup(context.Background(), "nonexistent.tar.gz")
	assert.Error(t, err)

	testFile := filepath.Join(dir, "test-backup.tar.gz")
	require.NoError(t, os.WriteFile(testFile, []byte("test"), 0644))
	err = svc.DeleteBackup(context.Background(), "test-backup.tar.gz")
	assert.NoError(t, err)
}

func TestService_ListBackups(t *testing.T) {
	targetDir := t.TempDir()
	target := backup.NewLocalBackend(targetDir)
	manager := &backup.BackupManager{
		Target:        target,
		AppVersion:    "test",
		SchemaVersion: 1,
	}
	svc := NewService(target, t.TempDir(), manager)

	ctx := context.Background()
	backups, err := svc.ListBackups(ctx)

	require.NoError(t, err)
	assert.NotNil(t, backups)
}
