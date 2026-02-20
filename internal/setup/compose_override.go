package setup

import (
	"fmt"
	"os"
	"strings"
)

// WriteComposeOverride generates a docker-compose.override.yml file based on wizard configuration.
// It disables Docker services when external services are selected and adjusts app dependencies.
func WriteComposeOverride(path string, config *WizardConfig) error {
	var lines []string
	lines = append(lines, "services:")

	// Disable services that are set to external mode
	if config.PostgresMode == "external" {
		lines = append(lines, "  postgres:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	if config.RedisMode == "external" {
		lines = append(lines, "  redis:")
		lines = append(lines, `    profiles: ["disabled"]`)
	}

	// Override app service's depends_on to only include docker-mode services
	lines = append(lines, "  app:")

	// If both postgres and redis are external, app has no dependencies
	if config.PostgresMode == "external" && config.RedisMode == "external" {
		lines = append(lines, "    depends_on: {}")
	} else {
		lines = append(lines, "    depends_on:")
		// Add dependencies for docker-mode services only
		if config.PostgresMode == "docker" {
			lines = append(lines, "      postgres:")
			lines = append(lines, "        condition: service_healthy")
		}
		if config.RedisMode == "docker" {
			lines = append(lines, "      redis:")
			lines = append(lines, "        condition: service_healthy")
		}
	}

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
