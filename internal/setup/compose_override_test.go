package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteComposeOverride_AllDocker(t *testing.T) {
	// Setup: temporary directory for test file
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")

	config := &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
	}

	// Execute
	err := WriteComposeOverride(overridePath, config)

	// Assert
	require.NoError(t, err, "WriteComposeOverride should succeed")

	// Verify file exists
	_, statErr := os.Stat(overridePath)
	require.NoError(t, statErr, "Override file should exist")

	// Read and verify content
	content, readErr := os.ReadFile(overridePath)
	require.NoError(t, readErr, "Should read override file")

	expectedContent := `services:
  app:
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
`

	assert.Equal(t, expectedContent, string(content), "Override should only include app depends_on for docker mode")
}

func TestWriteComposeOverride_PostgresExternal(t *testing.T) {
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")

	config := &WizardConfig{
		PostgresMode: "external",
		RedisMode:    "docker",
	}

	err := WriteComposeOverride(overridePath, config)
	require.NoError(t, err)

	content, readErr := os.ReadFile(overridePath)
	require.NoError(t, readErr)

	expectedContent := `services:
  postgres:
    profiles: ["disabled"]
  app:
    depends_on:
      redis:
        condition: service_healthy
`

	assert.Equal(t, expectedContent, string(content), "Postgres disabled, app depends only on redis")
}

func TestWriteComposeOverride_RedisExternal(t *testing.T) {
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")

	config := &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "external",
	}

	err := WriteComposeOverride(overridePath, config)
	require.NoError(t, err)

	content, readErr := os.ReadFile(overridePath)
	require.NoError(t, readErr)

	expectedContent := `services:
  redis:
    profiles: ["disabled"]
  app:
    depends_on:
      postgres:
        condition: service_healthy
`

	assert.Equal(t, expectedContent, string(content), "Redis disabled, app depends only on postgres")
}

func TestWriteComposeOverride_BothExternal(t *testing.T) {
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")

	config := &WizardConfig{
		PostgresMode: "external",
		RedisMode:    "external",
	}

	err := WriteComposeOverride(overridePath, config)
	require.NoError(t, err)

	content, readErr := os.ReadFile(overridePath)
	require.NoError(t, readErr)

	expectedContent := `services:
  postgres:
    profiles: ["disabled"]
  redis:
    profiles: ["disabled"]
  app:
    depends_on: {}
`

	assert.Equal(t, expectedContent, string(content), "Both disabled, app has no dependencies")
}

func TestWriteComposeOverride_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")

	config := &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
	}

	// Write initial content
	initialContent := []byte("initial content")
	require.NoError(t, os.WriteFile(overridePath, initialContent, 0600))

	// Execute
	err := WriteComposeOverride(overridePath, config)
	require.NoError(t, err)

	// Verify temp file was cleaned up
	tmpPath := overridePath + ".tmp"
	_, tmpErr := os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(tmpErr), "Temp file should be cleaned up after rename")

	// Verify new content replaced old
	content, readErr := os.ReadFile(overridePath)
	require.NoError(t, readErr)
	assert.NotEqual(t, string(initialContent), string(content), "Content should be replaced atomically")
}
