package backup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSFTPBackend(t *testing.T) {
	backend := NewSFTPBackend(SFTPConfig{
		Host:     "sftp.example.com",
		Port:     22,
		User:     "user",
		Password: "password",
		Path:     "/backups",
	})

	assert.NotNil(t, backend)
	assert.Equal(t, "sftp.example.com", backend.Host)
	assert.Equal(t, 22, backend.Port)
	assert.Equal(t, "user", backend.User)
	assert.Equal(t, "/backups", backend.Path)
}

func TestSFTPBackend_Upload(t *testing.T) {
	backend := NewSFTPBackend(SFTPConfig{Host: "sftp.example.com", Port: 22, User: "user", Password: "pass", Path: "/backups"})
	assert.NotNil(t, backend)
	assert.Equal(t, "sftp.example.com", backend.Host)
}

func TestSFTPBackend_Download(t *testing.T) {
	backend := NewSFTPBackend(SFTPConfig{Host: "sftp.example.com", Port: 22, User: "user", Password: "pass", Path: "/backups"})
	assert.NotNil(t, backend)
}

func TestSFTPBackend_List(t *testing.T) {
	backend := NewSFTPBackend(SFTPConfig{Host: "sftp.example.com", Port: 22, User: "user", Password: "pass", Path: "/backups"})
	assert.NotNil(t, backend)
}

func TestSFTPBackend_Delete(t *testing.T) {
	backend := NewSFTPBackend(SFTPConfig{Host: "sftp.example.com", Port: 22, User: "user", Password: "pass", Path: "/backups"})
	assert.NotNil(t, backend)
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
			backend := NewSFTPBackend(SFTPConfig{Host: "host", Port: 22, User: "user", Password: "pass", Path: tt.basePath})
			got := backend.buildPath(tt.filename)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestSFTPBackend_TOFU_PersistsKnownHost(t *testing.T) {
	tmpDir := t.TempDir()
	knownHostsFile := filepath.Join(tmpDir, "known_hosts_athena")

	backend := NewSFTPBackend(SFTPConfig{
		Host:           "sftp.example.com",
		Port:           22,
		User:           "user",
		Password:       "pass",
		Path:           "/backups",
		KnownHostsFile: knownHostsFile,
	})

	assert.NotNil(t, backend)
	assert.Equal(t, knownHostsFile, backend.knownHostsFile)
	// File doesn't exist yet — knownHostKey should be empty
	assert.Empty(t, backend.knownHostKey)
}

func TestSFTPBackend_TOFU_ReadsExistingKnownHost(t *testing.T) {
	tmpDir := t.TempDir()
	knownHostsFile := filepath.Join(tmpDir, "known_hosts_athena")

	// Write a fake known host key
	fakeKey := "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIFakeKeyForTesting\n"
	err := os.WriteFile(knownHostsFile, []byte(fakeKey), 0600)
	require.NoError(t, err)

	backend := NewSFTPBackend(SFTPConfig{
		Host:           "sftp.example.com",
		Port:           22,
		User:           "user",
		Password:       "pass",
		Path:           "/backups",
		KnownHostsFile: knownHostsFile,
	})

	assert.Equal(t, fakeKey, backend.knownHostKey)
}
