package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteEnvFile(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:    "docker",
		DatabaseURL:     "",
		RedisMode:       "docker",
		RedisURL:        "",
		EnableIPFS:      true,
		IPFSMode:        "docker",
		EnableClamAV:    false,
		EnableWhisper:   false,
		StoragePath:     "./data/storage",
		BackupEnabled:   true,
		BackupTarget:    "local",
		BackupSchedule:  "0 2 * * *",
		BackupRetention: "7",
		BackupLocalPath: "./backups",
		JWTSecret:       "test-jwt-secret-32chars-long",
		AdminUsername:   "admin",
		AdminEmail:      "admin@example.com",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	_, err = os.Stat(envPath)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "POSTGRES_MODE=docker")
	assert.Contains(t, contentStr, "REDIS_MODE=docker")
	assert.Contains(t, contentStr, "ENABLE_IPFS=true")
	assert.Contains(t, contentStr, "ENABLE_CLAMAV=false")
	assert.Contains(t, contentStr, "JWT_SECRET=test-jwt-secret-32chars-long")
	assert.Contains(t, contentStr, "SETUP_COMPLETED=true")
}

func TestWriteEnvFileWithExternalServices(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "external",
		DatabaseURL:   "postgres://user:pass@localhost:5432/athena",
		RedisMode:     "external",
		RedisURL:      "redis://localhost:6379/0",
		EnableIPFS:    true,
		IPFSMode:      "external",
		IPFSAPIUrl:    "http://localhost:5001",
		StoragePath:   "./data/storage",
		JWTSecret:     "another-test-secret-32chars",
		AdminUsername: "root",
		AdminEmail:    "root@example.com",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "POSTGRES_MODE=external")
	assert.Contains(t, contentStr, "DATABASE_URL=postgres://user:pass@localhost:5432/athena")
	assert.Contains(t, contentStr, "REDIS_MODE=external")
	assert.Contains(t, contentStr, "REDIS_URL=redis://localhost:6379/0")
	assert.Contains(t, contentStr, "IPFS_MODE=external")
	assert.Contains(t, contentStr, "IPFS_API_URL=http://localhost:5001")
}
