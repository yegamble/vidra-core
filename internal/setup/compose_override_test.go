package setup

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Helper: parse override YAML into a simple structure for assertions
// ---------------------------------------------------------------------------

type overrideResult struct {
	disabledServices []string            // services with profiles: ["disabled"]
	appDependsOn     map[string]string   // service -> condition
	appNoDeps        bool                // true when depends_on: {}
	raw              string              // full file content
}

func parseOverride(t *testing.T, content string) overrideResult {
	t.Helper()
	result := overrideResult{
		appDependsOn: make(map[string]string),
		raw:          content,
	}

	lines := strings.Split(content, "\n")
	var currentService string
	inAppDeps := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		// Top-level service definition (2-space indent, ends with colon)
		if strings.HasPrefix(line, "  ") && !strings.HasPrefix(line, "    ") && strings.HasSuffix(trimmed, ":") {
			svcName := strings.TrimSuffix(trimmed, ":")
			currentService = svcName
			inAppDeps = false
			continue
		}

		// Check for profiles: ["disabled"]
		if strings.Contains(line, `profiles: ["disabled"]`) && currentService != "" && currentService != "app" {
			result.disabledServices = append(result.disabledServices, currentService)
			continue
		}

		// App depends_on handling
		if currentService == "app" {
			if strings.Contains(line, "depends_on: {}") {
				result.appNoDeps = true
				continue
			}
			if strings.Contains(line, "depends_on:") && !strings.Contains(line, "{}") {
				inAppDeps = true
				continue
			}
			if inAppDeps && strings.HasPrefix(line, "      ") && strings.HasSuffix(trimmed, ":") {
				depName := strings.TrimSuffix(trimmed, ":")
				// Look ahead for condition
				if i+1 < len(lines) && strings.Contains(lines[i+1], "condition:") {
					cond := strings.TrimSpace(strings.SplitN(lines[i+1], ":", 2)[1])
					result.appDependsOn[depName] = cond
				}
			}
		}
	}

	return result
}

func runOverride(t *testing.T, config *WizardConfig) overrideResult {
	t.Helper()
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")

	err := WriteComposeOverride(overridePath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(overridePath)
	require.NoError(t, err)

	return parseOverride(t, string(content))
}

// ---------------------------------------------------------------------------
// Scenario 1: All Docker — nothing disabled
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_AllDocker(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		NginxEnabled:  true,
		EnableIPFS:    true,
		IPFSMode:      "docker",
		EnableIOTA:    true,
		IOTAMode:      "docker",
		EnableClamAV:  true,
		EnableWhisper: true,
		EnableEmail:   true,
		SMTPMode:      "docker",
	})

	assert.Empty(t, result.disabledServices, "No services should be disabled when all are docker")
	assert.Equal(t, "service_healthy", result.appDependsOn["postgres"])
	assert.Equal(t, "service_healthy", result.appDependsOn["redis"])
	assert.Len(t, result.appDependsOn, 2)
}

// ---------------------------------------------------------------------------
// Scenario 2: All External — everything disabled, no app deps
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_AllExternal(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "external",
		RedisMode:     "external",
		NginxEnabled:  false,
		EnableIPFS:    true,
		IPFSMode:      "external",
		EnableIOTA:    true,
		IOTAMode:      "external",
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   true,
		SMTPMode:      "external",
	})

	assert.Contains(t, result.disabledServices, "postgres")
	assert.Contains(t, result.disabledServices, "redis")
	assert.Contains(t, result.disabledServices, "nginx")
	assert.Contains(t, result.disabledServices, "ipfs")
	assert.Contains(t, result.disabledServices, "iota-node")
	assert.Contains(t, result.disabledServices, "clamav")
	assert.Contains(t, result.disabledServices, "whisper")
	assert.Contains(t, result.disabledServices, "mailpit")
	assert.True(t, result.appNoDeps, "App should have no dependencies")
}

