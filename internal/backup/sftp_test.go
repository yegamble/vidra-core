package backup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewSFTPBackend(t *testing.T) {
	backend := NewSFTPBackend("sftp.example.com", 22, "user", "password", "", "/backups")

	assert.NotNil(t, backend)
	assert.Equal(t, "sftp.example.com", backend.Host)
	assert.Equal(t, 22, backend.Port)
	assert.Equal(t, "user", backend.User)
	assert.Equal(t, "/backups", backend.Path)
}

func TestSFTPBackend_Upload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping SFTP integration test in short mode")
	}

	t.Run("upload with mocked server", func(t *testing.T) {
		t.Skip("SFTP server mocking not yet implemented")
	})
}

func TestSFTPBackend_Download(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping SFTP integration test in short mode")
	}

	t.Run("download with mocked server", func(t *testing.T) {
		t.Skip("SFTP server mocking not yet implemented")
	})
}

func TestSFTPBackend_List(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping SFTP integration test in short mode")
	}

	t.Run("list with mocked server", func(t *testing.T) {
		t.Skip("SFTP server mocking not yet implemented")
	})
}

func TestSFTPBackend_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping SFTP integration test in short mode")
	}

	t.Run("delete with mocked server", func(t *testing.T) {
		t.Skip("SFTP server mocking not yet implemented")
	})
}

func TestSFTPBackend_buildPath(t *testing.T) {
	tests := []struct {
		name     string
		basePath string
		filename string
		want     string
	}{
		{
			name:     "with base path",
			basePath: "/backups",
			filename: "file.tar.gz",
			want:     "/backups/file.tar.gz",
		},
		{
			name:     "empty base path",
			basePath: "",
			filename: "file.tar.gz",
			want:     "/file.tar.gz",
		},
		{
			name:     "base path without leading slash",
			basePath: "backups",
			filename: "file.tar.gz",
			want:     "/backups/file.tar.gz",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			backend := NewSFTPBackend("host", 22, "user", "pass", "", tt.basePath)
			got := backend.buildPath(tt.filename)
			assert.Equal(t, tt.want, got)
		})
	}
}
