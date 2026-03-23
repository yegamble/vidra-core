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
		DatabaseURL:   "postgres://user:pass@localhost:5432/vidra",
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
	assert.Contains(t, contentStr, "DATABASE_URL=postgres://user:pass@localhost:5432/vidra")
	assert.Contains(t, contentStr, "REDIS_MODE=external")
	assert.Contains(t, contentStr, "REDIS_URL=redis://localhost:6379/0")
	assert.Contains(t, contentStr, "IPFS_MODE=external")
	assert.Contains(t, contentStr, "IPFS_API_URL=http://localhost:5001")
}

func TestWriteEnvFileWithNginxHTTP(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		StoragePath:   "./data/storage",
		JWTSecret:     "test-secret-32chars-long-here",
		NginxEnabled:  true,
		NginxDomain:   "localhost",
		NginxPort:     80,
		NginxProtocol: "http",
		NginxTLSMode:  "",
		NginxEmail:    "",
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "NGINX_ENABLED=true")
	assert.Contains(t, contentStr, "NGINX_DOMAIN=localhost")
	assert.Contains(t, contentStr, "NGINX_PORT=80")
	assert.Contains(t, contentStr, "NGINX_PROTOCOL=http")
	assert.Contains(t, contentStr, "NGINX_TLS_MODE=")
	assert.Contains(t, contentStr, "PUBLIC_BASE_URL=http://localhost")
}

func TestWriteEnvFileWithNginxHTTPSSelfsigned(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		StoragePath:   "./data/storage",
		JWTSecret:     "test-secret-32chars-long-here",
		NginxEnabled:  true,
		NginxDomain:   "videos.example.com",
		NginxPort:     443,
		NginxProtocol: "https",
		NginxTLSMode:  "self-signed",
		NginxEmail:    "",
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "NGINX_ENABLED=true")
	assert.Contains(t, contentStr, "NGINX_DOMAIN=videos.example.com")
	assert.Contains(t, contentStr, "NGINX_PORT=443")
	assert.Contains(t, contentStr, "NGINX_PROTOCOL=https")
	assert.Contains(t, contentStr, "NGINX_TLS_MODE=self-signed")
	assert.Contains(t, contentStr, "PUBLIC_BASE_URL=https://videos.example.com")
}

func TestWriteEnvFileWithNginxHTTPSLetsencrypt(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		StoragePath:   "./data/storage",
		JWTSecret:     "test-secret-32chars-long-here",
		NginxEnabled:  true,
		NginxDomain:   "videos.example.com",
		NginxPort:     443,
		NginxProtocol: "https",
		NginxTLSMode:  "letsencrypt",
		NginxEmail:    "admin@example.com",
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "NGINX_ENABLED=true")
	assert.Contains(t, contentStr, "NGINX_DOMAIN=videos.example.com")
	assert.Contains(t, contentStr, "NGINX_PORT=443")
	assert.Contains(t, contentStr, "NGINX_PROTOCOL=https")
	assert.Contains(t, contentStr, "NGINX_TLS_MODE=letsencrypt")
	assert.Contains(t, contentStr, "NGINX_LETSENCRYPT_EMAIL=admin@example.com")
	assert.Contains(t, contentStr, "PUBLIC_BASE_URL=https://videos.example.com")
}

func TestWriteEnvFileWithNginxCustomPort(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		StoragePath:   "./data/storage",
		JWTSecret:     "test-secret-32chars-long-here",
		NginxEnabled:  true,
		NginxDomain:   "example.com",
		NginxPort:     8080,
		NginxProtocol: "http",
		NginxTLSMode:  "",
		NginxEmail:    "",
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "NGINX_PORT=8080")
	assert.Contains(t, contentStr, "PUBLIC_BASE_URL=http://example.com:8080")
}

func TestWriteEnvFileNginxDisabled(t *testing.T) {
	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		StoragePath:   "./data/storage",
		JWTSecret:     "test-secret-32chars-long-here",
		NginxEnabled:  false,
		AdminUsername: "admin",
		AdminEmail:    "admin@example.com",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "NGINX_ENABLED=false")
	assert.NotContains(t, contentStr, "NGINX_DOMAIN=")
	assert.NotContains(t, contentStr, "NGINX_PORT=")
	assert.NotContains(t, contentStr, "NGINX_PROTOCOL=")
	assert.Contains(t, contentStr, "PUBLIC_BASE_URL=http://localhost:8080")
}

