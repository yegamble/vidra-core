package backup

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestNewFTPBackend(t *testing.T) {
	backend := NewFTPBackend("ftp.example.com", 21, "user", "password", "/backups")

	assert.NotNil(t, backend)
	assert.Equal(t, "ftp.example.com", backend.Host)
	assert.Equal(t, 21, backend.Port)
	assert.Equal(t, "user", backend.User)
	assert.Equal(t, "/backups", backend.Path)
}

func TestFTPBackend_Upload(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FTP integration test in short mode")
	}

	t.Run("upload with mocked server", func(t *testing.T) {
		t.Skip("FTP server mocking not yet implemented")
	})
}

func TestFTPBackend_Download(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FTP integration test in short mode")
	}

	t.Run("download with mocked server", func(t *testing.T) {
		t.Skip("FTP server mocking not yet implemented")
	})
}

func TestFTPBackend_List(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FTP integration test in short mode")
	}

	t.Run("list with mocked server", func(t *testing.T) {
		t.Skip("FTP server mocking not yet implemented")
	})
}

func TestFTPBackend_Delete(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping FTP integration test in short mode")
	}

	t.Run("delete with mocked server", func(t *testing.T) {
		t.Skip("FTP server mocking not yet implemented")
	})
}
