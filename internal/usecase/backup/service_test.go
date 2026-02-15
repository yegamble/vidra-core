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
	svc := NewService(target, "./temp")

	assert.NotNil(t, svc)
}

func TestService_ListBackups(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	target := backup.NewLocalBackend("./test-backups")
	svc := NewService(target, "./temp")

	ctx := context.Background()
	backups, err := svc.ListBackups(ctx)

	require.NoError(t, err)
	assert.NotNil(t, backups)
}
