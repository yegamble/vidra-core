package setup

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateNginxConfigHTTP(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "nginx-conf")

	config := &WizardConfig{
		NginxDomain:   "localhost",
		NginxPort:     80,
		NginxProtocol: "http",
		NginxTLSMode:  "",
	}

	err := GenerateNginxConfig(config, outputDir)
	require.NoError(t, err)

	_, err = os.Stat(outputDir)
	require.NoError(t, err)

	mainConf := filepath.Join(outputDir, "default.conf")
	content, err := os.ReadFile(mainConf)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "server_name localhost")
	assert.Contains(t, contentStr, "listen 80")
	assert.Contains(t, contentStr, "server app:8080")
	assert.Contains(t, contentStr, "proxy_pass http://athena_app")
	assert.NotContains(t, contentStr, "ssl_certificate")
	assert.Contains(t, contentStr, "include /etc/nginx/conf.d/common-security.conf")
	assert.Contains(t, contentStr, "include /etc/nginx/conf.d/common-proxy.conf")

	securityConf := filepath.Join(outputDir, "security.conf")
	securityContent, err := os.ReadFile(securityConf)
	require.NoError(t, err)
	assert.Contains(t, string(securityContent), "X-Frame-Options")
	assert.Contains(t, string(securityContent), "X-Content-Type-Options")

	proxyConf := filepath.Join(outputDir, "proxy.conf")
	proxyContent, err := os.ReadFile(proxyConf)
	require.NoError(t, err)
	assert.Contains(t, string(proxyContent), "proxy_http_version")
	assert.Contains(t, string(proxyContent), "proxy_set_header")
}

func TestGenerateNginxConfigHTTPSSelfsigned(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "nginx-conf")

	config := &WizardConfig{
		NginxDomain:   "videos.example.com",
		NginxPort:     443,
		NginxProtocol: "https",
		NginxTLSMode:  "self-signed",
	}

	err := GenerateNginxConfig(config, outputDir)
	require.NoError(t, err)

	mainConf := filepath.Join(outputDir, "default.conf")
	content, err := os.ReadFile(mainConf)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "server_name videos.example.com")
	assert.Contains(t, contentStr, "listen 443 ssl")
	assert.Contains(t, contentStr, "ssl_certificate /etc/nginx/ssl/self-signed.crt")
	assert.Contains(t, contentStr, "ssl_certificate_key /etc/nginx/ssl/self-signed.key")
	assert.Contains(t, contentStr, "add_header Strict-Transport-Security")
	assert.Contains(t, contentStr, "server app:8080")
	assert.Contains(t, contentStr, "proxy_pass http://athena_app")
	assert.Contains(t, contentStr, "listen 80")
	assert.Contains(t, contentStr, "return 301 https://")
}

func TestGenerateNginxConfigHTTPSLetsencrypt(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "nginx-conf")

	config := &WizardConfig{
		NginxDomain:   "videos.example.com",
		NginxPort:     443,
		NginxProtocol: "https",
		NginxTLSMode:  "letsencrypt",
		NginxEmail:    "admin@example.com",
	}

	err := GenerateNginxConfig(config, outputDir)
	require.NoError(t, err)

	mainConf := filepath.Join(outputDir, "default.conf")
	content, err := os.ReadFile(mainConf)
	require.NoError(t, err)

	contentStr := string(content)
	assert.Contains(t, contentStr, "server_name videos.example.com")
	assert.Contains(t, contentStr, "listen 443 ssl")
	assert.Contains(t, contentStr, "ssl_certificate /etc/letsencrypt/live/videos.example.com/fullchain.pem")
	assert.Contains(t, contentStr, "ssl_certificate_key /etc/letsencrypt/live/videos.example.com/privkey.pem")
	assert.Contains(t, contentStr, "location /.well-known/acme-challenge/")
	assert.Contains(t, contentStr, "root /var/www/certbot")
	assert.Contains(t, contentStr, "add_header Strict-Transport-Security")
	assert.Contains(t, contentStr, "server app:8080")
	assert.Contains(t, contentStr, "proxy_pass http://athena_app")
}

func TestGenerateNginxConfigCreatesOutputDir(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "nested", "output", "dir")

	config := &WizardConfig{
		NginxDomain:   "localhost",
		NginxPort:     80,
		NginxProtocol: "http",
	}

	err := GenerateNginxConfig(config, outputDir)
	require.NoError(t, err)

	info, err := os.Stat(outputDir)
	require.NoError(t, err)
	assert.True(t, info.IsDir())

	mainConf := filepath.Join(outputDir, "default.conf")
	_, err = os.Stat(mainConf)
	require.NoError(t, err)
}

func TestGenerateNginxConfigInvalidMode(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "nginx-conf")

	config := &WizardConfig{
		NginxDomain:   "localhost",
		NginxPort:     80,
		NginxProtocol: "invalid-protocol",
		NginxTLSMode:  "",
	}

	err := GenerateNginxConfig(config, outputDir)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown nginx protocol")
}

func TestGenerateNginxConfigPlaceholderReplacement(t *testing.T) {
	tmpDir := t.TempDir()
	outputDir := filepath.Join(tmpDir, "nginx-conf")

	config := &WizardConfig{
		NginxDomain:   "custom.domain.com",
		NginxPort:     8443,
		NginxProtocol: "https",
		NginxTLSMode:  "self-signed",
	}

	err := GenerateNginxConfig(config, outputDir)
	require.NoError(t, err)

	mainConf := filepath.Join(outputDir, "default.conf")
	content, err := os.ReadFile(mainConf)
	require.NoError(t, err)

	contentStr := string(content)
	assert.NotContains(t, contentStr, "{{.Domain}}")
	assert.NotContains(t, contentStr, "{{.Port}}")
	assert.NotContains(t, contentStr, "{{.UpstreamAddr}}")

	assert.Contains(t, contentStr, "custom.domain.com")
	assert.Contains(t, contentStr, "server app:8080")
}
