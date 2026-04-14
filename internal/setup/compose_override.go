package setup

import (
	"fmt"
	"os"
	"strings"
)

// WriteComposeOverride generates a docker-compose.override.yml file based on wizard configuration.
// It disables Docker services when external services are selected or features are disabled,
// and adjusts app/nginx dependencies to only include active docker-mode services.
//
// Service handling:
//   - postgres, redis: default profile (always start). Disabled via override when external.
//   - nginx: default profile (always starts). Disabled via override when NginxEnabled=false.
//   - ipfs, iota-node, clamav, whisper, mailpit: profile-gated in docker-compose.yml.
//     Controlled by COMPOSE_PROFILES env var (written by WriteEnvFile).
//     Additionally disabled here via override to prevent "docker compose --profile full"
//     from starting services the user chose as external or disabled.
//   - certbot: profile-gated ("letsencrypt"). Controlled by COMPOSE_PROFILES only.
func WriteComposeOverride(path string, config *WizardConfig) error {
	var lines []string
	lines = append(lines, "services:")

	// --- Default-profile services: disable when external ---

	if config.PostgresMode == "external" {
		lines = append(lines, "  postgres:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	if config.RedisMode == "external" {
		lines = append(lines, "  redis:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	if !config.NginxEnabled {
		lines = append(lines, "  nginx:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	// --- Profile-gated services: disable when external or feature disabled ---
	// Even though these require a profile to start, we override them here so that
	// "docker compose --profile full up" doesn't start services the user explicitly
	// chose as external or disabled. The override profile "disabled" takes precedence.

	if !config.EnableIPFS || config.IPFSMode == "external" {
		lines = append(lines, "  ipfs:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	if !config.EnableBitcoin {
		lines = append(lines, "  bitcoind:")
		lines = append(lines, `    profiles: ["disabled"]`)
		lines = append(lines, "  nbxplorer:")
		lines = append(lines, `    profiles: ["disabled"]`)
		lines = append(lines, "  btcpay-server:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	if !config.EnableClamAV {
		lines = append(lines, "  clamav:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	if !config.EnableWhisper {
		lines = append(lines, "  whisper:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	if !config.EnableEmail || config.SMTPMode != "docker" {
		lines = append(lines, "  mailpit:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	// --- App service: adjust depends_on to only include active docker services ---
	lines = append(lines, "  app:")

	appDeps := buildAppDependencies(config)
	if len(appDeps) == 0 {
		lines = append(lines, "    depends_on: {}")
	} else {
		lines = append(lines, "    depends_on:")
		for _, dep := range appDeps {
			lines = append(lines, fmt.Sprintf("      %s:", dep))
			lines = append(lines, "        condition: service_healthy")
		}
	}

	// --- Nginx service: adjust depends_on when disabled ---
	// When nginx is enabled but app dependencies change, nginx still depends on app.
	// No override needed for nginx depends_on (it always depends on app).

	content := strings.Join(lines, "\n") + "\n"

	// Atomic write: write to temp file, then rename
	tmpPath := path + ".tmp"
	if err := os.WriteFile(tmpPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("writing temp compose override: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		os.Remove(tmpPath) // Clean up temp file on failure
		return fmt.Errorf("renaming compose override: %w", err)
	}

	return nil
}

// buildAppDependencies returns the list of Docker services that the app container
// should depend on, based on what the user configured as docker-mode.
func buildAppDependencies(config *WizardConfig) []string {
	var deps []string

	if config.PostgresMode == "docker" {
		deps = append(deps, "postgres")
	}
	if config.RedisMode == "docker" {
		deps = append(deps, "redis")
	}

	return deps
}
