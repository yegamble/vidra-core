package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestWriteEnvFile_ComposeProfiles_AllDockerServices(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		EnableIPFS:    true,
		IPFSMode:      "docker",
		EnableIOTA:    true,
		IOTAMode:      "docker",
		IOTANetwork:   "testnet",
		EnableClamAV:  true,
		EnableWhisper: true,
		EnableEmail:   true,
		SMTPMode:      "docker",
		SMTPHost:      "localhost",
		JWTSecret:     "test-secret",
		StoragePath:   "/data",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, readErr := os.ReadFile(envPath)
	require.NoError(t, readErr)

	// Verify COMPOSE_PROFILES includes all active Docker services
	// Expected: ipfs, iota, media (for ClamAV and Whisper), mail (for Mailpit)
	// Note: "media" appears once even though both ClamAV and Whisper use it
	assert.Contains(t, string(content), "COMPOSE_PROFILES=")

	// Parse the COMPOSE_PROFILES line
	lines := strings.Split(string(content), "\n")
	var profilesLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "COMPOSE_PROFILES=") {
			profilesLine = strings.TrimPrefix(line, "COMPOSE_PROFILES=")
			break
		}
	}

	require.NotEmpty(t, profilesLine, "COMPOSE_PROFILES should be set")

	profiles := strings.Split(profilesLine, ",")
	profilesMap := make(map[string]bool)
	for _, p := range profiles {
		profilesMap[p] = true
	}

	// Verify expected profiles are present
	assert.True(t, profilesMap["ipfs"], "Should include ipfs profile")
	assert.True(t, profilesMap["iota"], "Should include iota profile")
	assert.True(t, profilesMap["media"], "Should include media profile")
	assert.True(t, profilesMap["mail"], "Should include mail profile")
}

func TestWriteEnvFile_ComposeProfiles_ExternalIPFS(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		EnableIPFS:    true,
		IPFSMode:      "external", // External mode - should not activate ipfs profile
		IPFSAPIUrl:    "http://external-ipfs:5001",
		EnableIOTA:    true,
		IOTAMode:      "docker",
		IOTANetwork:   "testnet",
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   false,
		JWTSecret:     "test-secret",
		StoragePath:   "/data",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, readErr := os.ReadFile(envPath)
	require.NoError(t, readErr)

	lines := strings.Split(string(content), "\n")
	var profilesLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "COMPOSE_PROFILES=") {
			profilesLine = strings.TrimPrefix(line, "COMPOSE_PROFILES=")
			break
		}
	}

	require.NotEmpty(t, profilesLine, "COMPOSE_PROFILES should be set")

	// Should only have iota, not ipfs (since IPFS is external)
	assert.Equal(t, "iota", profilesLine)
}

func TestWriteEnvFile_ComposeProfiles_NoOptionalServices(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		EnableIPFS:    false,
		EnableIOTA:    false,
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   false,
		JWTSecret:     "test-secret",
		StoragePath:   "/data",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, readErr := os.ReadFile(envPath)
	require.NoError(t, readErr)

	// With no optional services, COMPOSE_PROFILES= should be present but empty
	// This ensures any previous value is cleared on re-run
	lines := strings.Split(string(content), "\n")
	foundProfilesLine := false
	for _, line := range lines {
		if strings.HasPrefix(line, "COMPOSE_PROFILES=") {
			foundProfilesLine = true
			profilesLine := strings.TrimPrefix(line, "COMPOSE_PROFILES=")
			assert.Empty(t, profilesLine, "COMPOSE_PROFILES should be empty when no optional services enabled")
			break
		}
	}

	assert.True(t, foundProfilesLine, "COMPOSE_PROFILES= line should always be present")
}

func TestWriteEnvFile_ComposeProfiles_ExternalSMTP(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		EnableIPFS:    false,
		EnableIOTA:    false,
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   true,
		SMTPMode:      "external",
		SMTPHost:      "smtp.gmail.com",
		SMTPPort:      587,
		JWTSecret:     "test-secret",
		StoragePath:   "/data",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, readErr := os.ReadFile(envPath)
	require.NoError(t, readErr)

	// Should NOT include "mail" profile since using external SMTP
	lines := strings.Split(string(content), "\n")
	for _, line := range lines {
		if strings.HasPrefix(line, "COMPOSE_PROFILES=") {
			profilesLine := strings.TrimPrefix(line, "COMPOSE_PROFILES=")
			assert.NotContains(t, profilesLine, "mail", "Should not include mail profile for external SMTP")
		}
	}
}

func TestWriteEnvFile_ComposeProfiles_MediaProfileDeduplication(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		EnableIPFS:    false,
		EnableIOTA:    false,
		EnableClamAV:  true, // Both use "media" profile
		EnableWhisper: true, // Both use "media" profile
		EnableEmail:   false,
		JWTSecret:     "test-secret",
		StoragePath:   "/data",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, readErr := os.ReadFile(envPath)
	require.NoError(t, readErr)

	lines := strings.Split(string(content), "\n")
	var profilesLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "COMPOSE_PROFILES=") {
			profilesLine = strings.TrimPrefix(line, "COMPOSE_PROFILES=")
			break
		}
	}

	require.NotEmpty(t, profilesLine, "COMPOSE_PROFILES should be set")

	// Should only have "media" once, not twice
	assert.Equal(t, "media", profilesLine, "Should deduplicate media profile")
}

func TestWriteEnvFile_ComposeProfiles_LetsEncrypt(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		EnableIPFS:    false,
		EnableIOTA:    false,
		EnableClamAV:  false,
		EnableEmail:   false,
		NginxEnabled:  true,
		NginxTLSMode:  "letsencrypt",
		NginxDomain:   "example.com",
		NginxPort:     443,
		NginxProtocol: "https",
		JWTSecret:     "test-secret",
		StoragePath:   "/data",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, readErr := os.ReadFile(envPath)
	require.NoError(t, readErr)

	lines := strings.Split(string(content), "\n")
	var profilesLine string
	for _, line := range lines {
		if strings.HasPrefix(line, "COMPOSE_PROFILES=") {
			profilesLine = strings.TrimPrefix(line, "COMPOSE_PROFILES=")
			break
		}
	}

	require.NotEmpty(t, profilesLine, "COMPOSE_PROFILES should be set")
	assert.Equal(t, "letsencrypt", profilesLine, "Should include letsencrypt profile")
}
