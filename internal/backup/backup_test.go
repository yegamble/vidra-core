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

func TestBackupComponents_Defaults(t *testing.T) {
	components := NewBackupComponents()

	assert.True(t, components.IncludeDatabase, "Database should be included by default")
	assert.True(t, components.IncludeRedis, "Redis should be included by default")
	assert.True(t, components.IncludeStorage, "Storage should be included by default")
	assert.Empty(t, components.ExcludeDirs, "No directories should be excluded by default")
}

func TestBackupComponents_SelectiveBackup(t *testing.T) {
	tests := []struct {
		name           string
		components     BackupComponents
		expectDatabase bool
		expectRedis    bool
		expectStorage  bool
		excludeDirs    []string
	}{
		{
			name: "database only",
			components: BackupComponents{
				IncludeDatabase: true,
				IncludeRedis:    false,
				IncludeStorage:  false,
			},
			expectDatabase: true,
			expectRedis:    false,
			expectStorage:  false,
		},
		{
			name: "exclude videos directory",
			components: BackupComponents{
				IncludeDatabase: true,
				IncludeRedis:    true,
				IncludeStorage:  true,
				ExcludeDirs:     []string{"videos", "thumbnails"},
			},
			expectDatabase: true,
			expectRedis:    true,
			expectStorage:  true,
			excludeDirs:    []string{"videos", "thumbnails"},
		},
		{
			name: "no redis",
			components: BackupComponents{
				IncludeDatabase: true,
				IncludeRedis:    false,
				IncludeStorage:  true,
			},
			expectDatabase: true,
			expectRedis:    false,
			expectStorage:  true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expectDatabase, tt.components.IncludeDatabase)
			assert.Equal(t, tt.expectRedis, tt.components.IncludeRedis)
			assert.Equal(t, tt.expectStorage, tt.components.IncludeStorage)
			assert.Equal(t, tt.excludeDirs, tt.components.ExcludeDirs)
		})
	}
}

func TestBackupComponents_GetIncludedComponents(t *testing.T) {
	tests := []struct {
		name       string
		components BackupComponents
		expected   []string
	}{
		{
			name:       "all components",
			components: NewBackupComponents(),
			expected:   []string{"database", "redis", "storage"},
		},
		{
			name: "database only",
			components: BackupComponents{
				IncludeDatabase: true,
				IncludeRedis:    false,
				IncludeStorage:  false,
			},
			expected: []string{"database"},
		},
		{
			name: "database and storage",
			components: BackupComponents{
				IncludeDatabase: true,
				IncludeRedis:    false,
				IncludeStorage:  true,
			},
			expected: []string{"database", "storage"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.components.GetIncludedComponents()
			assert.Equal(t, tt.expected, result)
		})
	}
}