// ---------------------------------------------------------------------------
// Scenario 3: Minimal — only required services, all optional disabled
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_Minimal(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		NginxEnabled:  true,
		EnableIPFS:    false,
		EnableIOTA:    false,
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   false,
	})

	assert.Contains(t, result.disabledServices, "ipfs")
	assert.Contains(t, result.disabledServices, "iota-node")
	assert.Contains(t, result.disabledServices, "clamav")
	assert.Contains(t, result.disabledServices, "whisper")
	assert.Contains(t, result.disabledServices, "mailpit")
	assert.NotContains(t, result.disabledServices, "postgres")
	assert.NotContains(t, result.disabledServices, "redis")
	assert.NotContains(t, result.disabledServices, "nginx")
	assert.Equal(t, "service_healthy", result.appDependsOn["postgres"])
	assert.Equal(t, "service_healthy", result.appDependsOn["redis"])
}

// ---------------------------------------------------------------------------
// Scenario 4: Core Docker + optional external
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_CoreDockerOptionalExternal(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		NginxEnabled:  true,
		EnableIPFS:    true,
		IPFSMode:      "external",
		EnableIOTA:    true,
		IOTAMode:      "external",
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   true,
		SMTPMode:      "external",
	})

	assert.Contains(t, result.disabledServices, "ipfs")
	assert.Contains(t, result.disabledServices, "iota-node")
	assert.Contains(t, result.disabledServices, "clamav")
	assert.Contains(t, result.disabledServices, "whisper")
	assert.Contains(t, result.disabledServices, "mailpit")
	assert.NotContains(t, result.disabledServices, "postgres")
	assert.NotContains(t, result.disabledServices, "redis")
	assert.NotContains(t, result.disabledServices, "nginx")
}

// ---------------------------------------------------------------------------
// Scenario 5: Core external + optional docker
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_CoreExternalOptionalDocker(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "external",
		RedisMode:     "external",
		NginxEnabled:  true,
		EnableIPFS:    true,
		IPFSMode:      "docker",
		EnableIOTA:    true,
		IOTAMode:      "docker",
		EnableClamAV:  true,
		EnableWhisper: true,
		EnableEmail:   true,
		SMTPMode:      "docker",
	})

	assert.Contains(t, result.disabledServices, "postgres")
	assert.Contains(t, result.disabledServices, "redis")
	assert.NotContains(t, result.disabledServices, "ipfs")
	assert.NotContains(t, result.disabledServices, "iota-node")
	assert.NotContains(t, result.disabledServices, "clamav")
	assert.NotContains(t, result.disabledServices, "whisper")
	assert.NotContains(t, result.disabledServices, "mailpit")
	assert.NotContains(t, result.disabledServices, "nginx")
	assert.True(t, result.appNoDeps, "App should have no docker dependencies when core is external")
}

// ---------------------------------------------------------------------------
// Scenario 6: Mixed — postgres docker, redis external, ipfs docker, rest off
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_MixedPostgresDockerRedisExternal(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "external",
		NginxEnabled:  true,
		EnableIPFS:    true,
		IPFSMode:      "docker",
		EnableIOTA:    false,
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   false,
	})

	assert.Contains(t, result.disabledServices, "redis")
	assert.Contains(t, result.disabledServices, "iota-node")
	assert.Contains(t, result.disabledServices, "clamav")
	assert.Contains(t, result.disabledServices, "whisper")
	assert.Contains(t, result.disabledServices, "mailpit")
	assert.NotContains(t, result.disabledServices, "postgres")
	assert.NotContains(t, result.disabledServices, "ipfs")
	assert.Equal(t, "service_healthy", result.appDependsOn["postgres"])
	assert.Len(t, result.appDependsOn, 1, "Only postgres should be an app dependency")
}

