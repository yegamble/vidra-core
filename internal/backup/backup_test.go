package backup

import (
	"context"
	"io"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestBackupTargetInterface(t *testing.T) {
	var _ BackupTarget = &MockBackupTarget{}
}

type MockBackupTarget struct {
	uploadCalled   bool
	downloadCalled bool
	listCalled     bool
	deleteCalled   bool
}

func (m *MockBackupTarget) Upload(ctx context.Context, reader io.Reader, path string) error {
	m.uploadCalled = true
	return nil
}

func (m *MockBackupTarget) Download(ctx context.Context, path string) (io.ReadCloser, error) {
	m.downloadCalled = true
	return io.NopCloser(nil), nil
}

func (m *MockBackupTarget) List(ctx context.Context, prefix string) ([]BackupEntry, error) {
	m.listCalled = true
	return []BackupEntry{}, nil
}

func (m *MockBackupTarget) Delete(ctx context.Context, path string) error {
	m.deleteCalled = true
	return nil
}

func TestBackupJobCreation(t *testing.T) {
	job := &BackupJob{
		ID:     "test-job-1",
		Status: StatusPending,
	}

	assert.Equal(t, "test-job-1", job.ID)
	assert.Equal(t, StatusPending, job.Status)
}

func TestBackupResultValidation(t *testing.T) {
	result := &BackupResult{
		JobID:     "test-job-1",
		Success:   true,
		BytesSize: 1024,
		Message:   "Backup completed",
	}

	assert.True(t, result.Success)
	assert.Equal(t, int64(1024), result.BytesSize)
}

func TestBackupEntryComparison(t *testing.T) {
	entry1 := BackupEntry{
		Path: "backup-2026-01-01.tar.gz",
		Size: 1024,
	}

	entry2 := BackupEntry{
		Path: "backup-2026-01-02.tar.gz",
		Size: 2048,
	}

	assert.NotEqual(t, entry1.Path, entry2.Path)
	assert.Less(t, entry1.Size, entry2.Size)
}
