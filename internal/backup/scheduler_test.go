package backup

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestNewScheduler(t *testing.T) {
	tempDir := t.TempDir()
	target := NewLocalBackend(tempDir)
	manager := NewBackupManager(target, "1.0.0", 61, "postgres://localhost/test", "redis://localhost", "/tmp/storage")

	scheduler := NewScheduler(manager, "0 2 * * *", 7)

	assert.NotNil(t, scheduler)
	assert.Equal(t, 7, scheduler.retention)
	assert.Equal(t, 24*time.Hour, scheduler.tickInterval)
}

func TestSchedulerStartStop(t *testing.T) {
	tempDir := t.TempDir()
	target := NewLocalBackend(tempDir)
	manager := NewBackupManager(target, "1.0.0", 61, "postgres://localhost/test", "redis://localhost", "/tmp/storage")

	scheduler := NewScheduler(manager, "0 2 * * *", 7)

	ctx := context.Background()
	scheduler.Start(ctx)

	time.Sleep(100 * time.Millisecond)

	scheduler.Stop()

	select {
	case <-scheduler.doneChan:
	case <-time.After(1 * time.Second):
		t.Fatal("Scheduler did not stop within timeout")
	}
}

func TestSchedulerRetention(t *testing.T) {
	if testing.Short() {
		t.Skip("Integration test - requires actual backup files and pg_dump")
	}

	tempDir := t.TempDir()
	target := NewLocalBackend(tempDir)
	manager := NewBackupManager(target, "1.0.0", 61, "postgres://localhost/test", "redis://localhost", "/tmp/storage")

	scheduler := NewScheduler(manager, "0 2 * * *", 3)

	assert.Equal(t, 3, scheduler.retention)
	assert.NotNil(t, scheduler.manager)
}