// ---------------------------------------------------------------------------
// Scenario 7: Mixed — postgres external, redis docker, iota docker, rest off
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_MixedPostgresExternalRedisDocker(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "external",
		RedisMode:     "docker",
		NginxEnabled:  true,
		EnableIPFS:    false,
		EnableIOTA:    true,
		IOTAMode:      "docker",
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   false,
	})

	assert.Contains(t, result.disabledServices, "postgres")
	assert.Contains(t, result.disabledServices, "ipfs")
	assert.Contains(t, result.disabledServices, "clamav")
	assert.Contains(t, result.disabledServices, "whisper")
	assert.Contains(t, result.disabledServices, "mailpit")
	assert.NotContains(t, result.disabledServices, "redis")
	assert.NotContains(t, result.disabledServices, "iota-node")
	assert.Equal(t, "service_healthy", result.appDependsOn["redis"])
	assert.Len(t, result.appDependsOn, 1, "Only redis should be an app dependency")
}

// ---------------------------------------------------------------------------
// Scenario 8: Production-like — all external, no docker containers needed
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_ProductionLike(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "external",
		RedisMode:     "external",
		NginxEnabled:  false, // external reverse proxy (e.g., Cloudflare, Caddy)
		EnableIPFS:    true,
		IPFSMode:      "external",
		EnableIOTA:    false,
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   true,
		SMTPMode:      "external",
	})

	// All containers should be disabled
	assert.Contains(t, result.disabledServices, "postgres")
	assert.Contains(t, result.disabledServices, "redis")
	assert.Contains(t, result.disabledServices, "nginx")
	assert.Contains(t, result.disabledServices, "ipfs")
	assert.Contains(t, result.disabledServices, "iota-node")
	assert.Contains(t, result.disabledServices, "clamav")
	assert.Contains(t, result.disabledServices, "whisper")
	assert.Contains(t, result.disabledServices, "mailpit")
	assert.True(t, result.appNoDeps)
}

// ---------------------------------------------------------------------------
// Scenario 9: Dev minimal — core docker + mailpit only
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_DevMinimal(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		NginxEnabled:  true,
		EnableIPFS:    false,
		EnableIOTA:    false,
		EnableClamAV:  false,
		EnableWhisper: false,
		EnableEmail:   true,
		SMTPMode:      "docker",
	})

	assert.Contains(t, result.disabledServices, "ipfs")
	assert.Contains(t, result.disabledServices, "iota-node")
	assert.Contains(t, result.disabledServices, "clamav")
	assert.Contains(t, result.disabledServices, "whisper")
	assert.NotContains(t, result.disabledServices, "mailpit", "Mailpit should be active for docker email")
	assert.NotContains(t, result.disabledServices, "postgres")
	assert.NotContains(t, result.disabledServices, "redis")
	assert.NotContains(t, result.disabledServices, "nginx")
}

// ---------------------------------------------------------------------------
// Scenario 10: Media stack — core docker + IPFS + ClamAV + Whisper
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_MediaStack(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		NginxEnabled:  true,
		EnableIPFS:    true,
		IPFSMode:      "docker",
		EnableIOTA:    false,
		EnableClamAV:  true,
		EnableWhisper: true,
		EnableEmail:   false,
	})

	assert.Contains(t, result.disabledServices, "iota-node")
	assert.Contains(t, result.disabledServices, "mailpit")
	assert.NotContains(t, result.disabledServices, "postgres")
	assert.NotContains(t, result.disabledServices, "redis")
	assert.NotContains(t, result.disabledServices, "nginx")
	assert.NotContains(t, result.disabledServices, "ipfs")
	assert.NotContains(t, result.disabledServices, "clamav")
	assert.NotContains(t, result.disabledServices, "whisper")
}

// ---------------------------------------------------------------------------
// Individual service toggle tests
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_PostgresExternal(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "external",
		RedisMode:    "docker",
		NginxEnabled: true,
	})

	assert.Contains(t, result.disabledServices, "postgres")
	assert.NotContains(t, result.disabledServices, "redis")
	assert.Equal(t, "service_healthy", result.appDependsOn["redis"])
	assert.Len(t, result.appDependsOn, 1)
}

func TestWriteComposeOverride_RedisExternal(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "external",
		NginxEnabled: true,
	})

	assert.Contains(t, result.disabledServices, "redis")
	assert.NotContains(t, result.disabledServices, "postgres")
	assert.Equal(t, "service_healthy", result.appDependsOn["postgres"])
	assert.Len(t, result.appDependsOn, 1)
}