func TestWriteEnvFileWithEmailDocker(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:    "docker",
		RedisMode:       "docker",
		EnableEmail:     true,
		SMTPHost:        "localhost",
		SMTPPort:        1025,
		SMTPFromAddress: "noreply@localhost",
		SMTPFromName:    "Vidra Core",
		StoragePath:     "./data/storage",
		JWTSecret:       "test-secret-at-least-32-characters",
		NginxEnabled:    false,
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "ENABLE_EMAIL=true")
	assert.Contains(t, contentStr, "SMTP_TRANSPORT=smtp")
	assert.Contains(t, contentStr, "SMTP_HOST=localhost")
	assert.Contains(t, contentStr, "SMTP_PORT=1025")
	assert.Contains(t, contentStr, "SMTP_FROM=noreply@localhost")
	assert.Contains(t, contentStr, "SMTP_FROM_NAME=Vidra Core")
	assert.Contains(t, contentStr, "SMTP_TLS=false")
}

func TestWriteEnvFileWithEmailExternal(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode:        "docker",
		RedisMode:           "docker",
		EnableEmail:         true,
		SMTPHost:            "smtp.mailgun.org",
		SMTPPort:            587,
		SMTPUsername:        "postmaster@example.com",
		SMTPPassword:        "secret",
		SMTPTLS:             false,
		SMTPDisableSTARTTLS: false,
		SMTPFromAddress:     "noreply@example.com",
		SMTPFromName:        "My Platform",
		StoragePath:         "./data/storage",
		JWTSecret:           "test-secret-at-least-32-characters",
		NginxEnabled:        false,
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "ENABLE_EMAIL=true")
	assert.Contains(t, contentStr, "SMTP_HOST=smtp.mailgun.org")
	assert.Contains(t, contentStr, "SMTP_PORT=587")
	assert.Contains(t, contentStr, "SMTP_USERNAME=postmaster@example.com")
	assert.Contains(t, contentStr, "SMTP_PASSWORD=secret")
	assert.Contains(t, contentStr, "SMTP_FROM=noreply@example.com")
	assert.Contains(t, contentStr, "SMTP_FROM_NAME=My Platform")
}

func TestWriteEnvFileEmailDisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		EnableEmail:  false,
		StoragePath:  "./data/storage",
		JWTSecret:    "test-secret-at-least-32-characters",
		NginxEnabled: false,
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "ENABLE_EMAIL=false")
	assert.NotContains(t, contentStr, "SMTP_HOST=")
}

func TestWriteEnvFileIOTADocker(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		JWTSecret:    "test-jwt-secret-32chars-long",
		EnableIOTA:   true,
		IOTAMode:     "docker",
		IOTANetwork:  "testnet",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "ENABLE_IOTA=true")
	assert.Contains(t, contentStr, "IOTA_MODE=docker")
	assert.Contains(t, contentStr, "IOTA_NETWORK=testnet")
	assert.Contains(t, contentStr, "IOTA_NODE_URL=http://iota-node:9000")
	assert.Contains(t, contentStr, "IOTA_WALLET_ENCRYPTION_KEY=")
	assert.NotContains(t, contentStr, "IOTA_WALLET_ENCRYPTION_KEY=\n")
}

func TestWriteEnvFileIOTAExternal(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		JWTSecret:    "test-jwt-secret-32chars-long",
		EnableIOTA:   true,
		IOTAMode:     "external",
		IOTANodeURL:  "http://my-iota-node.example.com:14265",
		IOTANetwork:  "mainnet",
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "ENABLE_IOTA=true")
	assert.Contains(t, contentStr, "IOTA_MODE=external")
	assert.Contains(t, contentStr, "IOTA_NODE_URL=http://my-iota-node.example.com:14265")
	assert.Contains(t, contentStr, "IOTA_NETWORK=mainnet")
}

func TestWriteEnvFileIOTADisabled(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping file I/O test in short mode")
	}

	tmpDir := t.TempDir()
	envPath := filepath.Join(tmpDir, ".env")

	config := &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		JWTSecret:    "test-jwt-secret-32chars-long",
		EnableIOTA:   false,
	}

	err := WriteEnvFile(envPath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(envPath)
	require.NoError(t, err)
	contentStr := string(content)

	assert.Contains(t, contentStr, "ENABLE_IOTA=false")
	assert.NotContains(t, contentStr, "IOTA_NODE_URL=")
	assert.NotContains(t, contentStr, "IOTA_WALLET_ENCRYPTION_KEY=")
}
