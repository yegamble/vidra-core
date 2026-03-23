package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestLoadCSPConfig(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected CSPConfig
	}{
		{
			name:    "defaults when no env vars set",
			envVars: map[string]string{},
			expected: CSPConfig{
				Enabled:    true,
				ReportOnly: false,
				ReportURI:  "",
			},
		},
		{
			name: "CSP disabled",
			envVars: map[string]string{
				"CSP_ENABLED": "false",
			},
			expected: CSPConfig{
				Enabled:    false,
				ReportOnly: false,
				ReportURI:  "",
			},
		},
		{
			name: "CSP report-only mode with URI",
			envVars: map[string]string{
				"CSP_REPORT_ONLY": "true",
				"CSP_REPORT_URI":  "https://example.com/csp-report",
			},
			expected: CSPConfig{
				Enabled:    true,
				ReportOnly: true,
				ReportURI:  "https://example.com/csp-report",
			},
		},
		{
			name: "CSP enabled with report URI",
			envVars: map[string]string{
				"CSP_ENABLED":    "true",
				"CSP_REPORT_URI": "https://example.com/csp-violations",
			},
			expected: CSPConfig{
				Enabled:    true,
				ReportOnly: false,
				ReportURI:  "https://example.com/csp-violations",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			os.Unsetenv("CSP_ENABLED")
			os.Unsetenv("CSP_REPORT_ONLY")
			os.Unsetenv("CSP_REPORT_URI")

			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}

			cfg := loadCSPConfig()

			assert.Equal(t, tt.expected, cfg)
		})
	}
}