func TestWriteComposeOverride_BothCoreExternal(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "external",
		RedisMode:    "external",
		NginxEnabled: true,
	})

	assert.Contains(t, result.disabledServices, "postgres")
	assert.Contains(t, result.disabledServices, "redis")
	assert.True(t, result.appNoDeps)
}

func TestWriteComposeOverride_NginxDisabled(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		NginxEnabled: false,
	})

	assert.Contains(t, result.disabledServices, "nginx")
	assert.NotContains(t, result.disabledServices, "postgres")
	assert.NotContains(t, result.disabledServices, "redis")
}

func TestWriteComposeOverride_IPFSExternalDisablesDocker(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		NginxEnabled: true,
		EnableIPFS:   true,
		IPFSMode:     "external",
	})

	assert.Contains(t, result.disabledServices, "ipfs", "IPFS docker should be disabled when mode is external")
}

func TestWriteComposeOverride_IPFSDisabledDisablesDocker(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		NginxEnabled: true,
		EnableIPFS:   false,
	})

	assert.Contains(t, result.disabledServices, "ipfs", "IPFS docker should be disabled when feature is off")
}

func TestWriteComposeOverride_IOTAExternalDisablesDocker(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		NginxEnabled: true,
		EnableIOTA:   true,
		IOTAMode:     "external",
	})

	assert.Contains(t, result.disabledServices, "iota-node", "IOTA docker should be disabled when mode is external")
}

func TestWriteComposeOverride_IOTADisabledDisablesDocker(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		NginxEnabled: true,
		EnableIOTA:   false,
	})

	assert.Contains(t, result.disabledServices, "iota-node", "IOTA docker should be disabled when feature is off")
}

func TestWriteComposeOverride_EmailExternalDisablesMailpit(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		NginxEnabled: true,
		EnableEmail:  true,
		SMTPMode:     "external",
	})

	assert.Contains(t, result.disabledServices, "mailpit", "Mailpit should be disabled for external SMTP")
}

func TestWriteComposeOverride_EmailDisabledDisablesMailpit(t *testing.T) {
	result := runOverride(t, &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		NginxEnabled: true,
		EnableEmail:  false,
	})

	assert.Contains(t, result.disabledServices, "mailpit", "Mailpit should be disabled when email is off")
}

// ---------------------------------------------------------------------------
// Atomic write behavior
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_AtomicWrite(t *testing.T) {
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")

	config := &WizardConfig{
		PostgresMode: "docker",
		RedisMode:    "docker",
		NginxEnabled: true,
	}

	// Write initial content
	initialContent := []byte("initial content")
	require.NoError(t, os.WriteFile(overridePath, initialContent, 0600))

	err := WriteComposeOverride(overridePath, config)
	require.NoError(t, err)

	// Verify temp file was cleaned up
	tmpPath := overridePath + ".tmp"
	_, tmpErr := os.Stat(tmpPath)
	assert.True(t, os.IsNotExist(tmpErr), "Temp file should be cleaned up after rename")

	content, readErr := os.ReadFile(overridePath)
	require.NoError(t, readErr)
	assert.NotEqual(t, string(initialContent), string(content), "Content should be replaced atomically")
}

