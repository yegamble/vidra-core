package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

func parsePortFromArgs(args []string, defaultPort int) int {
	port := defaultPort
	for i := 0; i < len(args); i++ {
		arg := args[i]
		switch {
		case arg == "-port" || arg == "--port":
			if i+1 < len(args) {
				if p, err := strconv.Atoi(args[i+1]); err == nil {
					port = p
				}
				i++
			}
		case strings.HasPrefix(arg, "-port="):
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "-port=")); err == nil {
				port = p
			}
		case strings.HasPrefix(arg, "--port="):
			if p, err := strconv.Atoi(strings.TrimPrefix(arg, "--port=")); err == nil {
				port = p
			}
		}
	}
	return port
}

func validateJWTSecret(secret string) error {
	if !isProductionEnvironment() {
		return nil
	}
	normalized := strings.ToLower(strings.TrimSpace(secret))
	if len(secret) < 32 {
		return fmt.Errorf("JWT_SECRET is insecure for production: minimum length is 32 characters")
	}
	insecureSecrets := map[string]struct{}{
		"your-super-secret-jwt-key-change-in-production": {},
		"change-me":       {},
		"changeme":        {},
		"default":         {},
		"secret":          {},
		"jwt-secret":      {},
		"test-secret":     {},
		"test-jwt-secret": {},
	}
	if _, found := insecureSecrets[normalized]; found {
		return fmt.Errorf("JWT_SECRET is insecure for production: replace placeholder/default value")
	}
	if strings.Contains(normalized, "change-in-production") {
		return fmt.Errorf("JWT_SECRET is insecure for production: replace placeholder/default value")
	}
	return nil
}

func isProductionEnvironment() bool {
	for _, key := range []string{"ENV", "APP_ENV", "ENVIRONMENT", "GO_ENV", "NODE_ENV"} {
		value := strings.ToLower(strings.TrimSpace(os.Getenv(key)))
		if value == "prod" || value == "production" {
			return true
		}
	}
	return os.Getenv("KUBERNETES_SERVICE_HOST") != ""
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func getBoolEnv(key string, defaultValue bool) bool {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return strings.ToLower(value) == "true" || value == "1"
}

func getIntEnv(key string, defaultValue int) int {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	if parsed, err := strconv.Atoi(value); err == nil {
		return parsed
	}
	return defaultValue
}

func getInt64Env(key string, defaultValue int64) int64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	if parsed, err := strconv.ParseInt(value, 10, 64); err == nil {
		return parsed
	}
	return defaultValue
}

func getFloat64Env(key string, defaultValue float64) float64 {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	if parsed, err := strconv.ParseFloat(value, 64); err == nil {
		return parsed
	}
	return defaultValue
}

func getStringSliceEnv(key string, defaultValue []string) []string {
	value := os.Getenv(key)
	if value == "" {
		return defaultValue
	}
	return strings.Split(value, ",")
}
