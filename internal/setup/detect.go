package setup

import (
	"os"
	"strings"
)

func RequiresSetup(envPath string) (bool, string) {
	if _, err := os.Stat(envPath); os.IsNotExist(err) {
		return true, "missing .env file"
	}

	envContent, err := os.ReadFile(envPath)
	if err != nil {
		return true, "failed to read .env file"
	}

	lines := strings.Split(string(envContent), "\n")
	setupCompletedFound := false
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "SETUP_COMPLETED=") {
			setupCompletedFound = true
			value := strings.TrimPrefix(line, "SETUP_COMPLETED=")
			value = strings.TrimSpace(value)
			if value == "false" || value == "FALSE" || value == "0" {
				return true, "setup explicitly not completed"
			}
			if value == "true" || value == "TRUE" || value == "1" {
				return false, ""
			}
		}
	}

	if !setupCompletedFound {
		return true, "missing SETUP_COMPLETED flag"
	}

	return false, ""
}

func IsSetupCompleted() bool {
	value := os.Getenv("SETUP_COMPLETED")
	value = strings.ToLower(strings.TrimSpace(value))
	return value == "true" || value == "1"
}

type SetupMode struct {
	Port string
}

func NewSetupMode(port string) *SetupMode {
	if port == "" {
		port = "8080"
	}
	return &SetupMode{
		Port: port,
	}
}