// ---------------------------------------------------------------------------
// Table-driven: all single-service toggle permutations
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_ServiceToggleMatrix(t *testing.T) {
	tests := []struct {
		name             string
		config           *WizardConfig
		wantDisabled     []string
		wantNotDisabled  []string
		wantAppDeps      int
		wantAppNoDeps    bool
	}{
		{
			name: "IPFS docker mode - not disabled",
			config: &WizardConfig{
				PostgresMode: "docker", RedisMode: "docker", NginxEnabled: true,
				EnableIPFS: true, IPFSMode: "docker",
			},
			wantNotDisabled: []string{"ipfs"},
			wantAppDeps:     2,
		},
		{
			name: "IOTA docker + IPFS external",
			config: &WizardConfig{
				PostgresMode: "docker", RedisMode: "docker", NginxEnabled: true,
				EnableIPFS: true, IPFSMode: "external",
				EnableIOTA: true, IOTAMode: "docker",
			},
			wantDisabled:    []string{"ipfs"},
			wantNotDisabled: []string{"iota-node"},
			wantAppDeps:     2,
		},
		{
			name: "ClamAV on, Whisper off",
			config: &WizardConfig{
				PostgresMode: "docker", RedisMode: "docker", NginxEnabled: true,
				EnableClamAV: true, EnableWhisper: false,
			},
			wantNotDisabled: []string{"clamav"},
			wantDisabled:    []string{"whisper"},
			wantAppDeps:     2,
		},
		{
			name: "Whisper on, ClamAV off",
			config: &WizardConfig{
				PostgresMode: "docker", RedisMode: "docker", NginxEnabled: true,
				EnableClamAV: false, EnableWhisper: true,
			},
			wantNotDisabled: []string{"whisper"},
			wantDisabled:    []string{"clamav"},
			wantAppDeps:     2,
		},
		{
			name: "Email docker mode",
			config: &WizardConfig{
				PostgresMode: "docker", RedisMode: "docker", NginxEnabled: true,
				EnableEmail: true, SMTPMode: "docker",
			},
			wantNotDisabled: []string{"mailpit"},
			wantAppDeps:     2,
		},
		{
			name: "Nginx disabled, both core external",
			config: &WizardConfig{
				PostgresMode: "external", RedisMode: "external", NginxEnabled: false,
			},
			wantDisabled:  []string{"postgres", "redis", "nginx"},
			wantAppNoDeps: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := runOverride(t, tt.config)

			for _, svc := range tt.wantDisabled {
				assert.Contains(t, result.disabledServices, svc, "Expected %s to be disabled", svc)
			}
			for _, svc := range tt.wantNotDisabled {
				assert.NotContains(t, result.disabledServices, svc, "Expected %s to NOT be disabled", svc)
			}
			if tt.wantAppNoDeps {
				assert.True(t, result.appNoDeps, "Expected app to have no dependencies")
			}
			if tt.wantAppDeps > 0 {
				assert.Len(t, result.appDependsOn, tt.wantAppDeps)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Verify exact output format for backward compatibility
// ---------------------------------------------------------------------------

func TestWriteComposeOverride_ExactOutput_AllDocker(t *testing.T) {
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")

	config := &WizardConfig{
		PostgresMode:  "docker",
		RedisMode:     "docker",
		NginxEnabled:  true,
		EnableIPFS:    true,
		IPFSMode:      "docker",
		EnableIOTA:    true,
		IOTAMode:      "docker",
		EnableClamAV:  true,
		EnableWhisper: true,
		EnableEmail:   true,
		SMTPMode:      "docker",
	}

	err := WriteComposeOverride(overridePath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(overridePath)
	require.NoError(t, err)

	expected := `services:
  app:
    depends_on:
      postgres:
        condition: service_healthy
      redis:
        condition: service_healthy
`

	assert.Equal(t, expected, string(content))
}

func TestWriteComposeOverride_ExactOutput_BothExternal(t *testing.T) {
	tmpDir := t.TempDir()
	overridePath := filepath.Join(tmpDir, "docker-compose.override.yml")

	config := &WizardConfig{
		PostgresMode: "external",
		RedisMode:    "external",
		NginxEnabled: true,
	}

	err := WriteComposeOverride(overridePath, config)
	require.NoError(t, err)

	content, err := os.ReadFile(overridePath)
	require.NoError(t, err)

	// Verify postgres and redis are disabled, optional services also disabled (defaults)
	contentStr := string(content)
	assert.Contains(t, contentStr, "  postgres:\n"+`    profiles: ["disabled"]`)
	assert.Contains(t, contentStr, "  redis:\n"+`    profiles: ["disabled"]`)
	assert.Contains(t, contentStr, "    depends_on: {}")
}
