package backup

import (
	"context"
	"testing"

	"athena/internal/backup"

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

func TestService_ListBackups(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	target := backup.NewLocalBackend("./test-backups")
	manager := &backup.BackupManager{
		Target:        target,
		AppVersion:    "test",
		SchemaVersion: 1,
	}
	svc := NewService(target, "./temp", manager)

	ctx := context.Background()
	backups, err := svc.ListBackups(ctx)

	require.NoError(t, err)
	assert.NotNil(t, backups)
}
