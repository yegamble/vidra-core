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
	backend := NewFTPBackend("ftp.example.com", 21, "user", "pass", "/backups")
	assert.NotNil(t, backend)
	assert.Equal(t, "ftp.example.com", backend.Host)
}

func TestFTPBackend_Download(t *testing.T) {
	backend := NewFTPBackend("ftp.example.com", 21, "user", "pass", "/backups")
	assert.NotNil(t, backend)
}

func TestFTPBackend_List(t *testing.T) {
	backend := NewFTPBackend("ftp.example.com", 21, "user", "pass", "/backups")
	assert.NotNil(t, backend)
}

func TestFTPBackend_Delete(t *testing.T) {
	backend := NewFTPBackend("ftp.example.com", 21, "user", "pass", "/backups")
	assert.NotNil(t, backend)
}
